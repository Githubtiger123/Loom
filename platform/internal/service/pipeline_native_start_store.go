package service

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const nativeStartProtocol = "loom-native-start-v1"

// One immutable CAS request per instance. State is monotone; confirmation is
// evidence of accepted unsuspension, never evidence of task/run completion.
type nativeStartCheckpoint struct {
	SchemaVersion            int                              `json:"schemaVersion"`
	Protocol                 string                           `json:"protocol"`
	OperationID              string                           `json:"operationId"`
	WorkflowUID              string                           `json:"workflowUid"`
	SnapshotHash             string                           `json:"snapshotHash"`
	RequestHash              string                           `json:"requestHash"`
	Request                  k8s.PipelineWorkflowStartRequest `json:"request"`
	State                    string                           `json:"state"`
	ConfirmedResourceVersion string                           `json:"confirmedResourceVersion,omitempty"`
}

func nativeStartSummary(a *model.PipelineRunAttempt) (map[string]any, *nativeStartCheckpoint, error) {
	bad := func() (map[string]any, *nativeStartCheckpoint, error) {
		return nil, nil, &k8s.Permanent{Reason: "native start checkpoint is invalid or unsupported"}
	}
	if !validator.IsBoundedJSONObject(a.RuntimeSummary, 64<<10, 16, 4096) {
		return bad()
	}
	summary := map[string]any{}
	summaryDecoder := json.NewDecoder(bytes.NewReader(a.RuntimeSummary))
	summaryDecoder.UseNumber() // Preserve uint64 terminal receipt sequences.
	if summaryDecoder.Decode(&summary) != nil || summary == nil {
		return bad()
	}
	value, exists := summary["start"]
	if !exists {
		return summary, nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil || value == nil {
		return bad()
	}
	var checkpoint nativeStartCheckpoint
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&checkpoint) != nil {
		return bad()
	}
	return summary, &checkpoint, nil
}

func nativeStartCheckpointMatches(c *nativeStartCheckpoint, operationID string, ref k8s.PipelineWorkflowRef, snapshotHash, requestHash string) bool {
	if c == nil || c.SchemaVersion != 1 || c.Protocol != nativeStartProtocol || c.OperationID != operationID ||
		ref.UID == "" || c.WorkflowUID != ref.UID || c.SnapshotHash != snapshotHash || c.RequestHash != requestHash ||
		!validator.IsSHA256(snapshotHash) || !validator.IsSHA256(requestHash) ||
		!validator.IsKubernetesResourceVersion(c.Request.ResourceVersion) || !validator.IsCanonicalUUID(c.Request.Token) ||
		(c.Request.Protocol != "" && !pipelineir.IsPythonCompiler(c.Request.Protocol)) {
		return false
	}
	switch c.State {
	case "PREPARED", "ATTEMPTED":
		return c.ConfirmedResourceVersion == ""
	case "CONFIRMED":
		return validator.IsKubernetesResourceVersion(c.ConfirmedResourceVersion) && c.ConfirmedResourceVersion != c.Request.ResourceVersion
	}
	return false
}

func saveNativeStartCheckpoint(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, summary map[string]any, checkpoint *nativeStartCheckpoint) error {
	if a.ObservationVersion == ^uint64(0) {
		return mlExecutionConflict()
	}
	summary["start"] = checkpoint
	if checkpoint.State == "CONFIRMED" {
		summary["stagingState"] = "START_ACCEPTED"
	}
	raw, err := json.Marshal(summary)
	if err != nil || !validator.IsBoundedJSONObject(raw, 64<<10, 16, 4096) {
		return &k8s.Permanent{Reason: "native start summary exceeds persistence limits"}
	}
	r := st.DB.WithContext(ctx).Model(a).Where("instance_id = ? AND workflow_uid = ? AND observation_version = ?",
		a.InstanceID, checkpoint.WorkflowUID, a.ObservationVersion).
		Updates(map[string]any{"runtime_summary": string(raw), "observation_version": gorm.Expr("observation_version + 1")})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return mlExecutionConflict()
	}
	return nil
}

func nativeStartRows(ctx context.Context, st *repository.Store, in nativeStagingInput, ref k8s.PipelineWorkflowRef, snapshotHash, requestHash string) (*model.PipelineRunAttempt, map[string]any, *nativeStartCheckpoint, error) {
	a, j, err := nativeStagingRows(ctx, st, in)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := nativeStagingIntentUnchanged(a, j, snapshotHash, requestHash, ref, in); err != nil {
		return nil, nil, nil, err
	}
	if ref.UID == "" || !j.Attempted {
		return nil, nil, nil, mlExecutionConflict()
	}
	summary, checkpoint, err := nativeStartSummary(a)
	return a, summary, checkpoint, err
}

