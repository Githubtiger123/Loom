package service

import (
	"context"
	"encoding/json"

	wfapi "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// Storage is observed outside SQL locks. The implementation may only use the
// operator-configured endpoint and the immutable attempt root, never an endpoint
// or credential supplied by a component. An absent verifier fails closed.
type OfficialPipelineArtifactVerifier interface {
	VerifyOfficial(context.Context, string, string) error
}

type officialObservationProof struct {
	instance, uid, state string
	version              uint64
	terminal, absent     bool
	snapshotHash         string
}

type officialRuntimeSummary struct {
	State        string `json:"state"`
	MetadataHash string `json:"metadataHash"`
	Executions   int    `json:"executions"`
	Artifacts    int    `json:"artifacts"`
}

func officialWorkflowState(workflow *wfapi.Workflow) (string, bool) {
	switch workflow.Status.Phase {
	case wfapi.WorkflowSucceeded:
		return "SUCCEEDED", true
	case wfapi.WorkflowFailed, wfapi.WorkflowError:
		// Shutdown intent alone is not evidence that tasks stopped. This is
		// only evaluated after Argo has a terminal phase.
		if workflow.Spec.Shutdown == "Stop" || workflow.Spec.Shutdown == "Terminate" {
			return "STOPPED", true
		}
		return "FAILED", true
	case wfapi.WorkflowRunning:
		return "RUNNING", false
	default:
		return "PENDING", false
	}
}

func officialRuntimeRowsHash(rows []model.PipelineRuntimeObject) string {
	type entry struct {
		ID     int64           `json:"id"`
		Parent int64           `json:"parent"`
		Value  json.RawMessage `json:"value"`
	}
	values := make([]entry, 0, len(rows))
	for _, row := range rows {
		values = append(values, entry{row.ID, row.ParentID, row.Value})
	}
	raw, _ := json.Marshal(values)
	return mlHash(raw)
}

// Success requires both Argo terminal evidence and official launcher atomic
// publication. Failed/missing Pods are never converted into successful outputs.
func (s *Service) verifyOfficialCompletion(ctx context.Context, plan *officialSubmissionPlan, workflow *wfapi.Workflow, rows []model.PipelineRuntimeObject) (int, error) {
	if len(rows) == 0 || workflow.Status.OffloadNodeStatusVersion != "" || workflow.Status.CompressedNodes != "" || len(workflow.Status.Nodes) > k8s.MaxNativeWorkflowNodes {
		return 0, nativeFinalConflict()
	}
	children := map[int64]int{}
	for _, row := range rows {
		children[row.ParentID]++
	}
	rootCount, artifacts := 0, 0
	for _, row := range rows {
		var record runtimeExecution
		execution := &pb.Execution{}
		if json.Unmarshal(row.Value, &record) != nil || record.Config == nil || protojson.Unmarshal(record.Execution, execution) != nil || execution.GetId() != row.ID {
			return 0, nativeFinalConflict()
		}
		cfg := record.Config
		driver, driverOK := workflow.Status.Nodes[record.DriverNodeID]
		if !driverOK || driver.Name != record.DriverNodeName || driver.Type != wfapi.NodeTypePod || driver.Phase != wfapi.NodeSucceeded {
			return 0, nativeFinalConflict()
		}
		if cfg.NotTriggered {
			if execution.GetLastKnownState() != pb.Execution_CANCELED {
				return 0, nativeFinalConflict()
			}
			continue
		}
		switch cfg.ExecutionType {
		case metadata.ContainerExecutionTypeName:
			cached := execution.GetLastKnownState() == pb.Execution_CACHED
			if !record.Published || record.PublicationHash == "" || (!cached && execution.GetLastKnownState() != pb.Execution_COMPLETE) || (!cached && record.AssignedPodUID == "") {
				return 0, apperr.New(apperr.CodeUnavailable, "官方任务的输出发布尚未完整，不能确认成功")
			}
			node, ok := workflow.Status.Nodes[record.AssignedNodeID]
			if !cached && (!ok || node.ID != record.AssignedNodeID || node.Name != record.AssignedNodeName || node.Type != wfapi.NodeTypePod || node.Phase != wfapi.NodeSucceeded) {
				return 0, nativeFinalConflict()
			}
			for _, output := range record.Outputs {
				artifact := &pb.Artifact{}
				if protojson.Unmarshal(output.Artifact, artifact) != nil || artifact.GetId() <= 0 || artifact.GetState() != pb.Artifact_LIVE {
					return 0, nativeFinalConflict()
				}
				// These official types carry values in metadata, not files. Do
				// not invent an S3 object that the launcher never uploaded.
				if artifact.GetType() == "system.Metrics" || artifact.GetType() == "system.ClassificationMetrics" {
					artifacts++
					continue
				}
				verifier, ok := s.PipelineArtifacts.(OfficialPipelineArtifactVerifier)
				if !ok {
					return 0, officialRuntimeUnavailable()
				}
				if err := verifier.VerifyOfficial(ctx, plan.Policy.PipelineRoot, artifact.GetUri()); err != nil {
					return 0, err
				}
				artifacts++
			}
		case metadata.DagExecutionTypeName:
			if cfg.ParentDagID == 0 {
				rootCount++
			}
			expected := cfg.TotalDagTasks
			if cfg.IterationCount != nil {
				expected = cfg.IterationCount
			}
			if expected != nil && children[row.ID] != *expected {
				return 0, nativeFinalConflict()
			}
			if execution.GetLastKnownState() == pb.Execution_FAILED || execution.GetLastKnownState() == pb.Execution_CANCELED {
				return 0, nativeFinalConflict()
			}
		default:
			return 0, nativeFinalConflict()
		}
	}
	if rootCount != 1 {
		return 0, nativeFinalConflict()
	}
	return artifacts, nil
}

// Caller holds the same advisory target lock used by the command worker. All
// remote reads precede the transaction, which rechecks the identity and metadata
// hash while holding group -> run -> attempt locks. Runtime writers use the same
// SQL order, so no partial publication can slip into the archived success.
func (s *Service) persistOfficialObservation(ctx context.Context, plan *officialSubmissionPlan, value *unstructured.Unstructured, guard func() error) (*officialObservationProof, error) {
	ref, err := officialWorkflowRef(plan.Attempt)
	if err != nil || !k8s.PipelineWorkflowMatches(value, ref) {
		return nil, nativeFinalConflict()
	}
	workflow := &wfapi.Workflow{}
	if runtime.DefaultUnstructuredConverter.FromUnstructured(value.Object, workflow) != nil || kfpengine.VerifyFrozenWorkflow(plan.Workflow, workflow) != nil {
		return nil, nativeFinalConflict()
	}
	state, terminal := officialWorkflowState(workflow)
	if terminal && workflow.Status.FinishedAt.IsZero() {
		return nil, nativeFinalConflict()
	}
	scope := runtimeapi.Scope{GroupID: ref.GroupID, RunID: ref.RunID, InstanceID: ref.InstanceID}
	rows, err := s.Store.RuntimeObjects(ctx, scope, "execution")
	if err != nil {
		return nil, err
	}
	metadataHash, artifactCount := officialRuntimeRowsHash(rows), 0
	var cacheEntries []model.PipelineRuntimeCache
	if state == "SUCCEEDED" {
		artifactCount, err = s.verifyOfficialCompletion(ctx, plan, workflow, rows)
		if err != nil {
			return nil, err
		}
		cacheEntries, err = s.officialCacheEntries(ctx, plan, rows)
		if err != nil {
			return nil, err
		}
	}
	// Archive only identity and status, not managedFields or the full manifest.
	// Status can contain user outputs and is internal evidence, never a public DTO.
	evidence := struct {
		Name      string               `json:"name"`
		Namespace string               `json:"namespace"`
		UID       string               `json:"uid"`
		Engine    string               `json:"engine"`
		PlanHash  string               `json:"planHash"`
		Status    wfapi.WorkflowStatus `json:"status"`
	}{ref.Name, ref.Namespace, ref.UID, kfpengine.EngineRelease, ref.PlanHash, workflow.Status}
	snapshot, err := officialJSON(evidence)
	if err != nil || len(snapshot) > 2<<20 {
		return nil, nativeFinalConflict()
	}
	proof := &officialObservationProof{instance: ref.InstanceID, uid: ref.UID, state: state, terminal: terminal}
	if guard == nil {
		return nil, nativeFinalConflict()
	}
	if err := guard(); err != nil {
		return nil, err
	}
	err = repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		group, err := st.Group(ctx, ref.GroupID, true)
		if err != nil {
			return err
		}
		run, err := st.PipelineRun(ctx, ref.GroupID, ref.RunID, true)
		if err != nil {
			return err
		}
		attempt, err := st.PipelineRunAttempt(ctx, ref.GroupID, ref.RunID, ref.InstanceID, true)
		if err != nil {
			return err
		}
		current, err := officialWorkflowRef(attempt)
		if err != nil || current != ref || group.DeletedAt != nil || group.InstanceID != plan.Policy.GroupInstanceID || group.NamespaceUID == nil || *group.NamespaceUID != ref.NamespaceUID || group.Name != ref.Namespace || group.Cluster != s.Cluster || run.Cluster != s.Cluster || run.Namespace != ref.Namespace || run.InstanceID != ref.InstanceID || run.Engine != model.PipelineEngineNativeArgo || run.ExecutionCleaned || run.ExternalID == nil || *run.ExternalID != ref.UID {
			return nativeFinalConflict()
		}
		lockedRows, err := st.RuntimeObjects(ctx, scope, "execution")
		if err != nil {
			return err
		}
		if officialRuntimeRowsHash(lockedRows) != metadataHash {
			return apperr.New(apperr.CodeUnavailable, "官方任务元数据正在发布，稍后重新观测")
		}
		if attempt.WorkflowSnapshotHash != nil {
			// The terminal record is immutable. Argo housekeeping may change
			// its live status, but cannot replace the already archived result.
			if attempt.Phase == nil || *attempt.Phase != state || !terminal {
				return nativeFinalConflict()
			}
			proof.version, proof.snapshotHash = attempt.ObservationVersion, *attempt.WorkflowSnapshotHash
			return syncOfficialBusinessObservation(ctx, st, run, state)
		}
		summary, err := json.Marshal(officialRuntimeSummary{State: state, MetadataHash: metadataHash, Executions: len(rows), Artifacts: artifactCount})
		if err != nil {
			return err
		}
		nextVersion := attempt.ObservationVersion + 1
		fields := map[string]any{"phase": state, "runtime_summary": summary, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)"), "sync_attempted_at": gorm.Expr("UTC_TIMESTAMP(3)"), "sync_reason": nil, "observation_version": nextVersion}
		if !workflow.Status.StartedAt.IsZero() {
			fields["started_at"] = workflow.Status.StartedAt.Time.UTC()
		}
		if terminal {
			fields["finished_at"] = workflow.Status.FinishedAt.Time.UTC()
			fields["workflow_snapshot"], fields["workflow_snapshot_hash"], fields["workflow_archived_at"] = snapshot, mlHash(snapshot), gorm.Expr("UTC_TIMESTAMP(3)")
			proof.snapshotHash = mlHash(snapshot)
		}
		if err := st.DB.WithContext(ctx).Model(attempt).Updates(fields).Error; err != nil {
			return err
		}
		if err := saveOfficialCacheEntries(ctx, st, cacheEntries); err != nil {
			return err
		}
		// Observing reality is independent of the originating user's current
		// write grant or operation lease. Never finish or reopen that operation.
		if err := syncOfficialBusinessObservation(ctx, st, run, state); err != nil {
			return err
		}
		proof.version = nextVersion
		// Wake only waiting work; do not steal/extend a live lease or reopen
		// a succeeded/failed/superseded operation.
		return st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ? AND status IN ?", run.LastOperationID, []string{"PENDING", "RETRYING"}).Update("next_attempt_at", gorm.Expr("UTC_TIMESTAMP(3)")).Error
	})
	return proof, err
}

