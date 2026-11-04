package httpserver

import (
	"errors"
	"fmt"
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

// portalDomain 门户主域名（如 tech.abc.com）：优先系统设置，未配置时从 issuer 推断；
// 两者皆空则子站路由停用（按普通门户处理）。
func (s *Server) portalDomain() string {
	if d := canonicalHost(s.db.GetSetting("portal_domain")); d != "" {
		return d
	}
	if s.issuer != "" {
		if u, err := url.Parse(s.issuer); err == nil {
			return canonicalHost(u.Host)
		}
	}
	return ""
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
	label := strings.TrimSuffix(host, "."+domain)
	if label == "" || strings.Contains(label, ".") {
		return false // 仅支持一级子站标签，多级域名不当子站处理
	}
	upstream, name, ok := s.siteUpstream(label)
	if !ok {
		http.Error(w, "未知或已停用的子站: "+host, http.StatusNotFound)
		return true
	}
	s.proxySite(w, r, name, upstream)
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
func (s *Server) proxySite(w http.ResponseWriter, r *http.Request, name, upstream string) {
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme == "" || target.Host == "" ||
		(target.Scheme != "http" && target.Scheme != "https") {
		http.Error(w, "子站「"+name+"」的入口地址不合法: "+upstream, http.StatusBadGateway)
		return
	}
	if canonicalHost(target.Host) == canonicalHost(r.Host) {
		// 上游指向子站自身会形成回环，直接拒绝
		http.Error(w, "子站「"+name+"」的入口地址指向子站自身，已拒绝代理", http.StatusBadGateway)
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
			http.Error(w, "子站「"+name+"」上游不可达: "+upstream, http.StatusBadGateway)
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
	if d := s.portalDomain(); sub == d {
		return "", fmt.Errorf("子域名不能与门户主域名 %s 相同", d)
	}
	return sub, nil
}

// subdomainTaken 子站标签全局唯一：客户端与纯链接之间也不允许重复；
// excludeClientID / excludeLinkID 用于更新时排除自身。
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
