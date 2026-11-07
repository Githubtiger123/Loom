package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RefreshAccessTarget 在 worker 执行前按当前有效绑定重算，覆盖到期和用户状态变化。
func (s *Service) RefreshAccessTarget(ctx context.Context, groupID, userID uint64) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		if err := lockAuthorization(ctx, st); err != nil {
			return err
		}
		return refreshAccess(ctx, st, groupID, userID)
	})
}

// MaintainAccess 可由 cron 重复调用；全部变更先持久化，不直接操作集群。
func (s *Service) MaintainAccess(ctx context.Context) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		if err := lockAuthorization(ctx, st); err != nil {
			return err
		}
		expired := []model.Binding{}
		if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("status = ? AND expires_at <= UTC_TIMESTAMP(3)", "ACTIVE").Order("id").Limit(200).Find(&expired).Error; err != nil {
			return err
		}
		for _, e := range expired {
			if err := st.DB.WithContext(ctx).Model(&model.Binding{}).Where("id = ?", e.ID).
				Updates(map[string]any{"status": "EXPIRED", "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
			if err := st.BumpUserVersion(ctx, e.UserID); err != nil {
				return err
			}
			if e.Scope == "group" {
				if err := refreshAccess(ctx, st, e.ScopeID, e.UserID); err != nil {
					return err
				}
			}
			if err := audit(ctx, st, 0, e.ScopeID, "binding.expire", "binding", fmt.Sprint(e.ID), "", "SUCCEEDED"); err != nil {
				return err
			}
		}
		// 已就绪对象也需周期观测，修补进程中断与迟到写入。失败/冲突目标需人工重试，避免无限重试风暴。
		targets := []model.GroupAccess{}
		active := st.DB.WithContext(ctx).Model(&model.Operation{}).Select("1").
			Where("action = ? AND target_key = CONCAT(a.group_id,'/',a.user_id) AND status IN ?", "access.reconcile", []string{"PENDING", "RUNNING", "RETRYING"})
		if err := st.DB.WithContext(ctx).Table("tb_group_access a").Select("a.group_id,a.user_id,a.desired_version").
			Joins("JOIN tb_group g ON g.id = a.group_id").
			Where("g.cluster = ? AND g.status IN ? AND a.status IN ?", s.Cluster, []string{"CREATING", "ACTIVE", "DEGRADED", "DISABLING", "DISABLED", "RESTORING", "DELETING"}, []string{"PENDING", "APPLYING", "REVOKING", "READY"}).
			Where("a.observed_at IS NULL OR a.observed_at < TIMESTAMPADD(SECOND,-60,UTC_TIMESTAMP(3))").
			Where("NOT EXISTS (?)", active).Order("a.observed_at,a.group_id,a.user_id").Limit(100).Scan(&targets).Error; err != nil {
			return err
		}
		for _, t := range targets {
			payload, _ := json.Marshal(map[string]uint64{"groupId": t.GroupID, "userId": t.UserID})
			o := &model.Operation{ID: uuid.NewString(), Action: "access.reconcile", TargetType: "access", TargetKey: fmt.Sprintf("%d/%d", t.GroupID, t.UserID), TargetVersion: t.DesiredVersion, Status: "PENDING", Step: "OBSERVE", Payload: payload, Result: json.RawMessage(`{}`)}
			if err := st.InsertOperation(ctx, o, "observe-"+uuid.NewString(), fmt.Sprintf("%064d", t.DesiredVersion)); err != nil {
				return err
			}
		}
		return nil
	})
}
