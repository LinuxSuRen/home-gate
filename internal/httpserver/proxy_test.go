package httpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// echoUpstream 返回一个回显 path/Host/X-Forwarded-Host 的上游服务。
func echoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s host=%s xfh=%s", r.URL.Path, r.Host, r.Header.Get("X-Forwarded-Host"))
	}))
	t.Cleanup(up.Close)
	return up
}

// doHostReq 以指定 Host 发起请求（模拟子域名访问）。
func doHostReq(h http.Handler, method, rawURL string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, rawURL, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestSubdomainProxy 子站（子域名）反向代理：
// 配置门户域名后，链接/客户端可按 子域名.门户域名 访问，由 gate 纯 Go 反向代理到其入口地址；
// 门户自身、未知/停用子站、回环上游各有预期行为。
func TestSubdomainProxy(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	upstream := echoUpstream(t)

	// 配置门户域名
	w, _ := doJSON(t, h, "PUT", "/api/admin/settings", `{"portal_domain":"tech.abc.com"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("set portal domain: %d %s", w.Code, w.Body.String())
	}
	// 非法域名被拒
	w, _ = doJSON(t, h, "PUT", "/api/admin/settings", `{"portal_domain":"https://x"}`, adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid portal domain should 400, got %d", w.Code)
	}

	// 链接配置子域名（输入含大写与结尾点号，应归一化为 license）
	w, out := doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"license","url":"`+upstream.URL+`","subdomain":"License."}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create link with subdomain: %d %s", w.Code, w.Body.String())
	}
	lid := fmt.Sprintf("%v", out["data"].(map[string]any)["id"])
	if got := out["data"].(map[string]any)["subdomain"]; got != "license" {
		t.Fatalf("subdomain should normalize to license, got %v", got)
	}

	// 子域名访问（Host 含端口也应匹配），路径与查询透传、Host 改写、附加 X-Forwarded-Host
	rec := doHostReq(h, "GET", "http://license.tech.abc.com:8080/svc/page?q=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("subsite proxy: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "path=/svc/page") {
		t.Fatalf("path should pass through, body: %s", body)
	}
	if !strings.Contains(body, "host="+strings.TrimPrefix(upstream.URL, "http://")) {
		t.Fatalf("upstream Host should be rewritten, body: %s", body)
	}
	if !strings.Contains(body, "xfh=license.tech.abc.com:8080") {
		t.Fatalf("X-Forwarded-Host expected, body: %s", body)
	}

	// 门户自身域名不受影响
	if rec := doHostReq(h, "GET", "http://tech.abc.com/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("portal host should serve as usual, got %d", rec.Code)
	}
	// 与门户域名无关的 Host（如 IP）不受影响
	if rec := doHostReq(h, "GET", "http://127.0.0.1:8080/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("unrelated host should serve as usual, got %d", rec.Code)
	}
	// 未配置的子站 → 404
	if rec := doHostReq(h, "GET", "http://unknown.tech.abc.com/"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown subdomain should 404, got %d", rec.Code)
	}
	// 多级子域名不当子站处理（回退门户 SPA）
	if rec := doHostReq(h, "GET", "http://a.b.tech.abc.com/"); rec.Code != http.StatusOK {
		t.Fatalf("multi-level host should fall back to portal, got %d", rec.Code)
	}

	// 停用后子站 404
	w, _ = doJSON(t, h, "PUT", "/api/admin/links/"+lid, `{"active":false}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("disable link: %d %s", w.Code, w.Body.String())
	}
	if rec := doHostReq(h, "GET", "http://license.tech.abc.com/"); rec.Code != http.StatusNotFound {
		t.Fatalf("inactive subsite should 404, got %d", rec.Code)
	}

	// 清除子域名后恢复 404；随后可分配给客户端
	w, _ = doJSON(t, h, "PUT", "/api/admin/links/"+lid, `{"subdomain":""}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("clear subdomain: %d %s", w.Code, w.Body.String())
	}

	// 客户端（OAuth 应用）配子域名：回源到 portal_url
	upstream2 := echoUpstream(t)
	w, out = doJSON(t, h, "POST", "/api/admin/clients",
		`{"client_name":"Sale","redirect_uris":["https://sale.test/cb"],"portal_url":"`+upstream2.URL+`","subdomain":"sale"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create client with subdomain: %d %s", w.Code, w.Body.String())
	}
	if rec := doHostReq(h, "GET", "http://sale.tech.abc.com/"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "path=/") {
		t.Fatalf("client subsite proxy: %d %s", rec.Code, rec.Body.String())
	}
	// 列表回传 subdomain
	w, list := doJSON(t, h, "GET", "/api/admin/clients", "", adminHdr)
	found := false
	for _, c := range list["data"].([]any) {
		if m := c.(map[string]any); m["subdomain"] == "sale" {
			found = true
		}
	}
	if !found {
		t.Fatalf("client list should include subdomain, got %v", list["data"])
	}

	// 上游指向子站自身 → 拒绝代理（防回环）
	w, _ = doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"loop","url":"http://looped.tech.abc.com","subdomain":"looped"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create loop link: %d %s", w.Code, w.Body.String())
	}
	if rec := doHostReq(h, "GET", "http://looped.tech.abc.com/"); rec.Code != http.StatusBadGateway {
		t.Fatalf("self-referencing upstream should 502, got %d", rec.Code)
	}

	// 上游不可达 → 502
	w, _ = doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"dead","url":"http://127.0.0.1:1","subdomain":"dead"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create dead link: %d %s", w.Code, w.Body.String())
	}
	if rec := doHostReq(h, "GET", "http://dead.tech.abc.com/"); rec.Code != http.StatusBadGateway {
		t.Fatalf("unreachable upstream should 502, got %d", rec.Code)
	}
}

