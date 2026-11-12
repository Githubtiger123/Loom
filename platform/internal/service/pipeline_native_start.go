package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

// Machine-only acceptance receipt. This confirms dispatch to Argo, not a run
// result; the existing worker and observer own later execution progress.
type NativePipelineStartReceipt struct {
	WorkflowName string
	WorkflowUID  string
	Accepted     bool
}

// ResumeNativePipelineSubmission grants a first start only to an existing,
// authorized native submit claim whose suspended Workflow UID is already bound.
// It never calls KFP, creates resources, re-arms a failed CAS or completes the
// operation. Every retry uses the same durable start token and original RV.
func (s *Service) ResumeNativePipelineSubmission(ctx context.Context, claim *MLExecutionClaim) (*NativePipelineStartReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if s.NativePipelineClient == nil || s.PipelineVerifier == nil || !s.PipelineConfig.NativeObserverEnabled {
		return nil, apperr.New(apperr.CodeUnavailable, "原生启动依赖尚未配置")
	}
	if claim == nil || claim.MLExecutionView == nil || !mlExecutionLeaseValid(s.PipelinePeer(), pipelineLease(claim)) {
		return nil, mlExecutionInvalid()
	}
	var input nativeStagingInput
	if err := s.nativeStagingTx(ctx, claim, nil, func(_ *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error { input = in; return nil }); err != nil {
		return nil, err
	}
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(input.Command.ResourceID))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	guard := func() error {
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return err
		}
		if _, err := s.RenewMLExecution(ctx, s.PipelinePeer(), claim.OperationID, pipelineLease(claim)); err != nil {
			return err
		}
		return s.PipelineVerifier.VerifyPipelineSpace(ctx, &MLExecutionClaim{MLExecutionView: &MLExecutionView{Space: &input.Space}, Command: input.Command})
	}
	if err := guard(); err != nil {
		return nil, err
	}
	var attempt *model.PipelineRunAttempt
	var journal *model.PipelineSubmission
	if err := s.nativeStagingTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error {
		var err error
		attempt, journal, err = nativeStagingRows(ctx, st, in)
		return err
	}); err != nil {
		return nil, err
	}
	if attempt == nil || journal == nil {
		return nil, &k8s.Permanent{Reason: "native start requires a persisted staged submission"}
	}
	frozen, err := restoreNativeStaticNodes(attempt, journal)
	if err != nil {
		return nil, err
	}
	ref, valid := nativePipelineRef(attempt)
	if !valid || ref.UID == "" || frozen.OperationID != claim.OperationID ||
		nativeStagingInputHash(frozen.Input) != nativeStagingInputHash(input) || !nativePipelineJournalMatches(journal, attempt, ref) {
		return nil, mlExecutionConflict()
	}
	_, checkpoint, err := nativeStartSummary(attempt)
	if err != nil {
		return nil, err
	}
	var frozenSnapshot nativeStagingSnapshot
	if json.Unmarshal(attempt.Snapshot, &frozenSnapshot) != nil {
		return nil, nativeFinalConflict()
	}
	hasCache := false
	limits := map[string]int32{}
	for _, task := range frozen.Plan.Tasks() {
		hasCache = hasCache || task.Cache
		if task.Group != nil && task.Group.Iterator != nil && task.Group.Iterator.Parallelism > 0 {
			limits["parallelism-"+task.TaskName] = task.Group.Iterator.Parallelism
		}
	}
	if hasCache || len(limits) > 0 {
		if err = s.NativePipelineClient.EnsureNativeCacheProjection(ctx, ref, frozenSnapshot.Policy.CacheSeeds, limits, guard); err != nil {
			return nil, err
		}
	}
	if checkpoint == nil {
		live, err := s.NativePipelineClient.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
		if err != nil {
			return nil, err
		}
		request, err := k8s.PrepareNativePipelineStart(live, ref, journal.RequestHash)
		if err != nil {
			return nil, err
		}
		prepared := nativeStartCheckpoint{SchemaVersion: 1, Protocol: nativeStartProtocol, OperationID: claim.OperationID,
			WorkflowUID: ref.UID, SnapshotHash: frozen.SnapshotHash, RequestHash: journal.RequestHash, Request: request, State: "PREPARED"}
		if err := s.nativeStagingTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error {
			a, summary, current, err := nativeStartRows(ctx, st, in, ref, frozen.SnapshotHash, journal.RequestHash)
			if err != nil {
				return err
			}
			if current != nil || summary["stagingState"] != "STAGED_SUSPENDED" {
				return mlExecutionConflict()
			}
			if err := validateNativeUnstartedDefinitions(ctx, st, a, frozen); err != nil {
				return err
			}
			return saveNativeStartCheckpoint(ctx, st, a, summary, &prepared)
		}); err != nil {
			return nil, err
		}
		checkpoint = &prepared
	}
	if !nativeStartCheckpointMatches(checkpoint, claim.OperationID, ref, frozen.SnapshotHash, journal.RequestHash) {
		return nil, mlExecutionConflict()
	}
	if err := guard(); err != nil {
		return nil, err
	}
	// Commit attempted before any Patch, using the existing authorization,
	// domain/operation/receipt locks and a DB-clock lease check at commit time.
	if err := s.nativeStagingTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error {
		a, summary, current, err := nativeStartRows(ctx, st, in, ref, frozen.SnapshotHash, journal.RequestHash)
		if err != nil {
			return err
		}
		if current == nil || *current != *checkpoint {
			return mlExecutionConflict()
		}
		if current.State != "PREPARED" {
			return nil
		}
		updated := *current
		updated.State = "ATTEMPTED"
		if err := saveNativeStartCheckpoint(ctx, st, a, summary, &updated); err != nil {
			return err
		}
		checkpoint = &updated
		return nil
	}); err != nil {
		return nil, err
	}
	live, err := s.NativePipelineClient.ReconcileNativePipelineStart(ctx, ref, journal.RequestHash,
		checkpoint.Request, checkpoint.State == "ATTEMPTED", guard)
	if err != nil {
		return nil, err
	}
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return nil, err
	}
	if err := s.PipelineVerifier.VerifyPipelineSpace(ctx, &MLExecutionClaim{MLExecutionView: &MLExecutionView{Space: &input.Space}, Command: input.Command}); err != nil {
		return nil, err
	}
	// Do not weaken lease/authorization to write an operation receipt. On loss,
	// the Kubernetes token remains evidence for a later observer/authorized claim.
	if err := s.nativeStagingTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, run *model.PipelineRun) error {
		a, summary, current, err := nativeStartRows(ctx, st, in, ref, frozen.SnapshotHash, journal.RequestHash)
		if err != nil {
			return err
		}
		if current == nil || *current != *checkpoint {
			return mlExecutionConflict()
		}
		if current.State != "CONFIRMED" {
			confirmed := *current
			confirmed.State, confirmed.ConfirmedResourceVersion = "CONFIRMED", live.GetResourceVersion()
			if !nativeStartCheckpointMatches(&confirmed, claim.OperationID, ref, frozen.SnapshotHash, journal.RequestHash) {
				return mlExecutionConflict()
			}
			if err := saveNativeStartCheckpoint(ctx, st, a, summary, &confirmed); err != nil {
				return err
			}
		}
		return s.acknowledgeNativePipelineStart(ctx, st, claim, in, run, ref.UID)
	}); err != nil {
		return nil, err
	}
	return &NativePipelineStartReceipt{WorkflowName: ref.Name, WorkflowUID: ref.UID, Accepted: true}, nil
}

