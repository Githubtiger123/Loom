package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/kubeflow/pipelines/api/v2alpha1/go/pipelinespec"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"io"
	"strconv"
	"strings"
	"time"
)

type runtimeExecution struct {
	Epoch            uint64                      `json:"epoch"`
	Replayed         bool                        `json:"replayed,omitempty"`
	Execution        json.RawMessage             `json:"execution"`
	Config           *metadata.ExecutionConfig   `json:"config"`
	DriverNodeID     string                      `json:"driverNodeId"`
	DriverNodeName   string                      `json:"driverNodeName"`
	AssignedPodUID   string                      `json:"assignedPodUid"`
	AssignedNodeID   string                      `json:"assignedNodeId"`
	AssignedNodeName string                      `json:"assignedNodeName"`
	Published        bool                        `json:"published"`
	PublicationHash  string                      `json:"publicationHash"`
	Outputs          []runtimeapi.ArtifactRecord `json:"outputs"`
}
type runtimeTxn struct {
	ctx    context.Context
	store  *repository.Store
	scope  runtimeapi.Scope
	policy runtimeapi.FrozenPolicy
	retry  *officialRetryGrant
	cache  *officialCacheGrant
}

func runtimeDenied() error {
	return apperr.New(apperr.CodeForbidden, "Runtime 请求不属于当前任务或执行阶段")
}
func runtimeInvalid() error {
	return apperr.New(apperr.CodeInvalid, "Runtime 请求协议或字段无效")
}

