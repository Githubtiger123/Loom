package repository

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func (s *Store) LoginUser(ctx context.Context, issuer, username string) (*model.User, error) {
	u := &model.User{}
	err := s.query(ctx, false).Where("issuer = ? AND username = ? AND deleted_at IS NULL", issuer, username).First(u).Error
	return u, err
}

func (s *Store) Credential(ctx context.Context, userID uint64, lock bool) (*model.LocalCredential, error) {
	c := &model.LocalCredential{}
	err := s.query(ctx, lock).Where("user_id = ?", userID).First(c).Error
	return c, err
}
