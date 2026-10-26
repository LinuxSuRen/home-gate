package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/linuxsuren/home-gate/internal/model"
)

// 自更新：展示版本、检查 GitHub 最新 Release、一键下载替换并重启。
// 仅面向二进制部署；容器部署请通过镜像更新。
// 下载地址强制限定 GitHub 官方域名，且必须通过 Release 附带的 .sha256 校验。

const defaultGithubAPI = "https://api.github.com"
const updateRepo = "linuxsuren/home-gate"

// 下载产物只允许来自 GitHub 官方域名（github.com 与对象存储各子域）。
var downloadHostSuffixes = []string{"github.com", "githubusercontent.com"}

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
// 注意：升级入口对无法解析的当前版本（如 dev 构建）会直接拒绝，不会走到这里。
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

// underSystemd 检测是否由 systemd 管理（systemd 会为服务进程注入这两个环境变量）。
// systemd 环境下 fork-and-exit 会脱离 cgroup 监管，应改用 systemctl restart。
func underSystemd() bool {
	return os.Getenv("INVOCATION_ID") != "" || os.Getenv("NOTIFY_SOCKET") != ""
}

// unitFromCgroup 从 /proc/self/cgroup 内容解析 systemd 单元名（如 home-gate.service）。
func unitFromCgroup(content string) string {
	for _, line := range strings.Split(content, "\n") {
		// 形如 0::/system.slice/home-gate.service 或 1:name=systemd:/system.slice/home-gate.service
		idx := strings.LastIndex(line, "/")
		if idx < 0 || idx+1 >= len(line) {
			continue
		}
		name := line[idx+1:]
		if strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".scope") {
			return name
		}
	}
	return ""
}

// systemdUnit 返回当前服务对应的 systemd 单元名；非 systemd 环境返回空串。
func systemdUnit() string {
	if !underSystemd() {
		return ""
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	return unitFromCgroup(string(data))
}

// validateDownloadURL 校验产物下载地址：必须是 HTTPS 且主机属于 GitHub 官方域名，
// 防止 GitHub API 响应被篡改后经重定向指向任意主机执行任意代码。
func validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return fmt.Errorf("下载地址必须为 HTTPS: %s", raw)
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range downloadHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return nil
		}
	}
	return fmt.Errorf("下载地址主机 %s 不在允许列表（github.com / *.githubusercontent.com）", host)
}

// fetchChecksum 下载产物旁边的 .sha256 校验文件，返回十六进制摘要（取行首字段）。
func fetchChecksum(client *http.Client, dlURL string) (string, error) {
	resp, err := client.Get(dlURL + ".sha256")
	if err != nil {
		return "", fmt.Errorf("下载校验文件失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Release 缺少 %s.sha256 校验文件（HTTP %d），拒绝无校验的更新", filepath.Base(dlURL), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if err != nil {
		return "", fmt.Errorf("读取校验文件失败: %w", err)
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return "", fmt.Errorf("校验文件内容不合法")
	}
	return strings.ToLower(fields[0]), nil
}

// ---- HTTP handlers ----

// handleVersion 公开端点：当前版本与运行平台。
// 说明：自托管场景可接受公开版本号；如需隐藏请置于反向代理之后限制该路径。
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

// handleAdminUpgrade 管理员：一键升级——下载最新版、校验 SHA256、原地替换、重启进程。
// 慢链路提示：下载与校验在本请求内同步完成，最长可达数分钟；前端以长超时等待并轮询页面恢复。
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
	if _, ok := parseSemver(s.version); !ok {
		apiError(w, http.StatusBadRequest, "dev_build",
			"当前版本（"+s.version+"）无法解析，源码/开发构建不支持自更新，请以 Release 二进制部署")
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
	if err := validateDownloadURL(dlURL); err != nil {
		apiError(w, http.StatusBadRequest, "bad_source", err.Error())
		return
	}
	if err := downloadAndSwap(dlURL, exe); err != nil {
		apiError(w, http.StatusInternalServerError, "upgrade_failed", err.Error())
		return
	}
	s.db.Audit(admin.Username, "upgrade", s.version+" -> "+info.TagName, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"upgraded_to": info.TagName}})
	// 先让响应送达，再重启：systemd 环境走 systemctl；否则启动新进程并退出旧进程。
	// 两种路径都会先拉起「升级看门狗」：若新进程迟迟不健康则自动回滚 .old 备份。
	time.AfterFunc(500*time.Millisecond, func() {
		s.restartAfterUpgrade(exe, os.Args[1:])
	})
}

// restartAfterUpgrade 替换完成后的重启编排：
//  1. 以 .old 备份二进制拉起看门狗（监控新进程健康，失败自动回滚并拉起旧版）；
//  2. systemd 环境执行 systemctl restart（fork 出的子进程会脱离 cgroup 监管）；
//     非 systemd 环境以相同参数启动新进程（GATE_RESTART=1 等待端口释放）后退出旧进程。
func (s *Server) restartAfterUpgrade(exe string, args []string) {
	oldBackup := exe + ".old"
	if err := startUpgradeWatchdog(oldBackup, exe, args); err != nil {
		log.Printf("自更新：启动回滚看门狗失败（新进程异常时需手动用 %s 回滚）: %v", oldBackup, err)
	}
	if unit := systemdUnit(); unit != "" {
		if err := exec.Command("systemctl", "restart", unit).Run(); err != nil {
			log.Printf("自更新：systemctl restart %s 失败（二进制已替换，请手动重启）: %v", unit, err)
		} else {
			log.Printf("自更新：已请求 systemd 重启 %s", unit)
		}
		return
	}
	if underSystemd() {
		log.Printf("自更新：检测到 systemd 但无法确定单元名，请手动重启服务（二进制已替换）")
		return
	}
	if err := restartProcess(exe, args); err != nil {
		log.Printf("自更新：启动新进程失败（二进制已替换，手动重启即生效）: %v", err)
		return
	}
	log.Printf("自更新：%s 重启进程", filepath.Base(exe))
	os.Exit(0)
}

