package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"gorm.io/gorm"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func nativeTimeString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

type nativePersistedIO struct {
	Parameters map[string]any `json:"parameters"`
	Artifacts  map[string]struct {
		ID uint64 `json:"id"`
	} `json:"artifacts"`
}

func readPersistedIO(raw json.RawMessage) (nativePersistedIO, error) {
	var result nativePersistedIO
	if len(raw) == 0 || len(raw) > 128<<10 {
		return result, nativeFinalConflict()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return result, nativeFinalConflict()
	}
	return result, nil
}

// Read SQL evidence without reconstructing a legacy execution plan or claiming
// that an upstream system-driver node is a user component. Definition mappings
// stay UNRESOLVED until the official runtime projection supplies exact identity.
func (s *Service) readNativePipelineDAG(ctx context.Context, out *pipelineDAGRead) (*pipelineDAGRead, error) {
	n := out.run
	attempt, err := s.Store.PipelineRunAttempt(ctx, n.GroupID, n.ID, n.InstanceID, false)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil && attempt.EngineRevision != nil && *attempt.EngineRevision == kfpengine.EngineRelease {
		return s.readOfficialPipelineDAG(ctx, out, attempt)
	}
	rows := []model.PipelineTaskExecution{}
	if err := s.DB.WithContext(ctx).Where("group_id = ? AND run_id = ? AND instance_id = ?", n.GroupID, n.ID, n.InstanceID).
		Order("id").Limit(k8s.MaxNativeWorkflowNodes + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > k8s.MaxNativeWorkflowNodes {
		return nil, nativeFinalConflict()
	}
	out.metadata = &mlmd.Snapshot{ContextID: n.InstanceID, Executions: []mlmd.Execution{}, Events: []mlmd.Event{}, Artifacts: map[string]mlmd.Artifact{}}
	artifactIDs := map[uint64]bool{}
	for _, row := range rows {
		input, err := readPersistedIO(row.Inputs)
		if err != nil {
			return nil, err
		}
		output, err := readPersistedIO(row.Outputs)
		if err != nil {
			return nil, err
		}
		id := strconv.FormatUint(row.ID, 10)
		execution := mlmd.Execution{ID: id, Name: row.DefinitionPath, State: row.Phase, Custom: map[string]any{"inputs": input.Parameters, "outputs": output.Parameters}}
		out.metadata.Executions = append(out.metadata.Executions, execution)
		copy := execution
		out.executions[id] = &copy
		instance := PipelineNodeInstance{ID: "execution-" + id, Mapping: "UNRESOLVED", TaskID: "execution-" + id, ExecutionID: id,
			Name: row.DefinitionPath, State: row.Phase, StartTime: nativeTimeString(row.StartedAt), EndTime: nativeTimeString(row.FinishedAt), History: []kfp.RuntimeStatus{}, Children: []kfp.ChildTask{}}
		if row.ParentID != nil {
			instance.ParentID = "execution-" + strconv.FormatUint(*row.ParentID, 10)
		}
		if row.PodName != nil {
			instance.PodName = *row.PodName
		}
		if row.CacheSourceTaskID != nil {
			instance.CachedExecutionID = strconv.FormatUint(*row.CacheSourceTaskID, 10)
		}
		out.dag.Instances = append(out.dag.Instances, instance)
		for _, direction := range []struct {
			io        nativePersistedIO
			eventType int
		}{{input, 3}, {output, 4}} {
			for port, artifact := range direction.io.Artifacts {
				if artifact.ID == 0 {
					return nil, nativeFinalConflict()
				}
				artifactIDs[artifact.ID] = true
				out.metadata.Events = append(out.metadata.Events, mlmd.Event{ExecutionID: id, ArtifactID: strconv.FormatUint(artifact.ID, 10), Type: direction.eventType, Path: []any{port}})
			}
		}
	}
	ids := make([]uint64, 0, len(artifactIDs))
	for id := range artifactIDs {
		ids = append(ids, id)
	}
	if len(ids) > k8s.MaxNativeWorkflowNodes*32 {
		return nil, nativeFinalConflict()
	}
	if len(ids) != 0 {
		artifacts := []model.PipelineArtifact{}
		if err := s.DB.WithContext(ctx).Where("group_id = ? AND id IN ? AND state = ?", n.GroupID, ids, "PUBLISHED").Find(&artifacts).Error; err != nil {
			return nil, err
		}
		for _, artifact := range artifacts {
			metadata := map[string]any{}
			decoder := json.NewDecoder(bytes.NewReader(artifact.Metadata))
			decoder.UseNumber()
			if decoder.Decode(&metadata) != nil {
				return nil, nativeFinalConflict()
			}
			id := strconv.FormatUint(artifact.ID, 10)
			uri := ""
			if artifact.URI != nil {
				uri = *artifact.URI
			}
			out.metadata.Artifacts[id] = mlmd.Artifact{ID: id, Type: artifact.TypeName, TypeVersion: artifact.TypeSchemaVersion, StorageURI: uri, State: artifact.State, Properties: map[string]any{}, Custom: metadata}
		}
	}
	status := "PERSISTED_ONLY"
	if n.ExecutionCleaned {
		status = "CLEANED"
	}
	out.dag.Runtime, out.dag.Metadata = PipelineDataSource{Status: status}, PipelineDataSource{Status: status}
	return out, nil
}

func (s *Service) nativePipelineRuntime(ctx context.Context, actor uint64, run *model.PipelineRun) (*PipelineRuntime, error) {
	data, err := s.readPipelineDAG(ctx, actor, run.GroupID, run.ID, "")
	if err != nil {
		return nil, err
	}
	result := &PipelineRuntime{InstanceID: run.InstanceID, Revision: run.Revision, State: data.dag.State, Tasks: []PipelineRuntimeTask{}}
	attempt, readErr := s.Store.PipelineRunAttempt(ctx, run.GroupID, run.ID, run.InstanceID, false)
	if readErr != nil && !errors.Is(readErr, gorm.ErrRecordNotFound) {
		return nil, readErr
	}
	if readErr == nil {
		result.Diagnostics = &PipelineRuntimeDiagnostics{EngineRelease: attempt.EngineRevision, WorkflowName: attempt.WorkflowName, WorkflowUID: attempt.WorkflowUID, PlanDigest: attempt.PlanHash, Phase: attempt.Phase, Reason: attempt.Reason, SyncReason: attempt.SyncReason, ObservedAt: attempt.ObservedAt, SyncAttemptedAt: attempt.SyncAttemptedAt, ArchivedAt: attempt.WorkflowArchivedAt}
		result.CreatedAt = attempt.CreatedAt.UTC().Format(time.RFC3339Nano)
		result.FinishedAt = nativeTimeString(attempt.FinishedAt)
	}
	if run.ExternalID != nil {
		result.ExternalID = *run.ExternalID
	}
	for _, node := range data.dag.Instances {
		result.Tasks = append(result.Tasks, PipelineRuntimeTask{ID: node.TaskID, Name: node.Name, PodName: node.PodName, ExecutionID: node.ExecutionID, State: node.State, ParentID: node.ParentID, StartTime: node.StartTime, EndTime: node.EndTime})
	}
	if err := s.checkPipelineDAGRead(ctx, actor, run); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) nativePipelineTaskPodRef(ctx context.Context, run *model.PipelineRun, taskID, container string) (k8s.PipelinePodRef, error) {
	var empty k8s.PipelinePodRef
	id, err := strconv.ParseUint(strings.TrimPrefix(taskID, "execution-"), 10, 64)
	if err != nil || id == 0 || taskID != "execution-"+strconv.FormatUint(id, 10) {
		return empty, apperr.New(apperr.CodeInvalid, "请使用当前 runtime 返回的 taskId；旧重试定位需对应版本读取")
	}
	attempt, err := s.Store.PipelineRunAttempt(ctx, run.GroupID, run.ID, run.InstanceID, false)
	if err != nil {
		return empty, err
	}
	if attempt.WorkflowUID == nil || attempt.WorkflowName == nil || attempt.NamespaceUID == nil || attempt.PlanHash == nil || attempt.Namespace == nil || attempt.ClusterID == nil ||
		*attempt.ClusterID != s.Cluster || *attempt.Namespace != run.Namespace || run.ExternalID == nil || *run.ExternalID != *attempt.WorkflowUID {
		return empty, nativeFinalConflict()
	}
	if attempt.EngineRevision != nil && *attempt.EngineRevision == kfpengine.EngineRelease {
		if id > uint64(^uint64(0)>>1) {
			return empty, nativeFinalConflict()
		}
		return s.officialTaskPodRef(ctx, run, attempt, int64(id), container)
	}
	row := &model.PipelineTaskExecution{}
	if err := s.DB.WithContext(ctx).Where("id = ? AND group_id = ? AND run_id = ? AND instance_id = ?", id, run.GroupID, run.ID, run.InstanceID).First(row).Error; err != nil {
		return empty, domainError(err)
	}
	var state struct {
		NodeName string `json:"nodeName"`
	}
	if json.Unmarshal(row.RuntimeState, &state) != nil || state.NodeName == "" || row.PodName == nil || row.PodUID == nil || row.ArgoNodeID == nil {
		return empty, apperr.New(apperr.CodeConflict, "节点没有完整的已持久化 Pod 身份，拒绝推测")
	}
	ref := &k8s.PipelineWorkflowRef{GroupID: run.GroupID, RunID: run.ID, InstanceID: run.InstanceID, Namespace: run.Namespace, NamespaceUID: *attempt.NamespaceUID, Name: *attempt.WorkflowName, UID: *attempt.WorkflowUID, PlanHash: *attempt.PlanHash}
	return k8s.PipelinePodRef{Namespace: run.Namespace, RunID: run.InstanceID, Pod: *row.PodName, Container: container, NativeWorkflow: ref, NativePodUID: *row.PodUID, NativeNodeID: *row.ArgoNodeID, NativeNodeName: state.NodeName}, nil
}
