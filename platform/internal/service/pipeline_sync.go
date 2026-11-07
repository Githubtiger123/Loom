package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 只读观测使用独立租约。不要把这里的租约当成创建/停止/删除的执行权限。
type pipelineSyncClaim struct {
	model.PipelineRun
	SyncFailures uint32
	Owner        string `gorm:"-"`
}

func (pipelineSyncClaim) TableName() string { return "tb_pipeline_run" }

type pipelineSyncSample struct {
	Run         *kfp.Run
	Space       *MLExecutionSpace
	ExternalID  string
	RequestHash string
	BindJournal bool
	Reason      string
	Busy        bool
}

// SyncNextPipelineRun 不依赖 Operation 的状态或原请求人的权限。
// 每次最多领取一个已尝试提交的 Loom Run；不扫描 KFP 全集群资源。
func (s *Service) SyncNextPipelineRun(ctx context.Context) (bool, error) {
	if s.PipelineClient == nil {
		return false, fmt.Errorf("Pipeline 状态同步需要配置 KFP 客户端")
	}
	c := &pipelineSyncClaim{}
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Select("id", "group_id", "instance_id", "revision", "cluster", "sync_failures").
			Where("cluster = ? AND sync_enabled = TRUE AND deleted_at IS NULL AND sync_next_at <= UTC_TIMESTAMP(3)", s.Cluster).
			Where("sync_lease_until IS NULL OR sync_lease_until <= UTC_TIMESTAMP(3)").
			Order("sync_next_at").Order("id").Take(c).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Owner = uuid.NewString()
		return st.DB.WithContext(ctx).Model(&model.PipelineRun{}).Where("id = ?", c.ID).Updates(map[string]any{
			"sync_lease_owner": c.Owner, "sync_lease_until": gorm.Expr("TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(3))"),
			"sync_next_at": gorm.Expr("TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(3))"),
		}).Error
	})
	if err != nil || c.Owner == "" {
		return false, err
	}
	// 与命令执行器共用目标锁，防止更早采集的状态在较新观测/删除后落库。
	lock, err := repository.TargetLock(ctx, s.DB, c.Cluster, "pipeline_run", fmt.Sprint(c.ID))
	if err != nil {
		return true, s.finishPipelineSync(ctx, c, pipelineSyncSample{Busy: true})
	}
	defer lock.Close()
	sample, err := s.readPipelineSync(ctx, c)
	if ctx.Err() != nil {
		return true, ctx.Err() // 重启/退出由数据库租约到期恢复。
	}
	if err != nil {
		sample = pipelineSyncSample{Reason: "INTERNAL_ERROR"}
	}
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return true, err
	}
	return true, s.finishPipelineSync(ctx, c, sample)
}

