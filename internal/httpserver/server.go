// Package httpserver home-gate HTTP 服务：门户 API + OAuth2/OIDC 授权服务器 + DCR。
package httpserver

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/linuxsuren/home-gate/internal/model"
	"github.com/linuxsuren/home-gate/internal/store"
	"github.com/linuxsuren/home-gate/internal/token"
	"github.com/linuxsuren/home-gate/web"
)

const sessionCookie = "gate_session"

type Server struct {
	db     *store.DB
	tm     *token.Manager
	issuer string // 外部可达的基础地址，如 https://gate.example.com
	mux    *http.ServeMux
}

func New(db *store.DB, tm *token.Manager, issuer string) *Server {
	s := &Server{db: db, tm: tm, issuer: issuer, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	// 公开端点
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /.well-known/openid-configuration", s.handleDiscovery)
	s.mux.HandleFunc("GET /.well-known/jwks.json", s.handleJWKS)

	// OAuth2 / OIDC
	s.mux.HandleFunc("GET /oauth2/authorize", s.handleAuthorize)
	s.mux.HandleFunc("POST /oauth2/authorize", s.handleAuthorize)
	s.mux.HandleFunc("POST /oauth2/token", s.handleToken)
	s.mux.HandleFunc("GET /oauth2/userinfo", s.handleUserinfo)
	s.mux.HandleFunc("POST /oauth2/userinfo", s.handleUserinfo)
	s.mux.HandleFunc("GET /oauth2/logout", s.handleLogout)

	// DCR（RFC 7591/7592）
	s.mux.HandleFunc("POST /oauth2/register", s.handleDCRCreate)
	s.mux.HandleFunc("GET /oauth2/register/{client_id}", s.handleDCRGet)
	s.mux.HandleFunc("PUT /oauth2/register/{client_id}", s.handleDCRUpdate)
	s.mux.HandleFunc("DELETE /oauth2/register/{client_id}", s.handleDCRDelete)

	// 门户认证 API
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogoutAPI)
	s.mux.HandleFunc("GET /api/auth/me", s.handleMe)
	s.mux.HandleFunc("POST /api/auth/change-password", s.handleChangePassword)
	s.mux.HandleFunc("GET /api/auth/feishu/enabled", s.handleFeishuEnabled)
	s.mux.HandleFunc("GET /api/auth/feishu/start", s.handleFeishuStart)
	s.mux.HandleFunc("GET /api/auth/feishu/callback", s.handleFeishuCallback)
	s.mux.HandleFunc("GET /api/apps", s.requireSession(s.handleApps))

	// 管理端 API（admin 组）
	s.mux.HandleFunc("GET /api/admin/users", s.requireAdmin(s.handleAdminListUsers))
	s.mux.HandleFunc("POST /api/admin/users", s.requireAdmin(s.handleAdminCreateUser))
	s.mux.HandleFunc("PUT /api/admin/users/{id}", s.requireAdmin(s.handleAdminUpdateUser))
	s.mux.HandleFunc("DELETE /api/admin/users/{id}", s.requireAdmin(s.handleAdminDeleteUser))
	s.mux.HandleFunc("POST /api/admin/users/{id}/reset-password", s.requireAdmin(s.handleAdminResetPassword))
	s.mux.HandleFunc("GET /api/admin/groups", s.requireAdmin(s.handleAdminListGroups))
	s.mux.HandleFunc("POST /api/admin/groups", s.requireAdmin(s.handleAdminCreateGroup))
	s.mux.HandleFunc("DELETE /api/admin/groups/{id}", s.requireAdmin(s.handleAdminDeleteGroup))
	s.mux.HandleFunc("GET /api/admin/links", s.requireAdmin(s.handleAdminListLinks))
	s.mux.HandleFunc("POST /api/admin/links", s.requireAdmin(s.handleAdminCreateLink))
	s.mux.HandleFunc("PUT /api/admin/links/{id}", s.requireAdmin(s.handleAdminUpdateLink))
	s.mux.HandleFunc("DELETE /api/admin/links/{id}", s.requireAdmin(s.handleAdminDeleteLink))
	s.mux.HandleFunc("GET /api/admin/clients", s.requireAdmin(s.handleAdminListClients))
	s.mux.HandleFunc("POST /api/admin/clients", s.requireAdmin(s.handleAdminCreateClient))
	s.mux.HandleFunc("PUT /api/admin/clients/{id}", s.requireAdmin(s.handleAdminUpdateClient))
	s.mux.HandleFunc("POST /api/admin/clients/{id}/reset-secret", s.requireAdmin(s.handleAdminResetSecret))
	s.mux.HandleFunc("GET /api/admin/initial-tokens", s.requireAdmin(s.handleAdminListInitialTokens))
	s.mux.HandleFunc("POST /api/admin/initial-tokens", s.requireAdmin(s.handleAdminCreateInitialToken))
	s.mux.HandleFunc("DELETE /api/admin/initial-tokens/{id}", s.requireAdmin(s.handleAdminRevokeInitialToken))
	s.mux.HandleFunc("GET /api/admin/audit", s.requireAdmin(s.handleAdminAudit))
	s.mux.HandleFunc("PUT /api/admin/settings", s.requireAdmin(s.handleAdminSettings))
	s.mux.HandleFunc("GET /api/admin/settings", s.requireAdmin(s.handleAdminGetSettings))

	// 前端静态资源（/、/login 等均交给 SPA）
	s.mux.HandleFunc("/", s.handleStatic)
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		go s.db.CleanupExpired() // 惰性清理过期数据；失败不影响请求
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// ---- 会话中间件 ----

func (s *Server) currentUser(r *http.Request) *model.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, ok := s.db.GetSession(c.Value)
	if !ok {
		return nil
	}
	u, err := s.db.FindUserByID(sess.UserID)
	if err != nil || !u.Active {
		return nil
	}
	s.db.TouchSession(sess)
	return u
}

func (s *Server) requireSession(next func(w http.ResponseWriter, r *http.Request, u *model.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			apiError(w, http.StatusUnauthorized, "unauthorized", "未登录")
			return
		}
		next(w, r, u)
	}
}

func (s *Server) requireAdmin(next func(w http.ResponseWriter, r *http.Request, u *model.User)) http.HandlerFunc {
	return s.requireSession(func(w http.ResponseWriter, r *http.Request, u *model.User) {
		if !u.HasGroup("admin") {
			apiError(w, http.StatusForbidden, "forbidden", "需要管理员权限")
			return
		}
		next(w, r, u)
	})
}

// ---- 工具 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": errCode, "message": msg}})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func readJSONBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func isJSONReq(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "application/json")
}

// ---- 静态资源 ----

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/oauth2/") ||
		strings.HasPrefix(r.URL.Path, "/.well-known/") {
		http.NotFound(w, r)
		return
	}
	h := web.Handler()
	h.ServeHTTP(w, r)
}

// formValue 兼容 form 与 query 取值。
func formValue(r *http.Request, key string) string {
	if v := r.PostFormValue(key); v != "" {
		return v
	}
	return r.URL.Query().Get(key)
}

func redirectURIAllowed(list []string, uri string) bool {
	for _, u := range list {
		if u == uri {
			return true
		}
	}
	return false
}

func parseRedirectURIs(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func scopeHas(scope, want string) bool {
	for _, s := range strings.Fields(scope) {
		if s == want {
			return true
		}
	}
	return false
}
