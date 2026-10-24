// Package web 嵌入前端构建产物（web/dist）。
package web

import (
	"embed"
	"strings"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// Handler 返回 SPA 静态资源处理器（未命中的路径回退到 index.html）。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err != nil {
				// SPA 回退：交给前端路由（如 /login）
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
