package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AdvisoryLock 把 MySQL 会话锁固定在同一连接上；不能通过普通连接池查询检查/释放。
// 底层 sql.Conn 仅用于连接生命周期管理，查询仍使用绑定该连接的 GORM。
type AdvisoryLock struct {
	conn *sql.Conn
	DB   *gorm.DB
	name string
}

func AcquireLock(ctx context.Context, db *gorm.DB, name string, waitSeconds int) (*AdvisoryLock, error) {
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	pinned := db.Session(&gorm.Session{NewDB: true, Context: ctx, SkipDefaultTransaction: true})
	pinned.Statement.ConnPool = conn
	lock := &AdvisoryLock{conn: conn, DB: pinned, name: name}
	var got sql.NullInt64
	if err := pinned.Raw("SELECT GET_LOCK(?, ?)", name, waitSeconds).Scan(&got).Error; err != nil {
		// 请求出错不代表服务端未取得锁；丢弃连接，不能把可能持锁的会话放回池中。
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return nil, err
	}
	if !got.Valid || got.Int64 != 1 {
		_ = conn.Close()
		return nil, fmt.Errorf("MySQL 命名锁暂不可用")
	}
	return lock, nil
}

func (l *AdvisoryLock) Check(ctx context.Context) error {
	var owned bool
	if err := l.DB.WithContext(ctx).Raw("SELECT COALESCE(IS_USED_LOCK(?) = CONNECTION_ID(), 0)", l.name).Scan(&owned).Error; err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("MySQL 命名锁已丢失")
	}
	return nil
}

func (l *AdvisoryLock) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var released sql.NullInt64
	err := l.DB.WithContext(ctx).Raw("SELECT RELEASE_LOCK(?)", l.name).Scan(&released).Error
	if err != nil || !released.Valid || released.Int64 != 1 {
		_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = l.conn.Close()
}
