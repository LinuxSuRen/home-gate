package httpserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
	"github.com/linuxsuren/home-gate/internal/store"
	"github.com/linuxsuren/home-gate/internal/token"
)

const testIssuer = "https://gate.test"
const testVersion = "v0.0.1-test"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/gate.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// 测试结束关闭连接池，释放 SQLite 文件句柄（否则 Windows 上 t.TempDir 清理会失败）
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	tm, err := token.NewManager(db.DB)
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	return New(db, tm, testIssuer, testVersion)
}

// doJSON 执行 JSON 请求并返回响应与解析结果。
// 与前端封装一致：非 GET 请求自动携带 X-Requested-With（管理端变更接口的 CSRF 防御要求）。
func doJSON(t *testing.T, h http.Handler, method, path, body string, header map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	out := map[string]any{}
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w, out
}

func loginAdmin(t *testing.T, h http.Handler) map[string]string {
	t.Helper()
	w, _ := doJSON(t, h, "POST", "/api/auth/login", `{"username":"admin","password":"admin123"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login admin: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Header().Get("Set-Cookie")
	return map[string]string{"Cookie": strings.Split(cookie, ";")[0]}
}

func issueInitialToken(t *testing.T, h http.Handler, hdr map[string]string) string {
	t.Helper()
	w, out := doJSON(t, h, "POST", "/api/admin/initial-tokens", `{"remark":"test","days":1}`, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("issue initial token: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	return data["token"].(string)
}

// TestFeishuEnabledProbe 未配置飞书时探测接口应返回 enabled=false（登录页据此隐藏入口）。
func TestFeishuEnabledProbe(t *testing.T) {
	s := newTestServer(t)
	w, out := doJSON(t, s.Handler(), "GET", "/api/auth/feishu/enabled", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("feishu enabled probe: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	if data["enabled"] != false {
		t.Fatalf("expected enabled=false when unconfigured, got %v", data["enabled"])
	}
}

// TestFullFlow 覆盖：登录 → DCR 注册 → 授权（SSO 静默放行）→ 换 token → userinfo → 刷新。
func TestFullFlow(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// 1. 管理员登录
	adminHdr := loginAdmin(t, h)

	// 2. 签发 initial token 并 DCR 注册客户端
	it := issueInitialToken(t, h, adminHdr)
	dcrBody := `{
		"client_name":"demo-app",
		"redirect_uris":["https://notify.test/oauth/callback"],
		"scope":"openid profile email offline_access",
		"token_endpoint_auth_method":"client_secret_basic",
		"portal_url":"https://notify.test",
		"groups_map":{"admin":"admin","operate":"operate","view":"view"}
	}`
	w, reg := doJSON(t, h, "POST", "/oauth2/register", dcrBody,
		map[string]string{"Authorization": "Bearer " + it})
	if w.Code != http.StatusCreated {
		t.Fatalf("dcr register: %d %s", w.Code, w.Body.String())
	}
	clientID := reg["client_id"].(string)
	clientSecret := reg["client_secret"].(string)
	regToken := reg["registration_access_token"].(string)

	// 3. RFC 7592 查询注册信息
	w, _ = doJSON(t, h, "GET", "/oauth2/register/"+clientID, "",
		map[string]string{"Authorization": "Bearer " + regToken})
	if w.Code != http.StatusOK {
		t.Fatalf("dcr get: %d %s", w.Code, w.Body.String())
	}
	// 无凭据被拒
	w, _ = doJSON(t, h, "GET", "/oauth2/register/"+clientID, "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("dcr get without token should 401, got %d", w.Code)
	}

	// 4. 修改管理员密码（授权前要求）
	w, _ = doJSON(t, h, "POST", "/api/auth/change-password",
		`{"old_password":"admin123","new_password":"newpass123"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}

	// 5. 授权（带会话 → SSO 静默发码）
	state := "st-123"
	nonce := "n-123"
	authURL := "/oauth2/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri":  {"https://notify.test/oauth/callback"},
		"scope": {"openid profile email offline_access"},
		"state": {state}, "nonce": {nonce},
	}.Encode()
	req := httptest.NewRequest("GET", authURL, nil)
	for k, v := range adminHdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize: %d %s", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("state") != state || loc.Query().Get("code") == "" {
		t.Fatalf("authorize redirect missing code/state: %s", loc.String())
	}
	code := loc.Query().Get("code")

	// 6. 未注册回调地址被拒
	bad := "/oauth2/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri":  {"https://evil.test/oauth/callback"},
		"scope": {"openid"}, "state": {state},
	}.Encode()
	req = httptest.NewRequest("GET", bad, nil)
	for k, v := range adminHdr {
		req.Header.Set(k, v)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	loc2, _ := url.Parse(rec.Header().Get("Location"))
	if loc2 == nil || loc2.Query().Get("error") != "invalid_redirect_uri" {
		t.Fatalf("bad redirect_uri should be rejected, got %v %s", rec.Code, rec.Header().Get("Location"))
	}

	// 7. 授权码换 token（basic auth）
	verifier := "test-verifier-012345678901234567890123456789"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	// 授权码签发时未带 PKCE，confidential 客户端可不带 —— 但需与签发一致；
	// 这里重新走一次带 PKCE 的授权拿新码。
	authURL2 := authURL + "&code_challenge=" + url.QueryEscape(challenge) + "&code_challenge_method=S256"
	req = httptest.NewRequest("GET", authURL2, nil)
	for k, v := range adminHdr {
		req.Header.Set(k, v)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	loc3, _ := url.Parse(rec.Header().Get("Location"))
	code = loc3.Query().Get("code")
	if code == "" {
		t.Fatalf("second authorize (SSO) missing code: %s", loc3.String())
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://notify.test/oauth/callback"},
		"code_verifier": {verifier},
	}
	req = httptest.NewRequest("POST", "/oauth2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	var tok map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" || tok["id_token"] == "" || refresh == "" {
		t.Fatalf("token response missing fields: %v", tok)
	}

	// 授权码重放被拒
	req = httptest.NewRequest("POST", "/oauth2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code replay should fail, got %d", rec.Code)
	}

	// 8. userinfo
	req = httptest.NewRequest("GET", "/oauth2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("userinfo: %d %s", rec.Code, rec.Body.String())
	}
	var ui map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &ui)
	if ui["sub"] != "admin" {
		t.Fatalf("userinfo sub = %v", ui["sub"])
	}
	groups, _ := ui["groups"].([]any)
	if len(groups) == 0 || groups[0] != "admin" {
		t.Fatalf("userinfo groups = %v", groups)
	}

	// 9. 刷新令牌（轮换）
	form2 := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	req = httptest.NewRequest("POST", "/oauth2/token", strings.NewReader(form2.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body.String())
	}
	var tok2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tok2)
	newRefresh, _ := tok2["refresh_token"].(string)
	if newRefresh == "" || newRefresh == refresh {
		t.Fatalf("refresh token should rotate: %v", tok2)
	}
	// 旧 refresh token 失效
	req = httptest.NewRequest("POST", "/oauth2/token", strings.NewReader(form2.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("old refresh token should fail, got %d", rec.Code)
	}

	// 10. OIDC 发现与 JWKS
	w, disc := doJSON(t, h, "GET", "/.well-known/openid-configuration", "", nil)
	if w.Code != http.StatusOK || disc["issuer"] != testIssuer {
		t.Fatalf("discovery: %d %v", w.Code, disc["issuer"])
	}
	w, jwks := doJSON(t, h, "GET", "/.well-known/jwks.json", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("jwks: %d", w.Code)
	}
	keys := jwks["keys"].([]any)
	k0 := keys[0].(map[string]any)
	if k0["kty"] != "OKP" || k0["crv"] != "Ed25519" || k0["x"] == "" {
		t.Fatalf("jwks key unexpected: %v", k0)
	}

	// 11. 审计已有记录
	w, audit := doJSON(t, h, "GET", "/api/admin/audit?page=1&page_size=50", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("audit: %d %s", w.Code, w.Body.String())
	}
	if audit["total"].(float64) < 3 {
		t.Fatalf("audit too few entries: %v", audit["total"])
	}

	// 12. 登出
	req = httptest.NewRequest("GET", "/oauth2/logout?post_logout_redirect_uri=https://notify.test/", nil)
	for k, v := range adminHdr {
		req.Header.Set(k, v)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("logout: %d", rec.Code)
	}

}

