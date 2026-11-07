package repository

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

func (s *Store) User(ctx context.Context, id uint64, lock bool) (*model.User, error) {
	u := &model.User{}
	err := s.query(ctx, lock).First(u, "id = ?", id).Error
	return u, err
}
func (s *Store) Identity(ctx context.Context, issuer, subject string) (*model.User, error) {
	u := &model.User{}
	err := s.query(ctx, false).Where("issuer = ? AND subject = ?", issuer, subject).First(u).Error
	return u, err
}
func (s *Store) Users(ctx context.Context, after uint64, limit int) ([]model.User, error) {
	users := []model.User{}
	err := s.query(ctx, false).Where("id > ?", after).Order("id").Limit(limit).Find(&users).Error
	return users, err
}
func (s *Store) InsertUser(ctx context.Context, u *model.User) error {
	return s.query(ctx, false).Create(u).Error
}
func (s *Store) RecordLogin(ctx context.Context, id uint64) error {
	return s.query(ctx, false).Model(&model.User{}).Where("id = ? AND status = ?", id, "ACTIVE").
		Update("last_login_at", gorm.Expr("UTC_TIMESTAMP(3)")).Error
}
func (s *Store) SetUserStatus(ctx context.Context, id uint64, status string) error {
	return s.query(ctx, false).Model(&model.User{}).Where("id = ? AND status <> ?", id, status).
		Updates(map[string]any{"status": status, "authorization_version": gorm.Expr("authorization_version + 1"), "session_version": gorm.Expr("session_version + 1")}).Error
}
func (s *Store) BumpUserVersion(ctx context.Context, id uint64) error {
	return s.query(ctx, false).Model(&model.User{}).Where("id = ?", id).
		Update("authorization_version", gorm.Expr("authorization_version + 1")).Error
}
