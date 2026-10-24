package httpserver

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
	"github.com/linuxsuren/home-gate/internal/store"
	"github.com/linuxsuren/home-gate/internal/token"
)

const (
	authCodeTTL     = 5 * time.Minute
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
)

// baseURL 外部可达基础地址；未配置 issuer 时按请求 Host 推断。
func (s *Server) baseURL(r *http.Request) string {
	if s.issuer != "" {
		return s.issuer
	}
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	iss := s.baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/oauth2/authorize",
		"token_endpoint":                        iss + "/oauth2/token",
		"userinfo_endpoint":                     iss + "/oauth2/userinfo",
		"jwks_uri":                              iss + "/.well-known/jwks.json",
		"end_session_endpoint":                  iss + "/oauth2/logout",
		"registration_endpoint":                 iss + "/oauth2/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"EdDSA"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"scopes_supported":                      []string{"openid", "profile", "email", "offline_access"},
		"claims_supported":                      []string{"sub", "iss", "aud", "exp", "iat", "name", "email", "preferred_username", "groups", "nonce"},
	})
}

func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.tm.JWKS())
}

// handleAuthorize 授权端点。已有会话即静默发码（SSO 核心）；无会话跳登录页。
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	scope := store.TrimScope(q.Get("scope"))
	if scope == "" {
		scope = "openid profile"
	}

	client, err := s.db.FindClient(clientID)
	if err != nil || !client.Active {
	 authorizeError(w, r, redirectURI, "invalid_client", "客户端不存在或已停用")
		return
	}
	if !redirectURIAllowed(parseRedirectURIs(client.RedirectURIs), redirectURI) {
		authorizeError(w, r, redirectURI, "invalid_redirect_uri", "回调地址未注册")
		return
	}
	if rt := q.Get("response_type"); rt != "code" {
		authorizeError(w, r, redirectURI, "unsupported_response_type", "仅支持 code")
		return
	}
	challenge, method := q.Get("code_challenge"), q.Get("code_challenge_method")
	if client.TokenEndpointAuthMethod == "none" && challenge == "" {
		authorizeError(w, r, redirectURI, "invalid_request", "公共客户端必须携带 PKCE")
		return
	}

	u := s.currentUser(r)
	if u == nil {
		// 跳转登录页，登录后回跳本 URL
		loginURL := "/login?next=" + url.QueryEscape(r.URL.RequestURI())
		if q.Get("prompt") == "none" {
			authorizeError(w, r, redirectURI, "login_required", "需要登录")
			return
		}
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}
	if q.Get("prompt") == "login" {
		// 强制重新登录：销毁当前会话后跳登录页
		if c, err := r.Cookie(sessionCookie); err == nil {
			s.db.DeleteSession(c.Value)
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	if u.MustChangePasswd {
		authorizeError(w, r, redirectURI, "account_password_change_required", "请先在门户修改初始密码")
		return
	}

	code := store.RandomToken(32)
	ac := &model.AuthCode{
		Code: code, ClientID: clientID, UserID: u.ID,
		RedirectURI: redirectURI, Scope: scope,
		Nonce: q.Get("nonce"), CodeChallenge: challenge,
		CodeChallengeMethod: method,
		ExpiresAt: time.Now().Add(authCodeTTL),
	}
	if err := s.db.Create(ac).Error; err != nil {
		authorizeError(w, r, redirectURI, "server_error", "服务内部错误")
		return
	}
	s.db.Audit(u.Username, "authorize", "client="+clientID+" scope="+scope, clientIP(r))

	v := url.Values{}
	v.Set("code", code)
	if state != "" {
		v.Set("state", state)
	}
	v.Set("iss", s.baseURL(r))
	http.Redirect(w, r, redirectURI+"?"+v.Encode(), http.StatusFound)
}

func authorizeError(w http.ResponseWriter, r *http.Request, redirectURI, code, desc string) {
	// 回调地址不可信时不能重定向，直接回错误 JSON
	if redirectURI == "" || !strings.HasPrefix(redirectURI, "http") {
		apiError(w, http.StatusBadRequest, code, desc)
		return
	}
	v := url.Values{}
	v.Set("error", code)
	v.Set("error_description", desc)
	http.Redirect(w, r, redirectURI+"?"+v.Encode(), http.StatusFound)
}

// handleToken 授权码 / 刷新令牌两种 grant。
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request", "请求格式错误")
		return
	}
	client, ok := s.authenticateClient(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth2"`)
		tokenError(w, "invalid_client", "客户端认证失败")
		return
	}
	switch formValue(r, "grant_type") {
	case "authorization_code":
		s.tokenAuthorizationCode(w, r, client)
	case "refresh_token":
		s.tokenRefresh(w, r, client)
	default:
		tokenError(w, "unsupported_grant_type", "不支持的 grant_type")
	}
}

