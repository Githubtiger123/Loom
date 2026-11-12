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
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Internal observation, not a business deletion/edit completion receipt.
type NativePipelineCleanupObservation struct {
	WorkflowName   string
	WorkflowUID    string
	WorkflowAbsent bool
	PodsAbsent     bool
}

func nativeCleanupLiveEvidence(obj *unstructured.Unstructured, ref k8s.PipelineWorkflowRef, requestHash string,
	frozen *nativeStaticNodeObservation, a *model.PipelineRunAttempt, archive *nativeTerminalArchive, archivedNodes map[string]k8s.PipelineWorkflowNode) error {
	copy := obj.DeepCopy()
	// An independently deleting terminal object can be observed, not rescued
	// or forcibly finalized. Archival must exist before Delete is prepared.
	copy.SetDeletionTimestamp(nil)
	obs, nodes, err := validateNativeTerminalWorkflow(copy, ref, requestHash, frozen)
	if err != nil {
		return err
	}
	if obs.Phase != archive.Phase || !sameNativeTime(nativeSQLTime(obs.StartedAt), nativeSQLTime(a.StartedAt)) ||
		!sameNativeTime(nativeSQLTime(obs.FinishedAt), nativeSQLTime(a.FinishedAt)) {
		return nativeFinalConflict()
	}
	left, err := json.Marshal(nodes)
	if err != nil {
		return err
	}
	right, err := json.Marshal(archivedNodes)
	if err != nil {
		return err
	}
	lh, le := nativeJSONHash(left)
	rh, re := nativeJSONHash(right)
	if le != nil || re != nil || lh != rh {
		return nativeFinalConflict()
	}
	summary, _, err := nativeStartSummary(a)
	if err != nil {
		return err
	}
	return validateNativeFinalStart(a, frozen, ref, requestHash, copy, summary)
}

