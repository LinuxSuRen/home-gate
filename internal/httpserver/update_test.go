package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
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

// fakeBinary 返回 2MB 的确定性假二进制（通过 downloadAndSwap 的体积下限校验）。
func fakeBinary() []byte {
	buf := make([]byte, 2<<20)
	for i := range buf {
		buf[i] = byte(i)
	}
	return buf
}

// fakeUpdateServer 构造假的 GitHub API + 产物下载服务。
// /download 提供带正确 .sha256 的产物；/wrong 的校验值不匹配；/nocalc 缺少校验文件。
func fakeUpdateServer(t *testing.T, tag string, withAsset bool) *httptest.Server {
	t.Helper()
	buf := fakeBinary()
	sum := sha256.Sum256(buf)
	goodSum := hex.EncodeToString(sum[:])
	badSum := sha256.Sum256([]byte("not the binary"))
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
			w.Write(buf)
		case "/download.sha256":
			fmt.Fprintf(w, "%s  home-gate-%s-%s\n", goodSum, runtime.GOOS, runtime.GOARCH)
		case "/wrong":
			w.Write(buf)
		case "/wrong.sha256":
			fmt.Fprintf(w, "%s  home-gate\n", hex.EncodeToString(badSum[:]))
		case "/nocalc":
			w.Write(buf)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestValidateDownloadURL 下载地址白名单：仅允许 GitHub 官方域名的 HTTPS 地址。
func TestValidateDownloadURL(t *testing.T) {
	ok := []string{
		"https://github.com/linuxsuren/home-gate/releases/download/v1.0.0/home-gate-linux-amd64",
		"https://objects.githubusercontent.com/gh-release/asset.bin",
		"https://release.github.com/whatever",
	}
	for _, u := range ok {
		if err := validateDownloadURL(u); err != nil {
			t.Fatalf("validateDownloadURL(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{
		"http://github.com/asset",                            // 非 HTTPS
		"https://evil.example.com/asset",                     // 非白名单主机
		"https://evil.github.com.evil.com/asset",             // 后缀伪装
		"https://githubusercontent.com.evil.example/asset",   // 后缀伪装
		"https://user:pass@evil.example.com/asset",           // 非白名单主机（带凭据）
		"://missing-scheme",                                  // 非法地址
	}
	for _, u := range bad {
		if err := validateDownloadURL(u); err == nil {
			t.Fatalf("validateDownloadURL(%q) = nil, want error", u)
		}
	}
}

// TestUnitFromCgroup 从 cgroup 内容解析 systemd 单元名。
func TestUnitFromCgroup(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0::/system.slice/home-gate.service\n", "home-gate.service"},
		{"1:name=systemd:/system.slice/gate.scope\n", "gate.scope"},
		{"0::/user.slice/user-1000.slice/user@1000.service/init.scope\n", "init.scope"},
		{"0::/..\n", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := unitFromCgroup(c.in); got != c.want {
			t.Fatalf("unitFromCgroup(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWatchAddrFromArgs 看门狗从启动参数提取监听地址。
func TestWatchAddrFromArgs(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{}, "127.0.0.1:8080"},
		{[]string{"-addr", ":9090"}, "127.0.0.1:9090"},
		{[]string{"-addr=:9999", "-data=/var/gate"}, "127.0.0.1:9999"},
		{[]string{"-addr", "0.0.0.0:7070", "-data=./d"}, "0.0.0.0:7070"},
	}
	for _, c := range cases {
		if got := watchAddrFromArgs(c.args); got != c.want {
			t.Fatalf("watchAddrFromArgs(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// TestRollbackUpgrade 回滚文件操作：新二进制移出为 .failed，.old 备份恢复为当前程序。
func TestRollbackUpgrade(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "home-gate")
	if err := os.WriteFile(exe, []byte("bad-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+".old", []byte("good-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RollbackUpgrade(exe); err != nil {
		t.Fatalf("RollbackUpgrade: %v", err)
	}
	cur, _ := os.ReadFile(exe)
	if string(cur) != "good-old" {
		t.Fatalf("exe should be restored to backup, got %q", cur)
	}
	failed, _ := os.ReadFile(exe + ".failed")
	if string(failed) != "bad-new" {
		t.Fatalf(".failed should keep the bad binary, got %q", failed)
	}
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatalf(".old should be consumed: %v", err)
	}
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

// TestCsrfHeaderRequired 变更型管理接口必须携带 X-Requested-With 请求头（CSRF 纵深防御）。
func TestCsrfHeaderRequired(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	// 不带头 → 403（即使已登录管理员）
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{"username":"x","password":"x12345678"}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range adminHdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mutating admin API without X-Requested-With should 403, got %d %s", rec.Code, rec.Body.String())
	}

	// 带头 → 正常进入业务校验（缺少必填字段返回 400，而非 403）
	req2 := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{"username":"x","password":"x12345678"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Requested-With", "XMLHttpRequest")
	for k, v := range adminHdr {
		req2.Header.Set(k, v)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusForbidden {
		t.Fatalf("mutating admin API with X-Requested-With should not be rejected as CSRF: %d %s", rec2.Code, rec2.Body.String())
	}

	// GET 管理接口不需要该头
	w, _ := doJSON(t, h, "GET", "/api/admin/settings", "", adminHdr)
	if w.Code != http.StatusOK {
		t.Fatalf("GET admin API should not require the header: %d %s", w.Code, w.Body.String())
	}
}

// TestUpgradeDevBuildRefused dev/源码构建（版本不可解析）拒绝自更新。
func TestUpgradeDevBuildRefused(t *testing.T) {
	s := newTestServer(t)
	s.githubAPI = fakeUpdateServer(t, "v9.9.9", true).URL
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	w, out := doJSON(t, h, "POST", "/api/admin/upgrade", "", adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dev build upgrade should 400, got %d %s", w.Code, w.Body.String())
	}
	if code := out["error"].(map[string]any)["code"]; code != "dev_build" {
		t.Fatalf("expected dev_build, got %v", code)
	}
}

// TestUpgradeRejectsNonGithubAsset 产物地址不在 GitHub 白名单（或非 HTTPS）时拒绝升级。
func TestUpgradeRejectsNonGithubAsset(t *testing.T) {
	s := newTestServer(t)
	s.version = "v0.0.1" // 可解析版本，跳过 dev 拒绝分支
	s.githubAPI = fakeUpdateServer(t, "v9.9.9", true).URL
	h := s.Handler()
	adminHdr := loginAdmin(t, h)

	w, out := doJSON(t, h, "POST", "/api/admin/upgrade", "", adminHdr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-github asset should 400, got %d %s", w.Code, w.Body.String())
	}
	if code := out["error"].(map[string]any)["code"]; code != "bad_source" {
		t.Fatalf("expected bad_source, got %v", code)
	}
}

// TestDownloadAndSwap 下载替换核心逻辑：
// 校验通过 → 原子替换并保留 .old；校验不匹配/缺校验文件/404 → 拒绝且原文件不受影响。
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

	// SHA256 不匹配：拒绝替换，原文件保持不变
	if err := downloadAndSwap(fake.URL+"/wrong", exe); err == nil {
		t.Fatal("checksum mismatch should fail")
	}
	got, _ = os.ReadFile(exe)
	if len(got) != 2<<20 {
		t.Fatalf("exe should stay untouched on checksum mismatch, len=%d", len(got))
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatalf(".new should be cleaned on checksum mismatch: %v", err)
	}

	// Release 缺少 .sha256 校验文件：拒绝更新
	if err := downloadAndSwap(fake.URL+"/nocalc", exe); err == nil {
		t.Fatal("missing checksum should fail")
	}
	got, _ = os.ReadFile(exe)
	if len(got) != 2<<20 {
		t.Fatalf("exe should stay untouched when checksum missing, len=%d", len(got))
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
