package runtimeclient

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/kubeflow/pipelines/api/v2alpha1/go/pipelinespec"
	"github.com/kubeflow/pipelines/backend/src/v2/objectstore"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

type Metadata struct{ transport *runtimeapi.Client }

var _ metadata.ClientInterface = (*Metadata)(nil)

func NewMetadata(transport *runtimeapi.Client) (*Metadata, error) {
	if transport == nil {
		return nil, fmt.Errorf("runtime metadata transport is required")
	}
	return &Metadata{transport}, nil
}
func (c *Metadata) call(ctx context.Context, op runtimeapi.Operation, request runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	raw, err = c.transport.Call(ctx, op, raw)
	if err != nil {
		return nil, err
	}
	var reply runtimeapi.MetadataReply
	if err = json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("invalid metadata response")
	}
	return &reply, nil
}
func pipelineFromReply(reply *runtimeapi.MetadataReply) (*metadata.Pipeline, error) {
	if reply.Pipeline == nil {
		return nil, fmt.Errorf("missing pipeline context")
	}
	p, r := &pb.Context{}, &pb.Context{}
	if err := protojson.Unmarshal(reply.Pipeline.Pipeline, p); err != nil {
		return nil, err
	}
	if err := protojson.Unmarshal(reply.Pipeline.Run, r); err != nil {
		return nil, err
	}
	return metadata.NewPipeline(metadata.Contexts{Pipeline: p, Run: r})
}
func executionFromReply(reply *runtimeapi.MetadataReply) (*metadata.Execution, error) {
	pipeline, err := pipelineFromReply(reply)
	if err != nil {
		return nil, err
	}
	execution := &pb.Execution{}
	if err = protojson.Unmarshal(reply.Execution, execution); err != nil {
		return nil, err
	}
	return metadata.BindExecution(execution, pipeline)
}
func artifactFromRecord(record runtimeapi.ArtifactRecord) (*metadata.OutputArtifact, error) {
	artifact := &pb.Artifact{}
	if err := protojson.Unmarshal(record.Artifact, artifact); err != nil {
		return nil, err
	}
	if artifact.GetId() <= 0 {
		return nil, fmt.Errorf("invalid artifact identity")
	}
	return &metadata.OutputArtifact{Name: record.Name, Schema: record.Schema, Artifact: artifact}, nil
}
func (c *Metadata) GetPipeline(ctx context.Context, name, run, namespace, resource, root, store string) (*metadata.Pipeline, error) {
	r, err := c.call(ctx, runtimeapi.GetRunContext, runtimeapi.MetadataRequest{PipelineName: name, RunID: run, Namespace: namespace, PipelineRoot: root, StoreSessionInfo: store})
	if err != nil {
		return nil, err
	}
	return pipelineFromReply(r)
}
func (c *Metadata) GetPipelineFromExecution(ctx context.Context, id int64) (*metadata.Pipeline, error) {
	r, err := c.call(ctx, runtimeapi.GetExecution, runtimeapi.MetadataRequest{ID: id})
	if err != nil {
		return nil, err
	}
	return pipelineFromReply(r)
}
func (c *Metadata) GetExecution(ctx context.Context, id int64) (*metadata.Execution, error) {
	r, err := c.call(ctx, runtimeapi.GetExecution, runtimeapi.MetadataRequest{ID: id})
	if err != nil {
		return nil, err
	}
	return executionFromReply(r)
}
func (c *Metadata) GetExecutionByTypeAndName(ctx context.Context, kind, name string) (*metadata.Execution, error) {
	r, err := c.call(ctx, runtimeapi.FindExecution, runtimeapi.MetadataRequest{TypeName: kind, Name: name})
	if err != nil {
		return nil, err
	}
	return executionFromReply(r)
}
func (c *Metadata) GetDAG(ctx context.Context, id int64) (*metadata.DAG, error) {
	e, err := c.GetExecution(ctx, id)
	if err != nil {
		return nil, err
	}
	return &metadata.DAG{Execution: e}, nil
}
func (c *Metadata) GetExecutions(ctx context.Context, ids []int64) ([]*pb.Execution, error) {
	r, err := c.call(ctx, runtimeapi.GetExecution, runtimeapi.MetadataRequest{IDs: ids})
	if err != nil {
		return nil, err
	}
	result := make([]*pb.Execution, 0, len(r.Executions))
	for _, raw := range r.Executions {
		e := &pb.Execution{}
		if err = protojson.Unmarshal(raw, e); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, nil
}
func (c *Metadata) CreateExecution(ctx context.Context, pipeline *metadata.Pipeline, config *metadata.ExecutionConfig) (*metadata.Execution, error) {
	if pipeline == nil {
		return nil, fmt.Errorf("pipeline is required")
	}
	r, err := c.call(ctx, runtimeapi.CreateExecution, runtimeapi.MetadataRequest{Config: config})
	if err != nil {
		return nil, err
	}
	return executionFromReply(r)
}
func (c *Metadata) PrePublishExecution(ctx context.Context, execution *metadata.Execution, config *metadata.ExecutionConfig) (*metadata.Execution, error) {
	r, err := c.call(ctx, runtimeapi.PrePublishExecution, runtimeapi.MetadataRequest{ID: execution.GetID(), Config: config})
	if err != nil {
		return nil, err
	}
	return executionFromReply(r)
}
func (c *Metadata) PublishExecution(ctx context.Context, execution *metadata.Execution, values map[string]*structpb.Value, artifacts []*metadata.OutputArtifact, state pb.Execution_State) error {
	req := runtimeapi.MetadataRequest{ID: execution.GetID(), Parameters: values, State: int32(state)}
	for _, artifact := range artifacts {
		raw, err := runtimeapi.Encode(artifact.Artifact)
		if err != nil {
			return err
		}
		req.Artifacts = append(req.Artifacts, runtimeapi.ArtifactRecord{Name: artifact.Name, Schema: artifact.Schema, Artifact: raw})
	}
	_, err := c.call(ctx, runtimeapi.PublishExecution, req)
	return err
}
func (c *Metadata) GetExecutionsInDAG(ctx context.Context, dag *metadata.DAG, pipeline *metadata.Pipeline, filter bool) (map[string]*metadata.Execution, error) {
	r, err := c.call(ctx, runtimeapi.ListDAGExecutions, runtimeapi.MetadataRequest{ID: dag.Execution.GetID(), Filter: filter})
	if err != nil {
		return nil, err
	}
	executions := make([]*pb.Execution, 0, len(r.Executions))
	for _, raw := range r.Executions {
		e := &pb.Execution{}
		if err = protojson.Unmarshal(raw, e); err != nil {
			return nil, err
		}
		executions = append(executions, e)
	}
	return metadata.IndexDAGExecutions(dag, executions)
}
func (c *Metadata) UpdateDAGExecutionsState(ctx context.Context, dag *metadata.DAG, pipeline *metadata.Pipeline) error {
	return metadata.ReconcileDAG(ctx, c, dag, pipeline)
}
func (c *Metadata) PutDAGExecutionState(ctx context.Context, id int64, state pb.Execution_State) error {
	_, err := c.call(ctx, runtimeapi.UpdateDAGState, runtimeapi.MetadataRequest{ID: id, State: int32(state)})
	return err
}
func (c *Metadata) UpdateExecutionPluginProperties(ctx context.Context, e *metadata.Execution, props map[string]string) error {
	_, err := c.call(ctx, runtimeapi.UpdatePluginProperties, runtimeapi.MetadataRequest{ID: e.GetID(), Properties: props})
	return err
}
func (c *Metadata) GetArtifacts(ctx context.Context, ids []int64) ([]*pb.Artifact, error) {
	r, err := c.call(ctx, runtimeapi.GetArtifacts, runtimeapi.MetadataRequest{IDs: ids})
	if err != nil {
		return nil, err
	}
	result := make([]*pb.Artifact, 0, len(r.Artifacts))
	for _, record := range r.Artifacts {
		a, err := artifactFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, a.Artifact)
	}
	return result, nil
}
func (c *Metadata) GetArtifactName(ctx context.Context, id int64) (string, error) {
	r, err := c.call(ctx, runtimeapi.GetArtifacts, runtimeapi.MetadataRequest{IDs: []int64{id}})
	if err != nil {
		return "", err
	}
	if len(r.Artifacts) != 1 || r.Artifacts[0].Name == "" {
		return "", fmt.Errorf("artifact not found")
	}
	// Official GetArtifactName returns the output event's port key, not the
	// numeric artifact ID stored in Artifact.name/RuntimeArtifact.name.
	return r.Artifacts[0].Name, nil
}
func (c *Metadata) GetEventsByArtifactIDs(ctx context.Context, ids []int64) ([]*pb.Event, error) {
	r, err := c.call(ctx, runtimeapi.GetArtifactEvents, runtimeapi.MetadataRequest{IDs: ids})
	if err != nil {
		return nil, err
	}
	events := make([]*pb.Event, 0, len(r.Events))
	for _, raw := range r.Events {
		e := &pb.Event{}
		if err = protojson.Unmarshal(raw, e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}
func (c *Metadata) GetOutputArtifactsByExecutionId(ctx context.Context, id int64) (map[string]*metadata.OutputArtifact, error) {
	r, err := c.call(ctx, runtimeapi.GetExecutionOutputs, runtimeapi.MetadataRequest{ID: id})
	if err != nil {
		return nil, err
	}
	result := map[string]*metadata.OutputArtifact{}
	for _, record := range r.Artifacts {
		a, err := artifactFromRecord(record)
		if err != nil {
			return nil, err
		}
		result[a.Name] = a
	}
	return result, nil
}
func (c *Metadata) GetInputArtifactsByExecutionID(ctx context.Context, id int64) (map[string]*pipelinespec.ArtifactList, error) {
	r, err := c.call(ctx, runtimeapi.GetExecutionInputs, runtimeapi.MetadataRequest{ID: id})
	if err != nil {
		return nil, err
	}
	result := map[string]*pipelinespec.ArtifactList{}
	for name, raw := range r.Inputs {
		list := &pipelinespec.ArtifactList{}
		if err = protojson.Unmarshal(raw, list); err != nil {
			return nil, err
		}
		result[name] = list
	}
	return result, nil
}
func (c *Metadata) RecordArtifact(ctx context.Context, name, schema string, artifact *pipelinespec.RuntimeArtifact, state pb.Artifact_State, bucket *objectstore.Config) (*metadata.OutputArtifact, error) {
	raw, err := runtimeapi.Encode(artifact)
	if err != nil {
		return nil, err
	}
	r, err := c.call(ctx, runtimeapi.EnsureArtifact, runtimeapi.MetadataRequest{Name: name, Schema: schema, Artifact: raw, State: int32(state)})
	if err != nil {
		return nil, err
	}
	if len(r.Artifacts) != 1 {
		return nil, fmt.Errorf("artifact registration was not confirmed")
	}
	return artifactFromRecord(r.Artifacts[0])
}
func (c *Metadata) GetOrInsertArtifactType(ctx context.Context, schema string) (int64, error) {
	r, err := c.call(ctx, runtimeapi.EnsureArtifactType, runtimeapi.MetadataRequest{Schema: schema})
	if err != nil {
		return 0, err
	}
	if r.ID <= 0 {
		return 0, fmt.Errorf("invalid artifact type ID")
	}
	return r.ID, nil
}
func (c *Metadata) FindMatchedArtifact(ctx context.Context, artifact *pb.Artifact, pipelineID int64) (*pb.Artifact, error) {
	raw, err := runtimeapi.Encode(artifact)
	if err != nil {
		return nil, err
	}
	r, err := c.call(ctx, runtimeapi.FindArtifact, runtimeapi.MetadataRequest{ID: pipelineID, Artifact: raw})
	if err != nil {
		return nil, err
	}
	if len(r.Artifacts) == 0 {
		return nil, nil
	}
	if len(r.Artifacts) != 1 {
		return nil, fmt.Errorf("ambiguous artifact match")
	}
	a, err := artifactFromRecord(r.Artifacts[0])
	if err != nil {
		return nil, err
	}
	return a.Artifact, nil
}
