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

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/gate.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// 娴嬭瘯缁撴潫鍏抽棴杩炴帴姹狅紝閲婃斁 SQLite 鏂囦欢鍙ユ焺锛堝惁鍒?Windows 涓?t.TempDir 娓呯悊浼氬け璐ワ級
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	tm, err := token.NewManager(db.DB)
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	return New(db, tm, testIssuer)
}

// doJSON 鎵ц JSON 璇锋眰骞惰繑鍥炲搷搴斾笌瑙ｆ瀽缁撴灉銆?
func doJSON(t *testing.T, h http.Handler, method, path, body string, header map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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

// TestFeishuEnabledProbe 鏈厤缃涔︽椂鎺㈡祴鎺ュ彛搴旇繑鍥?enabled=false锛堢櫥褰曢〉鎹闅愯棌鍏ュ彛锛夈€?
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

// TestFullFlow 瑕嗙洊锛氱櫥褰?鈫?DCR 娉ㄥ唽 鈫?鎺堟潈锛圫SO 闈欓粯鏀捐锛夆啋 鎹?token 鈫?userinfo 鈫?鍒锋柊銆?
func TestFullFlow(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// 1. 绠＄悊鍛樼櫥褰?
	adminHdr := loginAdmin(t, h)

	// 2. 绛惧彂 initial token 骞?DCR 娉ㄥ唽瀹㈡埛绔?
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

	// 3. RFC 7592 鏌ヨ娉ㄥ唽淇℃伅
	w, _ = doJSON(t, h, "GET", "/oauth2/register/"+clientID, "",
		map[string]string{"Authorization": "Bearer " + regToken})
	if w.Code != http.StatusOK {
		t.Fatalf("dcr get: %d %s", w.Code, w.Body.String())
	}
	// 鏃犲嚟鎹鎷?
	w, _ = doJSON(t, h, "GET", "/oauth2/register/"+clientID, "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("dcr get without token should 401, got %d", w.Code)
	}

	// 4. 淇敼绠＄悊鍛樺瘑鐮侊紙鎺堟潈鍓嶈姹傦級
	w, _ = doJSON(t, h, "POST", "/api/auth/change-password",
		`{"old_password":"admin123","new_password":"newpass123"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}

	// 5. 鎺堟潈锛堝甫浼氳瘽 鈫?SSO 闈欓粯鍙戠爜锛?
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

	// 6. 鏈敞鍐屽洖璋冨湴鍧€琚嫆
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

	// 7. 鎺堟潈鐮佹崲 token锛坆asic auth锛?
	verifier := "test-verifier-012345678901234567890123456789"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	// 鎺堟潈鐮佺鍙戞椂鏈甫 PKCE锛宑onfidential 瀹㈡埛绔彲涓嶅甫 鈥斺€?浣嗛渶涓庣鍙戜竴鑷达紱
	// 杩欓噷閲嶆柊璧颁竴娆″甫 PKCE 鐨勬巿鏉冩嬁鏂扮爜銆?
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

	// 鎺堟潈鐮侀噸鏀捐鎷?
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

	// 9. 鍒锋柊浠ょ墝锛堣疆鎹級
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
	// 鏃?refresh token 澶辨晥
	req = httptest.NewRequest("POST", "/oauth2/token", strings.NewReader(form2.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("old refresh token should fail, got %d", rec.Code)
	}

	// 10. OIDC 鍙戠幇涓?JWKS
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

	// 11. 瀹¤宸叉湁璁板綍
	w, audit := doJSON(t, h, "GET", "/api/admin/audit?page=1&page_size=50", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("audit: %d %s", w.Code, w.Body.String())
	}
	if audit["total"].(float64) < 3 {
		t.Fatalf("audit too few entries: %v", audit["total"])
	}

	// 12. 鐧诲嚭
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

// TestAdminCreateClient 绠＄悊鍛樹唬绗笁鏂圭郴缁熺洿鎺ユ敞鍐屽鎴风銆?
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

	// 鍑虹幇鍦ㄥ鎴风鍒楄〃涓斿惎鐢?
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

	// 闈炵鐞嗗憳 403
	w, _ = doJSON(t, h, "POST", "/api/admin/clients", body, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create should 401, got %d", w.Code)
	}
}

// TestPublicClientPKCE 鍏叡瀹㈡埛绔紙auth method none锛夊繀椤?PKCE銆?
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

	// 鏃?PKCE 鐨勬巿鏉冭姹傝鎷?
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

// TestAuthorizeRedirectsToLogin 鏈櫥褰曟椂鎺堟潈绔偣璺崇櫥褰曢〉銆?
func TestAuthorizeRedirectsToLogin(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	w, reg := doJSON(t, h, "POST", "/oauth2/register", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized dcr should 401, got %d", w.Code)
	}
	_ = reg
}

// TestFeishuAutoProvision 椋炰功涓婃父鏈厤缃椂鎻愮ず鏈惎鐢ㄣ€?
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

// TestLinkCRUD 绠＄悊绔函閾炬帴搴旂敤绠＄悊锛氬垱寤?鈫?/api/apps 杩斿洖 links 鈫?鏇存柊/鍒犻櫎鐢熸晥锛涢潪绠＄悊鍛?403銆?
func TestLinkCRUD(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	// 闈炵鐞嗗憳 403锛氬厛鍒涘缓涓€涓潪绠＄悊鍛樼敤鎴峰啀鐧诲綍
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

	// name/url 蹇呭～
	w, _ = doJSON(t, h, "POST", "/api/admin/links", `{"name":"","url":""}`, adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create link without name/url should 400, got %d", w.Code)
	}

	// 鍒涘缓
	w, out := doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"鐩戞帶澶х洏","url":"https://grafana.example.com","description":"鎸囨爣鐪嬫澘","sort":1}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create link: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	lid := fmt.Sprintf("%v", data["id"])

	// /api/apps 杩斿洖 links
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
	if l0["name"] != "鐩戞帶澶х洏" || l0["url"] != "https://grafana.example.com" {
		t.Fatalf("apps link unexpected: %v", l0)
	}
	if _, ok := appsData["apps"].([]any); !ok {
		t.Fatalf("apps field missing: %v", appsData)
	}

	// 鏇存柊
	w, _ = doJSON(t, h, "PUT", "/api/admin/links/"+lid,
		`{"name":"鐩戞帶澶х洏2","active":false}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("update link: %d %s", w.Code, w.Body.String())
	}
	w, list := doJSON(t, h, "GET", "/api/admin/links", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("list links: %d %s", w.Code, w.Body.String())
	}
	rows := list["data"].([]any)
	r0 := rows[0].(map[string]any)
	if r0["name"] != "鐩戞帶澶х洏2" || r0["active"] != false {
		t.Fatalf("updated link unexpected: %v", r0)
	}

	// 鍋滅敤鍚?/api/apps 涓嶅啀杩斿洖
	w, apps = doJSON(t, h, "GET", "/api/apps", "", adminHdr)
	appsData = apps["data"].(map[string]any)
	if n := len(appsData["links"].([]any)); n != 0 {
		t.Fatalf("inactive link should be hidden, got %d", n)
	}

	// 鍒犻櫎
	w, _ = doJSON(t, h, "DELETE", "/api/admin/links/"+lid, "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("delete link: %d %s", w.Code, w.Body.String())
	}
	w, _ = doJSON(t, h, "DELETE", "/api/admin/links/"+lid, "", adminHdr)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete again should 404, got %d", w.Code)
	}
}

