// Package redis 提供可选认证/权限缓存与分布式锁客户端。
// Token 和业务授权的权威状态均在 MySQL，Redis 丢失或故障可降级直查。
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Config 是连接参数。
type Config struct {
	Addr         string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
}

// New 建立可自动重连的客户端，不要求 Redis 此刻可用。
// 可降级调用方应保留客户端并在后续检查中重试，而不是永久缓存启动错误。
func New(cfg Config) *goredis.Client {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 2 * time.Second
	}

	return goredis.NewClient(&goredis.Options{
		Addr:                  cfg.Addr,
		Password:              cfg.Password,
		DB:                    cfg.DB,
		DialTimeout:           cfg.DialTimeout,
		ReadTimeout:           cfg.ReadTimeout,
		WriteTimeout:          cfg.WriteTimeout,
		PoolSize:              cfg.PoolSize,
		ContextTimeoutEnabled: true,
	})
}

// Open 建立客户端并 Ping 一次；要求启动时可用的调用方使用此方法。
func Open(ctx context.Context, cfg Config) (*goredis.Client, error) {
	c := New(cfg)
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 2 * time.Second
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("连接 Redis 失败（%s）: %w", cfg.Addr, err)
	}
	return c, nil
}
