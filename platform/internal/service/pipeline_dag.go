package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type PipelineMetadataReader interface {
	ReadRun(context.Context, string, string, string) (*mlmd.Snapshot, error)
}

type PipelineDataSource struct {
	Status     string     `json:"status"` // AVAILABLE, NOT_CONFIGURED, NOT_READY_OR_UNAVAILABLE, NOT_SUBMITTED, CLEANED
	ObservedAt *time.Time `json:"observedAt,omitempty"`
}
type PipelineNodeInstance struct {
	ID                string              `json:"nodeId"`
	DefinitionID      string              `json:"definitionNodeId,omitempty"`
	Mapping           string              `json:"mapping"` // EXACT or UNRESOLVED; never match by display name alone.
	ParentID          string              `json:"parentNodeId,omitempty"`
	TaskID            string              `json:"taskId,omitempty"`
	ExecutionID       string              `json:"executionId,omitempty"`
	ParentTaskID      string              `json:"parentTaskId,omitempty"`
	ParentExecutionID string              `json:"parentExecutionId,omitempty"`
	Name              string              `json:"name"`
	State             string              `json:"state"`
	MetadataState     string              `json:"metadataState,omitempty"`
	PodName           string              `json:"podName,omitempty"`
	CreatedAt         string              `json:"createdAt,omitempty"`
	StartTime         string              `json:"startTime,omitempty"`
	EndTime           string              `json:"endTime,omitempty"`
	CreatedAtEpochMs  string              `json:"createdAtEpochMs,omitempty"`
	UpdatedAtEpochMs  string              `json:"updatedAtEpochMs,omitempty"`
	IterationIndex    any                 `json:"iterationIndex,omitempty"`
	IterationCount    any                 `json:"iterationCount,omitempty"`
	CachedExecutionID string              `json:"cachedExecutionId,omitempty"`
	Error             *kfp.RuntimeError   `json:"error,omitempty"`
	History           []kfp.RuntimeStatus `json:"stateHistory"`
	Children          []kfp.ChildTask     `json:"childTasks"`
	Executor          *kfp.ExecutorDetail `json:"executor,omitempty"`
}
type PipelineDAG struct {
	InstanceID    string                   `json:"instanceId"`
	Revision      uint64                   `json:"revision"`
	ExternalID    *string                  `json:"externalId"`
	PackageDigest string                   `json:"packageDigest"`
	State         string                   `json:"state"`
	Error         *kfp.RuntimeError        `json:"error,omitempty"`
	History       []kfp.RuntimeStatus      `json:"stateHistory"`
	ReadAt        time.Time                `json:"readAt"`
	Runtime       PipelineDataSource       `json:"runtime"`
	Metadata      PipelineDataSource       `json:"metadata"`
	Definitions   []PipelineNodeDefinition `json:"definitions"`
	Edges         []PipelineDAGEdge        `json:"edges"`
	Instances     []PipelineNodeInstance   `json:"instances"`
}
type PipelineParameter struct {
	Available bool   `json:"available"`
	Value     any    `json:"value"`
	Source    string `json:"source"` // MLMD, SUBMITTED, UNRESOLVED
}
type PipelineArtifactRef struct {
	ID        string         `json:"artifactId"`
	Status    string         `json:"status"` // AVAILABLE or NOT_FOUND
	EventType int            `json:"eventType,omitempty"`
	Path      []any          `json:"path,omitempty"`
	Metadata  *mlmd.Artifact `json:"metadata,omitempty"`
}
type PipelineNodeIO struct {
	ParametersStatus string                           `json:"parametersStatus"`
	Parameters       map[string]PipelineParameter     `json:"parameters"`
	ArtifactsStatus  string                           `json:"artifactsStatus"`
	Artifacts        map[string][]PipelineArtifactRef `json:"artifacts"`
}
type PipelineNodeDetail struct {
	InstanceID string                  `json:"instanceId"`
	Revision   uint64                  `json:"revision"`
	ReadAt     time.Time               `json:"readAt"`
	Runtime    PipelineDataSource      `json:"runtime"`
	Metadata   PipelineDataSource      `json:"metadata"`
	Definition *PipelineNodeDefinition `json:"definition"`
	Instance   *PipelineNodeInstance   `json:"instance"`
	Inputs     PipelineNodeIO          `json:"inputs"`
	Outputs    PipelineNodeIO          `json:"outputs"`
}
type pipelineDAGRead struct {
	run        *model.PipelineRun
	spec       dto.PipelineRunSpec
	graph      *pipelineGraph
	dag        *PipelineDAG
	metadata   *mlmd.Snapshot
	executions map[string]*mlmd.Execution
	tasks      map[string]*kfp.TaskDetail // indexed by public instance node ID
}