// TestAdminCreateClient 管理员代第三方系统直接注册客户端。
func TestAdminCreateClient(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	body := `{"client_name":"GitLab","redirect_uris":["https://gitlab.test/users/auth/openid_connect/callback"],"token_endpoint_auth_method":"client_secret_basic","portal_url":"https://gitlab.test"}`
	w, out := doJSON(t, h, "POST", "/api/admin/clients", body, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create client: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	if data["client_id"] == "" || data["client_secret"] == "" || data["registration_access_token"] == "" {
		t.Fatalf("create client response missing credentials: %v", data)
	}

	// 出现在客户端列表且启用
	w, list := doJSON(t, h, "GET", "/api/admin/clients", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("list clients: %d", w.Code)
	}
	found := false
	for _, c := range list["data"].([]any) {
		m := c.(map[string]any)
		if m["client_id"] == data["client_id"] && m["active"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("created client not found in list")
	}

	// 非管理员 403
	w, _ = doJSON(t, h, "POST", "/api/admin/clients", body, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create should 401, got %d", w.Code)
	}
}

// TestPublicClientPKCE 公共客户端（auth method none）必须 PKCE。
func TestPublicClientPKCE(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)
	it := issueInitialToken(t, h, adminHdr)

	w, reg := doJSON(t, h, "POST", "/oauth2/register", `{
		"client_name":"license-web",
		"redirect_uris":["https://license.test/oidc/callback"],
		"token_endpoint_auth_method":"none"
	}`, map[string]string{"Authorization": "Bearer " + it})
	if w.Code != http.StatusCreated {
		t.Fatalf("dcr: %d %s", w.Code, w.Body.String())
	}
	clientID := reg["client_id"].(string)
	if _, has := reg["client_secret"]; has {
		t.Fatalf("public client should not get secret")
	}

	// 无 PKCE 的授权请求被拒
	authURL := "/oauth2/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {"https://license.test/oidc/callback"}, "scope": {"openid"},
	}.Encode()
	req := httptest.NewRequest("GET", authURL, nil)
	for k, v := range adminHdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc == nil || loc.Query().Get("error") == "" {
		t.Fatalf("authorize without PKCE should fail, got %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

// TestAuthorizeRedirectsToLogin 未登录时授权端点跳登录页。
func TestAuthorizeRedirectsToLogin(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	w, reg := doJSON(t, h, "POST", "/oauth2/register", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized dcr should 401, got %d", w.Code)
	}
	_ = reg
}

// TestFeishuAutoProvision 飞书上游未配置时提示未启用。
func TestFeishuDisabled(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	req := httptest.NewRequest("GET", "/api/auth/feishu/start", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("feishu start should 501, got %d", rec.Code)
	}
}

// TestLinkCRUD 管理端纯链接应用管理：创建 → /api/apps 返回 links → 更新/删除生效；非管理员 403。
func TestLinkCRUD(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	// 非管理员 403：先创建一个非管理员用户再登录
	w, _ := doJSON(t, h, "POST", "/api/admin/users",
		`{"username":"bob","password":"bob1234567","groups":["view"]}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create bob: %d %s", w.Code, w.Body.String())
	}
	w, _ = doJSON(t, h, "POST", "/api/auth/login", `{"username":"bob","password":"bob1234567"}`, nil)
	bobHdr := map[string]string{"Cookie": strings.Split(w.Header().Get("Set-Cookie"), ";")[0]}
	w, _ = doJSON(t, h, "GET", "/api/admin/links", "", bobHdr)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bob should be forbidden from admin links api, got %d", w.Code)
	}

	// name/url 必填
	w, _ = doJSON(t, h, "POST", "/api/admin/links", `{"name":"","url":""}`, adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create link without name/url should 400, got %d", w.Code)
	}

	// 创建
	w, out := doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"监控大盘","url":"https://grafana.example.com","description":"指标看板","sort":1}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create link: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	lid := fmt.Sprintf("%v", data["id"])

	// /api/apps 返回 links
	w, apps := doJSON(t, h, "GET", "/api/apps", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("apps: %d %s", w.Code, w.Body.String())
	}
	appsData := apps["data"].(map[string]any)
	links := appsData["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("apps links = %v", links)
	}
	l0 := links[0].(map[string]any)
	if l0["name"] != "监控大盘" || l0["url"] != "https://grafana.example.com" {
		t.Fatalf("apps link unexpected: %v", l0)
	}
	if _, ok := appsData["apps"].([]any); !ok {
		t.Fatalf("apps field missing: %v", appsData)
	}

	// 更新
	w, _ = doJSON(t, h, "PUT", "/api/admin/links/"+lid,
		`{"name":"监控大盘2","active":false}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("update link: %d %s", w.Code, w.Body.String())
	}
	w, list := doJSON(t, h, "GET", "/api/admin/links", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("list links: %d %s", w.Code, w.Body.String())
	}
	rows := list["data"].([]any)
	r0 := rows[0].(map[string]any)
	if r0["name"] != "监控大盘2" || r0["active"] != false {
		t.Fatalf("updated link unexpected: %v", r0)
	}

	// 停用后 /api/apps 不再返回
	w, apps = doJSON(t, h, "GET", "/api/apps", "", adminHdr)
	appsData = apps["data"].(map[string]any)
	if n := len(appsData["links"].([]any)); n != 0 {
		t.Fatalf("inactive link should be hidden, got %d", n)
	}

	// 删除
	w, _ = doJSON(t, h, "DELETE", "/api/admin/links/"+lid, "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("delete link: %d %s", w.Code, w.Body.String())
	}
	w, _ = doJSON(t, h, "DELETE", "/api/admin/links/"+lid, "", adminHdr)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete again should 404, got %d", w.Code)
	}
}

// TestSessionTimeout 管理员可配置登录会话超时时长：默认 24 小时；调整后新会话与 cookie 同步生效。
func TestSessionTimeout(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	lastSession := func() *model.Session {
		t.Helper()
		var sess model.Session
		if err := s.db.Order("created_at DESC").First(&sess).Error; err != nil {
			t.Fatalf("load session: %v", err)
		}
		return &sess
	}

	// 默认 24h（当天有效）
	sess := lastSession()
	if d := time.Until(sess.ExpiresAt); d < 23*time.Hour || d > 24*time.Hour+time.Minute {
		t.Fatalf("default session ttl should be ~24h, got %v", d)
	}

	// 调整为 2 小时
	w, _ := doJSON(t, h, "PUT", "/api/admin/settings", `{"session_ttl_hours":2}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("set ttl: %d %s", w.Code, w.Body.String())
	}

	// 新登录：会话 ~2h，cookie Max-Age=7200
	w, _ = doJSON(t, h, "POST", "/api/auth/login", `{"username":"admin","password":"admin123"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("re-login: %d %s", w.Code, w.Body.String())
	}
	if cookie := w.Header().Get("Set-Cookie"); !strings.Contains(cookie, "Max-Age=7200") {
		t.Fatalf("cookie Max-Age should be 7200, got %s", cookie)
	}
	sess = lastSession()
	if d := time.Until(sess.ExpiresAt); d < 1*time.Hour || d > 2*time.Hour+time.Minute {
		t.Fatalf("configured session ttl should be ~2h, got %v", d)
	}

	// 设置查询返回当前值
	w, out := doJSON(t, h, "GET", "/api/admin/settings", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("get settings: %d %s", w.Code, w.Body.String())
	}
	if got := out["data"].(map[string]any)["session_ttl_hours"].(float64); got != 2 {
		t.Fatalf("settings should return session_ttl_hours=2, got %v", got)
	}

	// 越界值拒绝
	for _, bad := range []string{"0", "721", "-1"} {
		w, _ = doJSON(t, h, "PUT", "/api/admin/settings", `{"session_ttl_hours":`+bad+`}`, adminHdr)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("session_ttl_hours=%s should be rejected, got %d", bad, w.Code)
		}
	}
}

// TestUserCRUD 管理端用户与组管理。
func TestUserCRUD(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	w, out := doJSON(t, h, "POST", "/api/admin/users",
		`{"username":"alice","password":"alice12345","name":"Alice","groups":["view","operate"]}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	uid := fmt.Sprintf("%v", data["id"])

	// 更新组（保持非管理员）
	w, _ = doJSON(t, h, "PUT", "/api/admin/users/"+uid, `{"groups":["view"]}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("update user: %d %s", w.Code, w.Body.String())
	}

	// 非管理员无权访问
	w, _ = doJSON(t, h, "POST", "/api/auth/login", `{"username":"alice","password":"alice12345"}`, nil)
	aliceCookie := map[string]string{"Cookie": strings.Split(w.Header().Get("Set-Cookie"), ";")[0]}
	w, _ = doJSON(t, h, "GET", "/api/admin/users", "", aliceCookie)
	if w.Code != http.StatusForbidden {
		t.Fatalf("alice should be forbidden from admin api, got %d", w.Code)
	}

	// 删除
	w, _ = doJSON(t, h, "DELETE", "/api/admin/users/"+uid, "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("delete user: %d %s", w.Code, w.Body.String())
	}
}
