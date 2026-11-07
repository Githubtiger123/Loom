package repository

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func normalizeOperation(o *model.Operation) {
	if len(o.Result) == 0 {
		o.Result = []byte("{}")
	}
}
func (s *Store) Operation(ctx context.Context, id string) (*model.Operation, error) {
	o := &model.Operation{}
	err := s.query(ctx, false).Where("operation_id = ?", id).First(o).Error
	normalizeOperation(o)
	return o, err
}
func (s *Store) RequestOperation(ctx context.Context, actorID uint64, key string) (*model.Operation, string, error) {
	o := &model.Operation{}
	err := s.query(ctx, false).Where("actor_id = ? AND request_key = ?", actorID, key).First(o).Error
	normalizeOperation(o)
	return o, o.PayloadHash, err
}
func (s *Store) InsertOperation(ctx context.Context, o *model.Operation, key, hash string) error {
	o.RequestKey, o.PayloadHash = key, hash
	if o.Cluster == "" {
		o.Cluster = "default"
		// 只有空间和访问操作的目标才携带 groupID，不能把 user/role ID 当空间。
		if o.TargetType == "group" || o.TargetType == "access" {
			id, err := strconv.ParseUint(strings.SplitN(o.TargetKey, "/", 2)[0], 10, 64)
			if err != nil {
				return err
			}
			group, err := s.Group(ctx, id, false)
			if err != nil {
				return err
			}
			o.Cluster = group.Cluster
		}
	}
	normalizeOperation(o)
	if o.Status == "SUCCEEDED" {
		now := time.Now().UTC()
		o.FinishedAt = &now
	}
	return s.query(ctx, false).Create(o).Error
}
func (s *Store) HasAccessOperation(ctx context.Context, target string, version *uint64) (bool, error) {
	var count int64
	db := s.query(ctx, false).Model(&model.Operation{}).
		Where("action = ? AND target_key = ? AND status IN ?", "access.reconcile", target, []string{"PENDING", "RUNNING", "RETRYING"})
	if version != nil {
		db = db.Where("target_version = ?", *version)
	}
	err := db.Count(&count).Error
	return count > 0, err
}
