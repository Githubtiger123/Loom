// Package repository 负责 GORM 持久化，不调用集群，也不决定业务授权。
package repository

import (
	"context"
	"database/sql"
	"errors"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store struct{ DB *gorm.DB }

func New(db *gorm.DB) *Store { return &Store{DB: db} }

// InTx 把同一个事务句柄传给所有仓储操作；提交失败必须返回给调用方。
func InTx(ctx context.Context, db *gorm.DB, fn func(*Store) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func (s *Store) query(ctx context.Context, lock bool) *gorm.DB {
	db := s.DB.WithContext(ctx)
	if lock {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return db
}

func IsDuplicate(err error) bool {
	var e *mysqlDriver.MySQLError
	return errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &e) && e.Number == 1062)
}
