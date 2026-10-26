package httpserver

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
)

// 子站（子域名）路由：门户域名 tech.abc.com 下的服务可配置子站标签（如 license），
// 访问 license.tech.abc.com 时由 home-gate 直接反向代理到该服务的入口地址，
// 全部基于标准库 net/http 实现，无需 nginx 等额外中间件。

var (
	// subdomainLabelRe 单级子站标签：小写字母/数字/中划线，不以中划线开头或结尾。
	subdomainLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// portalDomainRe 门户主域名：点分的合法域名（至少两级）。
	portalDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// canonicalHost 归一化 Host：去端口、转小写、去结尾点号。
func canonicalHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return ""
	}
	if hp, _, err := net.SplitHostPort(h); err == nil {
		h = hp
	}
	return strings.TrimSuffix(h, ".")
}

// hostWithinDomain 判断 host 是否等于 domain 或为其子域。
// 门户域内的任何主机（含其他子站）都禁止作为子站上游，防止子站互指形成代理成环自放大。
func hostWithinDomain(host, domain string) bool {
	host, domain = canonicalHost(host), canonicalHost(domain)
	return host != "" && domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}

// portalDomain 门户主域名（如 tech.abc.com）：优先系统设置，未配置时从 issuer 推断；
// 两者皆空则子站路由停用（按普通门户处理）。结果进程内缓存（每个请求都会经过这里），
// 管理员保存设置后由 invalidatePortalDomainCache 失效重载。
func (s *Server) portalDomain() string {
	s.pdMu.RLock()
	if s.pdLoaded {
		d := s.pdCache
		s.pdMu.RUnlock()
		return d
	}
	s.pdMu.RUnlock()

	s.pdMu.Lock()
	defer s.pdMu.Unlock()
	if !s.pdLoaded { // 双重检查：并发调用只查一次库
		d := ""
		if v := canonicalHost(s.db.GetSetting("portal_domain")); v != "" {
			d = v
		} else if s.issuer != "" {
			if u, err := url.Parse(s.issuer); err == nil {
				d = canonicalHost(u.Host)
			}
		}
		s.pdCache, s.pdLoaded = d, true
	}
	return s.pdCache
}

// invalidatePortalDomainCache 设置变更后清除门户域名缓存（下次请求重新从设置加载）。
func (s *Server) invalidatePortalDomainCache() {
	s.pdMu.Lock()
	s.pdCache, s.pdLoaded = "", false
	s.pdMu.Unlock()
}

// issuerHost 返回 issuer 的宿主（进程生命周期内不变）。
// issuer 宿主是门户/OAuth 端点所在主机，永远走门户原路由，不参与子站匹配。
func (s *Server) issuerHost() string {
	if s.issuer == "" {
		return ""
	}
	if u, err := url.Parse(s.issuer); err == nil {
		return canonicalHost(u.Host)
	}
	return ""
}

// reservedSubdomains 返回禁止注册为子站标签的保留标签：
// issuer 宿主位于门户域内时保留其首标签，防止子站抢占 issuer 主机导致
// 发往门户/OIDC 端点的流量被反代走、登录静默失效。
func (s *Server) reservedSubdomains(domain string) map[string]bool {
	out := map[string]bool{}
	if domain == "" {
		return out
	}
	if issuerHost := s.issuerHost(); issuerHost != "" && issuerHost != domain && hostWithinDomain(issuerHost, domain) {
		if label := strings.TrimSuffix(issuerHost, "."+domain); label != "" && !strings.Contains(label, ".") {
			out[label] = true
		}
	}
	return out
}

// maybeSubsite 子域名路由入口：命中子站（含未知/停用的子站）时已写响应并返回 true。
func (s *Server) maybeSubsite(w http.ResponseWriter, r *http.Request) bool {
	domain := s.portalDomain()
	if domain == "" {
		return false
	}
	host := canonicalHost(r.Host)
	if host == "" || host == domain || !strings.HasSuffix(host, "."+domain) {
		return false // 门户自身（或与本门户域名无关的访问）走原有路由
	}
	if host == s.issuerHost() {
		return false // issuer 宿主是门户/OAuth 入口所在主机，永远走门户原路由
	}
	label := strings.TrimSuffix(host, "."+domain)
	if label == "" || strings.Contains(label, ".") {
		return false // 仅支持一级子站标签，多级域名不当子站处理
	}
	upstream, name, ok := s.siteUpstream(label)
	if !ok {
		http.Error(w, "未知或已停用的子站: "+host, http.StatusNotFound)
		return true
	}
	s.proxySite(w, r, name, upstream, domain)
	return true
}