// authenticateClient 按 token_endpoint_auth_method 校验客户端。
func (s *Server) authenticateClient(r *http.Request) (*model.Client, bool) {
	clientID, clientSecret, hasBasic := r.BasicAuth()
	if !hasBasic {
		clientID = formValue(r, "client_id")
		clientSecret = formValue(r, "client_secret")
	}
	if clientID == "" {
		return nil, false
	}
	client, err := s.db.FindClient(clientID)
	if err != nil || !client.Active {
		return nil, false
	}
	switch client.TokenEndpointAuthMethod {
	case "none":
		// 公共客户端：无 secret，安全依赖 PKCE
		return client, true
	default:
		if clientSecret == "" || !store.CheckSecret(client.ClientSecretHash, clientSecret) {
			return nil, false
		}
		return client, true
	}
}

func tokenError(w http.ResponseWriter, code, desc string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code, "error_description": desc})
}

func (s *Server) tokenAuthorizationCode(w http.ResponseWriter, r *http.Request, client *model.Client) {
	code := formValue(r, "code")
	redirectURI := formValue(r, "redirect_uri")

	var ac model.AuthCode
	if err := s.db.Where("code = ?", code).First(&ac).Error; err != nil {
		println("DBG lookup fail:", err.Error(), "code=", code)
		tokenError(w, "invalid_grant", "授权码无效")
		return
	}

	// 先校验原始值再置 used（GORM 的 Update 会同步改写结构体字段）
	if ac.Used || time.Now().After(ac.ExpiresAt) {
		tokenError(w, "invalid_grant", "授权码无效")
		return
	}
	s.db.Model(&ac).Update("used", true)

	if ac.ClientID != client.ClientID || ac.RedirectURI != redirectURI {
		tokenError(w, "invalid_grant", "授权码与客户端不匹配")
		return
	}
	// PKCE 校验
	if ac.CodeChallenge != "" {
		verifier := formValue(r, "code_verifier")
		if !verifyPKCE(ac.CodeChallenge, verifier, ac.CodeChallengeMethod) {
			tokenError(w, "invalid_grant", "PKCE 校验失败")
			return
		}
	} else if client.TokenEndpointAuthMethod == "none" {
		tokenError(w, "invalid_grant", "缺少 PKCE")
		return
	}
	s.issueTokens(w, r, ac.UserID, client, ac.Scope, ac.Nonce)
}

func verifyPKCE(challenge, verifier, method string) bool {
	if verifier == "" {
		return false
	}
	switch method {
	case "S256", "":
		sum := sha256.Sum256([]byte(verifier))
		return subtle.ConstantTimeCompare([]byte(challenge),
			[]byte(base64.RawURLEncoding.EncodeToString(sum[:]))) == 1
	case "plain":
		return subtle.ConstantTimeCompare([]byte(challenge), []byte(verifier)) == 1
	}
	return false
}

