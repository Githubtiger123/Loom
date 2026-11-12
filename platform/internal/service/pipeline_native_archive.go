package service

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const nativeFinalProtocol = "loom-native-static-final-v1"
const maxNativeFinalSnapshot = 1 << 20

// This bounded archive is internal execution evidence, not a public Workflow
// DTO, log archive, arbitrary artifact manifest or permission for automatic GC.
type nativeTerminalArchive struct {
	SchemaVersion  int             `json:"schemaVersion"`
	Protocol       string          `json:"protocol"`
	OperationID    string          `json:"operationId"`
	SnapshotHash   string          `json:"snapshotHash"`
	RequestHash    string          `json:"requestHash"`
	PlanHash       string          `json:"planHash"`
	Phase          string          `json:"phase"`
	OutputProtocol string          `json:"outputProtocol"`
	TaskCount      int             `json:"taskCount"`
	TaskStateHash  string          `json:"taskStateHash"`
	Workflow       json.RawMessage `json:"workflow"`
}

type nativeTerminalTask struct {
	ID                uint64     `json:"id"`
	NodeKey           string     `json:"nodeKey"`
	DefinitionPath    string     `json:"definitionPath"`
	NodeKind          string     `json:"nodeKind"`
	ParentID          *uint64    `json:"parentId"`
	ArgoNodeID        *string    `json:"argoNodeId"`
	PodName           *string    `json:"podName"`
	PodUID            *string    `json:"podUid"`
	Phase             string     `json:"phase"`
	InputHash         string     `json:"inputHash"`
	OutputHash        string     `json:"outputHash"`
	StartedAt         *time.Time `json:"startedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`
	RetryAttemptsHash string     `json:"retryAttemptsHash,omitempty"`
}

func nativeFinalConflict() error {
	return &k8s.Permanent{Reason: "native terminal persistence evidence conflict"}
}

func nativeFinalPhase(phase string) bool {
	return phase == "SUCCEEDED" || phase == "FAILED" || phase == "STOPPED"
}

func nativeHasTerminalArchive(a *model.PipelineRunAttempt) bool {
	return len(a.WorkflowSnapshot) != 0 || a.WorkflowSnapshotHash != nil || a.WorkflowSnapshotRef != nil || a.WorkflowArchivedAt != nil
}

func sameNativeTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

// MySQL DATETIME(3) and CRD timestamps have different precision. Archive row
// times at SQL precision so replay does not depend on driver round/truncation.
func nativeSQLTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	value := t.UTC().Truncate(time.Millisecond)
	return &value
}

// Missing terminal timestamps/nodes are incomplete evidence, not success. Keep
// projecting known facts while waiting rather than inventing completion times.
func nativeTerminalFactsComplete(obs *k8s.PipelineWorkflowObservation, frozen *nativeStaticNodeObservation) bool {
	if !nativeFinalPhase(obs.Phase) || !obs.NodesComplete || obs.StartedAt == nil || obs.FinishedAt == nil ||
		(frozen.Plan.Compiler() != pipelineir.AdvancedCompilerVersion && len(frozen.Nodes) != len(frozen.Plan.Tasks())+1) {
		return false
	}
	for _, node := range frozen.Nodes {
		if node.NeverStarted {
			if obs.Phase != "STOPPED" {
				return false
			}
			continue
		}
		pythonSkipped := pipelineir.IsPythonCompiler(frozen.Plan.Compiler()) && node.Phase == "SKIPPED"
		unstartedFailure := frozen.Plan.Compiler() == pipelineir.AdvancedCompilerVersion && node.Phase == "FAILED"
		if (!nativeFinalPhase(node.Phase) && node.Phase != "SKIPPED") || (node.FinishedAt == nil && !pythonSkipped) ||
			(node.StartedAt == nil && node.Phase != "SKIPPED" && !node.CacheHit && !unstartedFailure) {
			return false
		}
	}
	return true
}

