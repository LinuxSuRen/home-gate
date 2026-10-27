package main

import (
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/linuxsuren/home-gate/internal/httpserver"
	"github.com/linuxsuren/home-gate/internal/store"
	"github.com/linuxsuren/home-gate/internal/token"
)

// isAddrInUse 判断「端口已被占用」类监听错误。
// Linux/macOS 为 syscall.EADDRINUSE；Windows 套接字错误是 WSAEADDRINUSE(10048)，
// 与 Go 标准库在 Windows 上定义的 EADDRINUSE 常量不是同一个值，需单独比对。
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return errors.Is(err, syscall.Errno(10048)) // Windows WSAEADDRINUSE
}

// listenWithDrift 监听 addr；端口被占用（EADDRINUSE）且允许漂移时，
// 从相邻端口起依次尝试 maxAttempts 个。返回监听器与实际监听地址。
func listenWithDrift(addr string, drift bool, maxAttempts int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil || !drift || !isAddrInUse(err) {
		return ln, addr, err
	}
	host, portStr, perr := net.SplitHostPort(addr)
	if perr != nil {
		return nil, addr, err
	}
	port, cerr := strconv.Atoi(portStr)
	if cerr != nil || port <= 0 {
		return nil, addr, err
	}
	for d := 1; d <= maxAttempts; d++ {
		next := net.JoinHostPort(host, strconv.Itoa(port+d))
		l, e := net.Listen("tcp", next)
		if e == nil {
			log.Printf("端口 %d 已被占用，自动漂移到 %s", port, next)
			return l, next, nil
		}
		if !isAddrInUse(e) {
			return nil, addr, e
		}
	}
	return nil, addr, err
}

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	drift := flag.Bool("drift", false, "监听端口被占用时自动漂移到相邻空闲端口（最多尝试 20 个）")
	dataDir := flag.String("data", "./data", "数据目录（SQLite）")
	issuer := flag.String("issuer", "", "外部可达基础地址（如 https://gate.example.com），默认按请求 Host 推断")
	flag.Parse()

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

	srv := httpserver.New(db, tm, *issuer)
	ln, finalAddr, err := listenWithDrift(*addr, *drift, 20)
	if err != nil {
		log.Fatalf("监听失败: %v", err)
	}
	addrShown := finalAddr
	if addrShown == "" || addrShown[0] == ':' {
		addrShown = "localhost" + addrShown
	}
	log.Printf("home-gate 门户启动: http://%s", addrShown)
	log.Fatal(http.Serve(ln, srv.Handler()))
}
