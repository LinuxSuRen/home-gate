package httpserver

import (
	"net/http"
	"strconv"

	"github.com/linuxsuren/home-gate/internal/model"
)

// ---- 门户关注（收藏）----

// handleListFavorites 当前用户的关注列表。
func (s *Server) handleListFavorites(w http.ResponseWriter, r *http.Request, u *model.User) {
	var favs []model.Favorite
	if err := s.db.Where("user_id = ?", u.ID).Order("id").Find(&favs).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "加载关注失败")
		return
	}
	if favs == nil {
		favs = []model.Favorite{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": favs})
}

// followTarget 校验关注目标存在性：link → ref 为链接 id；app → ref 为 client_id。
func (s *Server) followTarget(kind, ref string) bool {
	switch kind {
	case "link":
		id, err := strconv.ParseUint(ref, 10, 64)
		if err != nil {
			return false
		}
		var link model.AppLink
		return s.db.First(&link, id).Error == nil
	case "app":
		_, err := s.db.FindClient(ref)
		return err == nil
	}
	return false
}

// handleFollow 关注一个门户入口（幂等）。
func (s *Server) handleFollow(w http.ResponseWriter, r *http.Request, u *model.User) {
	kind, ref := r.PathValue("kind"), r.PathValue("ref")
	if kind == "" || ref == "" {
		apiError(w, http.StatusBadRequest, "invalid_request", "参数缺失")
		return
	}
	if !s.followTarget(kind, ref) {
		apiError(w, http.StatusNotFound, "not_found", "关注目标不存在")
		return
	}
	fav := model.Favorite{UserID: u.ID, Kind: kind, Ref: ref}
	if err := s.db.Where("user_id = ? AND kind = ? AND ref = ?", u.ID, kind, ref).
		FirstOrCreate(&fav).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "保存关注失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}

// handleUnfollow 取消关注。
func (s *Server) handleUnfollow(w http.ResponseWriter, r *http.Request, u *model.User) {
	kind, ref := r.PathValue("kind"), r.PathValue("ref")
	if err := s.db.Where("user_id = ? AND kind = ? AND ref = ?", u.ID, kind, ref).
		Delete(&model.Favorite{}).Error; err != nil {
		apiError(w, http.StatusInternalServerError, "server_error", "取消关注失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": "ok"})
}