func (s *Service) PipelineRunDAG(ctx context.Context, actor, groupID, id uint64) (*PipelineDAG, error) {
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := s.readPipelineDAG(readCtx, actor, groupID, id, "")
	if err != nil {
		return nil, err
	}
	if err := s.checkPipelineDAGRead(readCtx, actor, data.run); err != nil {
		return nil, err
	}
	return data.dag, nil
}

func (s *Service) PipelineRunNode(ctx context.Context, actor, groupID, id uint64, nodeID, expectedInstanceID string) (*PipelineNodeDetail, error) {
	if !validator.IsIdempotencyKey(nodeID) {
		return nil, apperr.New(apperr.CodeInvalid, "nodeId 无效，请使用 DAG 返回的 nodeId")
	}
	if expectedInstanceID != "" && !validator.IsCatalogID(expectedInstanceID) {
		return nil, apperr.New(apperr.CodeInvalid, "instanceId 无效")
	}
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := s.readPipelineDAG(readCtx, actor, groupID, id, nodeID)
	if err != nil {
		return nil, err
	}
	if expectedInstanceID != "" && data.run.InstanceID != expectedInstanceID {
		return nil, apperr.New(apperr.CodeConflict, "运行已重建，请重新获取 DAG")
	}
	out := &PipelineNodeDetail{InstanceID: data.run.InstanceID, Revision: data.run.Revision, ReadAt: data.dag.ReadAt, Runtime: data.dag.Runtime, Metadata: data.dag.Metadata, Definition: data.graph.byID[nodeID]}
	for i := range data.dag.Instances {
		if data.dag.Instances[i].ID == nodeID {
			out.Instance = &data.dag.Instances[i]
			out.Definition = data.graph.byID[out.Instance.DefinitionID]
			break
		}
	}
	if out.Definition == nil && out.Instance == nil {
		return nil, apperr.New(apperr.CodeNotFound, "当前运行中不存在该节点")
	}
	out.Inputs = buildNodeIO(data, out.Definition, out.Instance, true)
	out.Outputs = buildNodeIO(data, out.Definition, out.Instance, false)
	if err := s.checkPipelineDAGRead(readCtx, actor, data.run); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) readPipelineDAG(ctx context.Context, actor, groupID, id uint64, nodeID string) (*pipelineDAGRead, error) {
	return s.readPipelineDAGArtifacts(ctx, actor, groupID, id, nodeID, false)
}