func officialArchivedProof(attempt *model.PipelineRunAttempt) (*officialObservationProof, error) {
	if attempt == nil || attempt.WorkflowUID == nil || attempt.WorkflowSnapshotHash == nil || attempt.WorkflowArchivedAt == nil || attempt.Phase == nil || !pipelineTerminalState(*attempt.Phase) || len(attempt.WorkflowSnapshot) == 0 || mlHash(attempt.WorkflowSnapshot) != *attempt.WorkflowSnapshotHash {
		return nil, nativeFinalConflict()
	}
	return &officialObservationProof{instance: attempt.InstanceID, uid: *attempt.WorkflowUID, state: *attempt.Phase, terminal: true, version: attempt.ObservationVersion, snapshotHash: *attempt.WorkflowSnapshotHash}, nil
}

func syncOfficialBusinessObservation(ctx context.Context, st *repository.Store, run *model.PipelineRun, state string) error {
	if pipelineTerminalState(run.ObservedState) && run.ObservedState != state {
		return nil
	}
	if run.ObservedState == "RUNNING" && state == "PENDING" {
		return nil
	}
	fields := map[string]any{"observed_state": state, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}
	update := run.DesiredState == "RUNNING"
	if run.DesiredState == "STOPPED" {
		active, err := st.HasMLOperation(ctx, "pipeline_run", run.ID)
		if err != nil {
			return err
		}
		update = !active
	}
	if update {
		fields["status"], fields["status_reason"] = state, nil
	}
	if run.DesiredState == "RUNNING" {
		fields["applied_revision"] = run.Revision
	}
	changed := run.ObservedState != state || (update && run.Status != state)
	if err := st.DB.WithContext(ctx).Model(run).Updates(fields).Error; err != nil {
		return err
	}
	if changed && run.LastOperationID != nil {
		return mlRecord(ctx, st, &model.Operation{ID: *run.LastOperationID, Action: "pipeline.run.sync", TargetType: "pipeline_run"}, &mlObject{identity: &run.MLIdentity, state: &run.MLExecutionState, row: run}, "RUNTIME_OBSERVATION")
	}
	return nil
}

