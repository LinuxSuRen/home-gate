package httpserver

import (
	"net/http"
	"strconv"

	"github.com/linuxsuren/home-gate/internal/model"
)

// ---- 链接管理（纯链接应用入口，无 SSO） ----

func (s *Server) handleAdminListLinks(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var links []model.AppLink
	if err := s.db.Order("sort").Order("id").Find(&links).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载链接失败")
		return
	}
	if links == nil {
		links = []model.AppLink{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": links})
}

func (s *Server) handleAdminCreateLink(w http.ResponseWriter, r *http.Request, admin *model.User) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		LogoURL     string `json:"logo_url"`
		URL         string `json:"url"`
		Sort        int    `json:"sort"`
		Public      bool   `json:"public"`
		Active      *bool  `json:"active"`
	}
	if err := readJSONBody(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	if req.Name == "" || req.URL == "" {
		apiError(w, http.StatusBadRequest, "invalid_request", "名称与地址必填")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	link := &model.AppLink{
		Name: req.Name, Description: req.Description, LogoURL: req.LogoURL,
		URL: req.URL, Sort: req.Sort, Public: req.Public, Active: active,
	}
	if err := s.db.Create(link).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "创建链接失败")
		return
	}
	s.db.Audit(admin.Username, "link_create", "name="+req.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": link})
}

func (s *Server) handleAdminUpdateLink(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	var link model.AppLink
	if err := s.db.First(&link, id).Error; err != nil {
		apiError(w, http.StatusNotFound, "not_found", "链接不存在")
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		LogoURL     *string `json:"logo_url"`
		URL         *string `json:"url"`
		Sort        *int    `json:"sort"`
		Public      *bool   `json:"public"`
		Active      *bool   `json:"active"`
	}
	if err := readJSONBody(r, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "请求体不是合法 JSON")
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		if *req.Name == "" {
			apiError(w, http.StatusBadRequest, "invalid_request", "名称不能为空")
			return
		}
		updates["name"] = *req.Name
	}
	if req.URL != nil {
		if *req.URL == "" {
			apiError(w, http.StatusBadRequest, "invalid_request", "地址不能为空")
			return
		}
		updates["url"] = *req.URL
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.LogoURL != nil {
		updates["logo_url"] = *req.LogoURL
	}
	if req.Sort != nil {
		updates["sort"] = *req.Sort
	}
	if req.Public != nil {
		updates["public"] = *req.Public
	}
	if req.Active != nil {
		updates["active"] = *req.Active
	}
	if len(updates) > 0 {
		if err := s.db.Model(&link).Updates(updates).Error; err != nil {
			apiError(w, http.StatusInternalServerError, "server_error", "更新链接失败")
			return
		}
	}
	s.db.Audit(admin.Username, "link_update", "name="+link.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

func (s *Server) handleAdminDeleteLink(w http.ResponseWriter, r *http.Request, admin *model.User) {
	id, ok := parseID(r)
	if !ok {
		apiError(w, http.StatusBadRequest, "invalid_request", "无效的 id")
		return
	}
	var link model.AppLink
	if err := s.db.First(&link, id).Error; err != nil {
		apiError(w, http.StatusNotFound, "not_found", "链接不存在")
		return
	}
	if err := s.db.Delete(&link).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "删除链接失败")
		return
	}
	// 清理该链接的关注记录，避免遗留失效关注
	s.db.Where("kind = ? AND ref = ?", "link", strconv.FormatUint(uint64(link.ID), 10)).
		Delete(&model.Favorite{})
	s.db.Audit(admin.Username, "link_delete", "name="+link.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}
