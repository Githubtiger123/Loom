package service

import (
	"context"
	"encoding/json"
	"github.com/githubtiger123/loom/engine/kfp/metadata"

	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/model"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

type officialRetryGrant struct {
	previousHash   string
	previousPodUID string
}

func (s *Service) officialRetryGrant(ctx context.Context, scope *runtimeapi.Scope, op runtimeapi.Operation, request runtimeapi.MetadataRequest) (*officialRetryGrant, error) {
	if op != runtimeapi.PrePublishExecution || scope.Role != "LAUNCHER" || scope.ExecutionID != request.ID {
		return nil, nil
	}
	row, err := s.Store.RuntimeObject(ctx, *scope, scope.ExecutionID, "execution")
	if err != nil {
		return nil, err
	}
	var record runtimeExecution
	execution := &pb.Execution{}
	if json.Unmarshal(row.Value, &record) != nil || protojson.Unmarshal(record.Execution, execution) != nil {
		return nil, nativeFinalConflict()
	}
	if record.AssignedPodUID == "" || record.AssignedPodUID == scope.PodUID {
		return nil, nil
	}
	if execution.GetLastKnownState() == pb.Execution_CACHED {
		return nil, runtimeDenied()
	}
	if err := s.NativePipelineClient.VerifyOfficialPipelineRetry(ctx, *scope, execution.GetCustomProperties()["pod_name"].GetStringValue(), record.AssignedPodUID, record.AssignedNodeID); err != nil {
		return nil, runtimeDenied()
	}
	return &officialRetryGrant{previousHash: mlHash(row.Value), previousPodUID: record.AssignedPodUID}, nil
}

func (tx *runtimeTxn) handoffRetry(row *model.PipelineRuntimeObject, record *runtimeExecution, execution *pb.Execution) error {
	if tx.retry == nil || tx.retry.previousHash != mlHash(row.Value) || tx.retry.previousPodUID != record.AssignedPodUID || record.Epoch >= 32 {
		return runtimeDenied()
	}
	history := tx.newObject("execution-history", row.ObjectKey+"/"+record.AssignedPodUID, row.ID)
	if err := tx.store.SaveRuntimeObject(tx.ctx, history, record); err != nil {
		return err
	}
	record.Epoch++
	record.Published, record.PublicationHash, record.Outputs = false, "", nil
	delete(execution.CustomProperties, "outputs")
	// Upstream launcher propagates failure before Argo starts its next retry.
	// Reset only ancestor DAG failures after verified physical Pod handoff.
	parent := row.ParentID
	for depth := 0; parent > 0 && depth < 32; depth++ {
		ancestor, old, state, err := tx.execution(parent, false)
		if err != nil {
			return err
		}
		if state.GetLastKnownState() == pb.Execution_FAILED {
			state.LastKnownState = pb.Execution_RUNNING.Enum()
			if err := tx.saveExecution(ancestor, old, state); err != nil {
				return err
			}
		}
		parent = ancestor.ParentID
	}
	return nil
}

// Argo can retry after the main command published successfully but a system
// container failed. Advance only physical provenance and replay COMPLETE: never
// repeat the user's command or relabel the previous Pod's archived logs.
func (tx *runtimeTxn) handoffCompletedRetry(row *model.PipelineRuntimeObject, record *runtimeExecution, execution *pb.Execution) error {
	if tx.retry == nil || tx.retry.previousHash != mlHash(row.Value) || tx.retry.previousPodUID != record.AssignedPodUID || record.Epoch >= 32 || !record.Published {
		return runtimeDenied()
	}
	history := tx.newObject("execution-history", row.ObjectKey+"/"+record.AssignedPodUID, row.ID)
	if err := tx.store.SaveRuntimeObject(tx.ctx, history, record); err != nil {
		return err
	}
	record.Epoch++
	record.Replayed = true
	record.AssignedPodUID, record.AssignedNodeID, record.AssignedNodeName = tx.scope.PodUID, tx.scope.NodeID, tx.scope.NodeName
	execution.CustomProperties["pod_uid"] = metadata.StringValue(tx.scope.PodUID)
	execution.CustomProperties["pod_name"] = metadata.StringValue(tx.scope.PodName)
	return tx.saveExecution(row, record, execution)
}
