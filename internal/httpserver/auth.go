package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/linuxsuren/home-gate/internal/model"
	"github.com/linuxsuren/home-gate/internal/store"
)

func (s *Server) setSessionCookie(w http.ResponseWriter, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sid, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 24 * 3600,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// handleLogin 本地账号密码登录。支持 JSON 与 form 两种请求体。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if isJSONReq(r) {
		if err := readJSONBody(r, &req); err != nil {
			apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
			return
		}
	} else {
		req.Username = r.PostFormValue("username")
		req.Password = r.PostFormValue("password")
	}
	if req.Username == "" || req.Password == "" {
		apiError(w, http.StatusBadRequest, "invalid_request", "请输入用户名和密码")
		return
	}
	u, err := s.db.FindUserByUsername(req.Username)
	if err != nil || u.PasswordHash == "" || !u.Active ||
		bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		s.db.Audit(req.Username, "login_failed", "本地账号密码错误", clientIP(r))
		apiError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	sess, err := s.db.CreateSession(u.ID, clientIP(r))
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "创建会话失败")
		return
	}
	s.db.Audit(u.Username, "login", "本地账号登录", clientIP(r))
	s.setSessionCookie(w, sess.ID)
	writeJSON(w, http.StatusOK, map[string]any{"data": userBrief(u)})
}

func userBrief(u *model.User) map[string]any {
	groups := u.GroupNames()
	if groups == nil {
		groups = []string{}
	}
	return map[string]any{
		"id": u.ID, "username": u.Username, "name": u.Name,
		"email": u.Email, "groups": groups, "active": u.Active,
		"must_change_password": u.MustChangePasswd, "feishu": u.FeishuUnionID != "",
	}
}

func (s *Server) handleLogoutAPI(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if u := s.currentUser(r); u != nil {
			s.db.Audit(u.Username, "logout", "", clientIP(r))
		}
		s.db.DeleteSession(c.Value)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		apiError(w, http.StatusUnauthorized, "unauthorized", "未登录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": userBrief(u)})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		apiError(w, http.StatusUnauthorized, "unauthorized", "未登录")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := readJSONBody(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	if len(req.NewPassword) < 8 {
		apiError(w, http.StatusBadRequest, "weak_password", "新密码至少 8 位")
		return
	}
	if u.PasswordHash != "" {
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)) != nil {
			apiError(w, http.StatusBadRequest, "invalid_credentials", "原密码错误")
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "密码更新失败")
		return
	}
	if err := s.db.Model(u).Updates(map[string]any{
		"password_hash": string(hash), "must_change_passwd": false,
	}).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "密码更新失败")
		return
	}
	s.db.Audit(u.Username, "change_password", "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

// ---- 飞书上游联邦登录 ----

func (s *Server) feishuConfig() (appID, appSecret string, enabled bool) {
	appID = s.db.GetSetting("feishu_app_id")
	appSecret = s.db.GetSetting("feishu_app_secret")
	return appID, appSecret, appID != "" && appSecret != ""
}

// safeReturnTo 仅允许站内相对路径，防开放重定向。
func safeReturnTo(v string) string {
	if v == "" || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
		return "/"
	}
	return v
}

func (s *Server) handleFeishuStart(w http.ResponseWriter, r *http.Request) {
	appID, _, enabled := s.feishuConfig()
	if !enabled {
		apiError(w, http.StatusNotImplemented, "feishu_disabled", "飞书登录未配置")
		return
	}
	state := store.RandomToken(24)
	ret := safeReturnTo(r.URL.Query().Get("next"))
	if err := s.db.Create(&model.FeishuState{
		State: state, ReturnTo: ret,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "创建 state 失败")
		return
	}
	redirect := s.baseURL(r) + "/api/auth/feishu/callback"
	feishuURL := fmt.Sprintf(
		"https://open.feishu.cn/open-apis/authen/v1/authorize?app_id=%s&redirect_uri=%s&state=%s",
		url.QueryEscape(appID), url.QueryEscape(redirect), url.QueryEscape(state))
	http.Redirect(w, r, feishuURL, http.StatusFound)
}

