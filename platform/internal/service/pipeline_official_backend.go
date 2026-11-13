package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// This adapter is installed only by explicit operator configuration. Repository
// defaults remain disabled; constructing it neither contacts nor deploys K8s.
type officialPipelineBackend struct{ service *Service }

var _ OfficialPipelineBackend = (*officialPipelineBackend)(nil)

func (s *Service) ConfigureOfficialPipeline() {
	if s.PipelineConfig.NativeEnabled || s.PipelineConfig.NativeObserverEnabled {
		s.OfficialPipelineBackend = &officialPipelineBackend{service: s}
	}
}

func (b *officialPipelineBackend) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := b.service
	if s == nil || s.DB == nil || s.Store == nil || s.NativePipelineClient == nil || s.PipelineVerifier == nil || !s.PipelineConfig.RuntimeEnabled || !s.PipelineConfig.NativeObserverEnabled {
		return officialRuntimeUnavailable()
	}
	if _, ok := s.PipelineArtifacts.(OfficialPipelineArtifactVerifier); !ok {
		return officialRuntimeUnavailable()
	}
	if s.PipelineConfig.NativeCacheEnabled {
		if cache, ok := s.PipelineArtifacts.(officialCacheStore); !ok || !cache.CacheCopyConfigured() {
			return officialRuntimeUnavailable()
		}
	}
	return s.PipelineConfig.Validate()
}

// Authoritative command/space/lease are reloaded, never reconstructed from an
// operation supplied by a worker or an HTTP caller.
func (s *Service) officialClaim(ctx context.Context, in kfpengine.Operation) (*MLExecutionClaim, error) {
	var claim *MLExecutionClaim
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, target, receipt, err := mlLockExecution(ctx, st, in.ID)
		if err != nil {
			return err
		}
		run, ok := target.row.(*model.PipelineRun)
		if !ok || run.Engine != model.PipelineEngineNativeArgo || c.GroupID != in.GroupID || c.ResourceID != in.RunID || c.InstanceID != in.InstanceID || string(c.Action) != in.Action {
			return mlExecutionConflict()
		}
		lease := dto.MLExecutionLeaseReq{LeaseOwner: in.LeaseOwner, InstanceID: c.InstanceID, Revision: c.Revision, TargetFence: in.Fence}
		if err := mlLiveExecution(ctx, st, o, c, target, receipt, s.PipelinePeer(), lease); err != nil {
			return err
		}
		claim = &MLExecutionClaim{MLExecutionView: mlExecutionView(o, c, receipt), Command: *c, LeaseOwner: in.LeaseOwner}
		return nil
	})
	return claim, err
}