func (s *Server) tokenRefresh(w http.ResponseWriter, r *http.Request, client *model.Client) {
	raw := formValue(r, "refresh_token")
	hash := hashToken(raw)
	var rt model.RefreshToken
	if err := s.db.Where("hash = ?", hash).First(&rt).Error; err != nil {
		tokenError(w, "invalid_grant", "刷新令牌无效")
		return
	}
	// 先校验原始值再轮换吊销（Update 会同步改写结构体字段）
	if rt.Revoked || time.Now().After(rt.ExpiresAt) || rt.ClientID != client.ClientID {
		s.db.Model(&rt).Update("revoked", true) // 已泄漏的旧令牌再次使用，强制吊销
		tokenError(w, "invalid_grant", "刷新令牌无效")
		return
	}
	s.db.Model(&rt).Updates(map[string]any{"revoked": true})
	s.issueTokens(w, r, rt.UserID, client, rt.Scope, "")
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// issueTokens 签发 access/id/refresh token 并写入响应。
func (s *Server) issueTokens(w http.ResponseWriter, r *http.Request, userID uint, client *model.Client, scope, nonce string) {
	u, err := s.db.FindUserByID(userID)
	if err != nil || !u.Active {
		tokenError(w, "invalid_grant", "用户不存在或已禁用")
		return
	}
	now := time.Now()
	groups := u.GroupNames()

	access := &token.Claims{
		Iss: s.baseURL(r), Sub: u.Username, Aud: client.ClientID,
		Exp: now.Add(accessTokenTTL).Unix(), Iat: now.Unix(),
		Scope: scope, Groups: groups,
	}
	accessStr, err := s.tm.Sign(access)
	if err != nil {
		tokenError(w, "server_error", "签发失败")
		return
	}

	resp := map[string]any{
		"access_token": accessStr,
		"token_type":   "Bearer",
		"expires_in":   int(accessTokenTTL.Seconds()),
		"scope":        scope,
	}

	if scopeHas(scope, "openid") {
		idc := &token.Claims{
			Iss: s.baseURL(r), Sub: u.Username, Aud: client.ClientID,
			Exp: now.Add(accessTokenTTL).Unix(), Iat: now.Unix(),
			Nonce: nonce, Name: u.Name, Email: u.Email,
			Prefer: u.Username, Groups: groups,
		}
		if idStr, err := s.tm.Sign(idc); err == nil {
			resp["id_token"] = idStr
		}
	}

	if scopeHas(scope, "offline_access") {
		raw := store.RandomToken(32)
		rt := &model.RefreshToken{
			Token: raw, Hash: hashToken(raw), ClientID: client.ClientID,
			UserID: u.ID, Scope: scope,
			ExpiresAt: now.Add(refreshTokenTTL), CreatedAt: now,
		}
		if err := s.db.Create(rt).Error; err == nil {
			resp["refresh_token"] = raw
		}
	}
	s.db.Audit(u.Username, "token", "client="+client.ClientID+" grant="+formValue(r, "grant_type"), clientIP(r))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUserinfo(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="userinfo"`)
		apiError(w, http.StatusUnauthorized, "invalid_token", "缺少 access token")
		return
	}
	claims, err := s.tm.Parse(strings.TrimPrefix(auth, "Bearer "), s.issuer, "")
	if err != nil || !scopeHas(claims.Scope, "openid") {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		apiError(w, http.StatusUnauthorized, "invalid_token", "access token 无效")
		return
	}
	u, err := s.db.FindUserByUsername(claims.Sub)
	if err != nil || !u.Active {
		apiError(w, http.StatusUnauthorized, "invalid_token", "用户不存在或已禁用")
		return
	}
	groups := claims.Groups
	if groups == nil {
		groups = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sub": u.Username, "name": u.Name, "email": u.Email,
		"preferred_username": u.Username, "groups": groups,
	})
}

// handleLogout RP-initiated logout：销毁会话，可选回跳。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	postLogout := r.URL.Query().Get("post_logout_redirect_uri")
	if postLogout != "" {
		// 仅允许回跳到已注册客户端的回调域，防开放重定向
		if client := s.clientByRedirectDomain(postLogout); client != nil {
			if c, err := r.Cookie(sessionCookie); err == nil {
				s.db.DeleteSession(c.Value)
			}
			http.Redirect(w, r, postLogout, http.StatusFound)
			return
		}
		apiError(w, http.StatusBadRequest, "invalid_request", "回跳地址不在已注册范围内")
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.db.DeleteSession(c.Value)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) clientByRedirectDomain(u string) *model.Client {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme == "" {
		return nil
	}
	clients, err := s.db.ListClients()
	if err != nil {
		return nil
	}
	for i := range clients {
		for _, ru := range parseRedirectURIs(clients[i].RedirectURIs) {
			if p, err := url.Parse(ru); err == nil && p.Scheme == parsed.Scheme && p.Host == parsed.Host {
				return &clients[i]
			}
		}
	}
	return nil
}
