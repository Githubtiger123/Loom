package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
)

const nativeCleanupProtocol = "loom-native-archived-cleanup-v1"

// One cleanup request per archived instance. It is not the submit operation's
// final receipt, permission to terminate, or proof that an edit was applied.
type nativeCleanupCheckpoint struct {
	SchemaVersion int                                 `json:"schemaVersion"`
	Protocol      string                              `json:"protocol"`
	OperationID   string                              `json:"operationId"`
	CommandHash   string                              `json:"commandHash"`
	WorkflowUID   string                              `json:"workflowUid"`
	SnapshotHash  string                              `json:"snapshotHash"`
	ArchiveHash   string                              `json:"archiveHash"`
	RequestHash   string                              `json:"requestHash"`
	Request       k8s.PipelineWorkflowDeletionRequest `json:"request"`
	State         string                              `json:"state"`
}

func nativeCleanupSummary(a *model.PipelineRunAttempt) (map[string]any, *nativeCleanupCheckpoint, error) {
	summary, _, err := nativeStartSummary(a)
	if err != nil {
		return nil, nil, err
	}
	value, exists := summary["cleanup"]
	if !exists {
		return summary, nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	var checkpoint nativeCleanupCheckpoint
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if value == nil || d.Decode(&checkpoint) != nil {
		return nil, nil, &k8s.Permanent{Reason: "native cleanup checkpoint is invalid or unsupported"}
	}
	return summary, &checkpoint, nil
}

func nativeCleanupCheckpointMatches(c *nativeCleanupCheckpoint, operationID string, in nativeStagingInput,
	ref k8s.PipelineWorkflowRef, snapshotHash, archiveHash, requestHash string) bool {
	return c != nil && c.SchemaVersion == 1 && c.Protocol == nativeCleanupProtocol && c.OperationID == operationID &&
		c.CommandHash == nativeStagingInputHash(in) && c.WorkflowUID == ref.UID && ref.UID != "" &&
		c.SnapshotHash == snapshotHash && validator.IsSHA256(snapshotHash) && c.ArchiveHash == archiveHash && validator.IsSHA256(archiveHash) &&
		c.RequestHash == requestHash && validator.IsSHA256(requestHash) && k8s.PipelineWorkflowDeletionRequestValid(c.Request) &&
		(c.State == "PREPARED" || c.State == "ATTEMPTED" || c.State == "ABSENT")
}

// Cleanup commands carry the old execution package, plus a pending package for
// edit. No new desired package can be substituted for the archived execution.
func (s *Service) nativeCleanupInput(c *MLCommand, n *mlObject, e *mlExecution) (nativeStagingInput, error) {
	run, ok := n.row.(*model.PipelineRun)
	if !ok || (c.Action != MLPipelineDelete && c.Action != MLPipelineEdit) || c.Cluster != s.Cluster || c.PipelineRun == nil ||
		run.Engine != model.PipelineEngineNativeArgo || run.ExecutionCleaned || run.VersionRevision == nil ||
		run.VersionID != c.PipelineRun.VersionID || run.PipelineID != c.PipelineRun.PipelineID ||
		*run.VersionRevision != c.PipelineRun.VersionRevision || c.PipelineRun.VersionRevision == 0 ||
		e.GroupInstanceID == nil || e.NamespaceUID == nil || e.ProfileUID == nil {
		return nativeStagingInput{}, mlExecutionConflict()
	}
	raw, err := json.Marshal(c.PipelineRun)
	if err != nil {
		return nativeStagingInput{}, err
	}
	expected, err := nativeJSONHash(raw)
	actual, actualErr := nativeJSONHash(run.RequestedSpec)
	if err != nil || actualErr != nil || expected != actual {
		return nativeStagingInput{}, mlExecutionConflict()
	}
	if c.Action == MLPipelineEdit {
		if c.PendingPipelineRun == nil {
			return nativeStagingInput{}, mlExecutionConflict()
		}
		raw, err := json.Marshal(c.PendingPipelineRun)
		if err != nil {
			return nativeStagingInput{}, err
		}
		expected, err := nativeJSONHash(raw)
		actual, actualErr := nativeJSONHash(run.PendingSpec)
		if err != nil || actualErr != nil || expected != actual {
			return nativeStagingInput{}, mlExecutionConflict()
		}
	} else if len(run.PendingSpec) != 0 {
		return nativeStagingInput{}, mlExecutionConflict()
	}
	return nativeStagingInput{Command: *c, Space: MLExecutionSpace{InstanceID: *e.GroupInstanceID,
		NamespaceUID: *e.NamespaceUID, ProfileUID: *e.ProfileUID}}, nil
}

// Same execution locks and current DB-clock lease as submission, but only for
// cleanup actions. No Kubernetes calls or terminal operation writes in SQL.
func (s *Service) nativeCleanupTx(ctx context.Context, claim *MLExecutionClaim, expected *nativeStagingInput,
	fn func(*repository.Store, nativeStagingInput, *model.PipelineRun, *model.Operation) error) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		in, err := s.nativeCleanupInput(c, n, e)
		if err != nil {
			return err
		}
		if expected != nil && nativeStagingInputHash(*expected) != nativeStagingInputHash(in) {
			return mlExecutionConflict()
		}
		if err := fn(st, in, n.row.(*model.PipelineRun), o); err != nil {
			return err
		}
		var count int64
		if err := mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}