func (s *Server) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	stateID := r.URL.Query().Get("state")
	var st model.FeishuState
	if err := s.db.Where("state = ?", stateID).First(&st).Error; err != nil || time.Now().After(st.ExpiresAt) {
		apiError(w, http.StatusBadRequest, "invalid_state", "登录状态已过期，请重新登录")
		return
	}
	s.db.Delete(&st)

	_, appSecret, _ := s.feishuConfig()
	appID := s.db.GetSetting("feishu_app_id")
	u, err := s.feishuExchangeLogin(r, appID, appSecret, code)
	if err != nil {
		s.db.Audit("feishu", "login_failed", err.Error(), clientIP(r))
		apiError(w, http.StatusUnauthorized, "feishu_error", "飞书登录失败，请重试")
		return
	}
	sess, err := s.db.CreateSession(u.ID, clientIP(r))
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "创建会话失败")
		return
	}
	s.db.Audit(u.Username, "login", "飞书登录", clientIP(r))
	s.setSessionCookie(w, sess.ID)
	http.Redirect(w, r, st.ReturnTo, http.StatusFound)
}

// feishuExchangeLogin 用 code 换 user_access_token 并取用户信息，
// 按 union_id 命中或首次登录自动开户（默认 view 组）。
func (s *Server) feishuExchangeLogin(r *http.Request, appID, appSecret, code string) (*model.User, error) {
	tokResp, err := http.PostForm("https://open.feishu.cn/open-apis/authen/v2/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {appID},
		"client_secret": {appSecret},
		"code":          {code},
		"redirect_uri":  {s.baseURL(r) + "/api/auth/feishu/callback"},
	})
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer tokResp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokResp.Body, 1<<20)).Decode(&tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("token exchange failed")
	}

	infoResp, err := (&http.Client{Timeout: 10 * time.Second}).Do(&http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "https", Host: "open.feishu.cn", Path: "/open-apis/authen/v1/user_info"},
		Header: http.Header{"Authorization": {"Bearer " + tok.AccessToken}},
	})
	if err != nil {
		return nil, fmt.Errorf("userinfo: %w", err)
	}
	defer infoResp.Body.Close()
	var info struct {
		Code int `json:"code"`
		Data struct {
			UnionID     string `json:"union_id"`
			Name        string `json:"name"`
			Email       string `json:"email"`
			EnterpriseEmail string `json:"enterprise_email"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(infoResp.Body, 1<<20)).Decode(&info); err != nil || info.Code != 0 || info.Data.UnionID == "" {
		return nil, fmt.Errorf("userinfo failed")
	}
	email := info.Data.Email
	if email == "" {
		email = info.Data.EnterpriseEmail
	}

	if u, err := s.db.FindUserByFeishu(info.Data.UnionID); err == nil {
		if !u.Active {
			return nil, fmt.Errorf("账号已禁用")
		}
		return u, nil
	}
	// 首次登录自动开户，默认 view 组
	var viewGroup model.Group
	if err := s.db.Where("name = ?", "view").First(&viewGroup).Error; err != nil {
		return nil, err
	}
	username := info.Data.Name
	if username == "" {
		username = "feishu-" + info.Data.UnionID[:8]
	}
	// 用户名去重
	base := username
	for i := 1; ; i++ {
		if _, err := s.db.FindUserByUsername(username); err != nil {
			break
		}
		username = fmt.Sprintf("%s%d", base, i)
	}
	newUser := &model.User{
		Username: username, Name: info.Data.Name, Email: email,
		FeishuUnionID: info.Data.UnionID, Active: true,
		Groups: []model.Group{viewGroup},
	}
	if err := s.db.Create(newUser).Error; err != nil {
		return nil, err
	}
	s.db.Audit(username, "auto_provision", "飞书首次登录自动开户", clientIP(r))
	return newUser, nil
}

// handleApps 已接入系统入口卡片：OAuth 应用（DCR 注册的 portal_url）+ 纯链接入口。
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request, u *model.User) {
	clients, err := s.db.ListClients()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载失败")
		return
	}
	type app struct {
		ClientID    string `json:"client_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		LogoURL     string `json:"logo_url"`
		PortalURL   string `json:"portal_url"` // 仅作跳转目标，不在卡片上展示
	}
	apps := []app{}
	for _, c := range clients {
		if c.Active && c.PortalURL != "" {
			apps = append(apps, app{
				ClientID: c.ClientID, Name: c.Name,
				Description: c.Description, LogoURL: c.LogoURL,
				PortalURL: c.PortalURL,
			})
		}
	}
	var linkRows []model.AppLink
	if err := s.db.Where("active = ?", true).Order("sort").Order("id").Find(&linkRows).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载失败")
		return
	}
	type link struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		LogoURL     string `json:"logo_url"`
		URL         string `json:"url"` // 跳转目标
	}
	links := []link{}
	for _, l := range linkRows {
		links = append(links, link{
			Name: l.Name, Description: l.Description, LogoURL: l.LogoURL, URL: l.URL,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"apps": apps, "links": links,
	}})
}
