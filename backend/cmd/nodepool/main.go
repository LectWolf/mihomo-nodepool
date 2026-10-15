package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/config"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/database"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/server"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
			return healthcheck(cfg.HTTPAddr)
		}
		return fmt.Errorf("用法：nodepool [healthcheck]")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return fmt.Errorf("无法创建数据目录：%w", err)
	}
	db, err := database.Open(cfg.MySQL)
	if err != nil {
		return fmt.Errorf("无法初始化 MySQL 连接配置")
	}
	if db != nil {
		defer db.Close()
	}
	srv := &http.Server{
		Addr: cfg.HTTPAddr, Handler: server.Handler(db),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- srv.ListenAndServe() }()
	log.Printf("Mihomo NodePool %s 正在监听 %s", buildinfo.Version, cfg.HTTPAddr)
	select {
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}

func healthcheck(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("健康检查地址无效")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return fmt.Errorf("健康检查无法连接应用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("健康检查失败：HTTP %d", resp.StatusCode)
	}
	return nil
}
