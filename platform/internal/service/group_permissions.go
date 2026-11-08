package service

import (
	"context"
	"time"

	"github.com/githubtiger123/loom/platform/internal/rbac"
)

type GroupPermissionsView struct {
	UserID      uint64    `json:"userId"`
	GroupID     uint64    `json:"groupId"`
	Permissions []string  `json:"permissions"`
	EvaluatedAt time.Time `json:"evaluatedAt"`
}

// GroupPermissions only exposes the caller's group-scoped capabilities.
// Ownership, live group state and resource revisions remain command-time checks.
func (s *Service) GroupPermissions(ctx context.Context, actor, groupID uint64) (*GroupPermissionsView, error) {
	if _, err := s.Group(ctx, actor, groupID); err != nil {
		return nil, err
	}
	admin, err := s.Store.IsAdmin(ctx, actor)
	if err != nil {
		return nil, err
	}
	codes := []string{}
	if admin {
		for _, permission := range rbac.Permissions() {
			if permission.Scope == "group" {
				codes = append(codes, permission.Code)
			}
		}
	} else {
		codes, err = s.Store.EffectiveGroupPermissionCodes(ctx, actor, groupID)
		if err != nil {
			return nil, err
		}
	}
	return &GroupPermissionsView{UserID: actor, GroupID: groupID, Permissions: codes, EvaluatedAt: time.Now().UTC()}, nil
}