func validateNativeTerminalWorkflow(obj *unstructured.Unstructured, ref k8s.PipelineWorkflowRef, requestHash string,
	frozen *nativeStaticNodeObservation) (*k8s.PipelineWorkflowObservation, map[string]k8s.PipelineWorkflowNode, error) {
	if err := k8s.ValidateNativePipelineObservedSpec(obj, ref, requestHash); err != nil {
		return nil, nil, err
	}
	suspended, _, err := unstructured.NestedBool(obj.Object, "spec", "suspend")
	if err != nil || suspended || obj.GetDeletionTimestamp() != nil || !validator.IsKubernetesResourceVersion(obj.GetResourceVersion()) {
		return nil, nil, nativeFinalConflict()
	}
	obs, err := k8s.ObserveNativePipelineWorkflow(obj, ref)
	if err != nil {
		return nil, nil, err
	}
	if !nativeFinalPhase(obs.Phase) || !obs.NodesComplete || obs.StartedAt == nil || obs.FinishedAt == nil || obs.FinishedAt.Before(*obs.StartedAt) {
		return nil, nil, nativeFinalConflict()
	}
	nodes, err := k8s.MatchNativeStaticWorkflowNodes(ref, frozen.Plan, obs)
	if err != nil {
		return nil, nil, err
	}
	if (frozen.Plan.Compiler() != pipelineir.AdvancedCompilerVersion && len(nodes) != len(frozen.Plan.Tasks())+1) || nodes[frozen.Plan.Root().NodeKey].Phase != obs.Phase {
		return nil, nil, nativeFinalConflict()
	}
	for _, node := range nodes {
		if node.NeverStarted {
			if obs.Phase != "STOPPED" {
				return nil, nil, nativeFinalConflict()
			}
			continue
		}
		pythonSkipped := pipelineir.IsPythonCompiler(frozen.Plan.Compiler()) && node.Phase == "SKIPPED"
		unstartedFailure := frozen.Plan.Compiler() == pipelineir.AdvancedCompilerVersion && node.Phase == "FAILED"
		if (!nativeFinalPhase(node.Phase) && node.Phase != "SKIPPED") ||
			(obs.Phase == "SUCCEEDED" && node.Phase != "SUCCEEDED" && frozen.Plan.Compiler() != pipelineir.AdvancedCompilerVersion) ||
			(node.FinishedAt == nil && !pythonSkipped) || (node.StartedAt == nil && node.Phase != "SKIPPED" && !node.CacheHit && !unstartedFailure) ||
			(node.StartedAt != nil && ((node.FinishedAt != nil && node.FinishedAt.Before(*node.StartedAt)) || node.StartedAt.Before(*obs.StartedAt))) ||
			(node.FinishedAt != nil && (node.FinishedAt.Before(*obs.StartedAt) || obs.FinishedAt.Before(*node.FinishedAt))) {
			return nil, nil, nativeFinalConflict()
		}
	}
	if frozen.Plan.Compiler() == pipelineir.AdvancedCompilerVersion {
		// Earlier failed retry attempts are legitimate in a successful run.
		// Any other failed/error node contradicts a successful Workflow.
		if obs.Phase == "SUCCEEDED" {
			byID := map[string]k8s.PipelineWorkflowNode{}
			for _, n := range nodes {
				byID[n.ID] = n
			}
			for _, n := range nodes {
				if n.Phase == "FAILED" {
					parent := byID[n.ParentNodeID]
					if parent.Type != "Retry" || parent.Phase != "SUCCEEDED" {
						return nil, nil, nativeFinalConflict()
					}
				}
			}
		}
	}
	return obs, nodes, nil
}

