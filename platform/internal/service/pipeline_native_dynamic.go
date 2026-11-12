package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func nativeDefinition(plan *pipelineir.Plan, key string) (pipelineir.Task, bool) {
	for _, task := range plan.Tasks() {
		if task.NodeKey == key {
			return task, true
		}
	}
	return pipelineir.Task{}, false
}

func nativeDynamicSource(plan *pipelineir.Plan, key string, node k8s.PipelineWorkflowNode) (pipelineir.SourceNode, error) {
	if key == plan.Root().NodeKey {
		if node.DefinitionKey != key {
			return pipelineir.SourceNode{}, nativeFinalConflict()
		}
		return plan.Root(), nil
	}
	task, ok := nativeDefinition(plan, node.DefinitionKey)
	if !ok || key != k8s.NativeRuntimeNodeKey(task.NodeKey, node.ID) {
		return pipelineir.SourceNode{}, nativeFinalConflict()
	}
	source := task.SourceNode
	source.NodeKey = key
	switch node.Type {
	case "Retry":
		source.NodeKind = "RETRY"
	case "TaskGroup":
		source.NodeKind = "LOOP"
	case "Pod":
		source.NodeKind = "CONTAINER"
	case "DAG":
		source.NodeKind = "DAG"
	case "Skipped", "Omitted":
	default:
		return pipelineir.SourceNode{}, nativeFinalConflict()
	}
	return source, nil
}