func nativeCleanupIntentMatches(in nativeStagingInput, frozen *nativeStaticNodeObservation, ref k8s.PipelineWorkflowRef, run *model.PipelineRun) bool {
	c, original := in.Command, frozen.Input.Command
	if c.ResourceID != original.ResourceID || c.GroupID != original.GroupID || c.InstanceID != original.InstanceID ||
		c.Cluster != original.Cluster || c.Namespace != original.Namespace || c.Name != original.Name || c.Revision <= original.Revision ||
		in.Space != frozen.Input.Space || run.LastOperationID == nil || run.ExternalID == nil || *run.ExternalID != ref.UID ||
		(c.ExternalID != nil && *c.ExternalID != ref.UID) {
		return false
	}
	left, err := json.Marshal(c.PipelineRun)
	if err != nil {
		return false
	}
	right, err := json.Marshal(original.PipelineRun)
	if err != nil {
		return false
	}
	lh, le := nativeJSONHash(left)
	rh, re := nativeJSONHash(right)
	return le == nil && re == nil && lh == rh
}

// Revalidate locked final evidence on every checkpoint write and before RPC.
func nativeCleanupRows(ctx context.Context, st *repository.Store, in nativeStagingInput, run *model.PipelineRun,
	frozen *nativeStaticNodeObservation, ref k8s.PipelineWorkflowRef, archiveHash, requestHash string) (*model.PipelineRunAttempt, map[string]any, *nativeCleanupCheckpoint, []k8s.PipelineWorkflowPod, error) {
	if !nativeCleanupIntentMatches(in, frozen, ref, run) {
		return nil, nil, nil, nil, mlExecutionConflict()
	}
	a, j, err := nativeStagingRows(ctx, st, frozen.Input)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := nativeStagingIntentUnchanged(a, j, frozen.SnapshotHash, requestHash, ref, frozen.Input); err != nil {
		return nil, nil, nil, nil, err
	}
	if a.WorkflowSnapshotHash == nil || *a.WorkflowSnapshotHash != archiveHash {
		return nil, nil, nil, nil, nativeFinalConflict()
	}
	archive, nodes, err := readNativeTerminalArchive(a, j, frozen)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	hash, err := nativeTerminalTaskState(ctx, st, a, frozen, nodes)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if hash != archive.TaskStateHash {
		return nil, nil, nil, nil, nativeFinalConflict()
	}
	summary, checkpoint, err := nativeCleanupSummary(a)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if (summary["finalizationState"] != "SNAPSHOT_STORED" && summary["finalizationState"] != "COMPLETED") ||
		summary["finalizationProtocol"] != nativeFinalProtocol || summary["finalArchiveHash"] != archiveHash {
		return nil, nil, nil, nil, nativeFinalConflict()
	}
	if _, _, err := readNativeFinalReceipt(summary, frozen.OperationID, archiveHash, archive.Phase); err != nil {
		return nil, nil, nil, nil, err
	}
	if checkpoint != nil && (!nativeCleanupCheckpointMatches(checkpoint, *run.LastOperationID, in, ref, frozen.SnapshotHash, archiveHash, requestHash) || checkpoint.Request.Phase != archive.Phase) {
		return nil, nil, nil, nil, mlExecutionConflict()
	}
	rows := []model.PipelineTaskExecution{}
	if err := st.DB.WithContext(ctx).Where("group_id = ? AND run_id = ? AND instance_id = ? AND pod_uid IS NOT NULL",
		a.GroupID, a.RunID, a.InstanceID).Order("id").Limit(k8s.MaxNativeWorkflowNodes + 1).Find(&rows).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	if len(rows) > k8s.MaxNativeWorkflowNodes {
		return nil, nil, nil, nil, nativeFinalConflict()
	}
	pods := make([]k8s.PipelineWorkflowPod, 0, len(rows))
	for _, row := range rows {
		pods = append(pods, k8s.PipelineWorkflowPod{Name: *row.PodName, UID: *row.PodUID, NodeID: *row.ArgoNodeID})
	}
	return a, summary, checkpoint, pods, nil
}

func saveNativeCleanupCheckpoint(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, summary map[string]any, checkpoint *nativeCleanupCheckpoint) error {
	if a.ObservationVersion == ^uint64(0) {
		return mlExecutionConflict()
	}
	summary["cleanup"] = checkpoint
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	if !validator.IsBoundedJSONObject(raw, 64<<10, 16, 4096) {
		return &k8s.Permanent{Reason: "native cleanup summary exceeds persistence limits"}
	}
	r := st.DB.WithContext(ctx).Model(a).Where("instance_id = ? AND workflow_uid = ? AND observation_version = ? AND workflow_snapshot_hash = ?",
		a.InstanceID, checkpoint.WorkflowUID, a.ObservationVersion, checkpoint.ArchiveHash).
		Updates(map[string]any{"runtime_summary": string(raw), "observation_version": gorm.Expr("observation_version + 1")})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return mlExecutionConflict()
	}
	return nil
}
