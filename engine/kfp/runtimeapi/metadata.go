package runtimeapi

import (
	"encoding/json"
	"fmt"
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// Proto messages are encoded individually with protojson, never encoding/json
// on generated oneofs. No request carries authority to choose its run scope.
type MetadataRequest struct {
	Fingerprint      string                     `json:"fingerprint,omitempty"`
	Task             json.RawMessage            `json:"task,omitempty"`
	ID               int64                      `json:"id,omitempty"`
	IDs              []int64                    `json:"ids,omitempty"`
	Name             string                     `json:"name,omitempty"`
	TypeName         string                     `json:"typeName,omitempty"`
	PipelineName     string                     `json:"pipelineName,omitempty"`
	RunID            string                     `json:"runId,omitempty"`
	Namespace        string                     `json:"namespace,omitempty"`
	PipelineRoot     string                     `json:"pipelineRoot,omitempty"`
	StoreSessionInfo string                     `json:"storeSessionInfo,omitempty"`
	Config           *metadata.ExecutionConfig  `json:"config,omitempty"`
	Parameters       map[string]*structpb.Value `json:"parameters,omitempty"`
	Artifacts        []ArtifactRecord           `json:"artifacts,omitempty"`
	Artifact         json.RawMessage            `json:"artifact,omitempty"`
	Schema           string                     `json:"schema,omitempty"`
	State            int32                      `json:"state,omitempty"`
	Filter           bool                       `json:"filter,omitempty"`
	Properties       map[string]string          `json:"properties,omitempty"`
}

type PipelineRecord struct {
	Pipeline json.RawMessage `json:"pipeline"`
	Run      json.RawMessage `json:"run"`
}
type ArtifactRecord struct {
	Name     string          `json:"name"`
	Schema   string          `json:"schema"`
	Artifact json.RawMessage `json:"artifact"`
}
type MetadataReply struct {
	Hit         *bool                      `json:"hit,omitempty"`
	ExecutionID string                     `json:"executionId,omitempty"`
	Published   bool                       `json:"published,omitempty"`
	Pipeline    *PipelineRecord            `json:"pipeline,omitempty"`
	Execution   json.RawMessage            `json:"execution,omitempty"`
	Executions  []json.RawMessage          `json:"executions,omitempty"`
	Artifacts   []ArtifactRecord           `json:"artifacts,omitempty"`
	Events      []json.RawMessage          `json:"events,omitempty"`
	Inputs      map[string]json.RawMessage `json:"inputs,omitempty"`
	ID          int64                      `json:"id,omitempty"`
	Name        string                     `json:"name,omitempty"`
	Done        bool                       `json:"done,omitempty"`
}

func Encode(message proto.Message) (json.RawMessage, error) {
	if message == nil || !message.ProtoReflect().IsValid() {
		return nil, fmt.Errorf("runtime protobuf message is required")
	}
	return protojson.Marshal(message)
}
