package database

import (
	"database/sql"
	"net"
	"strconv"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/config"
	"github.com/go-sql-driver/mysql"
)

// Open returns nil when MySQL is not configured. Network checks use PingContext.
func Open(cfg config.MySQL) (*sql.DB, error) {
	return OpenTimeout(cfg, 5*time.Second)
}

// OpenTimeout 供迁移等长时间批量操作使用更长的读写超时
func OpenTimeout(cfg config.MySQL, io time.Duration) (*sql.DB, error) {
	if !cfg.Configured() {
		return nil, nil
	}
	driverConfig := mysql.NewConfig()
	driverConfig.Net = "tcp"
	driverConfig.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	driverConfig.User = cfg.User
	driverConfig.Passwd = cfg.Password
	driverConfig.DBName = cfg.Database
	driverConfig.ParseTime = true
	driverConfig.Loc = time.UTC
	driverConfig.Timeout = 5 * time.Second
	driverConfig.ReadTimeout = io
	driverConfig.WriteTimeout = io
	if cfg.TLS {
		driverConfig.TLSConfig = "true"
	}
	connector, err := mysql.NewConnector(driverConfig)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(3 * time.Minute)
	return db, nil
}
