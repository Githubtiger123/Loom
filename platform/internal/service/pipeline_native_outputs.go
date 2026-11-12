package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"time"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
)

type nativePythonArtifact struct {
	Metadata    map[string]json.RawMessage `json:"metadata"`
	SHA256      string                     `json:"sha256"`
	Size        int64                      `json:"size"`
	Kind        string                     `json:"kind,omitempty"`
	Format      string                     `json:"format,omitempty"`
	DecodedSize int64                      `json:"decodedSize,omitempty"`
	Entries     int                        `json:"entries,omitempty"`
}
type nativePythonBundle struct {
	Protocol   string                          `json:"protocol"`
	Parameters map[string]json.RawMessage      `json:"parameters"`
	Artifacts  map[string]nativePythonArtifact `json:"artifacts"`
}
type nativeArgoOutputs struct {
	Parameters []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"parameters"`
	Artifacts []struct {
		Name string `json:"name"`
		S3   struct {
			Endpoint string `json:"endpoint"`
			Bucket   string `json:"bucket"`
			Key      string `json:"key"`
		} `json:"s3"`
	} `json:"artifacts"`
}

func nativeOutputProtocol(p *pipelineir.Plan) string {
	if pipelineir.IsPythonCompiler(p.Compiler()) {
		return pipelineir.OutputProtocol(p.Compiler())
	}
	return "loom-static-empty-v1"
}

// Called under the existing Run/attempt/task locks. Only a successful Argo Pod
// (including successful artifact upload) can publish a declared output. Never
// accept a caller-supplied URI, root artifact or cross-instance producer.
func syncNativePythonOutputs(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, plan *pipelineir.Plan, row *model.PipelineTaskExecution, node k8s.PipelineWorkflowNode, runtime map[string]any) error {
	if node.Phase != "SUCCEEDED" {
		return nil
	}
	var task pipelineir.Task
	for _, candidate := range plan.Tasks() {
		if candidate.NodeKey == row.NodeKey || plan.Compiler() == pipelineir.AdvancedCompilerVersion && candidate.NodeKey == node.DefinitionKey {
			task = candidate
			break
		}
	}
	if task.Runtime == nil {
		return nativeFinalConflict()
	}
	var output nativeArgoOutputs
	if json.Unmarshal(node.Outputs, &output) != nil {
		return nativeFinalConflict()
	}
	encoded := ""
	for _, p := range output.Parameters {
		if p.Name == "loom-bundle" {
			if encoded != "" {
				return nativeFinalConflict()
			}
			encoded = p.Value
		}
	}
	if len(encoded) > 64<<10 {
		return nativeFinalConflict()
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !validator.IsBoundedJSONObject(raw, 48<<10, 16, 4096) {
		return nativeFinalConflict()
	}
	var bundle nativePythonBundle
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&bundle) != nil || bundle.Protocol != pipelineir.OutputProtocol(plan.Compiler()) || len(bundle.Parameters) != len(task.Runtime.Outputs.Parameters) || len(bundle.Artifacts) != len(task.Runtime.Outputs.Artifacts) {
		return nativeFinalConflict()
	}
	for name, port := range task.Runtime.Outputs.Parameters {
		valid := pipelineir.ParameterValid(port.Type, bundle.Parameters[name])
		if plan.Compiler() == pipelineir.AdvancedCompilerVersion {
			valid = pipelineir.AdvancedParameterValid(port.Type, bundle.Parameters[name])
		}
		if !valid {
			return nativeFinalConflict()
		}
	}
	// The frozen policy, not today's mutable config, owns the storage location.
	var snapshot nativeStagingSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil {
		return nativeFinalConflict()
	}
	ref, ok := nativePipelineRef(a)
	if !ok {
		return nativeFinalConflict()
	}
	artifacts := map[string]any{}
	for name, port := range task.Runtime.Outputs.Artifacts {
		v, exists := bundle.Artifacts[name]
		if !exists || !validator.IsSHA256(v.SHA256) || v.Size < 0 || len(v.Metadata) > 128 {
			return nativeFinalConflict()
		}
		if plan.Compiler() == pipelineir.AdvancedCompilerVersion {
			if (v.Kind != "FILE" && v.Kind != "DIRECTORY") || v.Format != "TAR_GZIP_V1" || v.DecodedSize < 0 || v.DecodedSize > 10<<30 || v.Size > 10<<30 || v.Entries < 1 || v.Entries > 10000 {
				return nativeFinalConflict()
			}
		} else if v.Kind != "" || v.Format != "" || v.DecodedSize != 0 || v.Entries != 0 {
			return nativeFinalConflict()
		}
		if port.Type.Title == "system.Metrics" {
			for key, value := range v.Metadata {
				var number float64
				if len(key) == 0 || len(key) > 128 || len(value) == 0 || json.Unmarshal(value, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) || value[0] == 'n' {
					return nativeFinalConflict()
				}
			}
		}
		portID := pipelineir.PortIndex(task.Runtime.Outputs.Artifacts, name)
		key := k8s.NativeArtifactKey(ref, task.TaskName, portID)
		if plan.Compiler() == pipelineir.AdvancedCompilerVersion {
			if row.PodName == nil {
				return nativeFinalConflict()
			}
			key += "/pods/" + *row.PodName
		} else if task.Retry != nil && node.Type == "Retry" {
			if len(node.Attempts) == 0 || node.Attempts[len(node.Attempts)-1].Phase != "SUCCEEDED" {
				return nativeFinalConflict()
			}
			key += "/attempt-" + jsonIndex(len(node.Attempts)-1)
		}
		found := false
		for _, transport := range output.Artifacts {
			if transport.Name == portID {
				if found || transport.S3.Endpoint != snapshot.Policy.ArtifactEndpoint || transport.S3.Bucket != snapshot.Policy.ArtifactBucket || transport.S3.Key != key {
					return nativeFinalConflict()
				}
				found = true
			}
		}
		if !found {
			return nativeFinalConflict()
		}
		objectValue := map[string]any{"endpoint": snapshot.Policy.ArtifactEndpoint, "bucket": snapshot.Policy.ArtifactBucket, "key": key, "sha256": v.SHA256, "size": v.Size}
		if v.Format != "" {
			objectValue["format"] = v.Format
			objectValue["kind"] = v.Kind
			objectValue["decodedSize"] = v.DecodedSize
			objectValue["entries"] = v.Entries
		}
		object, _ := json.Marshal(objectValue)
		metadata, _ := json.Marshal(v.Metadata)
		schema, _ := json.Marshal(port.Type)
		registration := mlHash([]byte(bundle.Protocol + "\x00" + a.InstanceID + "\x00" + row.NodeKey + "\x00" + name))
		artifact := &model.PipelineArtifact{}
		err := st.DB.WithContext(ctx).Where("group_id = ? AND registration_key = ?", a.GroupID, registration).First(artifact).Error
		if err == gorm.ErrRecordNotFound {
			now := time.Now().UTC().Truncate(time.Millisecond)
			uri := "s3://" + snapshot.Policy.ArtifactBucket + "/" + key
			provenance, _ := json.Marshal(map[string]any{"protocol": bundle.Protocol, "port": name, "nodeKey": row.NodeKey, "workflowUID": ref.UID, "digestSource": "COMPONENT_REPORTED"})
			artifact = &model.PipelineArtifact{GroupID: a.GroupID, ProducerInstanceID: &a.InstanceID, ProducerTaskID: &row.ID, RegistrationKey: registration,
				TypeName: port.Type.Title, TypeSchemaVersion: port.Type.Version, TypeSchema: schema, TypeSchemaHash: mlHash(schema), URI: &uri, ObjectRef: object, Metadata: metadata,
				Visualizations: json.RawMessage(`[]`), Provenance: provenance, State: "PUBLISHED", PublishedAt: &now}
			if err := st.DB.WithContext(ctx).Create(artifact).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			x, _ := nativeJSONHash(artifact.ObjectRef)
			y, _ := nativeJSONHash(object)
			m, _ := nativeJSONHash(artifact.Metadata)
			n, _ := nativeJSONHash(metadata)
			if artifact.ProducerInstanceID == nil || *artifact.ProducerInstanceID != a.InstanceID || artifact.ProducerTaskID == nil || *artifact.ProducerTaskID != row.ID || artifact.State != "PUBLISHED" || x != y || m != n || artifact.TypeSchemaHash != mlHash(schema) {
				return nativeFinalConflict()
			}
		}
		artifacts[name] = map[string]any{"id": artifact.ID, "type": port.Type, "metadata": v.Metadata, "sha256": v.SHA256, "size": v.Size}
	}
	data, _ := json.Marshal(map[string]any{"schemaVersion": 1, "parameters": bundle.Parameters, "artifacts": artifacts})
	if runtime["outputsSealed"] == true {
		previous, _ := nativeJSONHash(row.Outputs)
		next, _ := nativeJSONHash(data)
		if previous != next {
			return nativeFinalConflict()
		}
	}
	row.Outputs = data
	row.Inputs, _ = json.Marshal(map[string]any{"schemaVersion": 1, "parameters": task.Runtime.Parameters, "artifacts": task.Runtime.Artifacts})
	runtime["outputsSealed"] = true
	return nil
}
