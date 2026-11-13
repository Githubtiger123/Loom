package service

import (
	"context"
	"encoding/json"
	"strconv"

	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

// The official adapter projects the same committed protobuf records used by the
// driver. It does not reconstruct outputs from Pod phase or the old v3 tables.
// Keep the existing DAG/visualization DTOs and exact parent/task mapping logic.
func (s *Service) readOfficialPipelineDAG(ctx context.Context, out *pipelineDAGRead, attempt *model.PipelineRunAttempt) (*pipelineDAGRead, error) {
	n := out.run
	if attempt.EngineRevision == nil || *attempt.EngineRevision != kfpengine.EngineRelease || attempt.InstanceID != n.InstanceID || attempt.GroupID != n.GroupID || attempt.RunID != n.ID {
		return nil, nativeFinalConflict()
	}
	scope := runtimeapi.Scope{GroupID: n.GroupID, RunID: n.ID, InstanceID: n.InstanceID}
	rows, err := s.Store.RuntimeObjects(ctx, scope, "execution")
	if err != nil {
		return nil, err
	}
	out.metadata = &mlmd.Snapshot{ContextID: n.InstanceID, Executions: []mlmd.Execution{}, Events: []mlmd.Event{}, Artifacts: map[string]mlmd.Artifact{}}
	records := map[string]runtimeExecution{}
	artifacts := map[int64]runtimeapi.ArtifactRecord{}
	for _, row := range rows {
		var record runtimeExecution
		execution := &pb.Execution{}
		if json.Unmarshal(row.Value, &record) != nil || record.Config == nil || protojson.Unmarshal(record.Execution, execution) != nil || execution.GetId() != row.ID {
			return nil, nativeFinalConflict()
		}
		id := strconv.FormatInt(row.ID, 10)
		state := execution.GetLastKnownState().String()
		if record.Config.ExecutionType == metadata.ContainerExecutionTypeName && record.AssignedPodUID == "" && !record.Config.NotTriggered && execution.GetLastKnownState() != pb.Execution_CACHED {
			state = "NEW" // Driver metadata creation is not evidence of Pod start.
		}
		out.metadata.Executions = append(out.metadata.Executions, mlmd.Execution{ID: id, Name: execution.GetName(), State: state,
			CreatedAt: strconv.FormatInt(execution.GetCreateTimeSinceEpoch(), 10), UpdatedAt: strconv.FormatInt(execution.GetLastUpdateTimeSinceEpoch(), 10),
			Properties: officialMetadataProperties(execution.GetProperties()), Custom: officialMetadataProperties(execution.GetCustomProperties())})
		records[id] = record
		for port, ids := range record.Config.InputArtifactIDs {
			for index, artifactID := range ids {
				out.metadata.Events = append(out.metadata.Events, mlmd.Event{ExecutionID: id, ArtifactID: strconv.FormatInt(artifactID, 10), Type: 3, Path: []any{port, index}})
				if _, exists := artifacts[artifactID]; !exists {
					artifactRow, e := s.Store.RuntimeObject(ctx, scope, artifactID, "artifact")
					if e != nil {
						return nil, e
					}
					var artifact runtimeapi.ArtifactRecord
					if json.Unmarshal(artifactRow.Value, &artifact) != nil {
						return nil, nativeFinalConflict()
					}
					artifacts[artifactID] = artifact
				}
			}
		}
		if record.Published {
			for _, artifact := range record.Outputs {
				parsed := &pb.Artifact{}
				if protojson.Unmarshal(artifact.Artifact, parsed) != nil || parsed.GetId() <= 0 {
					return nil, nativeFinalConflict()
				}
				artifacts[parsed.GetId()] = artifact
				out.metadata.Events = append(out.metadata.Events, mlmd.Event{ExecutionID: id, ArtifactID: strconv.FormatInt(parsed.GetId(), 10), Type: 4, Path: []any{artifact.Name, 0}})
			}
		}
	}
	for artifactID, record := range artifacts {
		parsed, err := officialArtifactProjection(record)
		if err != nil || parsed.ID != strconv.FormatInt(artifactID, 10) {
			return nil, nativeFinalConflict()
		}
		out.metadata.Artifacts[parsed.ID] = parsed
	}
	if err := out.mergeInstances(&kfp.RunDetails{}); err != nil {
		return nil, err
	}
	history, err := s.Store.RuntimeObjects(ctx, scope, "execution-history")
	if err != nil {
		return nil, err
	}
	retries := map[string][]PipelineNodeRetry{}
	for _, row := range history {
		var record runtimeExecution
		execution := &pb.Execution{}
		if json.Unmarshal(row.Value, &record) != nil || protojson.Unmarshal(record.Execution, execution) != nil {
			return nil, nativeFinalConflict()
		}
		id := strconv.FormatInt(row.ParentID, 10)
		retries[id] = append(retries[id], PipelineNodeRetry{Epoch: record.Epoch, PodName: execution.GetCustomProperties()["pod_name"].GetStringValue(), PodUID: record.AssignedPodUID, State: execution.GetLastKnownState().String(), PublicationReplayed: record.Replayed})
	}
	for i := range out.dag.Instances {
		node := &out.dag.Instances[i]
		node.TaskID = node.ID
		node.Retries = retries[node.ExecutionID]
		if record, ok := records[node.ExecutionID]; ok && record.Config != nil && record.Config.DisplayName != "" {
			node.Name = record.Config.DisplayName
		}
	}
	status := "PERSISTED_ONLY"
	if n.ExecutionCleaned {
		status = "CLEANED"
	}
	out.dag.Runtime = PipelineDataSource{Status: status, ObservedAt: attempt.ObservedAt}
	out.dag.Metadata = PipelineDataSource{Status: "AVAILABLE", ObservedAt: attempt.ObservedAt}
	return out, nil
}

func officialMetadataProperties(values map[string]*pb.Value) map[string]any {
	result := map[string]any{}
	for key, value := range values {
		if value == nil {
			continue
		}
		switch v := value.Value.(type) {
		case *pb.Value_StringValue:
			result[key] = v.StringValue
		case *pb.Value_IntValue:
			result[key] = strconv.FormatInt(v.IntValue, 10)
		case *pb.Value_DoubleValue:
			result[key] = v.DoubleValue
		case *pb.Value_StructValue:
			result[key] = v.StructValue.AsMap()
		}
	}
	return result
}

func officialArtifactProjection(record runtimeapi.ArtifactRecord) (mlmd.Artifact, error) {
	parsed := &pb.Artifact{}
	if protojson.Unmarshal(record.Artifact, parsed) != nil || parsed.GetId() <= 0 {
		return mlmd.Artifact{}, nativeFinalConflict()
	}
	artifact, err := (&metadata.OutputArtifact{Name: record.Name, Schema: record.Schema, Artifact: parsed}).ToRuntimeArtifact()
	if err != nil {
		return mlmd.Artifact{}, err
	}
	return mlmd.Artifact{ID: strconv.FormatInt(parsed.GetId(), 10), Name: record.Name, TypeID: strconv.FormatInt(parsed.GetTypeId(), 10), Type: parsed.GetType(),
		State: parsed.GetState().String(), StorageURI: parsed.GetUri(), Properties: officialMetadataProperties(parsed.GetProperties()), Custom: artifact.GetMetadata().AsMap()}, nil
}

func (s *Service) officialTaskPodRef(ctx context.Context, run *model.PipelineRun, attempt *model.PipelineRunAttempt, id int64, container string) (k8s.PipelinePodRef, error) {
	empty := k8s.PipelinePodRef{}
	row, err := s.Store.RuntimeObject(ctx, runtimeapi.Scope{GroupID: run.GroupID, RunID: run.ID, InstanceID: run.InstanceID}, id, "execution")
	if err != nil {
		return empty, err
	}
	var record runtimeExecution
	execution := &pb.Execution{}
	if json.Unmarshal(row.Value, &record) != nil || protojson.Unmarshal(record.Execution, execution) != nil || record.AssignedPodUID == "" || record.AssignedNodeID == "" || record.AssignedNodeName == "" {
		return empty, nativeFinalConflict()
	}
	ref := &k8s.PipelineWorkflowRef{GroupID: run.GroupID, RunID: run.ID, InstanceID: run.InstanceID, Namespace: run.Namespace, NamespaceUID: *attempt.NamespaceUID, Name: *attempt.WorkflowName, UID: *attempt.WorkflowUID, PlanHash: *attempt.PlanHash}
	return k8s.PipelinePodRef{Namespace: run.Namespace, RunID: run.InstanceID, Pod: execution.GetCustomProperties()["pod_name"].GetStringValue(), Container: container, NativeWorkflow: ref,
		NativePodUID: record.AssignedPodUID, NativeNodeID: record.AssignedNodeID, NativeNodeName: record.AssignedNodeName}, nil
}
