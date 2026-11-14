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

// EnqueueKServeObservations 只从数据库登记项派生业务键；空 namespace 用于全量 sweep。
// 按 ID 游标每页最多 100 项，不在 namespace 队列键内串行读取 Kubernetes。
func (s *Service) EnqueueKServeObservations(ctx context.Context, namespace string, enqueue func(uint64)) error {
	const pageSize = 100
	var after uint64
	for {
		var page []model.KServeService
		q := s.DB.WithContext(ctx).Select("id").
			Where("cluster = ? AND sync_enabled = TRUE AND desired_state = 'SERVING' AND deleted_at IS NULL AND external_id IS NOT NULL AND external_id <> '' AND id > ?", s.Cluster, after)
		if namespace != "" {
			q = q.Where("namespace = ?", namespace)
		}
		if err := q.Order("id").Limit(pageSize).Find(&page).Error; err != nil {
			return err
		}
		for _, n := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			enqueue(n.ID)
			after = n.ID
		}
		if len(page) < pageSize {
			return nil
		}
	}
}

func (s *Service) claimKServeSync(ctx context.Context, id uint64) (*kserveSyncClaim, error) {
	var claim *kserveSyncClaim
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		n := &model.KServeService{}
		q := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("cluster = ? AND sync_enabled = TRUE AND deleted_at IS NULL AND (sync_lease_until IS NULL OR sync_lease_until <= UTC_TIMESTAMP(3))", s.Cluster).
			Where("NOT EXISTS (SELECT 1 FROM tb_operation o WHERE o.target_type = 'kserve_service' AND o.target_key = CAST(tb_kserve_service.id AS CHAR) AND o.status IN ?)", []string{"PENDING", "RUNNING", "RETRYING"})
		if id == 0 {
			q = q.Where("sync_next_at <= UTC_TIMESTAMP(3)")
		} else {
			// 事件可提前触发，但不能越过执行操作、同步租约或业务登记边界。
			q = q.Where("id = ? AND desired_state = 'SERVING' AND external_id IS NOT NULL AND external_id <> ''", id)
		}
		err := q.Order("sync_next_at").Order("id").First(n).Error
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

// SyncNextKServe 对 Informer 丢事件、断线和进程重启做持久租约周期修复。
func (s *Service) SyncNextKServe(ctx context.Context) (bool, error) {
	return s.syncKServe(ctx, 0)
}

// SyncKServeObservation 对单个登记服务做实时重读；与周期对账共用租约和写回 fence。
// 删除通知也走 GET，不接受事件对象中的 absent、UID 或 revision 作为事实。
func (s *Service) SyncKServeObservation(ctx context.Context, id uint64) error {
	if id == 0 {
		return nil
	}
	_, err := s.syncKServe(ctx, id)
	return err
}

func (s *Service) syncKServe(ctx context.Context, id uint64) (bool, error) {
	if s.KServeClient == nil || !s.KServeConfig.Enabled {
		return false, errors.New("KServe 同步依赖未配置")
	}
	claim, err := s.claimKServeSync(ctx, id)
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
		if n.InstanceID != claim.Service.InstanceID || n.SyncLeaseOwner == nil || *n.SyncLeaseOwner != claim.LeaseOwner {
			return mlExecutionConflict()
		}
		q := st.DB.WithContext(ctx).Model(n).Where("id = ? AND instance_id = ? AND sync_lease_owner = ?", n.ID, claim.Service.InstanceID, claim.LeaseOwner)
		fields := map[string]any{"sync_lease_owner": nil, "sync_lease_until": nil, "kserve_sync_attempted_at": gorm.Expr("UTC_TIMESTAMP(3)")}
		delay := s.KServeConfig.ResyncInterval.Std()
		sameUID := n.ExternalID == nil && claim.Service.ExternalID == nil ||
			n.ExternalID != nil && claim.Service.ExternalID != nil && *n.ExternalID == *claim.Service.ExternalID
		if n.Revision != claim.Service.Revision || n.AppliedRevision != claim.Service.AppliedRevision || !sameUID || !n.SyncEnabled || n.DesiredState != "SERVING" || n.DeletedAt != nil {
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

func applyKServeObservationFields(fields map[string]any, obs *k8s.KServeObservation) {
	fields["kserve_generation"], fields["kserve_observed_generation"] = obs.Generation, obs.ObservedGeneration
	fields["kserve_resource_version"], fields["kserve_ready"] = nullableString(obs.ResourceVersion), obs.Ready
	fields["kserve_url"], fields["kserve_address"], fields["kserve_reason"] = nullableString(obs.URL), nullableString(obs.Address), nullableString(obs.Reason)
}
