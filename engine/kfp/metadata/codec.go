package metadata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/kubeflow/pipelines/api/v2alpha1/go/pipelinespec"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
)

// Contexts is the transport form of the upstream private context pair.
type Contexts struct {
	Pipeline *pb.Context `json:"pipeline"`
	Run      *pb.Context `json:"run"`
}

func NewPipeline(contexts Contexts) (*Pipeline, error) {
	if contexts.Pipeline.GetId() <= 0 || contexts.Run.GetId() <= 0 {
		return nil, fmt.Errorf("pipeline and run context IDs are required")
	}
	return &Pipeline{pipelineCtx: proto.Clone(contexts.Pipeline).(*pb.Context), pipelineRunCtx: proto.Clone(contexts.Run).(*pb.Context)}, nil
}

func (p *Pipeline) Contexts() Contexts {
	if p == nil || p.pipelineCtx == nil || p.pipelineRunCtx == nil {
		return Contexts{}
	}
	return Contexts{Pipeline: proto.Clone(p.pipelineCtx).(*pb.Context), Run: proto.Clone(p.pipelineRunCtx).(*pb.Context)}
}

func BindExecution(value *pb.Execution, pipeline *Pipeline) (*Execution, error) {
	if value.GetId() <= 0 || pipeline == nil || pipeline.GetRunCtxID() <= 0 {
		return nil, fmt.Errorf("execution and pipeline binding are required")
	}
	return &Execution{Execution: proto.Clone(value).(*pb.Execution), pipeline: pipeline}, nil
}

var RuntimeArtifact = toRuntimeArtifact
var MLMDArtifact = toMLMDArtifact

// ExecutionConfig contains generated oneof messages. encoding/json alone cannot
// round-trip these fields; preserve the official protobuf JSON representation.
func (c ExecutionConfig) MarshalJSON() ([]byte, error) {
	type plain ExecutionConfig
	value := plain(c)
	value.OutputParameters, value.OutputArtifacts = nil, nil
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	parameters := map[string]json.RawMessage{}
	for name, spec := range c.OutputParameters {
		b, e := protojson.Marshal(spec)
		if e != nil {
			return nil, e
		}
		parameters[name] = b
	}
	artifacts := map[string]json.RawMessage{}
	for name, spec := range c.OutputArtifacts {
		b, e := protojson.Marshal(spec)
		if e != nil {
			return nil, e
		}
		artifacts[name] = b
	}
	fields["OutputParameters"], err = json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	fields["OutputArtifacts"], err = json.Marshal(artifacts)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func (c *ExecutionConfig) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	parameters, artifacts := fields["OutputParameters"], fields["OutputArtifacts"]
	delete(fields, "OutputParameters")
	delete(fields, "OutputArtifacts")
	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	type plain ExecutionConfig
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(rest))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("execution config has trailing data")
	}
	decode := func(raw json.RawMessage, allocate func(string) proto.Message) error {
		if len(raw) == 0 {
			return nil
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for name, encoded := range values {
			if err := protojson.Unmarshal(encoded, allocate(name)); err != nil {
				return err
			}
		}
		return nil
	}
	value.OutputParameters = map[string]*pipelinespec.DagOutputsSpec_DagOutputParameterSpec{}
	value.OutputArtifacts = map[string]*pipelinespec.DagOutputsSpec_DagOutputArtifactSpec{}
	if err = decode(parameters, func(name string) proto.Message {
		item := &pipelinespec.DagOutputsSpec_DagOutputParameterSpec{}
		value.OutputParameters[name] = item
		return item
	}); err != nil {
		return err
	}
	if err = decode(artifacts, func(name string) proto.Message {
		item := &pipelinespec.DagOutputsSpec_DagOutputArtifactSpec{}
		value.OutputArtifacts[name] = item
		return item
	}); err != nil {
		return err
	}
	*c = ExecutionConfig(value)
	return nil
}