// TestSubdomainFallbackAndValidation 门户域名未显式配置时按 -issuer 推断；
// 子域名校验：非法标签拒绝、客户端与链接之间全局唯一、DCR 自助注册不可设置子域名。
func TestSubdomainFallbackAndValidation(t *testing.T) {
	s := newTestServer(t) // issuer = https://gate.test → 门户域名推断为 gate.test
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	upstream := echoUpstream(t)

	// 按 issuer 推断的门户域名即可路由子站
	w, _ := doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"print","url":"`+upstream.URL+`","subdomain":"print"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create link: %d %s", w.Code, w.Body.String())
	}
	if rec := doHostReq(h, "GET", "http://print.gate.test/page"); rec.Code != http.StatusOK {
		t.Fatalf("issuer-derived subdomain should proxy, got %d", rec.Code)
	}

	// 非法标签拒绝（多级/非法字符/边界中划线/超长；punycode 形如 xn--xx 合法，不在拒绝之列）
	for _, bad := range []string{"a.b", "-x", "x-", "ab_c", "ab cd", strings.Repeat("a", 64)} {
		w, _ = doJSON(t, h, "POST", "/api/admin/links",
			`{"name":"t","url":"`+upstream.URL+`","subdomain":"`+bad+`"}`, adminHdr)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("subdomain %q should be rejected, got %d", bad, w.Code)
		}
	}

	// 跨表唯一：客户端占用后，链接再占用应被拒；反之亦然
	w, _ = doJSON(t, h, "POST", "/api/admin/clients",
		`{"client_name":"Dup","redirect_uris":["https://dup.test/cb"],"subdomain":"dup"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create client dup: %d %s", w.Code, w.Body.String())
	}
	w, _ = doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"dup","url":"`+upstream.URL+`","subdomain":"dup"}`, adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate subdomain across client/link should 400, got %d", w.Code)
	}
	w, _ = doJSON(t, h, "POST", "/api/admin/clients",
		`{"client_name":"Dup2","redirect_uris":["https://dup.test/cb"],"subdomain":"dup"}`, adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate subdomain across clients should 400, got %d", w.Code)
	}

	// DCR 自助注册携带 subdomain 应被忽略（仅管理员可配）
	it := issueInitialToken(t, h, adminHdr)
	w, reg := doJSON(t, h, "POST", "/oauth2/register", `{
		"client_name":"hijacker",
		"redirect_uris":["https://hijack.test/cb"],
		"subdomain":"print"
	}`, map[string]string{"Authorization": "Bearer " + it})
	if w.Code != http.StatusCreated {
		t.Fatalf("dcr register: %d %s", w.Code, w.Body.String())
	}
	clientID := reg["client_id"].(string)
	w, list := doJSON(t, h, "GET", "/api/admin/clients", "", adminHdr)
	for _, c := range list["data"].([]any) {
		m := c.(map[string]any)
		if m["client_id"] == clientID && m["subdomain"] != "" {
			t.Fatalf("DCR-registered client should not carry subdomain: %v", m)
		}
	}
	// print 子站仍指向原链接，未被抢注
	if rec := doHostReq(h, "GET", "http://print.gate.test/"); rec.Code != http.StatusOK {
		t.Fatalf("print subsite should still work after DCR attempt, got %d", rec.Code)
	}
}
