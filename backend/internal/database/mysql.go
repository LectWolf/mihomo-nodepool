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
	driverConfig.ReadTimeout = 5 * time.Second
	driverConfig.WriteTimeout = 5 * time.Second
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
