package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/config"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/database"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/go-sql-driver/mysql"
)

// migrateLegacy 从旧版单点登录与订阅共享数据库导入数据
func migrateLegacy(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("migrate-legacy", flag.ContinueOnError)
	accounts := fs.String("accounts-dsn", "", "legacy accounts (SSO) MySQL DSN")
	proxy := fs.String("proxy-dsn", "", "legacy proxy (subscription sharing) MySQL DSN")
	adminEmail := fs.String("admin-email", "", "legacy account email that becomes admin (id=1)")
	dryRun := fs.Bool("dry-run", false, "read only, print the plan")
	yes := fs.Bool("yes", false, "confirm wiping current data and importing")
	avatars := fs.Bool("avatars", false, "download third-party avatars")
	report := fs.String("report", "", "write JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *accounts == "" || *proxy == "" || *adminEmail == "" {
		return errors.New("用法：nodepool migrate-legacy -accounts-dsn DSN -proxy-dsn DSN -admin-email EMAIL [-dry-run | -yes] [-avatars] [-report FILE]；SUB_SECRET 通过环境变量 LEGACY_SUB_SECRET 提供")
	}
	if !*dryRun && !*yes {
		return errors.New("正式导入会清空当前站点除运行设置外的全部数据，请先用 -dry-run 核对，确认后加 -yes 执行")
	}
	if err := pool.SetDataKey(cfg.DataKey); err != nil {
		return err
	}
	openLegacy := func(dsn string) (*sql.DB, error) {
		c, err := mysql.ParseDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("旧库 DSN 无效：%w", err)
		}
		c.ParseTime, c.Loc = true, time.UTC
		db, err := sql.Open("mysql", c.FormatDSN())
		if err != nil {
			return nil, err
		}
		return db, db.Ping()
	}
	a, err := openLegacy(*accounts)
	if err != nil {
		return err
	}
	defer a.Close()
	p, err := openLegacy(*proxy)
	if err != nil {
		return err
	}
	defer p.Close()
	db, err := database.OpenTimeout(cfg.MySQL, 10*time.Minute)
	if err != nil {
		return err
	}
	if db == nil && !*dryRun {
		return errors.New("目标数据库未配置，请设置 MYSQL_* 环境变量")
	}
	if db != nil {
		defer db.Close()
	}
	store := pool.NewStore(db)
	ctx := context.Background()
	r, err := store.ImportLegacy(ctx, pool.LegacyOptions{
		Accounts: a, Proxy: p, SubSecret: os.Getenv("LEGACY_SUB_SECRET"), AdminEmail: *adminEmail,
		DryRun: *dryRun, Avatars: *avatars, Validate: core.New().Validate, Log: os.Stderr,
	})
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(r, "", "  ")
	if *report != "" {
		if err = os.WriteFile(*report, out, 0600); err != nil {
			return err
		}
	}
	fmt.Println(string(out))
	return nil
}
