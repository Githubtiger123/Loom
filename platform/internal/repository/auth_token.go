package repository

import (
	"context"
	"time"

	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) AuthToken(ctx context.Context, id string, lock bool) (*model.AuthToken, error) {
	t := &model.AuthToken{}
	err := s.query(ctx, lock).Where("token_id = ?", id).First(t).Error
	return t, err
}

// 缓存命中时仍直查数据库的可变状态，不允许旧缓存绕过撤销或清理。
func (s *Store) AuthTokenState(ctx context.Context, id string) (*model.AuthToken, error) {
	t := &model.AuthToken{}
	err := s.query(ctx, false).Omit("token_hash").Where("token_id = ?", id).First(t).Error
	return t, err
}

func (s *Store) InsertAuthTokens(ctx context.Context, tokens []model.AuthToken) error {
	return s.DB.WithContext(ctx).Create(&tokens).Error
}

func (s *Store) RevokeTokenFamily(ctx context.Context, family string, userID uint64, now time.Time) error {
	// 与 token 写入处于同一事务；设备撤销和重放检测共用这一条路径。
	if err := s.DB.WithContext(ctx).Model(&model.AuthSession{}).
		Where("family_id = ? AND user_id = ? AND revoked_at IS NULL", family, userID).
		Update("revoked_at", now).Error; err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(&model.AuthToken{}).
		Where("family_id = ? AND user_id = ? AND revoked_at IS NULL", family, userID).
		Update("revoked_at", now).Error
}

func (s *Store) ConsumeRefresh(ctx context.Context, id, replacement string, now time.Time) error {
	result := s.DB.WithContext(ctx).Model(&model.AuthToken{}).
		Where("token_id = ? AND token_type = ? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > ?", id, "REFRESH", now).
		Updates(map[string]any{"consumed_at": now, "replaced_by": replacement})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) RefreshCount(ctx context.Context, family string) (int64, error) {
	var n int64
	err := s.DB.WithContext(ctx).Model(&model.AuthToken{}).
		Where("family_id = ? AND token_type = ?", family, "REFRESH").Count(&n).Error
	return n, err
}

// 所有限流都走同一个 SQL 计数，Redis 中断/恢复不会重置额度或切换竞态。
func (s *Store) TakeAuthRate(ctx context.Context, key string, ttl time.Duration, maximum int) (bool, error) {
	allowed := false
	err := InTx(ctx, s.DB, func(st *Store) error {
		now := time.Now().UTC()
		bucket := &model.AuthRateBucket{BucketKey: key, ExpiresAt: now.Add(ttl)}
		if err := st.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(bucket).Error; err != nil {
			return err
		}
		if err := st.query(ctx, true).Where("bucket_key = ?", key).First(bucket).Error; err != nil {
			return err
		}
		if !bucket.ExpiresAt.After(now) {
			bucket.Attempts, bucket.ExpiresAt = 0, now.Add(ttl)
		}
		if bucket.Attempts < maximum+1 {
			bucket.Attempts++
		}
		allowed = bucket.Attempts <= maximum
		return st.DB.WithContext(ctx).Model(bucket).Updates(map[string]any{
			"attempts": bucket.Attempts, "expires_at": bucket.ExpiresAt,
		}).Error
	})
	return allowed, err
}

// 每轮有界清理；已消费 Refresh 必须保留到整个 family 绝对到期，才能识别重放。
func (s *Store) CleanupAuthRecords(ctx context.Context) error {
	now := time.Now().UTC()
	var ids []string
	if err := s.DB.WithContext(ctx).Model(&model.AuthToken{}).Where("family_expires_at <= ?", now).
		Order("family_expires_at, token_id").Limit(1000).Pluck("token_id", &ids).Error; err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := s.DB.WithContext(ctx).Where("token_id IN ? AND family_expires_at <= ?", ids, now).Delete(&model.AuthToken{}).Error; err != nil {
			return err
		}
	}
	var families []string
	if err := s.DB.WithContext(ctx).Model(&model.AuthSession{}).Where("expires_at <= ?", now).
		Order("expires_at, family_id").Limit(1000).Pluck("family_id", &families).Error; err != nil {
		return err
	}
	if len(families) > 0 {
		if err := s.DB.WithContext(ctx).Where("family_id IN ? AND expires_at <= ?", families, now).Delete(&model.AuthSession{}).Error; err != nil {
			return err
		}
	}
	var keys []string
	if err := s.DB.WithContext(ctx).Model(&model.AuthRateBucket{}).Where("expires_at <= ?", now).
		Order("expires_at, bucket_key").Limit(1000).Pluck("bucket_key", &keys).Error; err != nil {
		return err
	}
	if len(keys) > 0 {
		return s.DB.WithContext(ctx).Where("bucket_key IN ? AND expires_at <= ?", keys, now).Delete(&model.AuthRateBucket{}).Error
	}
	return nil
}