// PipelineRuntime performs remote authentication before acquiring SQL locks.
// Every operation then rechecks the immutable attempt/current run in one GORM
// transaction; no Kubernetes or object-store call is made under that lock.
func (s *Service) PipelineRuntime(ctx context.Context, token string, op runtimeapi.Operation, envelope runtimeapi.Envelope) (*runtimeapi.Reply, error) {
	if !op.Valid() || envelope.Protocol != runtimeapi.Protocol || len(envelope.Payload) == 0 || len(envelope.Payload) > runtimeapi.MaxMessageBytes/2 {
		return nil, runtimeInvalid()
	}
	expected := mlHash(append([]byte(runtimeapi.Protocol+"\x00"+string(op)+"\x00"), envelope.Payload...))
	if envelope.RequestID != expected {
		return nil, runtimeInvalid()
	}
	scope, err := s.AuthorizePipelineRuntimeWorkload(ctx, token)
	if err != nil {
		return nil, err
	}
	var request runtimeapi.MetadataRequest
	decoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, runtimeInvalid()
	}
	if len(request.IDs) > 256 || len(request.Artifacts) > 256 || len(request.Parameters) > 256 {
		return nil, runtimeInvalid()
	}
	retry, err := s.officialRetryGrant(ctx, scope, op, request)
	if err != nil {
		return nil, err
	}
	var cache *officialCacheGrant
	if op == runtimeapi.LookupCache {
		cache, err = s.prepareOfficialCache(ctx, scope, request)
		if err != nil {
			return nil, err
		}
	}
	var result json.RawMessage
	err = repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		// Lock order is group, run, then attempt, matching lifecycle operations.
		// These locks serialize first-create and idempotency receipts as well.
		group, e := st.Group(ctx, scope.GroupID, true)
		if e != nil {
			return e
		}
		run, e := st.PipelineRun(ctx, scope.GroupID, scope.RunID, true)
		if e != nil {
			return e
		}
		attempt, e := st.PipelineRunAttempt(ctx, scope.GroupID, scope.RunID, scope.InstanceID, true)
		if e != nil {
			return e
		}
		if run.InstanceID != scope.InstanceID || run.DeletedAt != nil || run.ExecutionCleaned || run.ExternalID == nil || *run.ExternalID != scope.WorkflowUID || attempt.WorkflowUID == nil || *attempt.WorkflowUID != scope.WorkflowUID || attempt.EngineRevision == nil || *attempt.EngineRevision != scope.EngineRevision {
			return runtimeDenied()
		}
		var snapshot struct {
			RuntimePolicy runtimeapi.FrozenPolicy `json:"runtimePolicy"`
		}
		if json.Unmarshal(attempt.Snapshot, &snapshot) != nil || snapshot.RuntimePolicy.ServiceAccountUID != scope.ServiceAccountUID || snapshot.RuntimePolicy.Protocol != runtimeapi.Protocol ||
			group.DeletedAt != nil || group.Status == "DELETED" || group.InstanceID != snapshot.RuntimePolicy.GroupInstanceID || group.Name != scope.Namespace || group.Cluster != run.Cluster || group.NamespaceUID == nil || *group.NamespaceUID != scope.NamespaceUID {
			return runtimeDenied()
		}
		if runtimeMutation(op) {
			var receipt model.PipelineRuntimeReceipt
			query := st.DB.WithContext(ctx).Where("instance_id = ? AND pod_uid = ? AND operation = ? AND request_id = ?", scope.InstanceID, scope.PodUID, string(op), envelope.RequestID)
			e = query.First(&receipt).Error
			if e == nil {
				// An old prepublish receipt contains RUNNING. A launcher that
				// resumes after publication must see the current committed state,
				// otherwise it could repeat the user command and its side effects.
				if op == runtimeapi.PrePublishExecution {
					tx := runtimeTxn{ctx: ctx, store: st, scope: *scope, policy: snapshot.RuntimePolicy}
					_, record, _, err := tx.execution(request.ID, true)
					if err != nil {
						return err
					}
					if record.AssignedPodUID != scope.PodUID {
						return runtimeDenied()
					}
					reply, err := tx.executionReply(record)
					if err != nil {
						return err
					}
					result, err = json.Marshal(reply)
					return err
				}
				result = receipt.Response
				return nil
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			var count int64
			if e = st.DB.WithContext(ctx).Model(&model.PipelineRuntimeReceipt{}).Where("instance_id = ?", scope.InstanceID).Count(&count).Error; e != nil {
				return e
			}
			if count >= 32000 {
				return runtimeInvalid()
			}
		}
		// A terminal archive seals this attempt. Exact receipts remain readable
		// above, but late writers cannot change metadata behind a final proof.
		if attempt.WorkflowArchivedAt != nil && runtimeMutation(op) {
			return runtimeDenied()
		}
		tx := runtimeTxn{ctx: ctx, store: st, scope: *scope, policy: snapshot.RuntimePolicy, retry: retry, cache: cache}
		reply, e := tx.dispatch(op, request)
		if e != nil {
			return e
		}
		result, e = json.Marshal(reply)
		if e != nil {
			return e
		}
		if len(result) > runtimeapi.MaxMessageBytes/2 {
			return runtimeInvalid()
		}
		if !runtimeMutation(op) {
			return nil
		}
		return st.DB.WithContext(ctx).Create(&model.PipelineRuntimeReceipt{InstanceID: scope.InstanceID, PodUID: scope.PodUID, Operation: string(op), RequestID: envelope.RequestID, Response: result, CreatedAt: time.Now().UTC()}).Error
	})
	if err != nil {
		return nil, domainError(err)
	}
	return &runtimeapi.Reply{Protocol: runtimeapi.Protocol, Payload: result}, nil
}
func runtimeMutation(op runtimeapi.Operation) bool {
	switch op {
	case runtimeapi.CreateExecution, runtimeapi.PrePublishExecution, runtimeapi.PublishExecution, runtimeapi.EnsureArtifact, runtimeapi.EnsureArtifactType, runtimeapi.UpdateDAGState, runtimeapi.LookupCache, runtimeapi.PublishCache:
		return true
	}
	return false
}
func (tx *runtimeTxn) newObject(kind, key string, parent int64) *model.PipelineRuntimeObject {
	return &model.PipelineRuntimeObject{GroupID: tx.scope.GroupID, RunID: tx.scope.RunID, InstanceID: tx.scope.InstanceID, Kind: kind, ObjectKey: mlHash([]byte(key)), ParentID: parent, WriterPodUID: tx.scope.PodUID}
}
func (tx *runtimeTxn) pipeline() (*runtimeapi.PipelineRecord, error) {
	result := &runtimeapi.PipelineRecord{}
	for _, kind := range []string{"pipeline-context", "run-context"} {
		row, err := tx.store.RuntimeObjectByKey(tx.ctx, tx.scope, kind, mlHash([]byte(kind)))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if tx.scope.Role != "DRIVER_ROOT_DAG" {
				return nil, runtimeDenied()
			}
			row = tx.newObject(kind, kind, 0)
			if err = tx.store.SaveRuntimeObject(tx.ctx, row, map[string]any{}); err != nil {
				return nil, err
			}
			name := tx.policy.PipelineName
			if kind == "run-context" {
				name = tx.scope.WorkflowUID
			}
			contextType := "system.Pipeline"
			if kind == "run-context" {
				contextType = "system.PipelineRun"
			}
			context := &pb.Context{Id: proto.Int64(row.ID), Name: proto.String(name), Type: proto.String(contextType), CustomProperties: map[string]*pb.Value{"pipeline_root": metadata.StringValue(tx.policy.PipelineRoot), "store_session_info": metadata.StringValue(tx.policy.StoreSessionInfo)}}
			raw, e := runtimeapi.Encode(context)
			if e != nil {
				return nil, e
			}
			if err = tx.store.SaveRuntimeObject(tx.ctx, row, json.RawMessage(raw)); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		if kind == "pipeline-context" {
			result.Pipeline = row.Value
		} else {
			result.Run = row.Value
		}
	}
	return result, nil
}
func (tx *runtimeTxn) execution(id int64, write bool) (*model.PipelineRuntimeObject, *runtimeExecution, *pb.Execution, error) {
	row, err := tx.store.RuntimeObject(tx.ctx, tx.scope, id, "execution")
	if errors.Is(err, gorm.ErrRecordNotFound) && !write && tx.scope.Role == "DRIVER_CONTAINER" {
		return tx.cacheExecution(id)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var record runtimeExecution
	if json.Unmarshal(row.Value, &record) != nil || record.Config == nil {
		return nil, nil, nil, nativeFinalConflict()
	}
	execution := &pb.Execution{}
	if protojson.Unmarshal(record.Execution, execution) != nil || execution.GetId() != id {
		return nil, nil, nil, nativeFinalConflict()
	}
	if tx.scope.Role == "LAUNCHER" && id != tx.scope.ExecutionID {
		// Official aggregation recursively visits ancestors, never siblings.
		if write || tx.requireAncestor(id) != nil {
			return nil, nil, nil, runtimeDenied()
		}
	}
	return row, &record, execution, nil
}

func (tx *runtimeTxn) requireAncestor(id int64) error {
	current := tx.scope.ExecutionID
	seen := map[int64]bool{}
	for depth := 0; current > 0 && depth <= 32; depth++ {
		if seen[current] {
			return nativeFinalConflict()
		}
		seen[current] = true
		row, err := tx.store.RuntimeObject(tx.ctx, tx.scope, current, "execution")
		if err != nil {
			return err
		}
		if row.ParentID == id && id > 0 {
			return nil
		}
		current = row.ParentID
	}
	return runtimeDenied()
}
func (tx *runtimeTxn) executionReply(record *runtimeExecution) (*runtimeapi.MetadataReply, error) {
	p, err := tx.pipeline()
	if err != nil {
		return nil, err
	}
	return &runtimeapi.MetadataReply{Pipeline: p, Execution: record.Execution}, nil
}
func (tx *runtimeTxn) saveExecution(row *model.PipelineRuntimeObject, record *runtimeExecution, execution *pb.Execution) error {
	raw, err := runtimeapi.Encode(execution)
	if err != nil {
		return err
	}
	record.Execution = raw
	return tx.store.SaveRuntimeObject(tx.ctx, row, record)
}
func (tx *runtimeTxn) dispatch(op runtimeapi.Operation, req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	switch op {
	case runtimeapi.LookupCache:
		return tx.lookupCache(req)
	case runtimeapi.PublishCache:
		return tx.publishCache(req)
	case runtimeapi.GetRunContext:
		if req.PipelineName != tx.policy.PipelineName || req.RunID != tx.scope.WorkflowUID || (req.Namespace != "" && req.Namespace != tx.scope.Namespace) || (req.PipelineRoot != "" && req.PipelineRoot != tx.policy.PipelineRoot) || (req.StoreSessionInfo != "" && req.StoreSessionInfo != tx.policy.StoreSessionInfo) {
			return nil, runtimeDenied()
		}
		p, err := tx.pipeline()
		return &runtimeapi.MetadataReply{Pipeline: p}, err
	case runtimeapi.CreateExecution:
		return tx.createExecution(req)
	case runtimeapi.GetExecution:
		if req.ID > 0 {
			_, record, _, err := tx.execution(req.ID, false)
			if err != nil {
				return nil, err
			}
			return tx.executionReply(record)
		}
		result := &runtimeapi.MetadataReply{}
		for _, id := range req.IDs {
			_, record, _, err := tx.execution(id, false)
			if err != nil {
				return nil, err
			}
			result.Executions = append(result.Executions, record.Execution)
		}
		return result, nil
	case runtimeapi.FindExecution:
		if !strings.HasPrefix(tx.scope.Role, "DRIVER_") {
			return nil, runtimeDenied()
		}
		rows, err := tx.store.RuntimeObjects(tx.ctx, tx.scope, "execution")
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			var record runtimeExecution
			if json.Unmarshal(row.Value, &record) != nil {
				return nil, nativeFinalConflict()
			}
			if record.Config != nil && record.Config.Name == req.Name && string(record.Config.ExecutionType) == req.TypeName {
				return tx.executionReply(&record)
			}
		}
		return nil, gorm.ErrRecordNotFound
	case runtimeapi.PrePublishExecution:
		return tx.prepublish(req)
	case runtimeapi.PublishExecution:
		return tx.publish(req)
	case runtimeapi.ListDAGExecutions:
		return tx.listDAG(req)
	case runtimeapi.UpdateDAGState:
		return tx.updateDAG(req)
	case runtimeapi.GetExecutionInputs:
		return tx.inputs(req.ID)
	case runtimeapi.GetExecutionOutputs:
		_, record, _, err := tx.execution(req.ID, false)
		if err != nil {
			return nil, err
		}
		return &runtimeapi.MetadataReply{Artifacts: record.Outputs}, nil
	case runtimeapi.EnsureArtifactType:
		return tx.artifactType(req.Schema)
	case runtimeapi.EnsureArtifact:
		return tx.registerArtifact(req)
	case runtimeapi.GetArtifacts:
		return tx.artifacts(req.IDs)
	case runtimeapi.GetArtifactEvents:
		return tx.artifactEvents(req.IDs)
	case runtimeapi.UpdatePluginProperties:
		return nil, apperr.New(apperr.CodeUnavailable, "运行时插件未启用")
	case runtimeapi.FindArtifact:
		return nil, apperr.New(apperr.CodeUnavailable, "Importer 尚未启用")
	default:
		return nil, runtimeInvalid()
	}
}
func (tx *runtimeTxn) createExecution(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	cfg := req.Config
	if cfg == nil || !strings.HasPrefix(tx.scope.Role, "DRIVER_") || cfg.ParentDagID != tx.scope.ParentDAGID {
		return nil, runtimeDenied()
	}
	if tx.scope.Role == "DRIVER_ROOT_DAG" {
		if cfg.ParentDagID != 0 || cfg.ExecutionType != metadata.DagExecutionTypeName || cfg.Name != "run/"+tx.scope.WorkflowUID {
			return nil, runtimeDenied()
		}
	} else {
		if cfg.TaskName != tx.scope.TaskName {
			return nil, runtimeDenied()
		}
		if _, _, _, err := tx.execution(cfg.ParentDagID, false); err != nil {
			return nil, err
		}
	}
	if len(cfg.PluginCustomProperties) != 0 {
		return nil, runtimeDenied()
	}
	if cfg.CachedMLMDExecutionID != "" {
		id, err := strconv.ParseInt(cfg.CachedMLMDExecutionID, 10, 64)
		if err != nil {
			return nil, runtimeDenied()
		}
		_, grant, _, err := tx.cacheExecution(id)
		if err != nil {
			return nil, err
		}
		if !tx.policy.CacheEnabled || grant.Config.FingerPrint != cfg.FingerPrint {
			return nil, runtimeDenied()
		}
	}
	if cfg.IterationIndex != nil && *cfg.IterationIndex != tx.scope.IterationIndex {
		return nil, runtimeDenied()
	}
	if cfg.ExecutionType != metadata.ContainerExecutionTypeName && cfg.ExecutionType != metadata.DagExecutionTypeName {
		return nil, runtimeInvalid()
	}
	// Digest pinning is checked against all component images by Compile before
	// freezing the plan. Upstream GenerateExecutionConfig does not set Image;
	// requiring that optional client field would reject every real cache task.
	if cfg.FingerPrint != "" && (!tx.policy.CacheEnabled || !validator.IsSHA256(cfg.FingerPrint)) {
		return nil, runtimeDenied()
	}
	if (tx.scope.Role == "DRIVER_CONTAINER") != (cfg.ExecutionType == metadata.ContainerExecutionTypeName) ||
		len(cfg.InputParameters) > 256 || len(cfg.InputArtifactIDs) > 256 || len(cfg.OutputParameters) > 256 || len(cfg.OutputArtifacts) > 256 ||
		(cfg.TotalDagTasks != nil && (*cfg.TotalDagTasks < 0 || *cfg.TotalDagTasks > 128)) ||
		(cfg.IterationCount != nil && (*cfg.IterationCount < 0 || *cfg.IterationCount > 4000)) {
		return nil, runtimeInvalid()
	}
	if cfg.ExecutionType == metadata.ContainerExecutionTypeName && cfg.Name != fmt.Sprintf("task/%d/%s/%d", tx.scope.ParentDAGID, tx.scope.TaskName, tx.scope.IterationIndex) {
		return nil, runtimeDenied()
	}
	for _, ids := range cfg.InputArtifactIDs {
		for _, id := range ids {
			if _, err := tx.artifact(id); err != nil {
				return nil, err
			}
		}
	}
	// Keep official deterministic execution identity across driver Pod retries.
	// A physical Argo node ID is provenance, not a new logical task. DAG drivers
	// intentionally have no MLMD Name, so use their verified parent/task/index.
	key := fmt.Sprintf("%s/%d/%s/%d", cfg.ExecutionType, cfg.ParentDagID, cfg.TaskName, tx.scope.IterationIndex)
	if tx.scope.Role == "DRIVER_ROOT_DAG" {
		key = "root/" + tx.scope.WorkflowUID
	}
	row, err := tx.store.RuntimeObjectByKey(tx.ctx, tx.scope, "execution", mlHash([]byte(key)))
	if err == nil {
		var record runtimeExecution
		if json.Unmarshal(row.Value, &record) != nil {
			return nil, nativeFinalConflict()
		}
		old, _ := json.Marshal(record.Config)
		current, _ := json.Marshal(cfg)
		if !bytes.Equal(old, current) {
			return nil, nativeFinalConflict()
		}
		record.DriverNodeID, record.DriverNodeName = tx.scope.NodeID, tx.scope.NodeName
		if err := tx.store.SaveRuntimeObject(tx.ctx, row, &record); err != nil {
			return nil, err
		}
		return tx.executionReply(&record)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	row = tx.newObject("execution", key, cfg.ParentDagID)
	if err = tx.store.SaveRuntimeObject(tx.ctx, row, map[string]any{}); err != nil {
		return nil, err
	}
	typeID := int64(1)
	if cfg.ExecutionType == metadata.ContainerExecutionTypeName {
		typeID = 2
	}
	execution, err := metadata.BuildExecution(cfg, typeID)
	if err != nil {
		return nil, err
	}
	execution.Id = proto.Int64(row.ID)
	// MLMD normally expands type names when reading an execution. The retained
	// official resolver needs that name, not just the numeric type ID.
	execution.Type = proto.String(string(cfg.ExecutionType))
	execution.CreateTimeSinceEpoch = proto.Int64(time.Now().UnixMilli())
	record := &runtimeExecution{Config: cfg, DriverNodeID: tx.scope.NodeID, DriverNodeName: tx.scope.NodeName, Outputs: []runtimeapi.ArtifactRecord{}}
	if err = tx.saveExecution(row, record, execution); err != nil {
		return nil, err
	}
	return tx.executionReply(record)
}
func (tx *runtimeTxn) prepublish(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if tx.scope.Role != "LAUNCHER" || req.ID != tx.scope.ExecutionID || req.Config == nil || req.Config.PodUID != tx.scope.PodUID || req.Config.PodName != tx.scope.PodName || req.Config.Namespace != tx.scope.Namespace {
		return nil, runtimeDenied()
	}
	row, record, execution, err := tx.execution(req.ID, true)
	if err != nil {
		return nil, err
	}
	if record.AssignedPodUID != "" && record.AssignedPodUID != tx.scope.PodUID {
		if execution.GetLastKnownState() == pb.Execution_COMPLETE {
			if err := tx.handoffCompletedRetry(row, record, execution); err != nil {
				return nil, err
			}
			return tx.executionReply(record)
		}
		if err := tx.handoffRetry(row, record, execution); err != nil {
			return nil, err
		}
	} else if record.Published {
		return tx.executionReply(record)
	}
	record.AssignedPodUID = tx.scope.PodUID
	record.AssignedNodeID, record.AssignedNodeName = tx.scope.NodeID, tx.scope.NodeName
	execution.LastKnownState = pb.Execution_RUNNING.Enum()
	if execution.CustomProperties == nil {
		execution.CustomProperties = map[string]*pb.Value{}
	}
	execution.CustomProperties["pod_uid"] = metadata.StringValue(tx.scope.PodUID)
	execution.CustomProperties["pod_name"] = metadata.StringValue(tx.scope.PodName)
	execution.CustomProperties["namespace"] = metadata.StringValue(tx.scope.Namespace)
	if err = tx.saveExecution(row, record, execution); err != nil {
		return nil, err
	}
	return tx.executionReply(record)
}
func (tx *runtimeTxn) publish(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if req.State == int32(pb.Execution_CACHED) {
		return tx.publishCached(req)
	}
	if tx.scope.Role != "LAUNCHER" || req.ID != tx.scope.ExecutionID || (req.State != int32(pb.Execution_COMPLETE) && req.State != int32(pb.Execution_FAILED)) {
		return nil, runtimeDenied()
	}
	row, record, execution, err := tx.execution(req.ID, true)
	if err != nil {
		return nil, err
	}
	if record.AssignedPodUID != tx.scope.PodUID {
		return nil, runtimeDenied()
	}
	encoded, _ := json.Marshal(req)
	hash := mlHash(encoded)
	if record.Published {
		if record.PublicationHash != hash {
			return nil, nativeFinalConflict()
		}
		return &runtimeapi.MetadataReply{Done: true}, nil
	}
	ports := map[string]bool{}
	ids := map[int64]bool{}
	for _, artifact := range req.Artifacts {
		parsed := &pb.Artifact{}
		if protojson.Unmarshal(artifact.Artifact, parsed) != nil {
			return nil, runtimeInvalid()
		}
		if artifact.Name == "" || ports[artifact.Name] || ids[parsed.GetId()] {
			return nil, runtimeInvalid()
		}
		ports[artifact.Name], ids[parsed.GetId()] = true, true
		persisted, err := tx.artifact(parsed.GetId())
		if err != nil {
			return nil, err
		}
		stored := &pb.Artifact{}
		if protojson.Unmarshal(persisted.Artifact, stored) != nil || !proto.Equal(parsed, stored) || persisted.Name != artifact.Name {
			return nil, runtimeDenied()
		}
		if err := tx.requireArtifactOwner(parsed.GetId(), req.ID); err != nil {
			return nil, err
		}
	}
	execution.CustomProperties["outputs"] = &pb.Value{Value: &pb.Value_StructValue{StructValue: &structpb.Struct{Fields: req.Parameters}}}
	execution.LastKnownState = pb.Execution_State(req.State).Enum()
	execution.LastUpdateTimeSinceEpoch = proto.Int64(time.Now().UnixMilli())
	record.Published, record.PublicationHash, record.Outputs = true, hash, req.Artifacts
	if err = tx.saveExecution(row, record, execution); err != nil {
		return nil, err
	}
	return &runtimeapi.MetadataReply{Done: true}, nil
}
func (tx *runtimeTxn) listDAG(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if tx.scope.Role == "LAUNCHER" && (!req.Filter || tx.requireAncestor(req.ID) != nil) {
		return nil, runtimeDenied()
	}
	if _, _, _, err := tx.execution(req.ID, false); err != nil {
		return nil, err
	}
	rows, err := tx.store.RuntimeObjects(tx.ctx, tx.scope, "execution")
	if err != nil {
		return nil, err
	}
	reply := &runtimeapi.MetadataReply{}
	for _, row := range rows {
		if req.Filter && row.ParentID != req.ID {
			continue
		}
		var record runtimeExecution
		if json.Unmarshal(row.Value, &record) != nil {
			return nil, nativeFinalConflict()
		}
		if tx.scope.Role == "LAUNCHER" {
			// Aggregation needs only child state and indexing properties, not
			// a sibling's inputs, outputs, image, Pod or object-store addresses.
			child := &pb.Execution{}
			if protojson.Unmarshal(record.Execution, child) != nil {
				return nil, nativeFinalConflict()
			}
			properties := map[string]*pb.Value{}
			for _, key := range []string{"task_name", "parent_dag_id", "iteration_index"} {
				if value := child.GetCustomProperties()[key]; value != nil {
					properties[key] = value
				}
			}
			child.CustomProperties, child.Properties = properties, nil
			record.Execution, err = runtimeapi.Encode(child)
			if err != nil {
				return nil, err
			}
		}
		reply.Executions = append(reply.Executions, record.Execution)
	}
	return reply, nil
}
func (tx *runtimeTxn) updateDAG(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if tx.scope.Role == "LAUNCHER" && tx.requireAncestor(req.ID) != nil {
		return nil, runtimeDenied()
	}
	row, record, execution, err := tx.execution(req.ID, false)
	if err != nil {
		return nil, err
	}
	if record.Config.ExecutionType != metadata.DagExecutionTypeName {
		return nil, runtimeDenied()
	}
	children, err := tx.listDAG(runtimeapi.MetadataRequest{ID: req.ID, Filter: true})
	if err != nil {
		return nil, err
	}
	expected := execution.CustomProperties["total_dag_tasks"].GetIntValue()
	if count, ok := execution.CustomProperties["iteration_count"]; ok {
		expected = count.GetIntValue()
	} else if expected == 0 {
		// Upstream intentionally leaves root/unknown DAG state to the finalizer.
		return nil, runtimeDenied()
	}
	if execution.GetLastKnownState() == pb.Execution_CANCELED {
		return nil, runtimeDenied()
	}
	finished, failed := int64(0), false
	for _, raw := range children.Executions {
		child := &pb.Execution{}
		if protojson.Unmarshal(raw, child) != nil {
			return nil, nativeFinalConflict()
		}
		switch child.GetLastKnownState() {
		case pb.Execution_COMPLETE, pb.Execution_CACHED, pb.Execution_CANCELED:
			finished++
		case pb.Execution_FAILED:
			failed = true
		}
	}
	state := pb.Execution_RUNNING
	if finished == expected {
		state = pb.Execution_COMPLETE
	} else if failed {
		state = pb.Execution_FAILED
	}
	if state == pb.Execution_RUNNING || req.State != int32(state) {
		return nil, runtimeDenied()
	}
	old := execution.GetLastKnownState()
	if (old == pb.Execution_COMPLETE || old == pb.Execution_FAILED) && old != state {
		return nil, nativeFinalConflict()
	}
	execution.LastKnownState = state.Enum()
	execution.LastUpdateTimeSinceEpoch = proto.Int64(time.Now().UnixMilli())
	if err = tx.saveExecution(row, record, execution); err != nil {
		return nil, err
	}
	return &runtimeapi.MetadataReply{Done: true}, nil
}
func (tx *runtimeTxn) artifactType(schema string) (*runtimeapi.MetadataReply, error) {
	var parsed struct {
		Title string `yaml:"title"`
	}
	if len(schema) > 32<<10 || yaml.Unmarshal([]byte(schema), &parsed) != nil || parsed.Title == "" {
		return nil, runtimeInvalid()
	}
	key := mlHash([]byte(schema))
	row, err := tx.store.RuntimeObjectByKey(tx.ctx, tx.scope, "artifact-type", key)
	if err == nil {
		return &runtimeapi.MetadataReply{ID: row.ID}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	row = tx.newObject("artifact-type", schema, 0)
	if err = tx.store.SaveRuntimeObject(tx.ctx, row, map[string]string{"schema": schema}); err != nil {
		return nil, err
	}
	return &runtimeapi.MetadataReply{ID: row.ID}, nil
}
func (tx *runtimeTxn) registerArtifact(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if tx.scope.Role != "LAUNCHER" || req.Name == "" || len(req.Name) > 256 {
		return nil, runtimeDenied()
	}
	_, execution, _, err := tx.execution(tx.scope.ExecutionID, true)
	if err != nil {
		return nil, err
	}
	if execution.AssignedPodUID != tx.scope.PodUID || execution.Published {
		return nil, runtimeDenied()
	}
	runtime := &pipelinespec.RuntimeArtifact{}
	if protojson.Unmarshal(req.Artifact, runtime) != nil {
		return nil, runtimeInvalid()
	}
	if !validator.IsObjectURIWithin(tx.policy.PipelineRoot, runtime.GetUri()) {
		return nil, runtimeDenied()
	}
	typeReply, err := tx.artifactType(req.Schema)
	if err != nil {
		return nil, err
	}
	artifact, err := metadata.MLMDArtifact(runtime)
	if err != nil {
		return nil, err
	}
	artifact.TypeId = proto.Int64(typeReply.ID)
	var artifactSchema struct {
		Title string `yaml:"title"`
	}
	if yaml.Unmarshal([]byte(req.Schema), &artifactSchema) != nil || artifactSchema.Title == "" {
		return nil, runtimeInvalid()
	}
	artifact.Type = proto.String(artifactSchema.Title)
	artifact.State = pb.Artifact_LIVE.Enum()
	key := fmt.Sprint(tx.scope.ExecutionID) + "/" + fmt.Sprint(execution.Epoch) + "/" + req.Name
	row, err := tx.store.RuntimeObjectByKey(tx.ctx, tx.scope, "artifact", mlHash([]byte(key)))
	if err == nil {
		var stored runtimeapi.ArtifactRecord
		if json.Unmarshal(row.Value, &stored) != nil {
			return nil, nativeFinalConflict()
		}
		artifact.Id = proto.Int64(row.ID)
		artifact.Name = proto.String(strconv.FormatInt(row.ID, 10))
		existing := &pb.Artifact{}
		if protojson.Unmarshal(stored.Artifact, existing) != nil || !proto.Equal(artifact, existing) {
			return nil, nativeFinalConflict()
		}
		return &runtimeapi.MetadataReply{Artifacts: []runtimeapi.ArtifactRecord{stored}}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	row = tx.newObject("artifact", key, tx.scope.ExecutionID)
	if err = tx.store.SaveRuntimeObject(tx.ctx, row, map[string]any{}); err != nil {
		return nil, err
	}
	artifact.Id = proto.Int64(row.ID)
	artifact.Name = proto.String(strconv.FormatInt(row.ID, 10))
	raw, err := runtimeapi.Encode(artifact)
	if err != nil {
		return nil, err
	}
	record := runtimeapi.ArtifactRecord{Name: req.Name, Schema: req.Schema, Artifact: raw}
	if err = tx.store.SaveRuntimeObject(tx.ctx, row, record); err != nil {
		return nil, err
	}
	return &runtimeapi.MetadataReply{Artifacts: []runtimeapi.ArtifactRecord{record}}, nil
}
func (tx *runtimeTxn) artifact(id int64) (runtimeapi.ArtifactRecord, error) {
	row, err := tx.store.RuntimeObject(tx.ctx, tx.scope, id, "artifact")
	if err != nil {
		return runtimeapi.ArtifactRecord{}, err
	}
	var record runtimeapi.ArtifactRecord
	if json.Unmarshal(row.Value, &record) != nil {
		return record, nativeFinalConflict()
	}
	return record, nil
}
func (tx *runtimeTxn) requireArtifactOwner(id, executionID int64) error {
	row, err := tx.store.RuntimeObject(tx.ctx, tx.scope, id, "artifact")
	if err != nil {
		return err
	}
	if row.ParentID != executionID || row.WriterPodUID != tx.scope.PodUID {
		return runtimeDenied()
	}
	return nil
}
func (tx *runtimeTxn) artifacts(ids []int64) (*runtimeapi.MetadataReply, error) {
	reply := &runtimeapi.MetadataReply{}
	for _, id := range ids {
		if tx.scope.Role == "LAUNCHER" {
			_, record, _, err := tx.execution(tx.scope.ExecutionID, false)
			if err != nil {
				return nil, err
			}
			allowed := false
			for _, inputIDs := range record.Config.InputArtifactIDs {
				for _, input := range inputIDs {
					if input == id {
						allowed = true
					}
				}
			}
			if !allowed && tx.requireArtifactOwner(id, tx.scope.ExecutionID) != nil {
				return nil, runtimeDenied()
			}
		}
		a, err := tx.artifact(id)
		if err != nil {
			return nil, err
		}
		reply.Artifacts = append(reply.Artifacts, a)
	}
	return reply, nil
}
func (tx *runtimeTxn) inputs(id int64) (*runtimeapi.MetadataReply, error) {
	_, record, _, err := tx.execution(id, false)
	if err != nil {
		return nil, err
	}
	reply := &runtimeapi.MetadataReply{Inputs: map[string]json.RawMessage{}}
	for name, ids := range record.Config.InputArtifactIDs {
		list := &pipelinespec.ArtifactList{}
		for _, artifactID := range ids {
			a, err := tx.artifact(artifactID)
			if err != nil {
				return nil, err
			}
			artifact := &pb.Artifact{}
			if protojson.Unmarshal(a.Artifact, artifact) != nil {
				return nil, nativeFinalConflict()
			}
			runtime, err := (&metadata.OutputArtifact{Name: a.Name, Schema: a.Schema, Artifact: artifact}).ToRuntimeArtifact()
			if err != nil {
				return nil, err
			}
			list.Artifacts = append(list.Artifacts, runtime)
		}
		raw, err := runtimeapi.Encode(list)
		if err != nil {
			return nil, err
		}
		reply.Inputs[name] = raw
	}
	return reply, nil
}
func (tx *runtimeTxn) artifactEvents(ids []int64) (*runtimeapi.MetadataReply, error) {
	if tx.scope.Role == "LAUNCHER" {
		return nil, runtimeDenied()
	}
	rows, err := tx.store.RuntimeObjects(tx.ctx, tx.scope, "execution")
	if err != nil {
		return nil, err
	}
	wanted := map[int64]bool{}
	for _, id := range ids {
		if _, err = tx.artifact(id); err != nil {
			return nil, err
		}
		wanted[id] = true
	}
	reply := &runtimeapi.MetadataReply{}
	for _, row := range rows {
		var record runtimeExecution
		if json.Unmarshal(row.Value, &record) != nil {
			return nil, nativeFinalConflict()
		}
		for _, output := range record.Outputs {
			a := &pb.Artifact{}
			if protojson.Unmarshal(output.Artifact, a) != nil {
				return nil, nativeFinalConflict()
			}
			if !wanted[a.GetId()] {
				continue
			}
			event := &pb.Event{ArtifactId: proto.Int64(a.GetId()), ExecutionId: proto.Int64(row.ID), Type: pb.Event_OUTPUT.Enum(), Path: &pb.Event_Path{Steps: []*pb.Event_Path_Step{{Value: &pb.Event_Path_Step_Key{Key: output.Name}}}}}
			raw, err := runtimeapi.Encode(event)
			if err != nil {
				return nil, err
			}
			reply.Events = append(reply.Events, raw)
		}
	}
	return reply, nil
}