// All callers hold Run/attempt/journal locks. Validate registered static IO and
// task identities again, then hash only immutable final facts, not observation
// counters, heartbeat times, status messages or mutable diagnostic strings.
func nativeTerminalTaskState(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt,
	frozen *nativeStaticNodeObservation, nodes map[string]k8s.PipelineWorkflowNode) (string, error) {
	if frozen.Plan.Compiler() == pipelineir.AdvancedCompilerVersion {
		return nativeDynamicTerminalTaskState(ctx, st, a, frozen, nodes)
	}
	empty := &nativeStaticNodeObservation{Plan: frozen.Plan, Nodes: map[string]k8s.PipelineWorkflowNode{}}
	if _, err := syncNativePipelineTasks(ctx, st, a, empty); err != nil {
		return "", err
	}
	rows := []model.PipelineTaskExecution{}
	if err := st.DB.WithContext(ctx).Where("group_id = ? AND run_id = ? AND instance_id = ?", a.GroupID, a.RunID, a.InstanceID).
		Order("id").Limit(pipelineir.MaxTasks + 2).Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) != len(nodes) || len(rows) == 0 {
		return "", nativeFinalConflict()
	}
	projection := make([]nativeTerminalTask, 0, len(rows))
	for _, row := range rows {
		node, exists := nodes[row.NodeKey]
		if !exists || (!node.NeverStarted && (row.ArgoNodeID == nil || *row.ArgoNodeID != node.ID)) || (node.NeverStarted && row.ArgoNodeID != nil) || row.Phase != node.Phase ||
			!sameNativeTime(nativeSQLTime(row.StartedAt), nativeSQLTime(node.StartedAt)) ||
			!sameNativeTime(nativeSQLTime(row.FinishedAt), nativeSQLTime(node.FinishedAt)) ||
			(node.Type == "Pod" && (row.PodName == nil || !validator.IsDNS1123Subdomain(*row.PodName) || row.PodUID == nil || !validator.IsCatalogID(*row.PodUID))) ||
			(node.Type != "Pod" && (row.PodName != nil || row.PodUID != nil)) {
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
		projection = append(projection, nativeTerminalTask{ID: row.ID, NodeKey: row.NodeKey, DefinitionPath: row.DefinitionPath,
			NodeKind: row.NodeKind, ParentID: row.ParentID, ArgoNodeID: row.ArgoNodeID, PodName: row.PodName, PodUID: row.PodUID,
			Phase: row.Phase, InputHash: inputHash, OutputHash: outputHash,
			StartedAt: nativeSQLTime(row.StartedAt), FinishedAt: nativeSQLTime(row.FinishedAt)})
		if frozen.Plan.Compiler() == pipelineir.AdvancedCompilerVersion {
			attempts, err := nativeRetryAttempts(row.RuntimeState)
			if err != nil {
				return "", err
			}
			if len(attempts) != len(node.Attempts) {
				return "", nativeFinalConflict()
			}
			for i, attempt := range attempts {
				observed := node.Attempts[i]
				if attempt.NodeID != observed.ID || attempt.NodeName != observed.Name || attempt.Phase != observed.Phase || !nativeFinalPhase(attempt.Phase) || attempt.PodUID == "" || !sameNativeTime(attempt.StartedAt, nativeSQLTime(observed.StartedAt)) || !sameNativeTime(attempt.FinishedAt, nativeSQLTime(observed.FinishedAt)) {
					return "", nativeFinalConflict()
				}
			}
			raw, _ := json.Marshal(attempts)
			projection[len(projection)-1].RetryAttemptsHash = mlHash(raw)
		}
	}
	// The no-port compiler cannot declare/register artifacts, including STAGING
	// rows. Future artifact publishers must take the same Run/attempt fence.
	var artifactCount int64
	if err := st.DB.WithContext(ctx).Model(&model.PipelineArtifact{}).
		Where("group_id = ? AND producer_instance_id = ?", a.GroupID, a.InstanceID).Count(&artifactCount).Error; err != nil {
		return "", err
	}
	if !pipelineir.IsPythonCompiler(frozen.Plan.Compiler()) && artifactCount != 0 {
		return "", nativeFinalConflict()
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return "", err
	}
	return nativeJSONHash(raw)
}