// Acknowledgement binds identity and advances only the operation step. Keep the
// latest observed lifecycle, task state, report sequence and terminal gates.
func (s *Service) acknowledgeNativePipelineStart(ctx context.Context, st *repository.Store, claim *MLExecutionClaim, in nativeStagingInput, run *model.PipelineRun, uid string) error {
	e := &mlExecution{}
	if err := st.DB.WithContext(ctx).Where("operation_id = ?", claim.OperationID).First(e).Error; err != nil {
		return err // Already UPDATE-locked by nativeStagingTx/mlLockExecution.
	}
	if (e.ExternalID != nil && *e.ExternalID != uid) || (run.ExternalID != nil && *run.ExternalID != uid) {
		return mlExecutionConflict()
	}
	if err := st.DB.WithContext(ctx).Model(e).Where("operation_id = ?", claim.OperationID).Update("external_id", uid).Error; err != nil {
		return err
	}
	c := in.Command
	if err := st.DB.WithContext(ctx).Model(run).
		Where("id = ? AND group_id = ? AND instance_id = ? AND revision = ? AND last_operation_id = ?", c.ResourceID, c.GroupID, c.InstanceID, c.Revision, claim.OperationID).
		Update("external_id", uid).Error; err != nil {
		return err
	}
	o, err := st.Operation(ctx, claim.OperationID)
	if err != nil {
		return err
	}
	if err := result(o, map[string]any{"resourceId": c.ResourceID, "groupId": c.GroupID, "instanceId": c.InstanceID,
		"revision": c.Revision, "externalId": uid, "startAccepted": true, "executionPending": true,
		"status": run.Status, "observedState": run.ObservedState}); err != nil {
		return err
	}
	return mlLiveExecutionQuery(ctx, st, claim.OperationID, claim.LeaseOwner).
		Updates(map[string]any{"step": "OBSERVING", "result": string(o.Result)}).Error
}