func (b *officialPipelineBackend) Execute(ctx context.Context, in kfpengine.Operation) error {
	if err := b.Ready(ctx); err != nil {
		return err
	}
	s := b.service
	claim, err := s.officialClaim(ctx, in)
	if err != nil {
		return err
	}
	c := claim.Command
	lock, err := repository.TargetLock(ctx, s.DB, c.Cluster, "pipeline_run", fmt.Sprint(c.ResourceID))
	if err != nil {
		return err
	}
	defer lock.Close()
	guard := func() error {
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return err
		}
		view, err := s.RenewMLExecution(ctx, s.PipelinePeer(), claim.OperationID, pipelineLease(claim))
		if err != nil {
			return err
		}
		claim.MLExecutionView = view
		return s.PipelineVerifier.VerifyPipelineSpace(ctx, claim)
	}
	if err := guard(); err != nil {
		return err
	}
	var plan *officialSubmissionPlan
	if c.Action == MLPipelineSubmit {
		plan, err = s.prepareOfficialPipelineAttempt(ctx, claim)
	} else {
		var attempt *model.PipelineRunAttempt
		attempt, err = s.Store.PipelineRunAttempt(ctx, c.GroupID, c.ResourceID, c.InstanceID, false)
		// No frozen attempt proves no official create was attempted. It does
		// not prove absence for legacy engines, which this branch excludes.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return officialRuntimeUnavailable()
		}
		if err == nil {
			plan, err = s.readOfficialSubmissionPlan(ctx, attempt)
		}
	}
	if err != nil {
		return err
	}
	ref, err := officialWorkflowRef(plan.Attempt)
	if err != nil {
		return err
	}
	if err := guard(); err != nil {
		return err
	}
	var workflow *unstructured.Unstructured
	if c.Action == MLPipelineSubmit {
		workflow, err = s.NativePipelineClient.EnsureOfficialPipelineSuspended(ctx, plan.Workflow, ref, plan.Policy.ServiceAccountUID)
	} else {
		workflow, err = s.NativePipelineClient.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
		if err == nil && ref.UID == "" {
			// Response loss may leave an unbound, still-suspended create. It
			// can be adopted under a stop/delete lease, but is never resumed.
			workflow, err = s.NativePipelineClient.EnsureOfficialPipelineSuspended(ctx, plan.Workflow, ref, plan.Policy.ServiceAccountUID)
		}
	}
	if apierrors.IsNotFound(err) && ref.UID != "" {
		if c.Action == MLPipelineSubmit {
			proof, e := officialArchivedProof(plan.Attempt)
			if e != nil {
				return e
			}
			return s.reportOfficialPipeline(ctx, claim, proof)
		}
		proof, e := s.officialAbsentProof(ctx, plan.Attempt, c.Action)
		if e != nil {
			return e
		}
		return s.reportOfficialPipeline(ctx, claim, proof)
	}
	if err != nil {
		return err
	}
	if err := guard(); err != nil {
		return err
	}
	if err := s.bindOfficialWorkflow(ctx, claim, plan, workflow); err != nil {
		return err
	}
	ref.UID = string(workflow.GetUID())
	if err := guard(); err != nil {
		return err
	}
	if c.Action == MLPipelineSubmit {
		workflow, err = s.NativePipelineClient.ControlOfficialPipeline(ctx, plan.Workflow, ref, plan.Policy.ServiceAccountUID, true)
	} else {
		workflow, err = s.NativePipelineClient.ControlOfficialPipeline(ctx, plan.Workflow, ref, plan.Policy.ServiceAccountUID, false)
	}
	if err != nil {
		return err
	}
	proof, err := s.persistOfficialObservation(ctx, plan, workflow, guard)
	if err != nil {
		return err
	}
	if (c.Action == MLPipelineDelete || c.Action == MLPipelineEdit) && proof.terminal {
		if err := guard(); err != nil {
			return err
		}
		if err := s.NativePipelineClient.DeleteOfficialPipeline(ctx, plan.Workflow, ref); err != nil {
			return err
		}
		if _, err := s.NativePipelineClient.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name); !apierrors.IsNotFound(err) {
			if err != nil {
				return err
			}
			return s.yieldPipelineExecution(ctx, claim)
		}
		proof.absent = true
		if c.Action == MLPipelineDelete {
			proof.state = "DELETED"
		} else {
			proof.state = "STOPPED"
		}
	}
	if err := guard(); err != nil {
		return err
	}
	return s.reportOfficialPipeline(ctx, claim, proof)
}

func officialWorkflowRef(attempt *model.PipelineRunAttempt) (k8s.PipelineWorkflowRef, error) {
	if attempt == nil || attempt.Namespace == nil || attempt.NamespaceUID == nil || attempt.WorkflowName == nil || attempt.PlanHash == nil {
		return k8s.PipelineWorkflowRef{}, nativeFinalConflict()
	}
	ref := k8s.PipelineWorkflowRef{GroupID: attempt.GroupID, RunID: attempt.RunID, InstanceID: attempt.InstanceID, Namespace: *attempt.Namespace, NamespaceUID: *attempt.NamespaceUID, Name: *attempt.WorkflowName, PlanHash: *attempt.PlanHash}
	if attempt.WorkflowUID != nil {
		ref.UID = *attempt.WorkflowUID
	}
	return ref, nil
}

