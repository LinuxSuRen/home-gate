package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
)

// 自更新：展示版本、检查 GitHub 最新 Release、一键下载替换并重启。
// 仅面向二进制部署；容器部署请通过镜像更新。

const defaultGithubAPI = "https://api.github.com"
const updateRepo = "linuxsuren/home-gate"

// releaseInfo GitHub Release 元信息（仅取所需字段）。
type releaseInfo struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// assetURL 返回适配 goos/goarch 的产物下载地址（Release 产物命名 home-gate-{os}-{arch}）。
func (r *releaseInfo) assetURL(goos, goarch string) string {
	want := fmt.Sprintf("home-gate-%s-%s", goos, goarch)
	for _, a := range r.Assets {
		if a.Name == want {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// fetchLatestRelease 查询 GitHub 最新 Release。
func fetchLatestRelease(apiBase string) (*releaseInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, apiBase+"/repos/"+updateRepo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "home-gate-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}
	var info releaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return nil, fmt.Errorf("解析 Release 信息失败: %w", err)
	}
	if info.TagName == "" {
		return nil, fmt.Errorf("Release 信息缺少版本号")
	}
	return &info, nil
}

// parseSemver 解析 v1.2.3 形式版本号。
func parseSemver(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		if p == "" {
			return [3]int{}, false
		}
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return [3]int{}, false
			}
			n = n*10 + int(ch-'0')
		}
		out[i] = n
	}
	return out, true
}

// newerThan 判断 latest 是否新于 cur；无法解析时退化为「版本不同即有更新」。
func newerThan(cur, latest string) bool {
	c, ok1 := parseSemver(cur)
	l, ok2 := parseSemver(latest)
	if !ok1 || !ok2 {
		return strings.TrimSpace(latest) != strings.TrimSpace(cur)
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// inContainer 检测是否运行在容器中（容器内替换二进制重启后会随镜像还原，应改用镜像更新）。
func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// ---- HTTP handlers ----

// handleVersion 公开端点：当前版本与运行平台。
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"version": s.version, "goos": runtime.GOOS, "goarch": runtime.GOARCH,
	}})
}

// handleAdminUpdateCheck 管理员：对比 GitHub 最新 Release。
func (s *Server) handleAdminUpdateCheck(w http.ResponseWriter, r *http.Request, admin *model.User) {
	info, err := fetchLatestRelease(s.githubAPI)
	if err != nil {
		apiError(w, http.StatusBadGateway, "update_check_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"current":     s.version,
		"latest":      info.TagName,
		"has_update":  newerThan(s.version, info.TagName),
		"release_url": info.HTMLURL,
		"asset":       info.assetURL(runtime.GOOS, runtime.GOARCH) != "",
		"container":   inContainer(),
	}})
}

// handleAdminUpgrade 管理员：一键升级——下载最新版、原地替换、重启进程。
func (s *Server) handleAdminUpgrade(w http.ResponseWriter, r *http.Request, admin *model.User) {
	exe, err := os.Executable()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "no_executable", "无法定位当前程序路径")
		return
	}
	if inContainer() {
		apiError(w, http.StatusBadRequest, "container_deploy", "检测到容器部署，请更新镜像而非替换二进制")
		return
	}
	info, err := fetchLatestRelease(s.githubAPI)
	if err != nil {
		apiError(w, http.StatusBadGateway, "update_check_failed", err.Error())
		return
	}
	if !newerThan(s.version, info.TagName) {
		apiError(w, http.StatusBadRequest, "already_latest", "已是最新版本 "+info.TagName)
		return
	}
	dlURL := info.assetURL(runtime.GOOS, runtime.GOARCH)
	if dlURL == "" {
		apiError(w, http.StatusBadRequest, "no_asset",
			fmt.Sprintf("最新版本 %s 没有适配 %s/%s 的产物，请到 Release 页面确认", info.TagName, runtime.GOOS, runtime.GOARCH))
		return
	}
	if err := downloadAndSwap(dlURL, exe); err != nil {
		apiError(w, http.StatusInternalServerError, "upgrade_failed", err.Error())
		return
	}
	s.db.Audit(admin.Username, "upgrade", s.version+" -> "+info.TagName, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"upgraded_to": info.TagName}})
	// 先让响应送达，再启动新进程并退出旧进程
	time.AfterFunc(500*time.Millisecond, func() {
		if err := restartProcess(exe); err != nil {
			log.Printf("自更新：启动新进程失败（二进制已替换，手动重启即生效）: %v", err)
			return
		}
		log.Printf("自更新：%s -> %s，重启进程", s.version, info.TagName)
		os.Exit(0)
	})
}

// downloadAndSwap 下载新版二进制并原子替换 exePath；原文件保留为 .old 备份。
func downloadAndSwap(url, exePath string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	newPath := exePath + ".new"
	f, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20)); err != nil {
		f.Close()
		os.Remove(newPath)
		return fmt.Errorf("下载写入失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(newPath)
		return err
	}
	if st, err := os.Stat(newPath); err == nil && st.Size() < 1<<20 {
		os.Remove(newPath)
		return fmt.Errorf("下载的文件异常（%d 字节），已放弃", st.Size())
	}

	old := exePath + ".old"
	_ = os.Remove(old) // 清理历史备份
	if err := os.Rename(exePath, old); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("备份当前程序失败: %w", err)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(old, exePath) // 回滚
		return fmt.Errorf("替换程序失败: %w", err)
	}
	return nil
}

// restartProcess 以相同参数启动新进程（GATE_RESTART=1 使其等待端口释放），调用方随后退出。
func restartProcess(exePath string) error {
	cmd := exec.Command(exePath, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GATE_RESTART=1")
	return cmd.Start()
}