// Validate all static definitions without projecting a hypothetical live node.
// The SQL rows are locked by the shared validator before checking unstartedness.
func validateNativeUnstartedDefinitions(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, frozen *nativeStaticNodeObservation) error {
	empty := &nativeStaticNodeObservation{Plan: frozen.Plan, Nodes: map[string]k8s.PipelineWorkflowNode{}}
	if _, err := syncNativePipelineTasks(ctx, st, a, empty); err != nil {
		return err
	}
	var started int64
	query := st.DB.WithContext(ctx).Model(&model.PipelineTaskExecution{}).
		Where("group_id = ? AND run_id = ? AND instance_id = ?", a.GroupID, a.RunID, a.InstanceID)
	python := pipelineir.IsPythonCompiler(frozen.Plan.Compiler())
	if python {
		query = query.Where("node_key <> ?", frozen.Plan.Root().NodeKey)
	}
	err := query.
		Where("argo_node_id IS NOT NULL OR pod_uid IS NOT NULL OR phase <> 'PENDING' OR started_at IS NOT NULL OR finished_at IS NOT NULL").Count(&started).Error
	if err != nil {
		return err
	}
	if started != 0 || (a.Phase != nil && *a.Phase != "PENDING" && *a.Phase != "UNKNOWN" && !(python && *a.Phase == "RUNNING")) {
		return &k8s.Permanent{Reason: "native execution already contains started definitions"}
	}
	return nil
}

// Evidence-only recovery inside the observer transaction. A matching atomic
// token can confirm an attempted start after its caller lost lease/response.
// Never mint a checkpoint, patch Kubernetes, or alter an operation/receipt here.
func observeNativeStartCheckpoint(a *model.PipelineRunAttempt, frozen *nativeStaticNodeObservation, ref k8s.PipelineWorkflowRef,
	requestHash string, obj *unstructured.Unstructured, summary map[string]any) (string, error) {
	_, checkpoint, err := nativeStartSummary(a)
	if err != nil {
		return "", err
	}
	token, present := obj.GetAnnotations()[k8s.AnnotationPipelineStartToken]
	suspended, _, err := unstructured.NestedBool(obj.Object, "spec", "suspend")
	if err != nil {
		return "", err
	}
	if checkpoint == nil {
		if present || !suspended {
			return "START_CONFIRMATION_MISSING", nil
		}
		return "", nil
	}
	if !nativeStartCheckpointMatches(checkpoint, frozen.OperationID, ref, frozen.SnapshotHash, requestHash) {
		return "", &k8s.Permanent{Reason: "native observed start checkpoint identity conflict"}
	}
	if !present {
		if checkpoint.State == "CONFIRMED" {
			return "START_TOKEN_MISSING", nil
		}
		if obj.GetResourceVersion() != checkpoint.Request.ResourceVersion {
			return "START_RESULT_UNCERTAIN", nil
		}
		return "START_CONFIRMATION_PENDING", nil
	}
	if token != checkpoint.Request.Token || checkpoint.State == "PREPARED" {
		return "START_TOKEN_CONFLICT", nil
	}
	if suspended {
		return "START_RESUSPENDED", nil // Preserve earlier acceptance, never re-arm.
	}
	if obj.GetDeletionTimestamp() != nil {
		return "START_RESOURCE_DELETING", nil
	}
	if checkpoint.State == "ATTEMPTED" {
		checkpoint.State, checkpoint.ConfirmedResourceVersion = "CONFIRMED", obj.GetResourceVersion()
		if !nativeStartCheckpointMatches(checkpoint, frozen.OperationID, ref, frozen.SnapshotHash, requestHash) {
			return "", &k8s.Permanent{Reason: "native observed start confirmation is invalid"}
		}
		summary["start"] = checkpoint // Saved by the existing attempt observation CAS.
		summary["stagingState"] = "START_ACCEPTED"
	}
	return "", nil
}

// RuntimeSummary is otherwise returned in attempt history. Keep diagnostic
// counts, but never expose the internal token/CAS request or operation binding.
func redactNativeStartSummary(a *model.PipelineRunAttempt) json.RawMessage {
	summary, checkpoint, err := nativeStartSummary(a)
	if err != nil {
		return json.RawMessage(`{"schemaVersion":1,"redacted":true}`)
	}
	delete(summary, "start")
	delete(summary, "finalizationReceipt")
	delete(summary, "cleanup")
	delete(summary, "stop")
	if checkpoint != nil {
		switch checkpoint.State {
		case "PREPARED", "ATTEMPTED", "CONFIRMED":
			summary["startState"] = checkpoint.State
		}
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return json.RawMessage(`{"schemaVersion":1,"redacted":true}`)
	}
	return raw
}
