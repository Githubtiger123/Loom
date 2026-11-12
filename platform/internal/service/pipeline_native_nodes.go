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
	"gorm.io/gorm/clause"
)

type nativeStaticNodeObservation struct {
	Plan         *pipelineir.Plan
	OperationID  string
	Input        nativeStagingInput
	SnapshotHash string
	Nodes        map[string]k8s.PipelineWorkflowNode
	Pods         map[string]k8s.PipelineWorkflowPod
}

// Reuse frozen-intent verification, not the mutable runtime source map, as the
// authority for binding. No cluster I/O or transaction is performed here.
func restoreNativeStaticNodes(a *model.PipelineRunAttempt, j *model.PipelineSubmission) (*nativeStaticNodeObservation, error) {
	bad := func() (*nativeStaticNodeObservation, error) {
		return nil, &k8s.Permanent{Reason: "native frozen node plan is invalid or unsupported"}
	}
	if !validator.IsBoundedJSONObject(a.Snapshot, maxNativeStagingSnapshot, 64, 200000) {
		return bad()
	}
	var snapshot nativeStagingSnapshot
	d := json.NewDecoder(bytes.NewReader(a.Snapshot))
	d.DisallowUnknownFields()
	if d.Decode(&snapshot) != nil {
		return bad()
	}
	_, _, snapshotHash, err := restoreNativeStaging(snapshot.Input, snapshot.OperationID, a, j)
	if err != nil {
		return bad()
	}
	// Analyze is bounded and pinned to the compiler already verified by restore.
	// It cannot fetch a package or reinterpret it with an unsupported version.
	plan, report := pipelineir.AnalyzeVersion(snapshot.Input.Command.PipelineRun.PipelineSpec, snapshot.CompilerVersion)
	if plan == nil || !report.Compilable || a.PlanHash == nil || plan.Digest() != *a.PlanHash {
		return bad()
	}
	return &nativeStaticNodeObservation{Plan: plan, OperationID: snapshot.OperationID, Input: snapshot.Input, SnapshotHash: snapshotHash}, nil
}

type nativeTaskSyncStats struct {
	Matched, Missing, PendingOutputs, PendingPods, NonSucceeded int
}

