package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linuxsuren/home-gate/internal/httpserver"
	"github.com/linuxsuren/home-gate/internal/store"
	"github.com/linuxsuren/home-gate/internal/token"
	"github.com/linuxsuren/home-gate/internal/version"
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	dataDir := flag.String("data", "./data", "数据目录（SQLite）")
	issuer := flag.String("issuer", "", "外部可达基础地址（如 https://gate.example.com），默认按请求 Host 推断")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}
	db, err := store.Open(filepath.Join(*dataDir, "gate.db"))
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	tm, err := token.NewManager(db.DB)
	if err != nil {
		log.Fatalf("初始化签名密钥失败: %v", err)
	}

	srv := httpserver.New(db, tm, *issuer, version.Version)
	addrShown := *addr
	if addrShown == "" || addrShown[0] == ':' {
		addrShown = "localhost" + addrShown
	}
	if os.Getenv("GATE_RESTART") == "1" {
		waitForPortFree(*addr) // 自更新重启：等旧进程释放端口
	}
	log.Printf("home-gate %s 启动: http://%s", version.Version, addrShown)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}

// waitForPortFree 轮询直至地址不可达（旧进程已退出），每次约 0.6 秒，最多约 30 秒。
func waitForPortFree(addr string) {
	target := addr
	if strings.HasPrefix(target, ":") {
		target = "localhost" + target
	}
	log.Printf("自更新重启：等待旧进程释放端口 %s…", target)
	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp", target, 300*time.Millisecond)
		if err != nil {
			return // 端口已释放
		}
		conn.Close()
		time.Sleep(300 * time.Millisecond)
	}
	log.Println("等待端口释放超时，继续尝试启动…")
}
