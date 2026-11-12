package service

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

func jsonIndex(index int) string { return strconv.Itoa(index) }

// Retry attempts share their immutable logical task row. Actual Argo node IDs
// and Pod UIDs are retained individually; no attempt can replace another's Pod.
type nativeRetryAttempt struct {
	Index      int        `json:"index"`
	NodeID     string     `json:"nodeId"`
	NodeName   string     `json:"nodeName"`
	PodName    string     `json:"podName,omitempty"`
	PodUID     string     `json:"podUID,omitempty"`
	Phase      string     `json:"phase"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

func nativeRetryAttempts(raw json.RawMessage) ([]nativeRetryAttempt, error) {
	var state struct {
		Attempts []nativeRetryAttempt `json:"retryAttempts"`
	}
	if json.Unmarshal(raw, &state) != nil || len(state.Attempts) > pipelineir.MaxTaskRetries+1 {
		return nil, nativeFinalConflict()
	}
	seen := map[string]bool{}
	for i, a := range state.Attempts {
		if a.Index != i || a.NodeID == "" || a.NodeName == "" || seen[a.NodeID] || (a.PodName == "") != (a.PodUID == "") || (a.PodUID != "" && (!validator.IsCatalogID(a.PodUID) || !validator.IsDNS1123Subdomain(a.PodName))) {
			return nil, nativeFinalConflict()
		}
		seen[a.NodeID] = true
	}
	return state.Attempts, nil
}

func syncNativeRetryAttempts(raw json.RawMessage, node k8s.PipelineWorkflowNode, pods map[string]k8s.PipelineWorkflowPod, runtime map[string]any) (int, error) {
	previous, err := nativeRetryAttempts(raw)
	if err != nil {
		return 0, err
	}
	if len(node.Attempts) < len(previous) {
		return 0, nativeFinalConflict()
	}
	rows := make([]nativeRetryAttempt, 0, len(node.Attempts))
	pending := 0
	for i, n := range node.Attempts {
		a := nativeRetryAttempt{Index: i, NodeID: n.ID, NodeName: n.Name, Phase: n.Phase, StartedAt: nativeSQLTime(n.StartedAt), FinishedAt: nativeSQLTime(n.FinishedAt)}
		if i < len(previous) {
			p := previous[i]
			if p.NodeID != a.NodeID || p.NodeName != a.NodeName || terminalNativePhase(&p.Phase) && p.Phase != a.Phase {
				return 0, nativeFinalConflict()
			}
			a.PodName, a.PodUID = p.PodName, p.PodUID
		}
		if pod, ok := pods[n.ID]; ok {
			if a.PodUID != "" && (a.PodUID != pod.UID || a.PodName != pod.Name) {
				return 0, nativeFinalConflict()
			}
			a.PodName, a.PodUID = pod.Name, pod.UID
		}
		if a.PodUID == "" {
			pending++
		}
		rows = append(rows, a)
	}
	runtime["retryAttempts"] = rows
	return pending, nil
}
