package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type nativeStopCheckpoint struct {
	OperationID string `json:"operationId"`
	WorkflowUID string `json:"workflowUID"`
	Token       string `json:"token"`
	Revision    uint64 `json:"revision"`
	Completed   bool   `json:"completed"`
	ArchiveHash string `json:"archiveHash,omitempty"`
}

func nativeStopState(a *model.PipelineRunAttempt) (*nativeStopCheckpoint, error) {
	var summary struct {
		Stop *nativeStopCheckpoint `json:"stop"`
	}
	if json.Unmarshal(a.RuntimeSummary, &summary) != nil {
		return nil, nativeFinalConflict()
	}
	if c := summary.Stop; c != nil {
		if !validator.IsCanonicalUUID(c.OperationID) || !validator.IsCanonicalUUID(c.Token) || c.Revision <= a.Revision || a.WorkflowUID == nil || c.WorkflowUID != *a.WorkflowUID || c.Completed && !validator.IsSHA256(c.ArchiveHash) {
			return nil, nativeFinalConflict()
		}
	}
	return summary.Stop, nil
}

// Persist authorization before the external stop request. Each retry uses the
// same token, UID and frozen spec; a newer stop may continue that same request.
func (s *Service) nativeStopTx(ctx context.Context, claim *MLExecutionClaim, fn func(*repository.Store, *model.Operation, *MLCommand, *mlObject, *mlExecution, *model.PipelineRunAttempt, *nativeStopCheckpoint) error) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err = mlLiveExecution(ctx, st, o, c, n, e, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		run, ok := n.row.(*model.PipelineRun)
		if !ok || run.Engine != model.PipelineEngineNativeArgo || c.Action != MLPipelineStop || c.Cluster != s.Cluster || c.InstanceID != claim.InstanceID {
			return mlExecutionConflict()
		}
		a, err := st.PipelineRunAttempt(ctx, c.GroupID, c.ResourceID, c.InstanceID, true)
		if err != nil {
			return err
		}
		stop, err := nativeStopState(a)
		if err != nil {
			return err
		}
		if err = fn(st, o, c, n, e, a, stop); err != nil {
			return err
		}
		if o.Status == "SUCCEEDED" {
			return nil
		} // callback's final lease-CAS completed this transaction
		var count int64
		if err = mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}

func (s *Service) executeNativeStopClaim(ctx context.Context, claim *MLExecutionClaim) error {
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(claim.Command.ResourceID))
	if err != nil {
		return err
	}
	var ref k8s.PipelineWorkflowRef
	var requestHash string
	err = s.nativeStopTx(ctx, claim, func(st *repository.Store, o *model.Operation, c *MLCommand, n *mlObject, e *mlExecution, a *model.PipelineRunAttempt, stop *nativeStopCheckpoint) error {
		j := &model.PipelineSubmission{}
		if err := st.DB.WithContext(ctx).Where("instance_id = ?", a.InstanceID).First(j).Error; err != nil {
			return err
		}
		if _, err := restoreNativeStaticNodes(a, j); err != nil {
			return err
		}
		if stop == nil {
			if a.WorkflowUID == nil {
				return mlExecutionConflict()
			}
			stop = &nativeStopCheckpoint{OperationID: o.ID, WorkflowUID: *a.WorkflowUID, Token: uuid.NewString(), Revision: c.Revision}
		} else if stop.Completed {
			return mlExecutionConflict()
		} else {
			stop.OperationID = o.ID
			stop.Revision = c.Revision
		}
		summary, start, err := nativeStartSummary(a)
		if err != nil || start == nil || start.State != "CONFIRMED" {
			return mlExecutionConflict()
		}
		summary["stop"] = stop
		raw, _ := json.Marshal(summary)
		if err = st.DB.WithContext(ctx).Model(a).Where("instance_id = ? AND observation_version = ?", a.InstanceID, a.ObservationVersion).Updates(map[string]any{"runtime_summary": string(raw), "observation_version": gorm.Expr("observation_version + 1")}).Error; err != nil {
			return err
		}
		a.RuntimeSummary = raw
		var valid bool
		ref, valid = nativePipelineRef(a)
		if !valid {
			return nativeFinalConflict()
		}
		requestHash = j.RequestHash
		return nil
	})
	if err == nil {
		err = s.NativePipelineClient.RequestNativePipelineStop(ctx, ref, requestHash, func() error {
			return s.nativeStopTx(ctx, claim, func(_ *repository.Store, _ *model.Operation, _ *MLCommand, _ *mlObject, _ *mlExecution, a *model.PipelineRunAttempt, stop *nativeStopCheckpoint) error {
				if stop == nil || stop.Token != ref.StopToken || a.WorkflowUID == nil || *a.WorkflowUID != ref.UID {
					return mlExecutionConflict()
				}
				return repository.CheckTargetLock(ctx, lock)
			})
		})
	}
	lock.Close()
	if err != nil {
		return s.deferNativeClaim(ctx, claim, err)
	}
	if err = s.SyncNativePipelineWorkflow(ctx, ref.Namespace, ref.Name); err != nil {
		return s.deferNativeClaim(ctx, claim, err)
	}
	return s.finalizeNativeStop(ctx, claim)
}