func (s *Service) readPipelineDAGArtifacts(ctx context.Context, actor, groupID, id uint64, nodeID string, allArtifacts bool) (*pipelineDAGRead, error) {
	n, err := s.PipelineRun(ctx, actor, groupID, id)
	if err != nil {
		return nil, err
	}
	var spec dto.PipelineRunSpec
	if json.Unmarshal(n.RequestedSpec, &spec) != nil {
		return nil, apperr.New(apperr.CodeConflict, "运行配置快照无效")
	}
	graph, err := buildPipelineGraph(spec)
	if err != nil {
		return nil, apperr.New(apperr.CodeConflict, "运行快照不含可解析的 KFP v2 DAG，或图规模/层级超限")
	}
	out := &pipelineDAGRead{run: n, spec: spec, graph: graph, executions: map[string]*mlmd.Execution{}, tasks: map[string]*kfp.TaskDetail{}}
	out.dag = &PipelineDAG{InstanceID: n.InstanceID, Revision: n.Revision, ExternalID: n.ExternalID, PackageDigest: spec.PackageDigest, State: n.ObservedState, ReadAt: time.Now().UTC(), Runtime: PipelineDataSource{Status: "NOT_SUBMITTED"}, Metadata: PipelineDataSource{Status: "NOT_SUBMITTED"}, Definitions: graph.Definitions, Edges: graph.Edges, Instances: []PipelineNodeInstance{}}
	if n.Engine == model.PipelineEngineNativeArgo {
		return s.readNativePipelineDAG(ctx, out)
	}
	if n.ExecutionCleaned {
		out.dag.Runtime.Status = "CLEANED"
		out.dag.Metadata.Status = "CLEANED"
		return out, nil
	}
	if n.ExternalID == nil {
		return out, nil
	}
	kfpCtx, kfpCancel := context.WithTimeout(ctx, 8*time.Second)
	verified, run, details, err := s.readPipelineObservation(kfpCtx, actor, groupID, id)
	kfpCancel()
	if err != nil {
		return nil, err
	}
	if verified.InstanceID != n.InstanceID || verified.Revision != n.Revision {
		return nil, apperr.New(apperr.CodeConflict, "运行已变化，请刷新")
	}
	now := time.Now().UTC()
	out.dag.State = run.State
	out.dag.Error = run.Error
	out.dag.History = run.History
	out.dag.Runtime = PipelineDataSource{Status: "AVAILABLE", ObservedAt: &now}
	out.dag.Metadata.Status = "NOT_CONFIGURED"
	if s.PipelineMetadata != nil {
		// Leave time for the final authorization check even if MLMD stalls.
		end := time.Now().Add(10 * time.Second)
		if deadline, ok := ctx.Deadline(); ok && deadline.Add(-time.Second).Before(end) {
			end = deadline.Add(-time.Second)
		}
		metadataCtx, cancel := context.WithDeadline(ctx, end)
		executionID := ""
		if allArtifacts {
			executionID = "*" // Internal request: all events of this verified run context.
		}
		if strings.HasPrefix(nodeID, "execution-") {
			executionID = strings.TrimPrefix(nodeID, "execution-")
		}
		out.metadata, err = s.PipelineMetadata.ReadRun(metadataCtx, run.ID, n.Namespace, executionID)
		cancel()
		out.dag.Metadata.Status = "NOT_READY_OR_UNAVAILABLE"
		if err == nil && out.metadata != nil {
			if details.ContextID != "" && details.ContextID != "0" && details.ContextID.String() != out.metadata.ContextID {
				return nil, apperr.New(apperr.CodeConflict, "KFP 与 MLMD 的运行上下文不匹配")
			}
			at := time.Now().UTC()
			out.dag.Metadata = PipelineDataSource{Status: "AVAILABLE", ObservedAt: &at}
		} else {
			out.metadata = nil
		}
	}
	if err := out.mergeInstances(details); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) checkPipelineDAGRead(ctx context.Context, actor uint64, original *model.PipelineRun) error {
	fresh, err := s.PipelineRun(ctx, actor, original.GroupID, original.ID)
	if err != nil {
		return err
	}
	if fresh.InstanceID != original.InstanceID || fresh.Revision != original.Revision || fresh.ExecutionCleaned != original.ExecutionCleaned || !samePipelineExternalID(fresh.ExternalID, original.ExternalID) {
		return apperr.New(apperr.CodeConflict, "运行已变化，请刷新")
	}
	return nil
}
func samePipelineExternalID(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (r *pipelineDAGRead) mergeInstances(details *kfp.RunDetails) error {
	tasksByExecution := map[string]*kfp.TaskDetail{}
	taskNodes := map[string]string{}
	for i := range details.Tasks {
		t := &details.Tasks[i]
		eid := t.ExecutionID.String()
		nodeID := "task-" + mlHash([]byte(t.ID))[:32]
		if eid != "" && eid != "0" {
			if tasksByExecution[eid] != nil {
				return apperr.New(apperr.CodeConflict, "KFP executionId 重复，无法唯一定位节点")
			}
			tasksByExecution[eid] = t
			nodeID = "execution-" + eid
		}
		taskNodes[t.ID] = nodeID
		r.tasks[nodeID] = t
	}
	if r.metadata != nil {
		for i := range r.metadata.Executions {
			e := &r.metadata.Executions[i]
			r.executions[e.ID] = e
		}
	}
	resolved := map[string]string{}
	visiting := map[string]bool{}
	var resolve func(string, int) string
	resolve = func(eid string, depth int) string {
		if id, ok := resolved[eid]; ok {
			return id
		}
		if visiting[eid] || depth > 64 {
			return ""
		}
		visiting[eid] = true
		defer delete(visiting, eid)
		e := r.executions[eid]
		if e == nil {
			return ""
		}
		name := metadataText(e, "task_name")
		parent := metadataText(e, "parent_dag_id")
		if parent == "0" {
			parent = ""
		}
		definition := ""
		if parent == "" && name == "" {
			definition = "root"
		} else if parent != "" {
			parentDefinition := resolve(parent, depth+1)
			// A loop's iteration DAG has the same task_name as its loop wrapper.
			pd := r.graph.byID[parentDefinition]
			_, iteration := e.Custom["iteration_index"]
			if pd != nil && iteration && name == pd.TaskName && (len(pd.ParameterIterator) > 0 || len(pd.ArtifactIterator) > 0) {
				definition = parentDefinition
			} else {
				definition = r.graph.children[parentDefinition][name]
			}
		}
		resolved[eid] = definition
		return definition
	}
	for _, eid := range sortedKeys(r.executions) {
		e := r.executions[eid]
		parent := metadataText(e, "parent_dag_id")
		n := PipelineNodeInstance{ID: "execution-" + eid, DefinitionID: resolve(eid, 0), Mapping: "UNRESOLVED", ExecutionID: eid, ParentExecutionID: parent, Name: metadataText(e, "task_name"), State: metadataExecutionState(e.State), MetadataState: e.State, PodName: metadataText(e, "pod_name"), CreatedAtEpochMs: e.CreatedAt, UpdatedAtEpochMs: e.UpdatedAt, IterationIndex: e.Custom["iteration_index"], IterationCount: e.Custom["iteration_count"], CachedExecutionID: metadataText(e, "cached_execution_id"), History: []kfp.RuntimeStatus{}, Children: []kfp.ChildTask{}}
		if n.DefinitionID != "" {
			n.Mapping = "EXACT"
		}
		if parent != "" && parent != "0" && r.executions[parent] != nil {
			n.ParentID = "execution-" + parent
		}
		if t := tasksByExecution[eid]; t != nil {
			overlayKFPTask(&n, t)
			if n.ParentID == "" {
				n.ParentID = taskNodes[t.ParentID]
			}
		}
		r.dag.Instances = append(r.dag.Instances, n)
	}
	for _, t := range details.Tasks {
		if r.executions[t.ExecutionID.String()] != nil {
			continue
		}
		n := PipelineNodeInstance{ID: taskNodes[t.ID], Mapping: "UNRESOLVED", ParentID: taskNodes[t.ParentID], History: []kfp.RuntimeStatus{}, Children: []kfp.ChildTask{}}
		overlayKFPTask(&n, &t)
		r.dag.Instances = append(r.dag.Instances, n)
	}
	return nil
}

func overlayKFPTask(n *PipelineNodeInstance, t *kfp.TaskDetail) {
	n.TaskID = t.ID
	n.ExecutionID = t.ExecutionID.String()
	n.ParentTaskID = t.ParentID
	if t.Name != "" {
		n.Name = t.Name
	}
	if t.State != "" {
		n.State = t.State
	}
	if t.PodName != "" {
		n.PodName = t.PodName
	}
	n.CreatedAt = t.CreateTime
	n.StartTime = t.StartTime
	n.EndTime = t.EndTime
	n.Error = t.Error
	n.Executor = t.Executor
	if t.History != nil {
		n.History = t.History
	}
	if t.Children != nil {
		n.Children = t.Children
	}
	// Many KFP releases populate errors only in state_history. Do not mistake a
	// transient retried error for the final error of a successful task.
	if n.Error == nil && (n.State == "FAILED" || n.State == "CANCELED") {
		for i := len(n.History) - 1; i >= 0; i-- {
			if n.History[i].State == n.State && n.History[i].Error != nil {
				n.Error = n.History[i].Error
				break
			}
		}
	}
}
func metadataExecutionState(state string) string {
	switch state {
	case "NEW":
		return "PENDING"
	case "COMPLETE":
		return "SUCCEEDED"
	case "CANCELED":
		return "SKIPPED"
	default:
		return state
	}
}
func metadataText(e *mlmd.Execution, key string) string {
	v := e.Custom[key]
	if v == nil {
		v = e.Properties[key]
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func buildNodeIO(r *pipelineDAGRead, d *PipelineNodeDefinition, n *PipelineNodeInstance, input bool) PipelineNodeIO {
	out := PipelineNodeIO{ParametersStatus: "UNRESOLVED", Parameters: map[string]PipelineParameter{}, ArtifactsStatus: "UNRESOLVED", Artifacts: map[string][]PipelineArtifactRef{}}
	if d != nil {
		defs := d.Outputs
		if input {
			defs = d.Inputs
		}
		for name := range defs["parameters"] {
			out.Parameters[name] = PipelineParameter{Source: "UNRESOLVED"}
		}
		for name := range defs["artifacts"] {
			out.Artifacts[name] = []PipelineArtifactRef{}
		}
		// Submitted root values are authoritative even before the root execution exists.
		if input && d.ID == "root" {
			if values, err := pipelineRuntimeParameters(r.spec); err == nil {
				for name, value := range values {
					out.Parameters[name] = PipelineParameter{true, value, "SUBMITTED"}
				}
				out.ParametersStatus = "SUBMITTED"
			}
		}
	}
	if n == nil {
		return out
	}
	if e := r.executions[n.ExecutionID]; e != nil {
		key := "outputs"
		if input {
			key = "inputs"
		}
		if values, ok := e.Custom[key].(map[string]any); ok {
			for name, value := range values {
				source := "MLMD"
				if r.run.Engine == model.PipelineEngineNativeArgo {
					source = "NATIVE_ARGO"
				}
				out.Parameters[name] = PipelineParameter{true, value, source}
			}
			out.ParametersStatus = "AVAILABLE"
		}
		out.ArtifactsStatus = "AVAILABLE"
		for _, event := range r.metadata.Events {
			if event.ExecutionID != e.ID || !nodeIOEvent(event.Type, input) {
				continue
			}
			name := ""
			for _, part := range event.Path {
				if key, ok := part.(string); ok {
					name = key
					break
				}
			}
			if name == "" {
				name = "_unnamed"
			}
			ref := PipelineArtifactRef{ID: event.ArtifactID, Status: "NOT_FOUND", EventType: event.Type, Path: event.Path}
			if artifact, ok := r.metadata.Artifacts[event.ArtifactID]; ok {
				ref.Status = "AVAILABLE"
				a := artifact
				ref.Metadata = &a
			}
			out.Artifacts[name] = append(out.Artifacts[name], ref)
		}
	}
	// Keep KFP's declared references even when MLMD is lagging/unavailable.
	if t := r.tasks[n.ID]; t != nil {
		refs := t.Outputs
		if input {
			refs = t.Inputs
		}
		if len(refs) > 0 && out.ArtifactsStatus != "AVAILABLE" {
			out.ArtifactsStatus = "REFERENCES_ONLY"
		}
		for name, list := range refs {
			for _, aid := range list.IDs {
				found := false
				for _, a := range out.Artifacts[name] {
					if a.ID == aid.String() {
						found = true
						break
					}
				}
				if found {
					continue
				}
				// KFP artifact IDs alone are not enough to authorize a global MLMD lookup.
				out.Artifacts[name] = append(out.Artifacts[name], PipelineArtifactRef{ID: aid.String(), Status: "NOT_FOUND"})
			}
		}
	}
	return out
}
func nodeIOEvent(event int, input bool) bool {
	if input {
		return event == 2 || event == 3
	}
	return event == 1 || event == 4 || event == 7
}