func (s *Service) readPipelineSync(ctx context.Context, c *pipelineSyncClaim) (pipelineSyncSample, error) {
	n, err := s.Store.PipelineRun(ctx, c.GroupID, c.ID, false)
	if err != nil {
		return pipelineSyncSample{}, err
	}
	if n.InstanceID != c.InstanceID || n.Revision != c.Revision || n.Cluster != c.Cluster || n.DeletedAt != nil {
		return pipelineSyncSample{Busy: true}, nil
	}
	j := &pipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", n.InstanceID).First(j).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pipelineSyncSample{Reason: "IDENTITY_CONFLICT"}, nil
		}
		return pipelineSyncSample{}, err
	}
	if !j.Attempted || (j.ExternalID != nil && *j.ExternalID != n.InstanceID) || j.RunName != "loom-"+n.InstanceID ||
		(n.ExternalID != nil && *n.ExternalID != n.InstanceID) {
		return pipelineSyncSample{Reason: "IDENTITY_CONFLICT"}, nil
	}
	g, err := s.Store.Group(ctx, n.GroupID, false)
	if err != nil {
		return pipelineSyncSample{}, err
	}
	if g.DeletedAt != nil || g.Status == "DELETED" || g.Cluster != n.Cluster || g.Name != n.Namespace ||
		g.NamespaceUID == nil || g.ProfileUID == nil || *g.NamespaceUID == "" || *g.ProfileUID == "" || g.InstanceID == "" {
		return pipelineSyncSample{Reason: "SPACE_IDENTITY_CHANGED"}, nil
	}
	// 已持久化的机器回执证明原空间身份；不检查已撤权/停用的原用户，也不授予执行权限。
	e := &mlExecution{}
	err = s.DB.WithContext(ctx).Select("group_instance_id", "namespace_uid", "profile_uid").
		Where("instance_id = ? AND target_type = ? AND group_instance_id = ? AND namespace_uid = ? AND profile_uid = ?",
			n.InstanceID, "pipeline_run", g.InstanceID, *g.NamespaceUID, *g.ProfileUID).Take(e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pipelineSyncSample{Reason: "SPACE_IDENTITY_CHANGED"}, nil
	}
	if err != nil {
		return pipelineSyncSample{}, err
	}
	out := pipelineSyncSample{ExternalID: n.InstanceID, RequestHash: j.RequestHash, BindJournal: j.ExternalID == nil, Space: &MLExecutionSpace{
		InstanceID: *e.GroupInstanceID, NamespaceUID: *e.NamespaceUID, ProfileUID: *e.ProfileUID}}
	run, err := s.PipelineClient.GetRun(ctx, out.ExternalID)
	if err != nil {
		out.Reason = "EXTERNAL_UNAVAILABLE"
		if kfpHTTPStatus(err, 404) {
			out.Reason = "RESOURCE_NOT_FOUND"
		} else if kfpHTTPStatus(err, 401) || kfpHTTPStatus(err, 403) {
			out.Reason = "EXTERNAL_REJECTED"
		}
		return out, nil
	}
	if run.ID != out.ExternalID || run.Namespace != n.Namespace || run.Name != j.RunName ||
		run.Description != "loom:v1:"+n.InstanceID+":"+j.RequestHash {
		out.Reason = "IDENTITY_CONFLICT"
		return out, nil
	}
	if !validator.IsCatalogID(run.State) || len(run.State) > 32 {
		out.Reason = "EXTERNAL_RESPONSE_INVALID"
		return out, nil
	}
	out.Run = run
	return out, nil
}

func pipelineSyncSpaceMatches(g *model.Group, space *MLExecutionSpace) bool {
	return space != nil && g.DeletedAt == nil && g.Status != "DELETED" && g.InstanceID == space.InstanceID &&
		g.NamespaceUID != nil && *g.NamespaceUID == space.NamespaceUID &&
		g.ProfileUID != nil && *g.ProfileUID == space.ProfileUID
}