func validateNativeFinalStart(a *model.PipelineRunAttempt, frozen *nativeStaticNodeObservation, ref k8s.PipelineWorkflowRef,
	requestHash string, obj *unstructured.Unstructured, summary map[string]any) error {
	copy := *a
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	copy.RuntimeSummary = raw
	_, checkpoint, err := nativeStartSummary(&copy)
	if err != nil {
		return err
	}
	if !nativeStartCheckpointMatches(checkpoint, frozen.OperationID, ref, frozen.SnapshotHash, requestHash) ||
		checkpoint.State != "CONFIRMED" || obj.GetAnnotations()[k8s.AnnotationPipelineStartToken] != checkpoint.Request.Token {
		return nativeFinalConflict()
	}
	return nil
}

// Retain spec and complete status but not managedFields or unrelated metadata.
// The archive remains private: status messages/argv can contain sensitive data.
func nativeFinalWorkflowSnapshot(obj *unstructured.Unstructured) (json.RawMessage, error) {
	copy := &unstructured.Unstructured{Object: map[string]any{"apiVersion": obj.GetAPIVersion(), "kind": obj.GetKind()}}
	copy.SetName(obj.GetName())
	copy.SetNamespace(obj.GetNamespace())
	copy.SetUID(obj.GetUID())
	copy.SetResourceVersion(obj.GetResourceVersion())
	copy.SetLabels(map[string]string{"app.kubernetes.io/managed-by": obj.GetLabels()["app.kubernetes.io/managed-by"],
		"loom.ml/group-id": obj.GetLabels()["loom.ml/group-id"], "loom.ml/pipeline-run-id": obj.GetLabels()["loom.ml/pipeline-run-id"]})
	annotations := map[string]string{}
	for _, key := range []string{"loom.ml/instance-id", "loom.ml/pipeline-namespace-uid", "loom.ml/pipeline-plan-hash", k8s.AnnotationPipelineStartToken, k8s.AnnotationPipelineStopToken} {
		annotations[key] = obj.GetAnnotations()[key]
	}
	copy.SetAnnotations(annotations)
	for _, key := range []string{"spec", "status"} {
		value, exists, err := unstructured.NestedMap(obj.Object, key)
		if err != nil || !exists {
			return nil, nativeFinalConflict()
		}
		copy.Object[key] = value
	}
	return json.Marshal(copy.Object)
}

func readNativeTerminalArchive(a *model.PipelineRunAttempt, j *model.PipelineSubmission,
	frozen *nativeStaticNodeObservation) (*nativeTerminalArchive, map[string]k8s.PipelineWorkflowNode, error) {
	bad := func() (*nativeTerminalArchive, map[string]k8s.PipelineWorkflowNode, error) {
		return nil, nil, nativeFinalConflict()
	}
	// MySQL's JSON serializer inserts whitespace. Limit stored bytes before
	// compacting, then apply the same compact-payload limit as on creation.
	if len(a.WorkflowSnapshot) > 2*maxNativeFinalSnapshot {
		return bad()
	}
	var compact bytes.Buffer
	if json.Compact(&compact, a.WorkflowSnapshot) != nil ||
		!validator.IsBoundedJSONObject(compact.Bytes(), maxNativeFinalSnapshot, 64, 200000) || a.WorkflowSnapshotRef != nil ||
		a.WorkflowSnapshotHash == nil || !validator.IsSHA256(*a.WorkflowSnapshotHash) || a.WorkflowArchivedAt == nil {
		return bad()
	}
	hash, err := nativeJSONHash(a.WorkflowSnapshot)
	if err != nil || hash != *a.WorkflowSnapshotHash {
		return bad()
	}
	var archive nativeTerminalArchive
	d := json.NewDecoder(bytes.NewReader(a.WorkflowSnapshot))
	d.DisallowUnknownFields()
	if d.Decode(&archive) != nil || archive.SchemaVersion != 1 || archive.Protocol != nativeFinalProtocol ||
		archive.OperationID != frozen.OperationID || archive.SnapshotHash != frozen.SnapshotHash || archive.RequestHash != j.RequestHash ||
		a.PlanHash == nil || archive.PlanHash != *a.PlanHash || archive.OutputProtocol != nativeOutputProtocol(frozen.Plan) ||
		!validator.IsSHA256(archive.TaskStateHash) || (frozen.Plan.Compiler() != pipelineir.AdvancedCompilerVersion && archive.TaskCount != len(frozen.Plan.Tasks())+1) ||
		a.Phase == nil || archive.Phase != *a.Phase || !nativeFinalPhase(archive.Phase) {
		return bad()
	}
	ref, valid := nativePipelineRef(a)
	if !valid || !nativePipelineJournalMatches(j, a, ref) {
		return bad()
	}
	obj := &unstructured.Unstructured{}
	if json.Unmarshal(archive.Workflow, obj) != nil {
		return bad()
	}
	obs, nodes, err := validateNativeTerminalWorkflow(obj, ref, j.RequestHash, frozen)
	if err != nil || obs.Phase != archive.Phase || archive.TaskCount != len(nodes) {
		return bad()
	}
	if !sameNativeTime(nativeSQLTime(a.StartedAt), nativeSQLTime(obs.StartedAt)) ||
		!sameNativeTime(nativeSQLTime(a.FinishedAt), nativeSQLTime(obs.FinishedAt)) {
		return bad()
	}
	summary, _, err := nativeStartSummary(a)
	if err != nil || validateNativeFinalStart(a, frozen, ref, j.RequestHash, obj, summary) != nil {
		return bad()
	}
	return &archive, nodes, nil
}

