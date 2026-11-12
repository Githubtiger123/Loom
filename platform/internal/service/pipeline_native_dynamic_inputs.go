package service

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// Only published producers in this run, or producers explicitly bound by a
// validated cache hit in this run, may supply aliases and input lineage.
func nativeReusableArtifacts(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt) ([]model.PipelineArtifact, error) {
	sources := st.DB.WithContext(ctx).Model(&model.PipelineTaskExecution{}).
		Select("cache_source_task_id").Where("group_id = ? AND instance_id = ? AND cache_source_task_id IS NOT NULL", a.GroupID, a.InstanceID)
	rows := []model.PipelineArtifact{}
	err := st.DB.WithContext(ctx).Where("group_id = ? AND state = ?", a.GroupID, "PUBLISHED").
		Where("producer_instance_id = ? OR producer_task_id IN (?)", a.InstanceID, sources).
		Limit(128001).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) > 128000 {
		return nil, nativeFinalConflict()
	}
	return rows, nil
}

// All transport and manifest fields must identify the same sealed object.
// Matching only its digest/key would permit altered directory manifests or
// metadata to enter downstream lineage through an otherwise valid alias.
func nativePublishedArtifactMatches(candidate model.PipelineArtifact, port pipelineir.ArtifactPort, value nativePythonArtifact, endpoint, bucket, key string) bool {
	var object struct {
		Endpoint string `json:"endpoint"`
		Bucket   string `json:"bucket"`
		Key      string `json:"key"`
		nativePythonArtifact
	}
	if json.Unmarshal(candidate.ObjectRef, &object) != nil || key == "" || object.Endpoint != endpoint || object.Bucket != bucket || object.Key != key || object.SHA256 != value.SHA256 || object.Size != value.Size || object.Kind != value.Kind || object.Format != value.Format || object.DecodedSize != value.DecodedSize || object.Entries != value.Entries || candidate.TypeName != port.Type.Title || candidate.TypeSchemaVersion != port.Type.Version {
		return false
	}
	metadata, err := json.Marshal(value.Metadata)
	if err != nil {
		return false
	}
	oldHash, oldErr := nativeJSONHash(candidate.Metadata)
	newHash, newErr := nativeJSONHash(metadata)
	return oldErr == nil && newErr == nil && oldHash == newHash
}

// Resolve actual runtime inputs after all successful outputs/cache bindings
// have been published, regardless of node map iteration order. Definitions are
// never stored in place of actual loop values or actual input artifact IDs.
func syncNativeAdvancedInputs(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, o *nativeStaticNodeObservation) error {
	candidates, err := nativeReusableArtifacts(ctx, st, a)
	if err != nil {
		return err
	}
	var snapshot nativeStagingSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil {
		return nativeFinalConflict()
	}
	for key, node := range o.Nodes {
		if node.Type != "Pod" && !node.CacheHit || len(node.Inputs) == 0 {
			continue
		}
		task, ok := nativeDefinition(o.Plan, node.DefinitionKey)
		if !ok || task.Runtime == nil {
			return nativeFinalConflict()
		}
		var input nativeArgoOutputs
		if json.Unmarshal(node.Inputs, &input) != nil {
			return nativeFinalConflict()
		}
		values := map[string]json.RawMessage{}
		artifacts := map[string]nativePythonArtifact{}
		for _, p := range input.Parameters {
			var target any
			switch p.Name {
			case "loom-values":
				target = &values
			case "loom-artifacts":
				target = &artifacts
			default:
				continue
			}
			raw, decodeErr := base64.StdEncoding.DecodeString(p.Value)
			if decodeErr != nil || !validator.IsBoundedJSONObject(raw, 48<<10, 16, 4096) || json.Unmarshal(raw, target) != nil {
				return nativeFinalConflict()
			}
		}
		if len(values) != len(task.Runtime.Inputs.Parameters) || len(artifacts) != len(task.Runtime.Inputs.Artifacts) || len(input.Artifacts) != len(task.Runtime.Inputs.Artifacts) {
			return nativeFinalConflict()
		}
		for name, port := range task.Runtime.Inputs.Parameters {
			if !pipelineir.AdvancedParameterValid(port.Type, values[name]) {
				return nativeFinalConflict()
			}
		}
		resolved := map[string]any{}
		for name, port := range task.Runtime.Inputs.Artifacts {
			v, exists := artifacts[name]
			if !exists {
				return nativeFinalConflict()
			}
			transportKey := ""
			for _, artifact := range input.Artifacts {
				if artifact.Name == pipelineir.PortIndex(task.Runtime.Inputs.Artifacts, name) {
					if transportKey != "" || artifact.S3.Endpoint != snapshot.Policy.ArtifactEndpoint || artifact.S3.Bucket != snapshot.Policy.ArtifactBucket {
						return nativeFinalConflict()
					}
					transportKey = artifact.S3.Key
				}
			}
			if transportKey == "" {
				return nativeFinalConflict()
			}
			var id uint64
			for _, artifact := range candidates {
				if nativePublishedArtifactMatches(artifact, port, v, snapshot.Policy.ArtifactEndpoint, snapshot.Policy.ArtifactBucket, transportKey) {
					if id != 0 && id != artifact.ID {
						return nativeFinalConflict()
					}
					id = artifact.ID
				}
			}
			// A running downstream Pod may be observed before the producer's
			// SQL publication. Retry the whole observation, don't invent an ID.
			if id == 0 {
				return mlExecutionConflict()
			}
			resolved[name] = map[string]any{"id": id, "type": port.Type, "metadata": v.Metadata, "sha256": v.SHA256, "size": v.Size}
		}
		raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "parameters": values, "artifacts": resolved})
		if err := st.DB.WithContext(ctx).Model(&model.PipelineTaskExecution{}).
			Where("group_id = ? AND instance_id = ? AND node_key = ?", a.GroupID, a.InstanceID, key).Update("inputs", string(raw)).Error; err != nil {
			return err
		}
	}
	return nil
}