func (s *Service) finishPipelineSync(ctx context.Context, c *pipelineSyncClaim, sample pipelineSyncSample) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		// 与业务变更一致：先空间再实例。网络读取已在事务外完成。
		g, err := st.Group(ctx, c.GroupID, true)
		if err != nil {
			return err
		}
		n, err := st.PipelineRun(ctx, c.GroupID, c.ID, true)
		if err != nil {
			return err
		}
		q := st.DB.WithContext(ctx).Model(&model.PipelineRun{}).
			Where("id = ? AND instance_id = ? AND sync_lease_owner = ? AND sync_lease_until > UTC_TIMESTAMP(3)", c.ID, c.InstanceID, c.Owner)
		var live int64
		if err := q.Count(&live).Error; err != nil {
			return err
		}
		if live != 1 {
			return nil // 迟到响应不能覆盖接管者。
		}
		fields := map[string]any{"sync_lease_owner": nil, "sync_lease_until": nil}
		if n.Revision != c.Revision || n.DeletedAt != nil || sample.Busy {
			fields["sync_next_at"] = gorm.Expr("TIMESTAMPADD(SECOND,1,UTC_TIMESTAMP(3))")
			return q.Updates(fields).Error
		}
		if sample.Space != nil && (!pipelineSyncSpaceMatches(g, sample.Space) || g.Cluster != n.Cluster || g.Name != n.Namespace) {
			sample.Run, sample.Reason = nil, "SPACE_IDENTITY_CHANGED"
		}
		if sample.Run != nil && (sample.Space == nil || (n.ExternalID != nil && *n.ExternalID != sample.ExternalID)) {
			sample.Run, sample.Reason = nil, "IDENTITY_CONFLICT"
		}
		if n.ExecutionCleaned {
			// 编辑清理完成后保留旧 ID 只作历史，不让迟到观测覆盖已保存的新配置。
			fields["sync_enabled"] = false
			return q.Updates(fields).Error
		}
		fields["kfp_sync_attempted_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
		delay := s.PipelineConfig.SyncInterval.Std()
		if sample.Run == nil {
			fields["kfp_sync_reason"] = sample.Reason
			// 404、超时、撤销机器权限都只是观测异常，不伪造 FAILED/DELETED 或刷新 observed_at。
			failures := min(c.SyncFailures+1, uint32(16))
			fields["sync_failures"] = failures
			delay = min(15*time.Second*time.Duration(1<<min(failures-1, uint32(5))), 5*time.Minute)
		} else {
			updateStatus := n.DesiredState == "RUNNING"
			if n.DesiredState == "STOPPED" {
				active, err := st.HasMLOperation(ctx, "pipeline_run", n.ID)
				if err != nil {
					return err
				}
				updateStatus = !active
			}
			if sample.BindJournal {
				// 响应丢失且原操作已失败时，观测也可恢复已核验的外部身份；没有外部写操作。
				if err := st.DB.WithContext(ctx).Model(&pipelineSubmission{}).
					Where("instance_id = ? AND attempted = TRUE AND request_hash = ? AND (external_id IS NULL OR external_id = ?)",
						n.InstanceID, sample.RequestHash, sample.ExternalID).Update("external_id", sample.ExternalID).Error; err != nil {
					return err
				}
			}
			raw, state := sample.Run.State, pipelineRunState(sample.Run.State)
			fields["kfp_state"], fields["kfp_synced_at"] = raw, gorm.Expr("UTC_TIMESTAMP(3)")
			fields["kfp_sync_reason"], fields["sync_failures"] = nil, 0
			if n.ExternalID == nil {
				fields["external_id"] = sample.ExternalID
			}
			if pipelineTerminalState(state) && n.DesiredState != "DELETED" &&
				(n.DesiredState != "STOPPED" || n.Status == state) {
				delay = s.PipelineConfig.TerminalSyncInterval.Std()
			}
			if state == "UNKNOWN" || (n.ObservedState == "RUNNING" && state == "PENDING") {
				fields["kfp_sync_reason"] = "STATE_UNKNOWN_OR_REGRESSED"
			} else {
				changed := n.ObservedState != state
				fields["observed_state"], fields["observed_at"] = state, gorm.Expr("UTC_TIMESTAMP(3)")
				n.ObservedState = state
				// 只更新现实，不改 desired_state/deleted_at，不结束/重开操作单。
				// stop/delete 的完成仍由具备执行权限的命令确认，不能让观测覆盖清理进度。
				if updateStatus {
					changed = changed || n.Status != state
					fields["status"], fields["status_reason"] = state, nil
					n.Status = state
				}
				if n.DesiredState == "RUNNING" {
					// 唯一 RUNNING 意图是原始提交，已核对其不可变 Run ID/请求摘要。
					// 即使原操作撤权/失败，也可记录这个已发生的提交现实，而非授权新执行。
					fields["applied_revision"] = n.Revision
					n.AppliedRevision = n.Revision
				}
				if changed && n.LastOperationID != nil {
					o := &model.Operation{ID: *n.LastOperationID, Action: "pipeline.run.sync", TargetType: "pipeline_run"}
					if err := mlRecord(ctx, st, o, &mlObject{identity: &n.MLIdentity, state: &n.MLExecutionState, row: n}, "RUNTIME_OBSERVATION"); err != nil {
						return err
					}
				}
			}
		}
		// 抖动分散批量完成任务的下一轮查询；限速/排队时允许超过目标间隔。
		delay += time.Duration(rand.Int64N(max(int64(delay/5), 1)))
		fields["sync_next_at"] = gorm.Expr("TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(3))", delay.Microseconds())
		r := q.Where("revision = ?", c.Revision).Updates(fields)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}

func pipelineTerminalState(state string) bool {
	return state == "SUCCEEDED" || state == "FAILED" || state == "STOPPED"
}
