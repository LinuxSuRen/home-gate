package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
	"github.com/linuxsuren/home-gate/internal/store"
)

// ---- 用户管理 ----

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request, admin *model.User) {
	users, err := s.db.ListUsers()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载用户失败")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for i := range users {
		out = append(out, userBrief(&users[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var req struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		Name     string   `json:"name"`
		Email    string   `json:"email"`
		Groups   []string `json:"groups"`
	}
	if err := readJSONBody(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	if req.Username == "" || len(req.Password) < 8 {
		apiError(w, http.StatusBadRequest, "invalid_request", "用户名必填，密码至少 8 位")
		return
	}
	if _, err := s.db.FindUserByUsername(req.Username); err == nil {
		apiError(w, http.StatusBadRequest, "username_taken", "用户名已存在")
		return
	}
	u := &model.User{
		Username: req.Username, PasswordHash: store.HashSecret(req.Password),
		Name: req.Name, Email: req.Email, Active: true, MustChangePasswd: true,
	}
	if err := s.db.Create(u).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "创建用户失败")
		return
	}
	if err := s.db.SetUserGroups(u, req.Groups); err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "设置用户组失败")
		return
	}
	s.db.Audit(admin.Username, "user_create", "user="+req.Username, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": userBrief(u)})
}

func parseID(r *http.Request) (uint, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	u, err := s.db.FindUserByID(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	var req struct {
		Name   *string   `json:"name"`
		Email  *string   `json:"email"`
		Active *bool     `json:"active"`
		Groups *[]string `json:"groups"`
	}
	if err := readJSONBody(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Email != nil {
		updates["email"] = *req.Email
	}
	if req.Active != nil {
		updates["active"] = *req.Active
	}
	if len(updates) > 0 {
		if err := s.db.Model(u).Updates(updates).Error; err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "更新用户失败")
			return
		}
	}
	if req.Groups != nil {
		if err := s.db.SetUserGroups(u, *req.Groups); err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "设置用户组失败")
			return
		}
	}
	s.db.Audit(admin.Username, "user_update", "user="+u.Username, clientIP(r))
	updated, _ := s.db.FindUserByID(id)
	writeJSON(w, http.StatusOK, map[string]any{"data": userBrief(updated)})
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	u, err := s.db.FindUserByID(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	if u.Username == "admin" {
		apiError(w, http.StatusBadRequest, "protected_account", "内置管理员不可删除")
		return
	}
	if err := s.db.Delete(u).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "删除用户失败")
		return
	}
	s.db.Audit(admin.Username, "user_delete", "user="+u.Username, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	u, err := s.db.FindUserByID(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSONBody(r, &req); err != nil || len(req.Password) < 8 {
		apiError(w, http.StatusBadRequest, "invalid_request", "密码至少 8 位")
		return
	}
	if err := s.db.Model(u).Updates(map[string]any{
		"password_hash": store.HashSecret(req.Password), "must_change_passwd": true,
	}).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "重置密码失败")
		return
	}
	s.db.Audit(admin.Username, "user_reset_password", "user="+u.Username, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

// ---- 用户组 ----

func (s *Server) handleAdminListGroups(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var gs []model.Group
	if err := s.db.Order("id").Find(&gs).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载用户组失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": gs})
}

func (s *Server) handleAdminCreateGroup(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var req struct {
		Name   string `json:"name"`
		Remark string `json:"remark"`
	}
	if err := readJSONBody(r, &req); err != nil || req.Name == "" {
		apiError(w, http.StatusBadRequest, "invalid_request", "组名不能为空")
		return
	}
	g := model.Group{Name: req.Name, Remark: req.Remark}
	if err := s.db.Create(&g).Error; err != nil {
		apiError(w, http.StatusBadRequest, "group_taken", "组名已存在")
		return
	}
	s.db.Audit(admin.Username, "group_create", "group="+req.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": g})
}

func (s *Server) handleAdminDeleteGroup(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	var g model.Group
	if err := s.db.First(&g, id).Error; err != nil {
		apiError(w, http.StatusNotFound, "not_found", "用户组不存在")
		return
	}
	switch g.Name {
	case "admin", "operate", "view":
		apiError(w, http.StatusBadRequest, "protected_group", "内置组不可删除")
		return
	}
	if err := s.db.Select("Users").Delete(&g).Error; err != nil { // 级联清理关联
		apiError(w, http.StatusInternalServerError, "server_error", "删除用户组失败")
		return
	}
	s.db.Audit(admin.Username, "group_delete", "group="+g.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

// ---- 客户端管理 ----

func (s *Server) handleAdminListClients(w http.ResponseWriter, r *http.Request, admin *model.User) {
	clients, err := s.db.ListClients()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载客户端失败")
		return
	}
	out := make([]map[string]any, 0, len(clients))
	for _, c := range clients {
		out = append(out, map[string]any{
			"id": c.ID, "client_id": c.ClientID, "name": c.Name,
			"redirect_uris": parseRedirectURIs(c.RedirectURIs), "scopes": c.Scopes,
			"token_endpoint_auth_method": c.TokenEndpointAuthMethod,
			"portal_url": c.PortalURL, "active": c.Active,
			"description": c.Description, "logo_url": c.LogoURL,
			"issued_at": c.IssuedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// handleAdminCreateClient POST /api/admin/clients（管理员）
// 管理员代第三方系统直接注册客户端（如 GitLab 等自己管理的系统，
// 无需走 initial access token 自助流程）；secret 仅返回一次。
func (s *Server) handleAdminCreateClient(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var meta dcrMetadata
	if err := readJSONBody(r, &meta); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	if meta.ClientName == "" {
		apiError(w, http.StatusBadRequest, "invalid_client_metadata", "名称不能为空")
		return
	}
	if err := meta.normalize(); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}

	clientID := "gate-" + store.RandomToken(12)
	secret := ""
	secretHash := ""
	if meta.TokenEndpointAuthMethod != "none" {
		secret = store.RandomToken(32)
		secretHash = store.HashSecret(secret)
	}
	regToken := store.RandomToken(32)
	uris, _ := json.Marshal(meta.RedirectURIs)
	gm, _ := json.Marshal(meta.GroupsMap)
	client := &model.Client{
		ClientID: clientID, ClientSecretHash: secretHash,
		Name: meta.ClientName, RedirectURIs: string(uris),
		Scopes: store.TrimScope(meta.Scope),
		TokenEndpointAuthMethod: meta.TokenEndpointAuthMethod,
		PortalURL: meta.PortalURL, Description: meta.Description, LogoURL: meta.LogoURL,
		GroupsMap: string(gm),
		RegTokenHash: store.HashSecret(regToken),
		Active: true, IssuedAt: time.Now(),
	}
	if err := s.db.Create(client).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "创建客户端失败")
		return
	}
	s.db.Audit(admin.Username, "client_create", "client="+clientID+" name="+meta.ClientName, clientIP(r))
	resp := map[string]any{
		"client_id": clientID, "client_secret": secret,
		"registration_access_token": regToken, // RFC 7592 管理凭据（可选使用）
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resp})
}

func (s *Server) handleAdminUpdateClient(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	var c model.Client
	if err := s.db.First(&c, id).Error; err != nil {
		apiError(w, http.StatusNotFound, "not_found", "客户端不存在")
		return
	}
	var req struct {
		Active      *bool   `json:"active"`
		PortalURL   *string `json:"portal_url"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		LogoURL     *string `json:"logo_url"`
	}
	if err := readJSONBody(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	updates := map[string]any{}
	if req.Active != nil {
		updates["active"] = *req.Active
	}
	if req.PortalURL != nil {
		updates["portal_url"] = *req.PortalURL
	}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.LogoURL != nil {
		updates["logo_url"] = *req.LogoURL
	}
	if len(updates) > 0 {
		if err := s.db.Model(&c).Updates(updates).Error; err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "更新客户端失败")
			return
		}
	}
	s.db.Audit(admin.Username, "client_update", "client="+c.ClientID, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

func (s *Server) handleAdminResetSecret(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	var c model.Client
	if err := s.db.First(&c, id).Error; err != nil {
		apiError(w, http.StatusNotFound, "not_found", "客户端不存在")
		return
	}
	if c.TokenEndpointAuthMethod == "none" {
		apiError(w, http.StatusBadRequest, "invalid_request", "公共客户端无 secret")
		return
	}
	secret := store.RandomToken(32)
	if err := s.db.Model(&c).Update("client_secret_hash", store.HashSecret(secret)).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "重置 secret 失败")
		return
	}
	s.db.Audit(admin.Username, "client_reset_secret", "client="+c.ClientID, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"client_secret": secret}})
}

// ---- DCR initial access token ----

func (s *Server) handleAdminListInitialTokens(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var its []model.InitialToken
	if err := s.db.Order("id DESC").Find(&its).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载失败")
		return
	}
	for i := range its {
		its[i].TokenHash = "" // 不回传哈希
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": its})
}

func (s *Server) handleAdminCreateInitialToken(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var req struct {
		Remark string `json:"remark"`
		Days   int    `json:"days"` // 有效天数，默认 7
	}
	if err := readJSONBody(r, &req); err != nil {
		req.Remark = ""
	}
	if req.Days <= 0 {
		req.Days = 7
	}
	token := store.RandomToken(32)
	it := &model.InitialToken{
		TokenHash: store.HashSecret(token), Remark: req.Remark,
		ExpiresAt: time.Now().AddDate(0, 0, req.Days),
	}
	if err := s.db.Create(it).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "签发失败")
		return
	}
	s.db.Audit(admin.Username, "initial_token_create", "remark="+req.Remark, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id": it.ID, "token": token, "expires_at": it.ExpiresAt,
	}})
}

func (s *Server) handleAdminRevokeInitialToken(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	var it model.InitialToken
	if err := s.db.First(&it, id).Error; err != nil {
		apiError(w, http.StatusNotFound, "not_found", "令牌不存在")
		return
	}
	if err := s.db.Model(&it).Update("revoked", true).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "吊销失败")
		return
	}
	s.db.Audit(admin.Username, "initial_token_revoke", strconv.FormatUint(uint64(id), 10), clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

// ---- 审计 / 设置 ----

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request, admin *model.User) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := s.db.Model(&model.AuditLog{})
	if action := q.Get("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	if actor := q.Get("actor"); actor != "" {
		query = query.Where("actor = ?", actor)
	}
	var total int64
	query.Count(&total)
	var logs []model.AuditLog
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载审计失败")
		return
	}
	if logs == nil {
		logs = []model.AuditLog{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": logs, "page": page, "page_size": pageSize, "total": total,
	})
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request, admin *model.User) {
	appID, _, _ := s.feishuConfig()
	// 应用接入端点：接入方按此填写（非 OIDC 发现的需显式端点）
	base := s.baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"feishu_app_id": appID, "feishu_enabled": appID != "",
		// 登录会话有效时长（小时）：默认 24（当天有效），管理员可调
		"session_ttl_hours": s.db.GetSettingInt("session_ttl_hours", 24),
		// 飞书登录回调地址：需加入飞书开放平台应用的重定向 URL 白名单
		"feishu_callback": base + "/api/auth/feishu/callback",
		"oidc_endpoints": map[string]string{
			"discovery": base + "/.well-known/openid-configuration",
			"authorize": base + "/oauth2/authorize",
			"token":     base + "/oauth2/token",
			"userinfo":  base + "/oauth2/userinfo",
			"jwks":      base + "/.well-known/jwks.json",
		},
		// 推荐 scope：openid 走 OIDC 标准声明；offline_access 才会签发 refresh_token
		"oidc_scope": "openid profile email",
	}})
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var req struct {
		FeishuAppID      string `json:"feishu_app_id"`
		FeishuAppSecret  string `json:"feishu_app_secret"`
		SessionTTLHours  *int   `json:"session_ttl_hours"`
	}
	if err := readJSONBody(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	detail := ""
	if req.SessionTTLHours != nil {
		if *req.SessionTTLHours < 1 || *req.SessionTTLHours > 720 {
			apiError(w, http.StatusBadRequest, "invalid_request", "会话时长需在 1～720 小时之间")
			return
		}
		if err := s.db.SetSetting("session_ttl_hours", strconv.Itoa(*req.SessionTTLHours)); err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "保存设置失败")
			return
		}
		detail += fmt.Sprintf("会话时长=%dh ", *req.SessionTTLHours)
	}
	if req.FeishuAppID != "" {
		if err := s.db.SetSetting("feishu_app_id", req.FeishuAppID); err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "保存设置失败")
			return
		}
	}
	if req.FeishuAppSecret != "" {
		if err := s.db.SetSetting("feishu_app_secret", req.FeishuAppSecret); err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "保存设置失败")
			return
		}
		detail += "飞书上游配置"
	}
	if detail == "" {
		detail = "无变更"
	}
	s.db.Audit(admin.Username, "settings_update", strings.TrimSpace(detail), clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}
