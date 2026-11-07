package migration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

// Check 只读检查已安装版本，避免程序带着不兼容的表结构接收业务请求。
func Check(ctx context.Context, db *gorm.DB) error {
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		var installed model.SchemaMigration
		if err := db.WithContext(ctx).Where("name = ?", entry.Name()).First(&installed).Error; err != nil {
			return fmt.Errorf("数据库缺少迁移 %s，请先运行 -cmd=migrate: %w", entry.Name(), err)
		}
		if installed.Dirty || installed.Checksum != fmt.Sprintf("%x", sha256.Sum256(body)) {
			return fmt.Errorf("数据库迁移 %s 未完成或校验和不匹配，拒绝启动", entry.Name())
		}
	}
	return nil
}