func (s *Service) finalizeNativeStop(ctx context.Context, claim *MLExecutionClaim) error {
	before, err := s.Store.PipelineRunAttempt(ctx, claim.Command.GroupID, claim.Command.ResourceID, claim.InstanceID, false)
	if err != nil {
		return err
	}
	if nativeHasTerminalArchive(before) {
		if err = s.verifyNativeLogArchive(ctx, before); err != nil {
			return s.deferNativeClaim(ctx, claim, err)
		}
	}
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(claim.Command.ResourceID))
	if err != nil {
		return err
	}
	defer lock.Close()
	ready := false
	err = s.nativeStopTx(ctx, claim, func(st *repository.Store, o *model.Operation, c *MLCommand, n *mlObject, e *mlExecution, a *model.PipelineRunAttempt, stop *nativeStopCheckpoint) error {
		if !nativeHasTerminalArchive(a) {
			return nil
		}
		if stop == nil || stop.OperationID != o.ID || stop.Revision != c.Revision {
			return mlExecutionConflict()
		}
		j := &model.PipelineSubmission{}
		if err := st.DB.WithContext(ctx).Where("instance_id = ?", a.InstanceID).First(j).Error; err != nil {
			return err
		}
		frozen, err := restoreNativeStaticNodes(a, j)
		if err != nil {
			return err
		}
		archive, nodes, err := readNativeTerminalArchive(a, j, frozen)
		if err != nil {
			return err
		}
		hash, err := nativeTerminalTaskState(ctx, st, a, frozen, nodes)
		if err != nil || hash != archive.TaskStateHash {
			return nativeFinalConflict()
		}
		summary, _, err := nativeStartSummary(a)
		if err != nil {
			return err
		}
		stop.Completed = true
		stop.ArchiveHash = *a.WorkflowSnapshotHash
		summary["stop"] = stop
		raw, _ := json.Marshal(summary)
		if err = st.DB.WithContext(ctx).Model(a).Where("instance_id = ?", a.InstanceID).Update("runtime_summary", string(raw)).Error; err != nil {
			return err
		}
		if err = st.DB.WithContext(ctx).Model(n.row).Where("instance_id = ? AND revision = ?", c.InstanceID, c.Revision).Updates(map[string]any{"status": archive.Phase, "observed_state": archive.Phase, "applied_revision": c.Revision, "status_reason": nil}).Error; err != nil {
			return err
		}
		n.state.Status, n.state.ObservedState, n.state.AppliedRevision = archive.Phase, archive.Phase, c.Revision
		if err = mlRecord(ctx, st, o, n, "OBSERVATION"); err != nil {
			return err
		}
		if err = result(o, map[string]any{"resourceId": c.ResourceID, "instanceId": c.InstanceID, "status": archive.Phase, "archiveHash": stop.ArchiveHash, "executionPending": false}); err != nil {
			return err
		}
		actor := uint64(0)
		if err = st.DB.WithContext(ctx).Create(&model.Audit{OperationID: &o.ID, ActorID: &actor, ActorName: "ml-executor", GroupID: &c.GroupID, Action: o.Action, TargetType: o.TargetType, TargetID: o.TargetKey, Outcome: "SUCCEEDED"}).Error; err != nil {
			return err
		}
		r := mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Updates(map[string]any{"status": "SUCCEEDED", "step": "COMPLETED", "result": string(o.Result), "lease_owner": nil, "lease_until": nil, "finished_at": gorm.Expr("UTC_TIMESTAMP(3)"), "last_error": nil})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		o.Status = "SUCCEEDED"
		ready = true
		return nil
	})
	if err != nil {
		return err
	}
	if !ready {
		return s.deferNativeClaim(ctx, claim, nil)
	}
	return nil
}
