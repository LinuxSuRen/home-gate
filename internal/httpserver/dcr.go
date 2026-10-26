package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
	"github.com/linuxsuren/home-gate/internal/store"
)

// dcrMetadata RFC 7591 注册元数据（含本系统扩展字段 portal_url / groups_map）。
type dcrMetadata struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	Scope                   string   `json:"scope"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	PortalURL               string   `json:"portal_url"`   // 扩展：门户入口页跳转地址
	Description             string   `json:"description"`  // 扩展：应用描述（门户卡片展示）
	LogoURL                 string   `json:"logo_url"`     // 扩展：应用 logo 地址
	Subdomain               string   `json:"subdomain"`    // 扩展：子站标签（仅管理员接口生效，DCR 自助注册不开放，防抢注）
	GroupsMap               map[string]string `json:"groups_map"` // 扩展：gate 组名 -> 子系统角色
}

func (m *dcrMetadata) normalize() error {
	if len(m.RedirectURIs) == 0 {
		return errors.New("redirect_uris 不能为空")
	}
	for _, u := range m.RedirectURIs {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return errors.New("redirect_uri 必须是 http(s) 地址")
		}
	}
	if m.TokenEndpointAuthMethod == "" {
		m.TokenEndpointAuthMethod = "client_secret_basic"
	}
	switch m.TokenEndpointAuthMethod {
	case "client_secret_basic", "client_secret_post", "none":
	default:
		return errors.New("不支持的 token_endpoint_auth_method")
	}
	if m.Scope == "" {
		m.Scope = "openid profile email offline_access"
	}
	return nil
}

// handleDCRCreate RFC 7591 受保护注册：Authorization: Bearer <initial access token>。
func (s *Server) handleDCRCreate(w http.ResponseWriter, r *http.Request) {
	if !s.dcrAuthorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dcr"`)
		apiError(w, http.StatusUnauthorized, "invalid_token", "需要有效的 initial access token")
		return
	}
	var meta dcrMetadata
	if err := readJSONBody(r, &meta); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_client_metadata", "请求体不是合法 JSON")
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
	// 注意：subdomain 不在此持久化——子站标签仅管理员可配（防止经 DCR 抢注他人子站）
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
		apiError(w, http.StatusInternalServerError, "server_error", "注册失败")
		return
	}
	s.db.Audit("dcr", "dcr_register", "client="+clientID+" name="+meta.ClientName, clientIP(r))

	resp := map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        client.IssuedAt.Unix(),
		"client_name":                meta.ClientName,
		"redirect_uris":              meta.RedirectURIs,
		"scope":                      client.Scopes,
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"registration_client_uri":    s.baseURL(r) + "/oauth2/register/" + clientID,
		"registration_access_token":  regToken,
		"portal_url":                 meta.PortalURL,
		"groups_map":                 meta.GroupsMap,
	}
	if secret != "" {
		// secret 仅注册时返回一次
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	writeJSON(w, http.StatusCreated, resp)
}

// dcrAuthorized 校验 Bearer initial access token。
func (s *Server) dcrAuthorized(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	_, ok := s.db.ValidInitialToken(strings.TrimPrefix(auth, "Bearer "))
	return ok
}

// dcrManageAuthorized RFC 7592：registration access token 或 initial token 均可管理。
func (s *Server) dcrManageAuthorized(r *http.Request, client *model.Client) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	tok := strings.TrimPrefix(auth, "Bearer ")
	if store.CheckSecret(client.RegTokenHash, tok) {
		return true
	}
	_, ok := s.db.ValidInitialToken(tok)
	return ok
}

func (s *Server) handleDCRGet(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("client_id")
	client, err := s.db.FindClient(clientID)
	if err != nil {
		apiError(w, http.StatusNotFound, "invalid_client", "客户端不存在")
		return
	}
	if !s.dcrManageAuthorized(r, client) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dcr"`)
		apiError(w, http.StatusUnauthorized, "invalid_token", "管理凭据无效")
		return
	}
	writeJSON(w, http.StatusOK, s.clientMetadata(client, r))
}

func (s *Server) clientMetadata(c *model.Client, r *http.Request) map[string]any {
	var gm map[string]string
	_ = json.Unmarshal([]byte(c.GroupsMap), &gm)
	return map[string]any{
		"client_id":                  c.ClientID,
		"client_id_issued_at":        c.IssuedAt.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              parseRedirectURIs(c.RedirectURIs),
		"scope":                      c.Scopes,
		"token_endpoint_auth_method": c.TokenEndpointAuthMethod,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"registration_client_uri":    s.baseURL(r) + "/oauth2/register/" + c.ClientID,
		"portal_url":                 c.PortalURL,
		"description":                c.Description,
		"logo_url":                   c.LogoURL,
		"groups_map":                 gm,
	}
}

func (s *Server) handleDCRUpdate(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("client_id")
	client, err := s.db.FindClient(clientID)
	if err != nil {
		apiError(w, http.StatusNotFound, "invalid_client", "客户端不存在")
		return
	}
	if !s.dcrManageAuthorized(r, client) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dcr"`)
		apiError(w, http.StatusUnauthorized, "invalid_token", "管理凭据无效")
		return
	}
	var meta dcrMetadata
	if err := readJSONBody(r, &meta); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_client_metadata", "请求体不是合法 JSON")
		return
	}
	if err := meta.normalize(); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	uris, _ := json.Marshal(meta.RedirectURIs)
	gm, _ := json.Marshal(meta.GroupsMap)
	updates := map[string]any{
		"name": meta.ClientName, "redirect_uris": string(uris),
		"scopes": store.TrimScope(meta.Scope),
		"token_endpoint_auth_method": meta.TokenEndpointAuthMethod,
		"portal_url": meta.PortalURL, "groups_map": string(gm),
		"description": meta.Description, "logo_url": meta.LogoURL,
	}
	if err := s.db.Model(client).Updates(updates).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "更新失败")
		return
	}
	s.db.Audit("dcr", "dcr_update", "client="+clientID, clientIP(r))
	updated, _ := s.db.FindClient(clientID)
	writeJSON(w, http.StatusOK, s.clientMetadata(updated, r))
}

func (s *Server) handleDCRDelete(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("client_id")
	client, err := s.db.FindClient(clientID)
	if err != nil {
		apiError(w, http.StatusNotFound, "invalid_client", "客户端不存在")
		return
	}
	if !s.dcrManageAuthorized(r, client) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dcr"`)
		apiError(w, http.StatusUnauthorized, "invalid_token", "管理凭据无效")
		return
	}
	if err := s.db.Delete(client).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "删除失败")
		return
	}
	s.db.Audit("dcr", "dcr_delete", "client="+clientID, clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}