func (s *Service) officialAbsentProof(ctx context.Context, attempt *model.PipelineRunAttempt, action MLAction) (*officialObservationProof, error) {
	if attempt.WorkflowUID == nil || attempt.WorkflowSnapshotHash == nil || attempt.WorkflowArchivedAt == nil || len(attempt.WorkflowSnapshot) == 0 || mlHash(attempt.WorkflowSnapshot) != *attempt.WorkflowSnapshotHash {
		return nil, apperr.New(apperr.CodeConflict, "Workflow 已消失但缺少终态存档，不能确认清理完成")
	}
	state := "STOPPED"
	if action == MLPipelineDelete {
		state = "DELETED"
	} else if action != MLPipelineStop && action != MLPipelineEdit {
		return nil, mlExecutionInvalid()
	}
	if action == MLPipelineStop && attempt.Phase != nil && (*attempt.Phase == "SUCCEEDED" || *attempt.Phase == "FAILED") {
		// Natural completion won the race. Reuse its real archived terminal
		// state instead of changing a successful/failed job into STOPPED.
		return &officialObservationProof{instance: attempt.InstanceID, uid: *attempt.WorkflowUID, state: *attempt.Phase, terminal: true, version: attempt.ObservationVersion, snapshotHash: *attempt.WorkflowSnapshotHash}, nil
	}
	return &officialObservationProof{instance: attempt.InstanceID, uid: *attempt.WorkflowUID, state: state, terminal: true, absent: true, version: attempt.ObservationVersion, snapshotHash: *attempt.WorkflowSnapshotHash}, nil
}

