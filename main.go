package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/linuxsuren/home-gate/internal/httpserver"
	"github.com/linuxsuren/home-gate/internal/store"
	"github.com/linuxsuren/home-gate/internal/token"
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
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
	addrShown := *addr
	if addrShown == "" || addrShown[0] == ':' {
		addrShown = "localhost" + addrShown
	}
	log.Printf("home-gate 门户启动: http://%s", addrShown)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