// DeleteArchivedNativePipelineWorkflow is the worker's machine-only cleanup
// stage. It handles archived DELETE/EDIT cleanup, never a
// running stop, metadata-only deletion or an unbound/uncertain submission.
// The operation stays RUNNING/OBSERVING even after absence is confirmed: a
// separate terminal transaction must apply pending edits or logical deletion.
func (s *Service) DeleteArchivedNativePipelineWorkflow(ctx context.Context, claim *MLExecutionClaim) (*NativePipelineCleanupObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if s.NativePipelineClient == nil || s.PipelineVerifier == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "原生清理依赖尚未配置")
	}
	if claim == nil || claim.MLExecutionView == nil || !validator.IsCanonicalUUID(claim.OperationID) ||
		!mlExecutionLeaseValid(s.PipelinePeer(), pipelineLease(claim)) {
		return nil, mlExecutionInvalid()
	}
	var input nativeStagingInput
	if err := s.nativeCleanupTx(ctx, claim, nil, func(_ *repository.Store, in nativeStagingInput, _ *model.PipelineRun, _ *model.Operation) error {
		input = in
		return nil
	}); err != nil {
		return nil, err
	}
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(input.Command.ResourceID))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	guardSpace := func() error {
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return err
		}
		if _, err := s.RenewMLExecution(ctx, s.PipelinePeer(), claim.OperationID, pipelineLease(claim)); err != nil {
			return err
		}
		return s.PipelineVerifier.VerifyPipelineSpace(ctx, &MLExecutionClaim{MLExecutionView: &MLExecutionView{Space: &input.Space}, Command: input.Command})
	}
	if err := guardSpace(); err != nil {
		return nil, err
	}
	a, err := s.Store.PipelineRunAttempt(ctx, input.Command.GroupID, input.Command.ResourceID, input.Command.InstanceID, false)
	if err != nil {
		return nil, err
	}
	j := &model.PipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", a.InstanceID).First(j).Error; err != nil {
		return nil, err
	}
	frozen, err := restoreNativeStaticNodes(a, j)
	if err != nil {
		return nil, err
	}
	ref, valid := nativePipelineRef(a)
	if !valid || ref.UID == "" {
		return nil, mlExecutionConflict()
	}
	archive, archivedNodes, err := readNativeTerminalArchive(a, j, frozen)
	if err != nil {
		return nil, err
	}
	archiveHash := *a.WorkflowSnapshotHash
	var checkpoint *nativeCleanupCheckpoint
	var pods []k8s.PipelineWorkflowPod
	if err := s.nativeCleanupTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, run *model.PipelineRun, _ *model.Operation) error {
		var err error
		_, _, checkpoint, pods, err = nativeCleanupRows(ctx, st, in, run, frozen, ref, archiveHash, j.RequestHash)
		return err
	}); err != nil {
		return nil, err
	}
	if checkpoint == nil {
		live, readErr := s.NativePipelineClient.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
		request := k8s.PipelineWorkflowDeletionRequest{Mode: "OBSERVE_ABSENCE", Phase: archive.Phase}
		if readErr != nil && !apierrors.IsNotFound(readErr) {
			return nil, readErr
		}
		if readErr == nil {
			if err := nativeCleanupLiveEvidence(live, ref, j.RequestHash, frozen, a, archive, archivedNodes); err != nil {
				return nil, err
			}
			request.Mode, request.ResourceVersion = "DELETE", live.GetResourceVersion()
		}
		if !k8s.PipelineWorkflowDeletionRequestValid(request) {
			return nil, mlExecutionConflict()
		}
		prepared := nativeCleanupCheckpoint{SchemaVersion: 1, Protocol: nativeCleanupProtocol, OperationID: claim.OperationID,
			CommandHash: nativeStagingInputHash(input), WorkflowUID: ref.UID, SnapshotHash: frozen.SnapshotHash,
			ArchiveHash: archiveHash, RequestHash: j.RequestHash, Request: request, State: "PREPARED"}
		if err := s.nativeCleanupTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, run *model.PipelineRun, _ *model.Operation) error {
			fresh, summary, current, _, err := nativeCleanupRows(ctx, st, in, run, frozen, ref, archiveHash, j.RequestHash)
			if err != nil {
				return err
			}
			if current != nil {
				return mlExecutionConflict()
			}
			return saveNativeCleanupCheckpoint(ctx, st, fresh, summary, &prepared)
		}); err != nil {
			return nil, err
		}
		checkpoint = &prepared
	}
	// Commit attempted before any Delete, retaining the same request on retry.
	if err := s.nativeCleanupTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, run *model.PipelineRun, _ *model.Operation) error {
		fresh, summary, current, currentPods, err := nativeCleanupRows(ctx, st, in, run, frozen, ref, archiveHash, j.RequestHash)
		if err != nil {
			return err
		}
		if current == nil || *current != *checkpoint {
			return mlExecutionConflict()
		}
		pods = currentPods
		if current.State == "PREPARED" {
			updated := *current
			updated.State = "ATTEMPTED"
			if err := saveNativeCleanupCheckpoint(ctx, st, fresh, summary, &updated); err != nil {
				return err
			}
			checkpoint = &updated
		}
		return nil
	}); err != nil {
		return nil, err
	}
	guard := func() error {
		if err := guardSpace(); err != nil {
			return err
		}
		// Authorization, lease and archived task evidence are all rechecked
		// before external writes and again before persisting absence facts.
		return s.nativeCleanupTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, run *model.PipelineRun, _ *model.Operation) error {
			_, _, current, _, err := nativeCleanupRows(ctx, st, in, run, frozen, ref, archiveHash, j.RequestHash)
			if err != nil {
				return err
			}
			if current == nil || *current != *checkpoint {
				return mlExecutionConflict()
			}
			return nil
		})
	}
	observation, err := s.NativePipelineClient.ReconcileNativePipelineDeletion(ctx, ref, j.RequestHash,
		checkpoint.Request, pods, checkpoint.State == "ATTEMPTED", guard)
	if err != nil {
		return nil, err
	}
	if checkpoint.State == "ABSENT" && (!observation.WorkflowAbsent || !observation.PodsAbsent) {
		return nil, &k8s.Permanent{Reason: "native cleanup absence evidence regressed"}
	}
	if err := guard(); err != nil {
		return nil, err
	}
	if err := s.nativeCleanupTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, run *model.PipelineRun, o *model.Operation) error {
		fresh, summary, current, _, err := nativeCleanupRows(ctx, st, in, run, frozen, ref, archiveHash, j.RequestHash)
		if err != nil {
			return err
		}
		if current == nil || *current != *checkpoint {
			return mlExecutionConflict()
		}
		if observation.WorkflowAbsent && observation.PodsAbsent && current.State == "ATTEMPTED" {
			confirmed := *current
			confirmed.State = "ABSENT"
			if err := saveNativeCleanupCheckpoint(ctx, st, fresh, summary, &confirmed); err != nil {
				return err
			}
		}
		if err := result(o, map[string]any{"resourceId": in.Command.ResourceID, "groupId": in.Command.GroupID,
			"instanceId": in.Command.InstanceID, "revision": in.Command.Revision, "externalId": ref.UID,
			"resourceAbsent": observation.WorkflowAbsent, "podsAbsent": observation.PodsAbsent, "executionPending": true}); err != nil {
			return err
		}
		// Only acceptance/absence observation. No Run projection, applied
		// revision, report sequence, soft delete, edit or terminal receipt here.
		return mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).
			Updates(map[string]any{"step": "OBSERVING", "result": string(o.Result)}).Error
	}); err != nil {
		return nil, err
	}
	return &NativePipelineCleanupObservation{WorkflowName: ref.Name, WorkflowUID: ref.UID,
		WorkflowAbsent: observation.WorkflowAbsent, PodsAbsent: observation.PodsAbsent}, nil
}