func (s *Service) verifyOfficialObservationProof(ctx context.Context, st *repository.Store, run *model.PipelineRun, in dto.ReportMLExecutionReq, proof *officialObservationProof) error {
	if proof == nil || proof.instance != run.InstanceID || proof.uid != in.ExternalID || proof.state != in.ObservedState || proof.absent != in.ResourceAbsent || in.Outcome != "OBSERVED" || run.ExternalID == nil || *run.ExternalID != proof.uid {
		return mlExecutionConflict()
	}
	attempt, err := st.PipelineRunAttempt(ctx, run.GroupID, run.ID, run.InstanceID, true)
	if err != nil {
		return err
	}
	if attempt.EngineRevision == nil || *attempt.EngineRevision != kfpengine.EngineRelease || attempt.WorkflowUID == nil || *attempt.WorkflowUID != proof.uid || attempt.ObservationVersion != proof.version || attempt.Phase == nil {
		return nativeFinalConflict()
	}
	if proof.terminal {
		if attempt.WorkflowSnapshotHash == nil || *attempt.WorkflowSnapshotHash != proof.snapshotHash || attempt.WorkflowArchivedAt == nil || len(attempt.WorkflowSnapshot) == 0 || mlHash(attempt.WorkflowSnapshot) != proof.snapshotHash {
			return nativeFinalConflict()
		}
	} else if *attempt.Phase != proof.state {
		return nativeFinalConflict()
	}
	if proof.terminal && !proof.absent && *attempt.Phase != proof.state {
		return nativeFinalConflict()
	}
	return nil
}
