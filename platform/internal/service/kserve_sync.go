package service

import (
	"context"
	"errors"
	"time"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type kserveSyncClaim struct {
	Service    model.KServeService
	LeaseOwner string
}

func (s *Service) claimKServeSync(ctx context.Context) (*kserveSyncClaim, error) {
	var claim *kserveSyncClaim
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		n := &model.KServeService{}
		err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("cluster = ? AND sync_enabled = TRUE AND deleted_at IS NULL AND sync_next_at <= UTC_TIMESTAMP(3) AND (sync_lease_until IS NULL OR sync_lease_until <= UTC_TIMESTAMP(3))", s.Cluster).
			Where("NOT EXISTS (SELECT 1 FROM tb_operation o WHERE o.target_type = 'kserve_service' AND o.target_key = CAST(tb_kserve_service.id AS CHAR) AND o.status IN ?)", []string{"PENDING", "RUNNING", "RETRYING"}).
			Order("sync_next_at").Order("id").First(n).Error
		if err != nil {
			return err
		}
		owner := uuid.NewString()
		r := st.DB.WithContext(ctx).Model(n).Where("id = ? AND instance_id = ?", n.ID, n.InstanceID).Updates(map[string]any{
			"sync_lease_owner": owner, "sync_lease_until": gorm.Expr("TIMESTAMPADD(SECOND,45,UTC_TIMESTAMP(3))"),
		})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		claim = &kserveSyncClaim{Service: *n, LeaseOwner: owner}
		return nil
	})
	return claim, err
}

// SyncNextKServe 对 Watch 丢事件、断线和进程重启做周期修复。
func (s *Service) SyncNextKServe(ctx context.Context) (bool, error) {
	if s.KServeClient == nil || !s.KServeConfig.Enabled {
		return false, errors.New("KServe 同步依赖未配置")
	}
	claim, err := s.claimKServeSync(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	n := claim.Service
	uid := ""
	if n.ExternalID != nil {
		uid = *n.ExternalID
	}
	obs, readErr := s.KServeClient.ObserveKServe(ctx, k8s.KServeRef{ID: n.ID, GroupID: n.GroupID, Revision: n.AppliedRevision,
		InstanceID: n.InstanceID, Namespace: n.Namespace, Name: n.Name, UID: uid})
	return true, s.finishKServeSync(ctx, claim, obs, readErr)
}

func (s *Service) finishKServeSync(ctx context.Context, claim *kserveSyncClaim, obs *k8s.KServeObservation, readErr error) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		n := &model.KServeService{}
		if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", claim.Service.ID).First(n).Error; err != nil {
			return err
		}
		q := st.DB.WithContext(ctx).Model(n).Where("id = ? AND instance_id = ? AND sync_lease_owner = ?", n.ID, claim.Service.InstanceID, claim.LeaseOwner)
		fields := map[string]any{"sync_lease_owner": nil, "sync_lease_until": nil, "kserve_sync_attempted_at": gorm.Expr("UTC_TIMESTAMP(3)")}
		delay := s.KServeConfig.ResyncInterval.Std()
		if n.Revision != claim.Service.Revision || n.DesiredState != "SERVING" || n.DeletedAt != nil {
			fields["sync_next_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
			if n.DesiredState != "SERVING" || n.DeletedAt != nil {
				fields["sync_enabled"] = false
			}
			return q.Updates(fields).Error
		}
		if readErr != nil {
			fields["kserve_ready"] = false
			failures := min(n.SyncFailures+1, uint32(16))
			fields["sync_failures"], fields["kserve_reason"] = failures, "KSERVE_READ_FAILED"
			var permanent *k8s.Permanent
			if errors.As(readErr, &permanent) {
				fields["kserve_reason"], fields["observed_state"], fields["status"] = "IDENTITY_CONFLICT", "UNKNOWN", "ERROR"
				fields["status_reason"], fields["observed_at"] = "IDENTITY_CONFLICT", gorm.Expr("UTC_TIMESTAMP(3)")
			}
			delay = min(15*time.Second*time.Duration(1<<min(failures-1, uint32(5))), 5*time.Minute)
		} else if obs == nil {
			fields["kserve_ready"] = false
			fields["sync_failures"], fields["kserve_reason"] = n.SyncFailures+1, "KSERVE_EMPTY_OBSERVATION"
		} else if obs.Absent {
			fields["sync_failures"], fields["kserve_ready"] = 0, false
			fields["kserve_url"], fields["kserve_address"] = nil, nil
			if n.ExternalID != nil {
				// 已绑定对象在 SERVING 意图下消失是漂移；不能自动重建同 revision。
				fields["observed_state"], fields["status"], fields["status_reason"] = "FAILED", "FAILED", "RESOURCE_NOT_FOUND"
				fields["kserve_reason"], fields["observed_at"] = "RESOURCE_NOT_FOUND", gorm.Expr("UTC_TIMESTAMP(3)")
			}
		} else if n.ExternalID == nil || *n.ExternalID != obs.UID || obs.InstanceID != n.InstanceID || obs.Revision != n.AppliedRevision {
			fields["kserve_ready"] = false
			fields["sync_failures"], fields["kserve_reason"] = n.SyncFailures+1, "IDENTITY_CONFLICT"
		} else {
			applyKServeObservationFields(fields, obs)
			fields["sync_failures"], fields["kserve_synced_at"] = 0, gorm.Expr("UTC_TIMESTAMP(3)")
			if obs.State == "SERVING" || obs.State == "PENDING" || obs.State == "FAILED" {
				fields["observed_state"], fields["status"], fields["observed_at"] = obs.State, obs.State, gorm.Expr("UTC_TIMESTAMP(3)")
				fields["status_reason"] = nullableString(obs.Reason)
			}
		}
		fields["sync_next_at"] = gorm.Expr("TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(3))", delay.Microseconds())
		r := q.Updates(fields)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}

