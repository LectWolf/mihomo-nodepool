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
	"strings"
	"syscall"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/config"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/database"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/server"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/update"
	"github.com/joho/godotenv"
)

func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func run() error {
	envFile := os.Getenv("NODEPOOL_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("无法读取环境配置文件，请检查 .env 格式")
	}
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
			return healthcheck(cfg.HTTPAddr)
		}
		if os.Args[1] == "migrate-legacy" {
			return migrateLegacy(cfg, os.Args[2:])
		}
		return fmt.Errorf("用法：nodepool [healthcheck | migrate-legacy]")
	}
	if e = pool.SetDataKey(cfg.DataKey); e != nil {
		return e
	}
	// 只在测试环境打开：让回环地址上的模拟订阅源可用，生产必须保持关闭
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("NODEPOOL_ALLOW_PRIVATE_TARGETS"))); v == "1" || v == "true" {
		pool.AllowPrivateTargets = true
	}
	if e = os.MkdirAll(cfg.DataDir, 0700); e != nil {
		return fmt.Errorf("无法创建数据目录：%w", e)
	}
	db, e := database.Open(cfg.MySQL)
	if e != nil {
		return fmt.Errorf("无法初始化 MySQL 连接配置")
	}
	if db != nil {
		defer db.Close()
	}
	admin, e := auth.New(cfg.DataDir, os.Getenv("ADMIN_PASSWORD"))
	if e != nil {
		return e
	}
	updater, err := update.New(update.Options{Version: buildinfo.Version})
	if err != nil {
		return fmt.Errorf("无法初始化版本更新器：%w", err)
	}
	if admin.PasswordFile != "" {
		log.Printf("管理员密码文件：%s", admin.PasswordFile)
	}
	service := pool.NewService(pool.NewStore(db), core.New())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); service.Run(ctx) }()
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: server.New(server.Options{Service: service, Auth: admin, Updates: updater, TrustedProxies: cfg.TrustedProxies}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second}
	traffic.Server(srv)
	ln, e := net.Listen("tcp", cfg.HTTPAddr)
	if e != nil {
		stop()
		return e
	}
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- srv.Serve(traffic.Listen(ln, traffic.Default)) }()
	log.Printf("Mihomo NodePool %s 正在监听 %s", buildinfo.Version, cfg.HTTPAddr)
	select {
	case e = <-errorsCh:
		stop()
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e = srv.Shutdown(shutdownCtx)
	}
	stop()
	select {
	case <-workerDone:
	case <-time.After(10 * time.Second):
	}
	return e
}
func healthcheck(addr string) error {
	host, port, e := net.SplitHostPort(addr)
	if e != nil {
		return fmt.Errorf("健康检查地址无效")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, e := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if e != nil {
		return fmt.Errorf("健康检查无法连接应用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("健康检查失败：HTTP %d", resp.StatusCode)
	}
	return nil
}
