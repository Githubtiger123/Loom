package service

import (
	"context"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

// Stop uses Argo's graceful shutdown protocol, not CR deletion or a fabricated
// SQL terminal state. Cleanup remains gated by the terminal archive.
func nativeLifecycleAllowed(ctx context.Context, st *repository.Store, run *model.PipelineRun, action MLAction) error {
	if run.Engine != model.PipelineEngineNativeArgo {
		return nil
	}
	if action == MLPipelineStop {
		a, err := st.PipelineRunAttempt(ctx, run.GroupID, run.ID, run.InstanceID, false)
		if err != nil || a.WorkflowUID == nil {
			return apperr.New(apperr.CodeConflict, "请等待执行绑定 Workflow 后再停止")
		}
		_, checkpoint, err := nativeStartSummary(a)
		if err != nil || checkpoint == nil || checkpoint.State != "CONFIRMED" {
			return apperr.New(apperr.CodeConflict, "请等待 Workflow 启动确认后再停止")
		}
		return nil
	}
	if run.ExecutionCleaned {
		return nil
	}
	a, err := st.PipelineRunAttempt(ctx, run.GroupID, run.ID, run.InstanceID, false)
	if err != nil {
		return apperr.New(apperr.CodeConflict, "执行尚未取得终态快照，不能清理或编辑")
	}
	if !nativeHasTerminalArchive(a) || run.Status != "SUCCEEDED" && run.Status != "FAILED" && run.Status != "STOPPED" {
		return apperr.New(apperr.CodeConflict, "请等待执行终态、产物入库及最终回执，再清理或编辑")
	}
	return nil
}

func (s *Service) executeNativeLifecycleClaim(ctx context.Context, claim *MLExecutionClaim) error {
	if claim.Command.Action == MLPipelineStop {
		return s.executeNativeStopClaim(ctx, claim)
	}
	if claim.Command.Action != MLPipelineDelete && claim.Command.Action != MLPipelineEdit {
		return mlExecutionInvalid()
	}
	run, err := s.Store.PipelineRun(ctx, claim.Command.GroupID, claim.Command.ResourceID, false)
	if err != nil {
		return err
	}
	if !run.ExecutionCleaned {
		observation, err := s.DeleteArchivedNativePipelineWorkflow(ctx, claim)
		if err != nil {
			return s.deferNativeClaim(ctx, claim, err)
		}
		if !observation.WorkflowAbsent || !observation.PodsAbsent {
			return s.deferNativeClaim(ctx, claim, nil)
		}
	}
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		current, ok := n.row.(*model.PipelineRun)
		if !ok || current.Engine != model.PipelineEngineNativeArgo {
			return mlExecutionConflict()
		}
		if !current.ExecutionCleaned {
			a, err := st.PipelineRunAttempt(ctx, c.GroupID, c.ResourceID, c.InstanceID, true)
			if err != nil {
				return err
			}
			_, checkpoint, err := nativeCleanupSummary(a)
			if err != nil || checkpoint == nil || checkpoint.OperationID != o.ID || checkpoint.State != "ABSENT" || current.ExternalID == nil || checkpoint.WorkflowUID != *current.ExternalID {
				return mlExecutionConflict()
			}
		}
		fields := map[string]any{"execution_cleaned": true, "applied_revision": c.Revision, "status": "STOPPED", "observed_state": "STOPPED", "status_reason": nil}
		if c.Action == MLPipelineEdit {
			if err := applyPipelineEdit(ctx, st, c, n, fields, ""); err != nil {
				return err
			}
		} else {
			fields["deleted_at"] = time.Now().UTC()
			fields["status"], fields["observed_state"] = "DELETED", "ABSENT"
		}
		if err := st.DB.WithContext(ctx).Model(current).Where("id = ? AND revision = ? AND instance_id = ?", c.ResourceID, c.Revision, c.InstanceID).Updates(fields).Error; err != nil {
			return err
		}
		n.state.Status = fields["status"].(string)
		n.state.ObservedState = fields["observed_state"].(string)
		n.state.AppliedRevision = c.Revision
		if err := mlRecord(ctx, st, o, n, "OBSERVATION"); err != nil {
			return err
		}
		if err := result(o, map[string]any{"resourceId": c.ResourceID, "instanceId": c.InstanceID, "revision": c.Revision, "status": n.state.Status, "executionCleaned": true, "executionPending": false}); err != nil {
			return err
		}
		actor := uint64(0)
		if err := st.DB.WithContext(ctx).Create(&model.Audit{OperationID: &o.ID, ActorID: &actor, ActorName: "ml-executor", GroupID: &c.GroupID, Action: o.Action, TargetType: o.TargetType, TargetID: o.TargetKey, Outcome: "SUCCEEDED"}).Error; err != nil {
			return err
		}
		r := mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Updates(map[string]any{"status": "SUCCEEDED", "step": "COMPLETED", "result": string(o.Result), "lease_owner": nil, "lease_until": nil, "finished_at": gorm.Expr("UTC_TIMESTAMP(3)"), "last_error": nil})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}