// Called only after verified node projection in the existing observer SQL tx.
// Return nil data for a reused archive; an existing snapshot is never replaced.
func prepareNativeTerminalArchive(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, j *model.PipelineSubmission,
	frozen *nativeStaticNodeObservation, ref k8s.PipelineWorkflowRef, obj *unstructured.Unstructured, summary map[string]any) (json.RawMessage, string, error) {
	obs, nodes, err := validateNativeTerminalWorkflow(obj, ref, j.RequestHash, frozen)
	if err != nil {
		return nil, "", err
	}
	if err := validateNativeFinalStart(a, frozen, ref, j.RequestHash, obj, summary); err != nil {
		return nil, "", err
	}
	taskHash, err := nativeTerminalTaskState(ctx, st, a, frozen, nodes)
	if err != nil {
		return nil, "", err
	}
	if nativeHasTerminalArchive(a) {
		archive, _, err := readNativeTerminalArchive(a, j, frozen)
		if err != nil || archive.TaskStateHash != taskHash || archive.Phase != obs.Phase ||
			!sameNativeTime(nativeSQLTime(a.StartedAt), nativeSQLTime(obs.StartedAt)) ||
			!sameNativeTime(nativeSQLTime(a.FinishedAt), nativeSQLTime(obs.FinishedAt)) {
			return nil, "", nativeFinalConflict()
		}
		return nil, *a.WorkflowSnapshotHash, nil
	}
	workflow, err := nativeFinalWorkflowSnapshot(obj)
	if err != nil {
		return nil, "", err
	}
	archive := nativeTerminalArchive{SchemaVersion: 1, Protocol: nativeFinalProtocol, OperationID: frozen.OperationID,
		SnapshotHash: frozen.SnapshotHash, RequestHash: j.RequestHash, PlanHash: ref.PlanHash, Phase: obs.Phase,
		OutputProtocol: nativeOutputProtocol(frozen.Plan), TaskCount: len(nodes), TaskStateHash: taskHash, Workflow: workflow}
	raw, err := json.Marshal(archive)
	if err != nil || !validator.IsBoundedJSONObject(raw, maxNativeFinalSnapshot, 64, 200000) {
		return nil, "", &k8s.Permanent{Reason: "native terminal snapshot exceeds persistence limits"}
	}
	hash, err := nativeJSONHash(raw)
	return raw, hash, err
}
