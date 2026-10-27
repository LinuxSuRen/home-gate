package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestListenDrift 端口被占用时漂移到相邻空闲端口；未开启 drift 时保持报错。
func TestListenDrift(t *testing.T) {
	// 占住一个端口
	port := freePort(t)
	occ, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer occ.Close()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	// 未开启漂移：原样返回占用错误
	_, _, err = listenWithDrift(addr, false, 20)
	if !isAddrInUse(err) {
		t.Fatalf("without drift should return addr-in-use, got %v", err)
	}

	// 开启漂移：落在下一个空闲端口
	ln, finalAddr, err := listenWithDrift(addr, true, 20)
	if err != nil {
		t.Fatalf("listen with drift: %v", err)
	}
	defer ln.Close()
	got := ln.Addr().(*net.TCPAddr).Port
	if got != port+1 {
		t.Fatalf("drifted port = %d, want %d", got, port+1)
	}
	if finalAddr != net.JoinHostPort("127.0.0.1", strconv.Itoa(port+1)) {
		t.Fatalf("final addr = %s", finalAddr)
	}

	// 非占用类错误（地址非法）不触发漂移，原样报错
	_, _, err = listenWithDrift("127.0.0.1:notaport", true, 20)
	if err == nil || strings.Contains(err.Error(), "EADDRINUSE") {
		t.Fatalf("invalid addr should fail directly, got %v", err)
	}
}
