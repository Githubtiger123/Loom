// Package mysql 负责 GORM 初始化与底层连接池管理，不执行业务查询或自动迁移。
package mysql

import (
	"context"
	"fmt"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	DSN                                           string
	MaxOpen, MaxIdle                              int
	ConnMaxLifetime, ConnMaxIdleTime, PingTimeout time.Duration
}

func Open(ctx context.Context, cfg Config) (*gorm.DB, error) {
	parsed, err := mysqlDriver.ParseDSN(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("MySQL DSN 格式无效: %w", err)
	}
	// DATETIME 按 UTC 存取；统一每个池连接的 MySQL 会话时区。
	parsed.ParseTime, parsed.Loc = true, time.UTC
	if parsed.Params == nil {
		parsed.Params = map[string]string{}
	}
	parsed.Params["time_zone"] = "'+00:00'"
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN: parsed.FormatDSN(), SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		NowFunc:              func() time.Time { return time.Now().UTC() },
		// 默认日志不输出身份、请求负载等 SQL 参数。
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL 失败: %w", err)
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(cfg.MaxOpen)
	pool.SetMaxIdleConns(cfg.MaxIdle)
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	pool.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	if cfg.PingTimeout <= 0 {
		cfg.PingTimeout = 3 * time.Second
	}
	if err := Health(ctx, db, cfg.PingTimeout); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("连接 MySQL 失败: %w", err)
	}
	return db, nil
}

func Health(ctx context.Context, db *gorm.DB, timeout time.Duration) error {
	if db == nil {
		return fmt.Errorf("mysql 未初始化")
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return pool.PingContext(ctx)
}

func Close(db *gorm.DB) error {
	pool, err := db.DB()
	if err != nil {
		return err
	}
	return pool.Close()
}

func MaxOpenConnections(db *gorm.DB) (int, error) {
	pool, err := db.DB()
	if err != nil {
		return 0, err
	}
	return pool.Stats().MaxOpenConnections, nil
}
