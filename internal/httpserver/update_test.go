package httpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestVersionEndpoint 公开版本端点：返回注入的版本与运行平台。
func TestVersionEndpoint(t *testing.T) {
	s := newTestServer(t)
	w, out := doJSON(t, s.Handler(), "GET", "/api/version", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("version: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	if data["version"] != testVersion {
		t.Fatalf("version = %v", data["version"])
	}
	if data["goos"] != runtime.GOOS || data["goarch"] != runtime.GOARCH {
		t.Fatalf("platform = %v/%v", data["goos"], data["goarch"])
	}
}

// TestNewerThan 语义版本比较（含退化路径）。
func TestNewerThan(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.1", "v1.0.0", false},
		{"v1.2.0", "v1.10.0", true}, // 数值比较而非字符串
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "2.0.0", true}, // latest 可无 v 前缀
		{"dev", "v1.0.0", true},   // 无法解析：不同即视为有更新
		{"dev", "dev", false},
		{"", "v1.0.0", true},
	}
	for _, c := range cases {
		if got := newerThan(c.cur, c.latest); got != c.want {
			t.Fatalf("newerThan(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

// fakeUpdateServer 构造假的 GitHub API + 产物下载服务。
func fakeUpdateServer(t *testing.T, tag string, withAsset bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/linuxsuren/home-gate/releases/latest":
			assets := "[]"
			if withAsset {
				assets = fmt.Sprintf(`[{"name":"home-gate-%s-%s","browser_download_url":"%s/download"}]`,
					runtime.GOOS, runtime.GOARCH, "http://"+r.Host)
			}
			fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/linuxsuren/home-gate/releases/tag/%s","assets":%s}`,
				tag, tag, assets)
		case "/download":
			// 2MB 假二进制，通过 downloadAndSwap 的体积下限校验
			buf := make([]byte, 2<<20)
			for i := range buf {
				buf[i] = byte(i)
			}
			w.Write(buf)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestUpdateCheck 管理员检查更新：匿名 401、非管理员 403、管理员拿到对比结果。
func TestUpdateCheck(t *testing.T) {
	s := newTestServer(t)
	fake := fakeUpdateServer(t, "v9.9.9", true)
	s.githubAPI = fake.URL
	h := s.Handler()

	// 匿名 401
	w, _ := doJSON(t, h, "GET", "/api/admin/update/check", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous check should 401, got %d", w.Code)
	}

	// 非管理员 403
	adminHdr := loginAdmin(t, h)
	w, _ = doJSON(t, h, "POST", "/api/admin/users",
		`{"username":"bob","password":"bob1234567","groups":["view"]}`, adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("create bob: %d %s", w.Code, w.Body.String())
	}
	w, _ = doJSON(t, h, "POST", "/api/auth/login", `{"username":"bob","password":"bob1234567"}`, nil)
	bobHdr := map[string]string{"Cookie": strings.Split(w.Header().Get("Set-Cookie"), ";")[0]}
	w, _ = doJSON(t, h, "GET", "/api/admin/update/check", "", bobHdr)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bob check should 403, got %d", w.Code)
	}

	// 管理员：current=测试注入版本，latest=v9.9.9，有更新、有产物
	w, out := doJSON(t, h, "GET", "/api/admin/update/check", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("admin check: %d %s", w.Code, w.Body.String())
	}
	data := out["data"].(map[string]any)
	if data["current"] != testVersion || data["latest"] != "v9.9.9" || data["has_update"] != true {
		t.Fatalf("check result unexpected: %v", data)
	}
	if data["asset"] != true {
		t.Fatalf("asset should be true: %v", data)
	}
	if data["release_url"] == "" {
		t.Fatalf("release_url missing: %v", data)
	}
}

// TestUpgradeAlreadyLatest 已是最新版本时拒绝升级（不触碰可执行文件）。
func TestUpgradeAlreadyLatest(t *testing.T) {
	s := newTestServer(t)
	s.githubAPI = fakeUpdateServer(t, testVersion, true).URL
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	w, _ := doJSON(t, h, "POST", "/api/admin/upgrade", "", adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("upgrade at latest should 400, got %d %s", w.Code, w.Body.String())
	}
}

// TestDownloadAndSwap 下载替换核心逻辑：临时文件下载 → 原子替换 → 保留 .old 备份；失败路径不破坏原文件。
func TestDownloadAndSwap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "home-gate")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := fakeUpdateServer(t, "v9.9.9", true)

	if err := downloadAndSwap(fake.URL+"/download", exe); err != nil {
		t.Fatalf("downloadAndSwap: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || len(got) != 2<<20 {
		t.Fatalf("swapped binary unexpected: err=%v len=%d", err, len(got))
	}
	old, err := os.ReadFile(exe + ".old")
	if err != nil || string(old) != "old-binary" {
		t.Fatalf(".old backup unexpected: err=%v content=%q", err, string(old))
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatalf(".new should not remain: %v", err)
	}

	// 下载 404：原文件保持不变
	if err := downloadAndSwap(fake.URL+"/missing", exe); err == nil {
		t.Fatal("404 download should fail")
	}
	got, _ = os.ReadFile(exe)
	if len(got) != 2<<20 {
		t.Fatalf("exe should stay swapped after failed download, len=%d", len(got))
	}
}
