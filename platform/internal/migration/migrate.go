// Package migration 管理内嵌的、有校验和的 MySQL 8.4 迁移。
// DDL 在 MySQL 隐式提交，失败后保留错误，不伪装成可事务回滚。
package migration

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

//go:embed *.sql
var files embed.FS

// Apply 必须显式运行，不在 API 启动时静默修改表结构。
func Apply(ctx context.Context, db *gorm.DB) error {
	lock, err := inframysql.AcquireLock(ctx, db, "loom.schema.migrate", 10)
	if err != nil {
		return fmt.Errorf("获取迁移锁失败: %w", err)
	}
	defer lock.Close()
	conn := lock.DB.WithContext(ctx)
	// DDL 仍显式执行，不能用 AutoMigrate 绕过版本和校验和管理。
	if err := conn.Exec(`CREATE TABLE IF NOT EXISTS tb_schema_migration (
name VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
checksum CHAR(64) CHARACTER SET ascii NOT NULL, dirty BOOLEAN NOT NULL,
applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)) ENGINE=InnoDB`).Error; err != nil {
		return err
	}
	names, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
	for _, file := range names {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		body, err := files.ReadFile(file.Name())
		if err != nil {
			return err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		// Validate statement boundaries before recording dirty. Hash the original
		// embedded file, not the comment-free execution statements.
		statements, err := splitStatements(string(body))
		if err != nil {
			return fmt.Errorf("迁移 %s 无法解析: %w", file.Name(), err)
		}
		var existing model.SchemaMigration
		err = conn.Where("name = ?", file.Name()).First(&existing).Error
		if err == nil {
			if existing.Dirty {
				return fmt.Errorf("迁移 %s 上次中断，需管理员核实并修复后清除 dirty 标记", file.Name())
			}
			if existing.Checksum != checksum {
				return fmt.Errorf("已执行的迁移 %s 被修改，禁止继续", file.Name())
			}
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := conn.Create(&model.SchemaMigration{Name: file.Name(), Checksum: checksum, Dirty: true}).Error; err != nil {
			return err
		}
		execute := func(executor *gorm.DB) error {
			for index, statement := range statements {
				if err := executor.Exec(statement).Error; err != nil {
					return fmt.Errorf("迁移 %s 第 %d/%d 条语句失败（已标记 dirty）: %w", file.Name(), index+1, len(statements), err)
				}
			}
			return executor.Model(&model.SchemaMigration{}).Where("name = ?", file.Name()).
				Updates(map[string]any{"dirty": false, "applied_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error
		}
		if file.Name() == "000027_initial_admin.sql" {
			// Only this DML-only seed is transactional. Do not wrap MySQL DDL.
			// Serialize against CLI bootstrap as well as other migration runners.
			err = applyInitialAdmin(ctx, db, execute)
		} else {
			err = execute(conn)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func applyInitialAdmin(ctx context.Context, db *gorm.DB, execute func(*gorm.DB) error) error {
	lock, err := inframysql.AcquireLock(ctx, db, "loom.identity.bootstrap", 10)
	if err != nil {
		return fmt.Errorf("获取管理员初始化锁失败（已标记 dirty）: %w", err)
	}
	defer lock.Close()
	return lock.DB.WithContext(ctx).Transaction(execute)
}