// siteUpstream 按子站标签查找启用中的服务及其回源地址。
// 客户端回源到 portal_url，纯链接回源到 url；标签在两类服务之间全局唯一。
func (s *Server) siteUpstream(label string) (upstream, name string, ok bool) {
	var c model.Client
	if err := s.db.Where("subdomain = ? AND active = ?", label, true).First(&c).Error; err == nil && c.PortalURL != "" {
		return c.PortalURL, c.Name, true
	}
	var l model.AppLink
	if err := s.db.Where("subdomain = ? AND active = ?", label, true).First(&l).Error; err == nil && l.URL != "" {
		return l.URL, l.Name, true
	}
	return "", "", false
}

// proxySite 将请求反向代理到子站上游（标准库实现，自动支持 WebSocket 升级）。
// 错误响应只给访客通用文案，上游地址等细节进服务端日志，避免向匿名访客泄漏内网拓扑。
func (s *Server) proxySite(w http.ResponseWriter, r *http.Request, name, upstream, domain string) {
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme == "" || target.Host == "" ||
		(target.Scheme != "http" && target.Scheme != "https") {
		log.Printf("子站 %q 的入口地址不合法: %q", name, upstream)
		http.Error(w, "子站「"+name+"」的入口地址配置不合法，请联系管理员", http.StatusBadGateway)
		return
	}
	if hostWithinDomain(target.Host, domain) {
		// 门户域内的主机（门户自身或其他子站）一律不得作为上游：
		// 跨子站互指（a→b、b→a）会把单个请求放大为无限代理链，每跳新增 goroutine 与连接。
		log.Printf("子站 %q 的上游 %q 位于门户域 %q 内，拒绝代理（防代理成环）", name, target.Host, domain)
		http.Error(w, "子站「"+name+"」的入口地址不允许指向门户域内，已拒绝代理", http.StatusBadGateway)
		return
	}
	rp := &httputil.ReverseProxy{
		FlushInterval: 100 * time.Millisecond, // 兼容 SSE 等流式响应
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)         // 拼接回源路径，设置 scheme/host
			pr.Out.Host = target.Host // 回源携带上游 Host（虚拟主机语义）
			pr.SetXForwarded()        // 附加 X-Forwarded-For / Host / Proto
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("子站 %q 上游不可达: %v (upstream=%s)", name, err, upstream)
			http.Error(w, "子站「"+name+"」暂时不可用，请稍后重试", http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// normalizeSubdomain 校验并归一化子站标签；空串合法（表示清除）。
func (s *Server) normalizeSubdomain(sub string) (string, error) {
	sub = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sub), ".")))
	if sub == "" {
		return "", nil
	}
	if !subdomainLabelRe.MatchString(sub) {
		return "", errors.New("子域名仅支持小写字母、数字与中划线，且以字母或数字开头结尾（单级，不含点）")
	}
	// 保留标签：issuer 宿主在门户域内时其首标签不可被抢占（防 OAuth 入口被劫持）
	if s.reservedSubdomains(s.portalDomain())[sub] {
		return "", errors.New("该子域名与门户/OAuth 入口冲突，不可使用")
	}
	return sub, nil
}

// subdomainTaken 子站标签全局唯一：客户端与纯链接之间也不允许重复；
// excludeClientID / excludeLinkID 用于更新时排除自身。
// 调用方需持有 s.subdomainMu（检查与写入之间无窗口，防止并发注册撞名）。
func (s *Server) subdomainTaken(sub string, excludeClientID, excludeLinkID uint) bool {
	if sub == "" {
		return false
	}
	var cnt int64
	qc := s.db.Model(&model.Client{}).Where("subdomain = ?", sub)
	if excludeClientID != 0 {
		qc = qc.Where("id <> ?", excludeClientID)
	}
	if err := qc.Count(&cnt).Error; err != nil || cnt > 0 {
		return true
	}
	ql := s.db.Model(&model.AppLink{}).Where("subdomain = ?", sub)
	if excludeLinkID != 0 {
		ql = ql.Where("id <> ?", excludeLinkID)
	}
	if err := ql.Count(&cnt).Error; err != nil || cnt > 0 {
		return true
	}
	return false
}