func syncNativeAdvancedTasks(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, o *nativeStaticNodeObservation) (nativeTaskSyncStats, error) {
	stats := nativeTaskSyncStats{}
	rows := []model.PipelineTaskExecution{}
	if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id = ? AND run_id = ? AND instance_id = ?", a.GroupID, a.RunID, a.InstanceID).Order("id").Limit(k8s.MaxNativeWorkflowNodes + 1).Find(&rows).Error; err != nil {
		return stats, err
	}
	if len(rows) == 0 || len(rows) > k8s.MaxNativeWorkflowNodes {
		return stats, nativeFinalConflict()
	}
	byKey := map[string]*model.PipelineTaskExecution{}
	byArgo := map[string]*model.PipelineTaskExecution{}
	for i := range rows {
		row := &rows[i]
		byKey[row.NodeKey] = row
		if row.ArgoNodeID != nil {
			byArgo[*row.ArgoNodeID] = row
		}
	}
	if byKey[o.Plan.Root().NodeKey] == nil {
		return stats, nativeFinalConflict()
	}
	for key, node := range o.Nodes {
		if row := byKey[key]; row != nil {
			byArgo[node.ID] = row
		}
	}
	// First create each recognized runtime instance under its actual parent.
	// Status can introduce rows only after complete frozen-template matching.
	keys := []string{}
	for key := range o.Nodes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for range len(keys) {
		changed := false
		for _, key := range keys {
			node := o.Nodes[key]
			if byKey[key] != nil {
				continue
			}
			parent := byArgo[node.ParentNodeID]
			if parent == nil {
				continue
			}
			source, err := nativeDynamicSource(o.Plan, key, node)
			if err != nil {
				return stats, err
			}
			runtime, _ := json.Marshal(map[string]any{"schemaVersion": 1, "staticSource": source, "definitionKey": node.DefinitionKey, "nodeName": node.Name, "outputsSealed": source.NodeKind != "CONTAINER", "outputProtocol": pipelineir.AdvancedOutputProtocol})
			iteration, _ := json.Marshal(node.IterationPath)
			if len(node.IterationPath) == 0 {
				iteration = []byte("[]")
			}
			nodeID := node.ID
			row := &model.PipelineTaskExecution{GroupID: a.GroupID, RunID: a.RunID, InstanceID: a.InstanceID, NodeKey: key, DefinitionPath: source.DefinitionPath, NodeKind: source.NodeKind, ParentID: &parent.ID, IterationPath: iteration, RetryIndex: uint32(node.RetryIndex), ArgoNodeID: &nodeID, Phase: "PENDING", Inputs: json.RawMessage(`{"schemaVersion":1,"parameters":{},"artifacts":{}}`), Outputs: json.RawMessage(`{"schemaVersion":1,"parameters":{},"artifacts":{}}`), RuntimeState: runtime}
			if err = st.DB.WithContext(ctx).Create(row).Error; err != nil {
				return stats, err
			}
			byKey[key] = row
			byArgo[node.ID] = row
			changed = true
		}
		if !changed {
			break
		}
	}
	// Pods publish first. DAG/Retry aliases then reuse already published output
	// identities in this transaction, rather than registering the same file again.
	slices.SortStableFunc(keys, func(left, right string) int {
		a, b := o.Nodes[left], o.Nodes[right]
		rank := func(n k8s.PipelineWorkflowNode) int {
			if n.CacheHit {
				return 1
			}
			if n.Type == "Pod" {
				return 0
			}
			return 2
		}
		if rank(a) != rank(b) {
			return rank(a) - rank(b)
		}
		return len(b.Name) - len(a.Name)
	})
	for _, key := range keys {
		node := o.Nodes[key]
		row := byKey[key]
		if row == nil {
			return stats, nativeFinalConflict()
		}
		source, err := nativeDynamicSource(o.Plan, key, node)
		if err != nil {
			return stats, err
		}
		var saved struct {
			Source        pipelineir.SourceNode `json:"staticSource"`
			DefinitionKey string                `json:"definitionKey"`
			NodeName      string                `json:"nodeName"`
			Protocol      string                `json:"outputProtocol"`
		}
		if json.Unmarshal(row.RuntimeState, &saved) != nil || saved.Source != source || saved.Protocol != pipelineir.AdvancedOutputProtocol || key != o.Plan.Root().NodeKey && (saved.DefinitionKey != node.DefinitionKey || saved.NodeName != node.Name) || row.ArgoNodeID != nil && *row.ArgoNodeID != node.ID || row.CacheSourceTaskID != nil && !node.CacheHit || row.WriterEpoch != 0 || row.ReportSequence != 0 || row.ReportHash != nil {
			return stats, nativeFinalConflict()
		}
		if key != o.Plan.Root().NodeKey {
			parent := byArgo[node.ParentNodeID]
			if parent == nil || row.ParentID == nil || *row.ParentID != parent.ID {
				return stats, nativeFinalConflict()
			}
		}
		pod, hasPod := o.Pods[node.ID]
		if hasPod && (node.Type != "Pod" || row.PodUID != nil && (*row.PodUID != pod.UID || row.PodName == nil || *row.PodName != pod.Name)) {
			return stats, nativeFinalConflict()
		}
		if node.Type == "Pod" && !node.CacheHit && row.PodUID == nil && !hasPod && node.Phase == "SUCCEEDED" {
			stats.PendingPods++
		}
		if hasPod {
			row.PodName, row.PodUID = &pod.Name, &pod.UID
		}
		runtime := map[string]any{}
		if json.Unmarshal(row.RuntimeState, &runtime) != nil {
			return stats, nativeFinalConflict()
		}
		runtime["argoPhase"] = node.Phase
		if hasPod && pod.MainStarted || node.Type == "Pod" && !node.CacheHit && node.Phase == "SUCCEEDED" {
			runtime["mainStarted"] = true
			delete(runtime, "logUnavailableReason")
		} else if hasPod && pod.MainNeverStarted && node.Phase == "FAILED" && runtime["mainStarted"] != true {
			runtime["logUnavailableReason"] = "MAIN_NEVER_STARTED"
		}
		if node.Phase == "SUCCEEDED" && key != o.Plan.Root().NodeKey && node.Type != "TaskGroup" {
			if node.CacheHit {
				task, ok := nativeDefinition(o.Plan, node.DefinitionKey)
				if !ok {
					return stats, nativeFinalConflict()
				}
				if err = syncNativeCacheHit(ctx, st, a, o.Plan, task, row, node, runtime); err != nil {
					return stats, err
				}
			} else if node.Type == "Pod" {
				if row.PodName != nil {
					if err = syncNativePythonOutputs(ctx, st, a, o.Plan, row, node, runtime); err != nil {
						return stats, err
					}
					task, ok := nativeDefinition(o.Plan, node.DefinitionKey)
					if !ok {
						return stats, nativeFinalConflict()
					}
					if runtime["cachePublished"] != true {
						if err = publishNativeCache(ctx, st, a, task, row, node); err != nil {
							return stats, err
						}
						runtime["cachePublished"] = true
					}
				}
			} else {
				if err = syncNativeAliasOutputs(ctx, st, a, o.Plan, row, node, runtime); err != nil {
					return stats, err
				}
			}
		}
		if len(node.Inputs) > 0 {
			var input struct {
				Parameters []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"parameters"`
			}
			if json.Unmarshal(node.Inputs, &input) != nil {
				return stats, nativeFinalConflict()
			}
			for _, p := range input.Parameters {
				if p.Name == "loom-values" {
					raw, err := base64.StdEncoding.DecodeString(p.Value)
					if err != nil || !validator.IsBoundedJSONObject(raw, 48<<10, 16, 4096) {
						return stats, nativeFinalConflict()
					}
					var values map[string]json.RawMessage
					if json.Unmarshal(raw, &values) != nil {
						return stats, nativeFinalConflict()
					}
					row.Inputs, _ = json.Marshal(map[string]any{"schemaVersion": 1, "parameters": values, "artifacts": map[string]any{}})
				}
			}
		}
		phase := node.Phase
		if phase == "SUCCEEDED" && runtime["outputsSealed"] != true {
			phase = "RUNNING"
			stats.PendingOutputs++
		}
		if terminalNativePhase(&row.Phase) && row.Phase != phase {
			return stats, nativeFinalConflict()
		}
		state, _ := json.Marshal(runtime)
		if !validator.IsBoundedJSONObject(state, 64<<10, 16, 4096) {
			return stats, nativeFinalConflict()
		}
		fields := map[string]any{"argo_node_id": node.ID, "phase": phase, "runtime_state": string(state), "inputs": string(row.Inputs), "outputs": string(row.Outputs), "observation_version": gorm.Expr("observation_version + 1")}
		fields["cache_source_task_id"] = row.CacheSourceTaskID
		if hasPod {
			fields["pod_name"], fields["pod_uid"] = pod.Name, pod.UID
		}
		if node.StartedAt != nil {
			fields["started_at"] = nativeSQLTime(node.StartedAt)
		}
		if node.FinishedAt != nil {
			fields["finished_at"] = nativeSQLTime(node.FinishedAt)
		}
		r := st.DB.WithContext(ctx).Model(row).Where("id = ? AND observation_version = ?", row.ID, row.ObservationVersion).Updates(fields)
		if r.Error != nil {
			return stats, r.Error
		}
		if r.RowsAffected != 1 {
			return stats, mlExecutionConflict()
		}
		stats.Matched++
	}
	if err := syncNativeAdvancedInputs(ctx, st, a, o); err != nil {
		return stats, err
	}
	if len(o.Nodes) > 0 {
		for _, row := range rows {
			if row.ArgoNodeID != nil {
				if _, ok := o.Nodes[row.NodeKey]; !ok {
					return stats, nativeFinalConflict()
				}
			}
		}
	}
	return stats, nil
}

// Structural outputs alias existing artifact IDs. Endpoint/key/type/hash still
// have to match a published leaf from the same authorized run, not an arbitrary
// S3 URI or an artifact named by the component.
func syncNativeAliasOutputs(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, plan *pipelineir.Plan, row *model.PipelineTaskExecution, node k8s.PipelineWorkflowNode, runtime map[string]any) error {
	task, ok := nativeDefinition(plan, node.DefinitionKey)
	if !ok {
		return nativeFinalConflict()
	}
	var output nativeArgoOutputs
	if json.Unmarshal(node.Outputs, &output) != nil {
		return nativeFinalConflict()
	}
	var bundle nativePythonBundle
	bundleFound := false
	for _, p := range output.Parameters {
		if p.Name == "loom-bundle" {
			if bundleFound {
				return nativeFinalConflict()
			}
			bundleFound = true
			raw, err := base64.StdEncoding.DecodeString(p.Value)
			if err != nil || !validator.IsBoundedJSONObject(raw, 48<<10, 16, 4096) || json.Unmarshal(raw, &bundle) != nil {
				return nativeFinalConflict()
			}
		}
	}
	if bundle.Protocol != pipelineir.AdvancedOutputProtocol || len(bundle.Parameters) != len(task.Runtime.Outputs.Parameters) || len(bundle.Artifacts) != len(task.Runtime.Outputs.Artifacts) {
		return nativeFinalConflict()
	}
	for name, port := range task.Runtime.Outputs.Parameters {
		if !pipelineir.AdvancedParameterValid(port.Type, bundle.Parameters[name]) {
			return nativeFinalConflict()
		}
	}
	artifacts := map[string]any{}
	candidates, err := nativeReusableArtifacts(ctx, st, a)
	if err != nil {
		return err
	}
	var snapshot nativeStagingSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil {
		return nativeFinalConflict()
	}
	for name, port := range task.Runtime.Outputs.Artifacts {
		value, ok := bundle.Artifacts[name]
		if !ok {
			return nativeFinalConflict()
		}
		var transportKey string
		for _, artifact := range output.Artifacts {
			if artifact.Name == pipelineir.PortIndex(task.Runtime.Outputs.Artifacts, name) {
				if transportKey != "" || artifact.S3.Endpoint != snapshot.Policy.ArtifactEndpoint || artifact.S3.Bucket != snapshot.Policy.ArtifactBucket {
					return nativeFinalConflict()
				}
				transportKey = artifact.S3.Key
			}
		}
		var id uint64
		for _, candidate := range candidates {
			if nativePublishedArtifactMatches(candidate, port, value, snapshot.Policy.ArtifactEndpoint, snapshot.Policy.ArtifactBucket, transportKey) {
				if id != 0 && id != candidate.ID {
					return nativeFinalConflict()
				}
				id = candidate.ID
			}
		}
		if id == 0 {
			return nativeFinalConflict()
		}
		artifacts[name] = map[string]any{"id": id, "type": port.Type, "metadata": value.Metadata, "sha256": value.SHA256, "size": value.Size}
	}
	data, _ := json.Marshal(map[string]any{"schemaVersion": 1, "parameters": bundle.Parameters, "artifacts": artifacts})
	if runtime["outputsSealed"] == true {
		var previous struct {
			Artifacts  map[string]any `json:"artifacts"`
			Parameters map[string]any `json:"parameters"`
		}
		_ = json.Unmarshal(row.Outputs, &previous)
		if len(previous.Parameters) > 0 || len(previous.Artifacts) > 0 {
			old, _ := nativeJSONHash(row.Outputs)
			next, _ := nativeJSONHash(data)
			if old != next {
				return nativeFinalConflict()
			}
		}
	}
	row.Outputs = data
	runtime["outputsSealed"] = true
	return nil
}