// startUpgradeWatchdog 用旧版二进制（.old 备份）拉起看门狗进程：
// 新进程在期限内未恢复健康则回滚备份并按原参数拉起旧版，避免替换后服务永久失联。
// 原服务启动参数经 GATE_UPGRADE_ARGS 环境变量透传（\x00 分隔），避免与看门狗自身参数混淆。
func startUpgradeWatchdog(oldBackup, exe string, args []string) error {
	if _, err := os.Stat(oldBackup); err != nil {
		return fmt.Errorf("备份 %s 不存在: %w", oldBackup, err)
	}
	cmd := exec.Command(oldBackup,
		"-upgrade-watch-addr="+watchAddrFromArgs(args),
		"-upgrade-watch-exe="+exe)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GATE_UPGRADE_ARGS="+strings.Join(args, "\x00"))
	return cmd.Start()
}

// watchAddrFromArgs 从服务启动参数中提取监听地址（用于看门狗探测 /healthz）。
func watchAddrFromArgs(args []string) string {
	addr := ":8080"
	for i, a := range args {
		if strings.HasPrefix(a, "-addr=") {
			addr = strings.TrimPrefix(a, "-addr=")
		} else if a == "-addr" && i+1 < len(args) {
			addr = args[i+1]
		}
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return addr
}

// upgradeWatchTimeout 看门狗等待新进程恢复健康的期限（新进程自身等待端口释放最长约 15s）。
const upgradeWatchTimeout = 60 * time.Second

// RunUpgradeWatchdog 看门狗主逻辑：轮询目标 /healthz，超时则回滚并拉起旧版。
// 由 main.go 的看门狗模式调用，仅在看门狗进程内执行。
func RunUpgradeWatchdog(addr, exe string, args []string) {
	log.Printf("自更新看门狗：等待 http://%s/healthz 恢复（最多 %s）", addr, upgradeWatchTimeout)
	deadline := time.Now().Add(upgradeWatchTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Printf("自更新看门狗：新进程已恢复健康，退出")
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("自更新看门狗：新进程超时未恢复，回滚 %s", exe)
	if err := RollbackUpgrade(exe); err != nil {
		log.Printf("自更新看门狗：回滚失败，请手动恢复: %v", err)
		return
	}
	if unit := systemdUnit(); unit != "" {
		if err := exec.Command("systemctl", "restart", unit).Run(); err != nil {
			log.Printf("自更新看门狗：systemctl restart %s 失败: %v", unit, err)
		}
		return
	}
	// 非 systemd：直接按原参数拉起旧版（此时端口已被失败的新进程释放）
	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("自更新看门狗：拉起回滚后的旧版失败: %v", err)
	}
}

// RollbackUpgrade 将 .old 备份恢复为当前二进制；失败的新二进制保留为 .failed 供排查。
func RollbackUpgrade(exe string) error {
	oldBackup := exe + ".old"
	if _, err := os.Stat(oldBackup); err != nil {
		return fmt.Errorf("备份不存在: %w", err)
	}
	_ = os.Remove(exe + ".failed")
	if err := os.Rename(exe, exe+".failed"); err != nil {
		return fmt.Errorf("移出失败的新程序失败: %w", err)
	}
	if err := os.Rename(oldBackup, exe); err != nil {
		_ = os.Rename(exe+".failed", exe) // 尽量恢复原状
		return fmt.Errorf("恢复备份失败: %w", err)
	}
	return nil
}

// downloadAndSwap 下载新版二进制（强制 SHA256 校验）并原子替换 exePath；原文件保留为 .old 备份。
func downloadAndSwap(dlURL, exePath string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	// 先取校验值：Release 未提供 .sha256 时拒绝更新（不下载主体）
	wantSum, err := fetchChecksum(client, dlURL)
	if err != nil {
		return err
	}
	resp, err := client.Get(dlURL)
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
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(resp.Body, 200<<20)); err != nil {
		f.Close()
		os.Remove(newPath)
		return fmt.Errorf("下载写入失败: %w", err)
	}
	if err := f.Sync(); err != nil { // 落盘，防掉电截断
		f.Close()
		os.Remove(newPath)
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(newPath)
		return err
	}
	gotSum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotSum, wantSum) {
		os.Remove(newPath)
		return fmt.Errorf("SHA256 校验失败（期望 %s… 实际 %s…），已拒绝更新并保留原程序", wantSum[:12], gotSum[:12])
	}
	if st, err := os.Stat(newPath); err == nil && st.Size() < 1<<20 {
		os.Remove(newPath)
		return fmt.Errorf("下载的文件异常（%d 字节），已放弃", st.Size())
	}

	old := exePath + ".old"
	_ = os.Remove(old) // 清理历史备份
	if err := os.Rename(exePath, old); err != nil {
		// Windows 上若旧备份仍被进程占用（上次重启失败）则删除失败，改用带时间戳的备份名
		old = fmt.Sprintf("%s.old-%d", exePath, time.Now().Unix())
		if err := os.Rename(exePath, old); err != nil {
			os.Remove(newPath)
			return fmt.Errorf("备份当前程序失败: %w", err)
		}
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(old, exePath) // 回滚
		return fmt.Errorf("替换程序失败: %w", err)
	}
	return nil
}

// restartProcess 以相同参数启动新进程（GATE_RESTART=1 使其等待端口释放），调用方随后退出。
func restartProcess(exePath string, args []string) error {
	cmd := exec.Command(exePath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GATE_RESTART=1")
	return cmd.Start()
}