func (s *Service) bindOfficialWorkflow(ctx context.Context, claim *MLExecutionClaim, plan *officialSubmissionPlan, workflow *unstructured.Unstructured) error {
	ref, err := officialWorkflowRef(plan.Attempt)
	if err != nil {
		return err
	}
	uid := string(workflow.GetUID())
	if ref.UID == "" {
		ref.UID = uid
	}
	if uid == "" || !k8s.PipelineWorkflowMatches(workflow, ref) {
		return nativeFinalConflict()
	}
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, target, receipt, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, target, receipt, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		run, ok := target.row.(*model.PipelineRun)
		if !ok || run.InstanceID != ref.InstanceID || run.Engine != model.PipelineEngineNativeArgo || (run.ExternalID != nil && *run.ExternalID != uid) || (receipt.ExternalID != nil && *receipt.ExternalID != uid) {
			return mlExecutionConflict()
		}
		attempt, err := st.PipelineRunAttempt(ctx, c.GroupID, c.ResourceID, c.InstanceID, true)
		if err != nil {
			return err
		}
		current, err := officialWorkflowRef(attempt)
		if err != nil || current.Name != ref.Name || current.PlanHash != ref.PlanHash || (current.UID != "" && current.UID != uid) {
			return nativeFinalConflict()
		}
		if err := st.DB.WithContext(ctx).Model(attempt).Update("workflow_uid", uid).Error; err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(run).Updates(map[string]any{"external_id": uid, "sync_enabled": true}).Error; err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(receipt).Update("external_id", uid).Error; err != nil {
			return err
		}
		plan.Attempt.WorkflowUID, claim.ExternalID = &uid, &uid
		return nil
	})
}

func (s *Service) reportOfficialPipeline(ctx context.Context, claim *MLExecutionClaim, proof *officialObservationProof) error {
	if proof == nil {
		return nativeFinalConflict()
	}
	reason := ""
	if proof.state == "FAILED" {
		reason = "RESOURCE_FAILED"
	}
	_, err := s.reportMLExecution(ctx, s.PipelinePeer(), claim.OperationID, dto.ReportMLExecutionReq{
		MLExecutionLeaseReq: pipelineLease(claim), ReportID: uuid.NewString(), Sequence: claim.Sequence + 1,
		Outcome: "OBSERVED", ExternalID: proof.uid, ObservedState: proof.state, ResourceAbsent: proof.absent, Reason: reason,
	}, proof)
	if err != nil {
		return err
	}
	if !proof.terminal && !proof.absent {
		return s.yieldPipelineExecution(ctx, claim)
	}
	return nil
}

// The observer never mints execution leases. It records verified evidence and
// wakes the durable operation; only its current lease holder reports business
// completion through the private proof gate.
func (b *officialPipelineBackend) Observe(ctx context.Context, namespace, name string) error {
	if err := b.Ready(ctx); err != nil {
		return err
	}
	s := b.service
	value, err := s.NativePipelineClient.ReadPipelineWorkflow(ctx, namespace, name)
	if apierrors.IsNotFound(err) {
		return nil
	} // Never infer success from deletion.
	if err != nil {
		return err
	}
	a, err := s.Store.PipelineRuntimeAttempt(ctx, s.Cluster, namespace, value.GetAnnotations()["loom.ml/pipeline-namespace-uid"], name, string(value.GetUID()))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(a.RunID))
	if err != nil {
		return err
	}
	defer lock.Close()
	plan, err := s.readOfficialSubmissionPlan(ctx, a)
	if err != nil {
		return err
	}
	// Re-read after acquiring the shared execution/observation target lock.
	value, err = s.NativePipelineClient.ReadPipelineWorkflow(ctx, namespace, name)
	if err != nil {
		return err
	}
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return err
	}
	_, err = s.persistOfficialObservation(ctx, plan, value, func() error { return repository.CheckTargetLock(ctx, lock) })
	return err
}

// Retain a small canonical encoder for final evidence rather than storing an
// unstructured map whose JSON representation can vary between read paths.
func officialJSON(value any) (json.RawMessage, error) { return json.Marshal(value) }
