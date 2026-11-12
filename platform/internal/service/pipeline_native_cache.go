package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm/clause"
)

func (s *Service) nativeCacheSeeds(ctx context.Context, in nativeStagingInput) (map[string]k8s.NativeCacheSeed, error) {
	rows := []model.PipelineTaskCache{}
	if err := s.DB.WithContext(ctx).Where("group_id = ? AND pipeline_id = ? AND namespace_uid = ? AND expires_at > UTC_TIMESTAMP(3)", in.Command.GroupID, in.Command.PipelineRun.PipelineID, in.Space.NamespaceUID).Order("created_at DESC").Limit(128).Find(&rows).Error; err != nil {
		return nil, err
	}
	seeds := map[string]k8s.NativeCacheSeed{}
	size := 0
	for _, row := range rows {
		if !validator.IsSHA256(row.Fingerprint) || !validator.IsBoundedJSONObject(row.Outputs, 128<<10, 24, 10000) {
			return nil, nativeFinalConflict()
		}
		size += len(row.Outputs)
		if size > 256<<10 {
			break
		}
		seeds[row.Fingerprint] = k8s.NativeCacheSeed{SourceTaskID: row.SourceTaskID, SourceInstanceID: row.SourceInstanceID, NodeID: row.ArgoNodeID, Outputs: row.Outputs, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
	}
	return seeds, nil
}

// Cache only declared outputs. Argo adds main-logs and runtime fields to real
// Pod results, including same-run memoization entries; those are not reusable
// component outputs and must be normalized on both sides of a cache hit.
func nativeCacheOutputs(task pipelineir.Task, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, nativeFinalConflict()
	}
	var parameters []json.RawMessage
	if json.Unmarshal(fields["parameters"], &parameters) != nil {
		return nil, nativeFinalConflict()
	}
	keptParameters := []json.RawMessage{}
	for _, parameter := range parameters {
		var header struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(parameter, &header) != nil {
			return nil, nativeFinalConflict()
		}
		if header.Name == "loom-bundle" {
			keptParameters = append(keptParameters, parameter)
		}
	}
	if len(keptParameters) != 1 {
		return nil, nativeFinalConflict()
	}
	var artifacts []json.RawMessage
	if len(fields["artifacts"]) > 0 && json.Unmarshal(fields["artifacts"], &artifacts) != nil {
		return nil, nativeFinalConflict()
	}
	declared := map[string]bool{}
	for name := range task.Runtime.Outputs.Artifacts {
		declared[pipelineir.PortIndex(task.Runtime.Outputs.Artifacts, name)] = true
	}
	kept := []json.RawMessage{}
	for _, artifact := range artifacts {
		var header struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(artifact, &header) != nil {
			return nil, nativeFinalConflict()
		}
		if declared[header.Name] {
			kept = append(kept, artifact)
			delete(declared, header.Name)
		}
	}
	if len(declared) != 0 {
		return nil, nativeFinalConflict()
	}
	return json.Marshal(map[string]any{"parameters": keptParameters, "artifacts": kept})
}

func nativeCacheKey(task pipelineir.Task, node k8s.PipelineWorkflowNode) (string, error) {
	var inputs struct {
		Parameters []struct{ Name, Value string } `json:"parameters"`
	}
	if json.Unmarshal(node.Inputs, &inputs) != nil {
		return "", nativeFinalConflict()
	}
	values, artifacts := "", ""
	for _, p := range inputs.Parameters {
		if p.Name == "loom-values" {
			values = p.Value
		}
		if p.Name == "loom-artifacts" {
			artifacts = p.Value
		}
	}
	if values == "" || artifacts == "" {
		return "", nativeFinalConflict()
	}
	return pipelineir.CacheFingerprint(pipelineir.CacheIdentity(task), values, artifacts), nil
}

func publishNativeCache(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, task pipelineir.Task, row *model.PipelineTaskExecution, node k8s.PipelineWorkflowNode) error {
	if !task.Cache || node.CacheHit || node.Phase != "SUCCEEDED" || node.Type != "Pod" {
		return nil
	}
	key, err := nativeCacheKey(task, node)
	if err != nil {
		return err
	}
	if node.CacheKey != "" && node.CacheKey != key {
		return nativeFinalConflict()
	}
	var snapshot nativeStagingSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil {
		return nativeFinalConflict()
	}
	raw, err := nativeCacheOutputs(task, node.Outputs)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	cache := model.PipelineTaskCache{GroupID: a.GroupID, PipelineID: snapshot.Input.Command.PipelineRun.PipelineID, NamespaceUID: snapshot.Input.Space.NamespaceUID, Fingerprint: key, SourceTaskID: row.ID, SourceInstanceID: a.InstanceID, ArgoNodeID: node.ID, Outputs: raw, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	return st.DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "group_id"}, {Name: "pipeline_id"}, {Name: "namespace_uid"}, {Name: "fingerprint"}}, DoUpdates: clause.AssignmentColumns([]string{"source_task_id", "source_instance_id", "argo_node_id", "outputs", "created_at", "expires_at"})}).Create(&cache).Error
}