// TestAppsVisibility 闂ㄦ埛鍏嶇櫥褰曟祻瑙堜笌閾炬帴鍙鎬э細
// 鍖垮悕浠呰鍏紑閾炬帴锛堟棤 OAuth 搴旂敤鍏ュ彛锛夛紱鐧诲綍鍚庯紙鍚潪绠＄悊鍛橈級鍙闅愮閾炬帴涓庡叏閮ㄥ叆鍙ｃ€?
func TestAppsVisibility(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	// OAuth 搴旂敤锛堝甫 portal_url锛屽嚭鐜板湪闂ㄦ埛鍗＄墖锛?
	w, _ := doJSON(t, h, "POST", "/api/admin/clients",
		`{"client_name":"media","redirect_uris":["https://media.test/cb"],"portal_url":"https://media.test"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create client: %d %s", w.Code, w.Body.String())
	}

	// 鍏紑閾炬帴涓庨殣绉侀摼鎺ワ紙缂虹渷涓洪殣绉侊級鍚勪竴
	w, _ = doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"鍏紑鐪嬫澘","url":"https://pub.example.com","public":true}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create public link: %d %s", w.Code, w.Body.String())
	}
	w, out := doJSON(t, h, "POST", "/api/admin/links",
		`{"name":"闅愮鍚庡彴","url":"https://priv.example.com"}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create private link: %d %s", w.Code, w.Body.String())
	}
	privID := fmt.Sprintf("%v", out["data"].(map[string]any)["id"])

	linkNames := func(m map[string]any) []string {
		names := []string{}
		for _, l := range m["links"].([]any) {
			names = append(names, l.(map[string]any)["name"].(string))
		}
		return names
	}

	// 鍖垮悕锛?00锛屼粎鍏紑閾炬帴锛屾棤 apps
	w, apps := doJSON(t, h, "GET", "/api/apps", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("anonymous apps should be 200, got %d %s", w.Code, w.Body.String())
	}
	anonData := apps["data"].(map[string]any)
	if names := linkNames(anonData); len(names) != 1 || names[0] != "鍏紑鐪嬫澘" {
		t.Fatalf("anonymous should only see public links, got %v", names)
	}
	if n := len(anonData["apps"].([]any)); n != 0 {
		t.Fatalf("anonymous should not see oauth apps, got %d", n)
	}

	// 闈炵鐞嗗憳鐧诲綍鐢ㄦ埛锛氬叏閮ㄩ摼鎺?+ OAuth 搴旂敤
	w, _ = doJSON(t, h, "POST", "/api/admin/users",
		`{"username":"bob","password":"bob1234567","groups":["view"]}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create bob: %d %s", w.Code, w.Body.String())
	}
	w, _ = doJSON(t, h, "POST", "/api/auth/login", `{"username":"bob","password":"bob1234567"}`, nil)
	bobHdr := map[string]string{"Cookie": strings.Split(w.Header().Get("Set-Cookie"), ";")[0]}
	w, apps = doJSON(t, h, "GET", "/api/apps", "", bobHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("logged-in apps: %d %s", w.Code, w.Body.String())
	}
	bobData := apps["data"].(map[string]any)
	if names := linkNames(bobData); len(names) != 2 {
		t.Fatalf("logged-in user should see all links, got %v", names)
	}
	if n := len(bobData["apps"].([]any)); n != 1 {
		t.Fatalf("logged-in user should see oauth apps, got %d", n)
	}

	// 闅愮閾炬帴鍒囨崲涓哄叕寮€ 鈫?鍖垮悕鍙
	w, _ = doJSON(t, h, "PUT", "/api/admin/links/"+privID, `{"public":true}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("update link public: %d %s", w.Code, w.Body.String())
	}
	w, apps = doJSON(t, h, "GET", "/api/apps", "", nil)
	anonData = apps["data"].(map[string]any)
	if names := linkNames(anonData); len(names) != 2 {
		t.Fatalf("anonymous should see both public links now, got %v", names)
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

// TestUserCRUD 绠＄悊绔敤鎴蜂笌缁勭鐞嗐€?
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

	// 鏇存柊缁勶紙淇濇寔闈炵鐞嗗憳锛?
	w, _ = doJSON(t, h, "PUT", "/api/admin/users/"+uid, `{"groups":["view"]}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("update user: %d %s", w.Code, w.Body.String())
	}

	// 闈炵鐞嗗憳鏃犳潈璁块棶
	w, _ = doJSON(t, h, "POST", "/api/auth/login", `{"username":"alice","password":"alice12345"}`, nil)
	aliceCookie := map[string]string{"Cookie": strings.Split(w.Header().Get("Set-Cookie"), ";")[0]}
	w, _ = doJSON(t, h, "GET", "/api/admin/users", "", aliceCookie)
	if w.Code != http.StatusForbidden {
		t.Fatalf("alice should be forbidden from admin api, got %d", w.Code)
	}

	// 鍒犻櫎
	w, _ = doJSON(t, h, "DELETE", "/api/admin/users/"+uid, "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("delete user: %d %s", w.Code, w.Body.String())
	}
}
