package repository

import (
	"context"
	"time"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func (s *Store) AuthSession(ctx context.Context, family, domain string, userID uint64, lock bool) (*model.AuthSession, error) {
	row := &model.AuthSession{}
	err := s.query(ctx, lock).Where("family_id = ? AND auth_domain = ? AND user_id = ?", family, domain, userID).First(row).Error
	return row, err
}

func (s *Store) InsertAuthSession(ctx context.Context, row *model.AuthSession) error {
	return s.DB.WithContext(ctx).Create(row).Error
}

// 调用方必须使用刚从 SQL 验证的用户版本。仅列出尚未过期或撤销的当前版本设备。
func (s *Store) AuthSessions(ctx context.Context, domain string, userID, version uint64, after string, limit int, now time.Time) ([]model.AuthSession, error) {
	rows := []model.AuthSession{}
	err := s.DB.WithContext(ctx).Where("auth_domain = ? AND user_id = ? AND session_version = ? AND family_id > ? AND revoked_at IS NULL AND expires_at > ?",
		domain, userID, version, after, now).Order("family_id").Limit(limit).Find(&rows).Error
	return rows, err
}

func (s *Store) TouchAuthSession(ctx context.Context, family, domain string, userID uint64, now time.Time) error {
	return s.DB.WithContext(ctx).Model(&model.AuthSession{}).
		Where("family_id = ? AND auth_domain = ? AND user_id = ? AND revoked_at IS NULL", family, domain, userID).
		Update("last_refreshed_at", now).Error
}

// 密码和全部退出同时显式撤销元数据及双 token；跨域旧记录也不能复活。
// 必须在持有 LockAuthorization 的事务内调用。
func (s *Store) RevokeUserAuthSessions(ctx context.Context, userID uint64, now time.Time) error {
	if err := s.DB.WithContext(ctx).Model(&model.AuthSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error; err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(&model.AuthToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error
}