// ApplyKServeWatchObservation 写入已绑定 CR 的即时状态；Watch 不创建业务身份，也不重建资源。
func (s *Service) ApplyKServeWatchObservation(ctx context.Context, obs *k8s.KServeObservation) error {
	if obs == nil || obs.ID == 0 || obs.GroupID == 0 || obs.InstanceID == "" || obs.UID == "" {
		return nil
	}
	fields := map[string]any{}
	applyKServeObservationFields(fields, obs)
	fields["kserve_sync_attempted_at"], fields["kserve_synced_at"], fields["sync_failures"] = gorm.Expr("UTC_TIMESTAMP(3)"), gorm.Expr("UTC_TIMESTAMP(3)"), 0
	if obs.State == "SERVING" || obs.State == "PENDING" || obs.State == "FAILED" {
		fields["observed_state"], fields["status"], fields["observed_at"] = obs.State, obs.State, gorm.Expr("UTC_TIMESTAMP(3)")
		fields["status_reason"] = nullableString(obs.Reason)
	}
	return s.DB.WithContext(ctx).Model(&model.KServeService{}).
		Where("NOT EXISTS (SELECT 1 FROM tb_operation o WHERE o.target_type = 'kserve_service' AND o.target_key = CAST(tb_kserve_service.id AS CHAR) AND o.status IN ?)", []string{"PENDING", "RUNNING", "RETRYING"}).
		Where("id = ? AND group_id = ? AND instance_id = ? AND namespace = ? AND name = ? AND external_id = ? AND applied_revision = ? AND desired_state = 'SERVING' AND deleted_at IS NULL",
			obs.ID, obs.GroupID, obs.InstanceID, obs.Namespace, obs.Name, obs.UID, obs.Revision).Updates(fields).Error
}

func applyKServeObservationFields(fields map[string]any, obs *k8s.KServeObservation) {
	fields["kserve_generation"], fields["kserve_observed_generation"] = obs.Generation, obs.ObservedGeneration
	fields["kserve_resource_version"], fields["kserve_ready"] = nullableString(obs.ResourceVersion), obs.Ready
	fields["kserve_url"], fields["kserve_address"], fields["kserve_reason"] = nullableString(obs.URL), nullableString(obs.Address), nullableString(obs.Reason)
}
