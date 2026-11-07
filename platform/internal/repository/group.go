package repository

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func normalizeGroup(g *model.Group) {
	if len(g.QuotaRequested) == 0 {
		g.QuotaRequested = []byte("{}")
	}
	if len(g.QuotaObserved) == 0 {
		g.QuotaObserved = []byte("{}")
	}
}
func (s *Store) Group(ctx context.Context, id uint64, lock bool) (*model.Group, error) {
	g := &model.Group{}
	err := s.query(ctx, lock).First(g, "id = ?", id).Error
	normalizeGroup(g)
	return g, err
}
func (s *Store) Groups(ctx context.Context, userID uint64, admin bool, after uint64, limit int) ([]model.Group, error) {
	db := s.query(ctx, false).Model(&model.Group{}).Where("id > ? AND status <> ?", after, "DELETED")
	if !admin {
		sub := s.effective(ctx).Select("1").
			Where("b.scope_type = ? AND b.scope_id = tb_group.id AND b.user_id = ?", "group", userID)
		db = db.Where("EXISTS (?)", sub)
	}
	groups := []model.Group{}
	err := db.Order("id").Limit(limit).Find(&groups).Error
	for i := range groups {
		normalizeGroup(&groups[i])
	}
	return groups, err
}

// AccessLevel 仅从业务有效绑定聚合，不依赖外部 RoleBinding。
func (s *Store) AccessLevel(ctx context.Context, groupID, userID uint64) (string, error) {
	var rank int
	err := s.effective(ctx).Where("b.user_id = ? AND b.scope_type = ? AND b.scope_id = ?", userID, "group", groupID).
		Select("COALESCE(MAX(CASE r.access_level WHEN 'edit' THEN 2 WHEN 'view' THEN 1 ELSE 0 END),0)").Scan(&rank).Error
	if err != nil {
		return "", err
	}
	return []string{"none", "view", "edit"}[rank], nil
}
func (s *Store) OwnerCount(ctx context.Context, groupID uint64) (int, error) {
	var count int64
	err := s.effective(ctx).Where("b.scope_type = ? AND b.scope_id = ? AND r.code = ?", "group", groupID, "group_owner").
		Distinct("b.user_id").Count(&count).Error
	return int(count), err
}
func (s *Store) OwnerIDs(ctx context.Context, groupID uint64) ([]uint64, error) {
	ids := []uint64{}
	err := s.effective(ctx).Where("b.scope_type = ? AND b.scope_id = ? AND r.code = ?", "group", groupID, "group_owner").
		Distinct().Order("b.user_id").Pluck("b.user_id", &ids).Error
	return ids, err
}
func (s *Store) UserGroups(ctx context.Context, userID uint64) ([]uint64, error) {
	ids := []uint64{}
	err := s.query(ctx, false).Table("tb_role_binding b").Joins("JOIN tb_group g ON g.id = b.scope_id").
		Where("b.user_id = ? AND b.scope_type = ? AND b.status = ? AND g.status <> ?", userID, "group", "ACTIVE", "DELETED").
		Distinct().Order("b.scope_id").Pluck("b.scope_id", &ids).Error
	return ids, err
}
func (s *Store) MemberIDs(ctx context.Context, groupID uint64) ([]uint64, error) {
	ids := []uint64{}
	err := s.effective(ctx).Where("b.scope_type = ? AND b.scope_id = ?", "group", groupID).
		Distinct().Order("b.user_id").Pluck("b.user_id", &ids).Error
	return ids, err
}

type OwnerAccessState struct{ Count, Missing, Failed int64 }

func (s *Store) OwnerAccessState(ctx context.Context, groupID uint64, owner string) (*OwnerAccessState, error) {
	state := &OwnerAccessState{}
	receipt := s.FixedOwnerAccessReceipt(ctx)
	err := s.effective(ctx).
		Joins("LEFT JOIN tb_group_access a ON a.group_id = b.scope_id AND a.user_id = b.user_id").
		Where("b.scope_type = ? AND b.scope_id = ? AND r.code = ?", "group", groupID, "group_owner").
		Select(`COUNT(*) AS count,
   COALESCE(SUM(NOT COALESCE(a.desired_level='edit' AND a.applied_version=a.desired_version AND
   (a.status='READY' OR (a.status='DRIFTED' AND u.username=? AND EXISTS (?))),0)),0) AS missing,
   COALESCE(SUM(a.status='FAILED' OR (a.status='DRIFTED' AND NOT COALESCE(u.username=? AND EXISTS (?),0))),0) AS failed`, owner, receipt, owner, receipt).
		Scan(state).Error
	return state, err
}