func syncNativeCacheHit(ctx context.Context, st *repository.Store, a *model.PipelineRunAttempt, plan *pipelineir.Plan, task pipelineir.Task, row *model.PipelineTaskExecution, node k8s.PipelineWorkflowNode, runtime map[string]any) error {
	ref, ok := nativePipelineRef(a)
	if !ok || !task.Cache || (node.Type != "Pod" && node.Type != "Retry") || node.Phase != "SUCCEEDED" || node.CacheName != k8s.NativeCacheConfigMapName(ref) {
		return nativeFinalConflict()
	}
	key, err := nativeCacheKey(task, node)
	if err != nil || key != node.CacheKey {
		return nativeFinalConflict()
	}
	var snapshot nativeStagingSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil {
		return nativeFinalConflict()
	}
	actual, err := nativeCacheOutputs(task, node.Outputs)
	if err != nil {
		return err
	}
	newHash, newErr := nativeJSONHash(actual)
	if newErr != nil {
		return nativeFinalConflict()
	}
	source := &model.PipelineTaskExecution{}
	if row.CacheSourceTaskID != nil {
		// Cache TTL may elapse in a long run and a later execution can replace
		// the mutable SQL entry. A previously validated hit keeps its original
		// source and output commitment; never rebind it on observation replay.
		if runtime["cacheHit"] != true || runtime["cacheOutputsHash"] != newHash {
			return nativeFinalConflict()
		}
		if err = st.DB.WithContext(ctx).Where("id = ? AND group_id = ?", *row.CacheSourceTaskID, a.GroupID).First(source).Error; err != nil {
			return err
		}
		if runtime["cacheSourceInstanceId"] != source.InstanceID {
			return nativeFinalConflict()
		}
	} else {
		seedMatches := func(seed k8s.NativeCacheSeed) (bool, error) {
			expected, err := nativeCacheOutputs(task, seed.Outputs)
			if err != nil {
				return false, err
			}
			oldHash, err := nativeJSONHash(expected)
			return oldHash == newHash, err
		}
		seed, found := snapshot.Policy.CacheSeeds[key]
		matches := false
		if found {
			matches, err = seedMatches(seed)
			if err != nil {
				return err
			}
		}
		if !matches {
			// An expired frozen seed can be recomputed inside this Workflow.
			// Only its own newly published entry is a valid alternate source.
			cache := &model.PipelineTaskCache{}
			if err = st.DB.WithContext(ctx).Where("group_id = ? AND pipeline_id = ? AND namespace_uid = ? AND fingerprint = ? AND source_instance_id = ?", a.GroupID, snapshot.Input.Command.PipelineRun.PipelineID, snapshot.Input.Space.NamespaceUID, key, a.InstanceID).First(cache).Error; err != nil {
				return err
			}
			seed = k8s.NativeCacheSeed{SourceTaskID: cache.SourceTaskID, SourceInstanceID: cache.SourceInstanceID, NodeID: cache.ArgoNodeID, Outputs: cache.Outputs}
			matches, err = seedMatches(seed)
			if err != nil || !matches {
				return nativeFinalConflict()
			}
		}
		if err = st.DB.WithContext(ctx).Where("id = ? AND group_id = ? AND instance_id = ?", seed.SourceTaskID, a.GroupID, seed.SourceInstanceID).First(source).Error; err != nil {
			return err
		}
		if source.ArgoNodeID == nil || *source.ArgoNodeID != seed.NodeID {
			return nativeFinalConflict()
		}
	}
	if source.ID == row.ID || source.Phase != "SUCCEEDED" || source.ArgoNodeID == nil {
		return nativeFinalConflict()
	}
	var state struct {
		Sealed bool `json:"outputsSealed"`
	}
	if json.Unmarshal(source.RuntimeState, &state) != nil || !state.Sealed {
		return nativeFinalConflict()
	}
	row.Outputs = source.Outputs
	row.CacheSourceTaskID = &source.ID
	runtime["outputsSealed"] = true
	runtime["cacheHit"] = true
	runtime["cacheSourceInstanceId"] = source.InstanceID
	runtime["cacheOutputsHash"] = newHash
	return nil
}