// Lock all pre-registered definitions, including unbound ones. Bind and project
// facts atomically with the attempt CAS in the caller's existing transaction.
// No new row is synthesized from a status event and no Pod call occurs in SQL.
func syncNativePipelineTasks(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, observation *nativeStaticNodeObservation) (nativeTaskSyncStats, error) {
	if observation.Plan.Compiler() == pipelineir.AdvancedCompilerVersion {
		return syncNativeAdvancedTasks(ctx, st, a, observation)
	}
	stats := nativeTaskSyncStats{}
	bad := func() (nativeTaskSyncStats, error) {
		return nativeTaskSyncStats{}, &k8s.Permanent{Reason: "native registered node binding conflict"}
	}
	rows := []model.PipelineTaskExecution{}
	if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("group_id = ? AND run_id = ? AND instance_id = ?", a.GroupID, a.RunID, a.InstanceID).
		Order("id").Limit(pipelineir.MaxTasks + 2).Find(&rows).Error; err != nil {
		return stats, err
	}
	root := observation.Plan.Root()
	sources := map[string]pipelineir.SourceNode{root.NodeKey: root}
	for _, task := range observation.Plan.Tasks() {
		sources[task.NodeKey] = task.SourceNode
	}
	if len(rows) != len(sources) {
		return bad()
	}
	var rootID uint64
	for _, row := range rows {
		if row.NodeKey == root.NodeKey {
			if rootID != 0 || row.ParentID != nil {
				return bad()
			}
			rootID = row.ID
		}
	}
	if rootID == 0 {
		return bad()
	}
	seen := map[string]bool{}
	emptyIO, _ := nativeJSONHash([]byte(`{"schemaVersion":1,"parameters":{},"artifacts":{}}`))
	python := pipelineir.IsPythonCompiler(observation.Plan.Compiler())
	for _, row := range rows {
		source, exists := sources[row.NodeKey]
		var saved struct {
			SchemaVersion int                   `json:"schemaVersion"`
			Source        pipelineir.SourceNode `json:"staticSource"`
			OutputsSealed bool                  `json:"outputsSealed"`
			Protocol      string                `json:"outputProtocol"`
		}
		iteration, iterationErr := nativeJSONHash(row.IterationPath)
		inputHash, inputErr := nativeJSONHash(row.Inputs)
		outputHash, outputErr := nativeJSONHash(row.Outputs)
		if !exists || seen[row.NodeKey] || row.DefinitionPath != source.DefinitionPath || row.NodeKind != source.NodeKind ||
			(row.NodeKey != root.NodeKey && (row.ParentID == nil || *row.ParentID != rootID)) ||
			iterationErr != nil || iteration != mlHash([]byte("[]")) || row.RetryIndex != 0 || row.CacheSourceTaskID != nil ||
			row.WriterEpoch != 0 || row.ReportSequence != 0 || row.ReportHash != nil ||
			inputErr != nil || outputErr != nil || !python && (inputHash != emptyIO || outputHash != emptyIO) ||
			json.Unmarshal(row.RuntimeState, &saved) != nil || saved.SchemaVersion != 1 || saved.Source != source ||
			(!python && (!saved.OutputsSealed || saved.Protocol != "loom-static-empty-v1")) || (python && saved.Protocol != pipelineir.OutputProtocol(observation.Plan.Compiler())) ||
			(row.PodName == nil) != (row.PodUID == nil) || (row.PodUID != nil && row.ArgoNodeID == nil) {
			return bad()
		}
		seen[row.NodeKey] = true
		node, exists := observation.Nodes[row.NodeKey]
		if !exists {
			stats.Missing++
			continue // Preserve prior binding/facts; absence is not a stop.
		}
		if row.ArgoNodeID != nil && *row.ArgoNodeID != node.ID {
			return bad()
		}
		if node.NeverStarted && (row.ArgoNodeID != nil || row.PodUID != nil || row.Phase != "PENDING" && row.Phase != "STOPPED") {
			return bad()
		}
		pod, hasPod := observation.Pods[node.ID]
		if node.Type != "Pod" && (hasPod || row.PodUID != nil) {
			return bad()
		}
		if hasPod && row.PodUID != nil && (*row.PodUID != pod.UID || *row.PodName != pod.Name) {
			return bad()
		}
		if node.Type == "Pod" && !hasPod && row.PodUID == nil {
			stats.PendingPods++
		}
		stats.Matched++
		runtime := map[string]any{}
		if json.Unmarshal(row.RuntimeState, &runtime) != nil || runtime == nil {
			return bad()
		}
		runtime["argoPhase"] = node.Phase
		if node.Type == "Retry" {
			pending, err := syncNativeRetryAttempts(row.RuntimeState, node, observation.Pods, runtime)
			if err != nil {
				return stats, err
			}
			stats.PendingPods += pending
		}
		if python && source.NodeKind == "CONTAINER" {
			if err := syncNativePythonOutputs(ctx, st, a, observation.Plan, &row, node, runtime); err != nil {
				return stats, err
			}
		}
		data, err := json.Marshal(runtime)
		if err != nil {
			return stats, err
		}
		phase := node.Phase
		var reason any
		if phase == "SUCCEEDED" && runtime["outputsSealed"] != true {
			phase, reason = "RUNNING", "OUTPUTS_PENDING"
			stats.PendingOutputs++
		}
		if phase != "SUCCEEDED" {
			stats.NonSucceeded++
		}
		if phase == "FAILED" {
			reason = "ARGO_NODE_FAILED"
		}
		if phase == "UNKNOWN" {
			reason = "ARGO_NODE_STATE_UNKNOWN"
		}
		if terminalNativePhase(&row.Phase) && row.Phase != phase {
			return bad()
		}
		if row.ObservationVersion == ^uint64(0) {
			return stats, mlExecutionConflict()
		}
		fields := map[string]any{"argo_node_id": node.ID, "phase": phase, "reason": reason, "runtime_state": string(data),
			"observation_version": gorm.Expr("observation_version + 1")}
		if node.NeverStarted {
			fields["argo_node_id"] = nil
		}
		if python {
			fields["outputs"] = string(row.Outputs)
			fields["inputs"] = string(row.Inputs)
		}
		if hasPod {
			fields["pod_name"], fields["pod_uid"] = pod.Name, pod.UID
		}
		if node.StartedAt != nil {
			fields["started_at"] = nativeSQLTime(node.StartedAt)
		}
		if node.FinishedAt != nil {
			fields["finished_at"] = nativeSQLTime(node.FinishedAt)
		}
		query := st.DB.WithContext(ctx).Model(&model.PipelineTaskExecution{}).
			Where("group_id = ? AND run_id = ? AND instance_id = ? AND id = ? AND observation_version = ?", a.GroupID, a.RunID, a.InstanceID, row.ID, row.ObservationVersion)
		if row.ArgoNodeID == nil {
			query = query.Where("argo_node_id IS NULL")
		} else {
			query = query.Where("argo_node_id = ?", *row.ArgoNodeID)
		}
		r := query.Updates(fields)
		if r.Error != nil {
			return stats, r.Error
		}
		if r.RowsAffected != 1 {
			return stats, mlExecutionConflict()
		}
	}
	return stats, nil
}
