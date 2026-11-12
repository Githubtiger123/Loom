package service

import (
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

func nativeDynamicTerminalTaskState(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, frozen *nativeStaticNodeObservation, nodes map[string]k8s.PipelineWorkflowNode) (string, error) {
	rows := []model.PipelineTaskExecution{}
	if err := st.DB.WithContext(ctx).Where("group_id = ? AND run_id = ? AND instance_id = ?", a.GroupID, a.RunID, a.InstanceID).Order("id").Limit(k8s.MaxNativeWorkflowNodes + 1).Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) == 0 || len(rows) != len(nodes) || len(rows) > k8s.MaxNativeWorkflowNodes {
		return "", nativeFinalConflict()
	}
	byID := map[uint64]model.PipelineTaskExecution{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	projection := []any{}
	for _, row := range rows {
		node, ok := nodes[row.NodeKey]
		if !ok || row.ArgoNodeID == nil || *row.ArgoNodeID != node.ID || row.Phase != node.Phase || !sameNativeTime(nativeSQLTime(row.StartedAt), nativeSQLTime(node.StartedAt)) || !sameNativeTime(nativeSQLTime(row.FinishedAt), nativeSQLTime(node.FinishedAt)) || (node.Type == "Pod" && !node.CacheHit && node.Phase == "SUCCEEDED" && (row.PodUID == nil || row.PodName == nil)) || (row.PodName == nil) != (row.PodUID == nil) || (node.Type != "Pod" && (row.PodUID != nil || row.PodName != nil)) || node.CacheHit && (row.CacheSourceTaskID == nil || row.PodUID != nil) {
			return "", nativeFinalConflict()
		}
		source, err := nativeDynamicSource(frozen.Plan, row.NodeKey, node)
		if err != nil || row.DefinitionPath != source.DefinitionPath || row.NodeKind != source.NodeKind {
			return "", nativeFinalConflict()
		}
		var state struct {
			DefinitionKey        string `json:"definitionKey"`
			NodeName             string `json:"nodeName"`
			Sealed               bool   `json:"outputsSealed"`
			MainStarted          bool   `json:"mainStarted"`
			LogUnavailableReason string `json:"logUnavailableReason"`
		}
		if json.Unmarshal(row.RuntimeState, &state) != nil || row.Phase == "SUCCEEDED" && !state.Sealed {
			return "", nativeFinalConflict()
		}
		if state.LogUnavailableReason != "" && (state.LogUnavailableReason != "MAIN_NEVER_STARTED" || state.MainStarted || row.PodUID == nil || row.Phase != "FAILED") {
			return "", nativeFinalConflict()
		}
		if row.ParentID != nil {
			p, ok := byID[*row.ParentID]
			if !ok || p.ArgoNodeID == nil || *p.ArgoNodeID != node.ParentNodeID {
				return "", nativeFinalConflict()
			}
		} else if row.NodeKey != frozen.Plan.Root().NodeKey {
			return "", nativeFinalConflict()
		}
		inputHash, err := nativeJSONHash(row.Inputs)
		if err != nil {
			return "", err
		}
		outputHash, err := nativeJSONHash(row.Outputs)
		if err != nil {
			return "", err
		}
		projection = append(projection, map[string]any{"task": nativeTerminalTask{ID: row.ID, NodeKey: row.NodeKey, DefinitionPath: row.DefinitionPath, NodeKind: row.NodeKind, ParentID: row.ParentID, ArgoNodeID: row.ArgoNodeID, PodName: row.PodName, PodUID: row.PodUID, Phase: row.Phase, InputHash: inputHash, OutputHash: outputHash, StartedAt: nativeSQLTime(row.StartedAt), FinishedAt: nativeSQLTime(row.FinishedAt)}, "iteration": json.RawMessage(row.IterationPath), "retryIndex": row.RetryIndex, "cacheSourceTaskId": row.CacheSourceTaskID, "mainStarted": state.MainStarted, "logUnavailableReason": state.LogUnavailableReason})
	}
	raw, _ := json.Marshal(projection)
	return nativeJSONHash(raw)
}
