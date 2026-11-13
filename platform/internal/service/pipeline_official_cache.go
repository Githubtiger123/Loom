package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	api "github.com/kubeflow/pipelines/backend/api/v1beta1/go_client"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type officialCacheStore interface {
	CacheCopyConfigured() bool
	InventoryOfficial(context.Context, string, string) (*artifactstore.OfficialInventory, error)
	CopyOfficial(context.Context, string, artifactstore.OfficialInventory, string, string) (*artifactstore.OfficialInventory, error)
}

type officialCacheGrant struct {
	Source      model.PipelineRuntimeCache
	Record      runtimeExecution
	Inventories map[string]artifactstore.OfficialInventory
	Key         string
}

func officialCacheKey(policy runtimeapi.FrozenPolicy, namespaceUID string, owner uint64, fingerprint string) string {
	return mlHash([]byte(fmt.Sprintf("%s/%s/%d/%d/%s/%s", policy.GroupInstanceID, namespaceUID, policy.PipelineID, owner, policy.EngineRevision, fingerprint)))
}

func cacheGrantKey(scope runtimeapi.Scope, fingerprint string) string {
	return mlHash([]byte(fmt.Sprintf("%d/%s/%d/%s", scope.ParentDAGID, scope.TaskName, scope.IterationIndex, fingerprint)))
}

// A driver retry must preserve the original task's hit/miss decision. Otherwise
// a newly completed concurrent run could change CreateExecution's frozen config.
func existingOfficialCacheDecision(ctx context.Context, store *repository.Store, scope runtimeapi.Scope, fingerprint string) (string, bool, error) {
	key := fmt.Sprintf("%s/%d/%s/%d", metadata.ContainerExecutionTypeName, scope.ParentDAGID, scope.TaskName, scope.IterationIndex)
	row, err := store.RuntimeObjectByKey(ctx, scope, "execution", mlHash([]byte(key)))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var record runtimeExecution
	if json.Unmarshal(row.Value, &record) != nil || record.Config == nil || record.Config.FingerPrint != fingerprint {
		return "", false, nativeFinalConflict()
	}
	return record.Config.CachedMLMDExecutionID, true, nil
}

// Remote reads/copies happen outside SQL transactions. Only successful archived
// runs in the same group incarnation, namespace, pipeline, engine and creator
// are eligible. Missing/changed/expired source data yields a genuine miss.
func (s *Service) prepareOfficialCache(ctx context.Context, scope *runtimeapi.Scope, req runtimeapi.MetadataRequest) (*officialCacheGrant, error) {
	if scope.Role != "DRIVER_CONTAINER" || !validator.IsSHA256(req.Fingerprint) {
		return nil, runtimeDenied()
	}
	a, err := s.Store.PipelineRunAttempt(ctx, scope.GroupID, scope.RunID, scope.InstanceID, false)
	if err != nil {
		return nil, err
	}
	var snap officialAttemptSnapshot
	if json.Unmarshal(a.Snapshot, &snap) != nil || !snap.RuntimePolicy.CacheEnabled || snap.CreatedBy == nil {
		return nil, runtimeDenied()
	}
	if _, exists, err := existingOfficialCacheDecision(ctx, s.Store, *scope, req.Fingerprint); err != nil || exists {
		return nil, err
	}
	// Cross-Pod driver retries reuse the same already-materialized local grant.
	_, err = s.Store.RuntimeObjectByKey(ctx, *scope, "cache-execution", cacheGrantKey(*scope, req.Fingerprint))
	if err == nil {
		return &officialCacheGrant{Key: cacheGrantKey(*scope, req.Fingerprint)}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	key := officialCacheKey(snap.RuntimePolicy, scope.NamespaceUID, *snap.CreatedBy, req.Fingerprint)
	var candidates []model.PipelineRuntimeCache
	if err = s.DB.WithContext(ctx).Where("group_id = ? AND cache_key = ? AND expires_at > UTC_TIMESTAMP(3)", scope.GroupID, key).Order("id DESC").Limit(4).Find(&candidates).Error; err != nil {
		return nil, err
	}
	store, ok := s.PipelineArtifacts.(officialCacheStore)
	if !ok {
		return nil, officialRuntimeUnavailable()
	}
	for _, candidate := range candidates {
		source, err := s.Store.PipelineRun(ctx, scope.GroupID, candidate.RunID, false)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if source.DeletedAt != nil || source.InstanceID != candidate.InstanceID || source.CreatedBy == nil || *source.CreatedBy != *snap.CreatedBy {
			continue
		}
		if err = mlAllowed(ctx, s.Store, *snap.CreatedBy, &source.MLIdentity, "pipeline", "pipeline.read"); err != nil {
			continue
		}
		attempt, err := s.Store.PipelineRunAttempt(ctx, scope.GroupID, candidate.RunID, candidate.InstanceID, false)
		if err != nil {
			return nil, err
		}
		if attempt.WorkflowArchivedAt == nil || attempt.Phase == nil || *attempt.Phase != "SUCCEEDED" {
			continue
		}
		var sourceSnap officialAttemptSnapshot
		if json.Unmarshal(attempt.Snapshot, &sourceSnap) != nil || sourceSnap.RuntimePolicy.Protocol != runtimeapi.Protocol || sourceSnap.RuntimePolicy.PipelineID != snap.RuntimePolicy.PipelineID || sourceSnap.RuntimePolicy.GroupInstanceID != snap.RuntimePolicy.GroupInstanceID || attempt.NamespaceUID == nil || *attempt.NamespaceUID != scope.NamespaceUID {
			continue
		}
		row, err := s.Store.RuntimeObject(ctx, runtimeapi.Scope{GroupID: scope.GroupID, RunID: candidate.RunID, InstanceID: candidate.InstanceID}, candidate.ExecutionID, "execution")
		if err != nil {
			return nil, err
		}
		if mlHash(row.Value) != candidate.ExecutionHash {
			continue
		}
		var record runtimeExecution
		execution := &pb.Execution{}
		var inventories map[string]artifactstore.OfficialInventory
		if json.Unmarshal(row.Value, &record) != nil || !record.Published || record.Config == nil || record.Config.FingerPrint != req.Fingerprint || protojson.Unmarshal(record.Execution, execution) != nil || execution.GetLastKnownState() != pb.Execution_COMPLETE || json.Unmarshal(candidate.Inventory, &inventories) != nil {
			continue
		}
		grant := &officialCacheGrant{Source: candidate, Record: record, Inventories: map[string]artifactstore.OfficialInventory{}, Key: cacheGrantKey(*scope, req.Fingerprint)}
		valid := true
		for i, output := range record.Outputs {
			artifact := &pb.Artifact{}
			if protojson.Unmarshal(output.Artifact, artifact) != nil {
				return nil, nativeFinalConflict()
			}
			if output.Name == "executor-logs" {
				continue
			} // A cache hit must not claim another execution's logs.
			uri := strings.TrimRight(snap.RuntimePolicy.PipelineRoot, "/") + fmt.Sprintf("/__cache/%d/%d", candidate.ID, artifact.GetId())
			if inv, exists := inventories[output.Name]; exists {
				current, err := store.InventoryOfficial(ctx, sourceSnap.RuntimePolicy.PipelineRoot, inv.URI)
				if err != nil {
					var app *apperr.Error
					if !errors.As(err, &app) || (app.Code != apperr.CodeNotFound && app.Code != apperr.CodeConflict) {
						return nil, err
					}
					valid = false
					break
				}
				if current.Digest() != inv.Digest() {
					valid = false
					break
				}
				copied, err := store.CopyOfficial(ctx, sourceSnap.RuntimePolicy.PipelineRoot, inv, snap.RuntimePolicy.PipelineRoot, uri)
				if err != nil {
					return nil, err
				}
				grant.Inventories[output.Name] = *copied
			} else if artifact.GetType() != "system.Metrics" && artifact.GetType() != "system.ClassificationMetrics" {
				valid = false
				break
			}
			artifact.Uri = proto.String(uri)
			raw, err := runtimeapi.Encode(artifact)
			if err != nil {
				return nil, err
			}
			grant.Record.Outputs[i].Artifact = raw
		}
		if valid {
			return grant, nil
		}
	}
	return nil, nil
}

func (tx *runtimeTxn) lookupCache(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if !tx.policy.CacheEnabled || tx.scope.Role != "DRIVER_CONTAINER" || !validator.IsSHA256(req.Fingerprint) {
		return nil, runtimeDenied()
	}
	hit := false
	reply := &runtimeapi.MetadataReply{Hit: &hit}
	if id, exists, err := existingOfficialCacheDecision(tx.ctx, tx.store, tx.scope, req.Fingerprint); err != nil {
		return nil, err
	} else if exists {
		hit, reply.ExecutionID = id != "", id
		return reply, nil
	}
	key := cacheGrantKey(tx.scope, req.Fingerprint)
	row, err := tx.store.RuntimeObjectByKey(tx.ctx, tx.scope, "cache-execution", key)
	if err == nil {
		hit = true
		reply.ExecutionID = strconv.FormatInt(row.ID, 10)
		return reply, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	grant := tx.cache
	if grant == nil {
		return reply, nil
	}
	if grant.Key != key || grant.Source.GroupID != tx.scope.GroupID || grant.Source.ExpiresAt.Before(time.Now()) {
		return nil, runtimeDenied()
	}
	// Recheck source deletion, publication and retention after external copies.
	var source model.PipelineRuntimeCache
	if err = tx.store.DB.WithContext(tx.ctx).Where("id = ? AND group_id = ? AND expires_at > UTC_TIMESTAMP(3)", grant.Source.ID, tx.scope.GroupID).First(&source).Error; err != nil {
		return nil, err
	}
	sourceRun, err := tx.store.PipelineRun(tx.ctx, tx.scope.GroupID, source.RunID, false)
	if err != nil {
		return nil, err
	}
	if sourceRun.DeletedAt != nil || sourceRun.InstanceID != source.InstanceID {
		return nil, runtimeDenied()
	}
	currentRun, err := tx.store.PipelineRun(tx.ctx, tx.scope.GroupID, tx.scope.RunID, false)
	if err != nil {
		return nil, err
	}
	if currentRun.CreatedBy == nil || sourceRun.CreatedBy == nil || *currentRun.CreatedBy != *sourceRun.CreatedBy {
		return nil, runtimeDenied()
	}
	if err = mlAllowed(tx.ctx, tx.store, *currentRun.CreatedBy, &sourceRun.MLIdentity, "pipeline", "pipeline.read"); err != nil {
		return nil, err
	}
	sourceRow, err := tx.store.RuntimeObject(tx.ctx, runtimeapi.Scope{GroupID: source.GroupID, RunID: source.RunID, InstanceID: source.InstanceID}, source.ExecutionID, "execution")
	if err != nil {
		return nil, err
	}
	if mlHash(sourceRow.Value) != source.ExecutionHash {
		return nil, nativeFinalConflict()
	}
	row = tx.newObject("cache-execution", "", tx.scope.ParentDAGID)
	row.ObjectKey = key
	record := grant.Record
	copyConfig := *record.Config
	copyConfig.ParentDagID, copyConfig.TaskName = tx.scope.ParentDAGID, tx.scope.TaskName
	iteration := tx.scope.IterationIndex
	copyConfig.IterationIndex = &iteration
	record.Config = &copyConfig
	execution := &pb.Execution{}
	if protojson.Unmarshal(record.Execution, execution) != nil {
		return nil, nativeFinalConflict()
	}
	record.Outputs = nil
	if err = tx.store.SaveRuntimeObject(tx.ctx, row, map[string]any{}); err != nil {
		return nil, err
	}
	for _, output := range grant.Record.Outputs {
		if output.Name == "executor-logs" {
			continue
		}
		artifact := &pb.Artifact{}
		if protojson.Unmarshal(output.Artifact, artifact) != nil {
			return nil, nativeFinalConflict()
		}
		a := tx.newObject("artifact", fmt.Sprintf("cache/%d/%s", row.ID, output.Name), row.ID)
		if err = tx.store.SaveRuntimeObject(tx.ctx, a, map[string]any{}); err != nil {
			return nil, err
		}
		artifact.Id = proto.Int64(a.ID)
		artifact.Name = proto.String(strconv.FormatInt(a.ID, 10))
		output.Artifact, err = runtimeapi.Encode(artifact)
		if err != nil {
			return nil, err
		}
		if err = tx.store.SaveRuntimeObject(tx.ctx, a, output); err != nil {
			return nil, err
		}
		record.Outputs = append(record.Outputs, output)
	}
	execution.Id = proto.Int64(row.ID)
	record.Execution, err = runtimeapi.Encode(execution)
	if err != nil {
		return nil, err
	}
	if err = tx.store.SaveRuntimeObject(tx.ctx, row, &record); err != nil {
		return nil, err
	}
	hit = true
	reply.ExecutionID = strconv.FormatInt(row.ID, 10)
	return reply, nil
}

func (tx *runtimeTxn) publishCache(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if !tx.policy.CacheEnabled || tx.scope.Role != "LAUNCHER" {
		return nil, runtimeDenied()
	}
	task := &api.Task{}
	if protojson.Unmarshal(req.Task, task) != nil || task.GetNamespace() != tx.scope.Namespace || task.GetPipelineName() != "pipeline/"+tx.policy.PipelineName || task.GetRunId() != tx.scope.WorkflowUID || task.GetMlmdExecutionID() != strconv.FormatInt(tx.scope.ExecutionID, 10) || !validator.IsSHA256(task.GetFingerprint()) {
		return nil, runtimeDenied()
	}
	_, record, _, err := tx.execution(tx.scope.ExecutionID, true)
	if err != nil {
		return nil, err
	}
	if record.AssignedPodUID != tx.scope.PodUID || record.Config.FingerPrint != task.GetFingerprint() {
		return nil, runtimeDenied()
	}
	row := tx.newObject("cache-candidate", strconv.FormatInt(tx.scope.ExecutionID, 10), tx.scope.ExecutionID)
	existing, err := tx.store.RuntimeObjectByKey(tx.ctx, tx.scope, row.Kind, row.ObjectKey)
	if err == nil {
		row = existing
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err = tx.store.SaveRuntimeObject(tx.ctx, row, map[string]string{"fingerprint": task.GetFingerprint()}); err != nil {
		return nil, err
	}
	return &runtimeapi.MetadataReply{Published: true}, nil // Candidate only; lookup cannot see this row.
}

func (tx *runtimeTxn) publishCached(req runtimeapi.MetadataRequest) (*runtimeapi.MetadataReply, error) {
	if tx.scope.Role != "DRIVER_CONTAINER" || !tx.policy.CacheEnabled || req.State != int32(pb.Execution_CACHED) {
		return nil, runtimeDenied()
	}
	row, record, execution, err := tx.execution(req.ID, true)
	if err != nil {
		return nil, err
	}
	if record.Config.ParentDagID != tx.scope.ParentDAGID || record.Config.TaskName != tx.scope.TaskName {
		return nil, runtimeDenied()
	}
	id, err := strconv.ParseInt(record.Config.CachedMLMDExecutionID, 10, 64)
	if err != nil {
		return nil, runtimeDenied()
	}
	_, source, sourceExecution, err := tx.cacheExecution(id)
	if err != nil {
		return nil, err
	}
	expected := []byte("{}")
	if outputs := sourceExecution.GetCustomProperties()["outputs"].GetStructValue(); outputs != nil {
		expected, err = outputs.MarshalJSON()
		if err != nil {
			return nil, err
		}
	}
	if req.Parameters == nil {
		req.Parameters = map[string]*structpb.Value{}
	}
	actual, err := json.Marshal(req.Parameters)
	if err != nil {
		return nil, err
	}
	// Compare decoded values, avoiding JSON map iteration or whitespace issues.
	var left, right any
	if json.Unmarshal(expected, &left) != nil || json.Unmarshal(actual, &right) != nil {
		return nil, runtimeInvalid()
	}
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	if string(l) != string(r) || len(req.Artifacts) != len(source.Outputs) {
		return nil, runtimeDenied()
	}
	seen := make(map[string]bool, len(req.Artifacts))
	for _, output := range req.Artifacts {
		if seen[output.Name] {
			return nil, runtimeDenied()
		}
		seen[output.Name] = true
		found := false
		for _, allowed := range source.Outputs {
			left, right := &pb.Artifact{}, &pb.Artifact{}
			if output.Name == allowed.Name && protojson.Unmarshal(output.Artifact, left) == nil && protojson.Unmarshal(allowed.Artifact, right) == nil && proto.Equal(left, right) {
				found = true
				break
			}
		}
		if !found {
			return nil, runtimeDenied()
		}
	}
	raw, _ := json.Marshal(req)
	hash := mlHash(raw)
	if record.Published {
		if record.PublicationHash != hash {
			return nil, nativeFinalConflict()
		}
		return &runtimeapi.MetadataReply{Done: true}, nil
	}
	for _, output := range source.Outputs {
		artifact := &pb.Artifact{}
		if protojson.Unmarshal(output.Artifact, artifact) != nil {
			return nil, nativeFinalConflict()
		}
		if err = tx.store.DB.WithContext(tx.ctx).Model(&model.PipelineRuntimeObject{}).Where("id = ? AND instance_id = ? AND kind = 'artifact' AND parent_id = ?", artifact.GetId(), tx.scope.InstanceID, id).Update("parent_id", row.ID).Error; err != nil {
			return nil, err
		}
	}
	execution.LastKnownState = pb.Execution_CACHED.Enum()
	if execution.CustomProperties == nil {
		execution.CustomProperties = map[string]*pb.Value{}
	}
	execution.CustomProperties["outputs"] = sourceExecution.GetCustomProperties()["outputs"]
	execution.LastUpdateTimeSinceEpoch = proto.Int64(time.Now().UnixMilli())
	record.Published, record.PublicationHash, record.Outputs = true, hash, source.Outputs
	if err = tx.saveExecution(row, record, execution); err != nil {
		return nil, err
	}
	return &runtimeapi.MetadataReply{Done: true}, nil
}

func (tx *runtimeTxn) cacheExecution(id int64) (*model.PipelineRuntimeObject, *runtimeExecution, *pb.Execution, error) {
	row, err := tx.store.RuntimeObject(tx.ctx, tx.scope, id, "cache-execution")
	if err != nil {
		return nil, nil, nil, err
	}
	var record runtimeExecution
	execution := &pb.Execution{}
	if tx.scope.Role != "DRIVER_CONTAINER" || json.Unmarshal(row.Value, &record) != nil || record.Config == nil || record.Config.TaskName != tx.scope.TaskName || record.Config.ParentDagID != tx.scope.ParentDAGID || record.Config.IterationIndex == nil || *record.Config.IterationIndex != tx.scope.IterationIndex || protojson.Unmarshal(record.Execution, execution) != nil {
		return nil, nil, nil, runtimeDenied()
	}
	return row, &record, execution, nil
}

// Called outside the archive transaction. Entries are persisted in that same
// transaction only after Argo success and a stable metadata hash are confirmed.
func (s *Service) officialCacheEntries(ctx context.Context, plan *officialSubmissionPlan, rows []model.PipelineRuntimeObject) ([]model.PipelineRuntimeCache, error) {
	if !plan.Policy.CacheEnabled {
		return nil, nil
	}
	var snap officialAttemptSnapshot
	if json.Unmarshal(plan.Attempt.Snapshot, &snap) != nil || snap.CreatedBy == nil || plan.Attempt.NamespaceUID == nil {
		return nil, nativeFinalConflict()
	}
	store, ok := s.PipelineArtifacts.(officialCacheStore)
	if !ok {
		return nil, officialRuntimeUnavailable()
	}
	var entries []model.PipelineRuntimeCache
	for _, row := range rows {
		var record runtimeExecution
		execution := &pb.Execution{}
		if json.Unmarshal(row.Value, &record) != nil || record.Config == nil || protojson.Unmarshal(record.Execution, execution) != nil {
			return nil, nativeFinalConflict()
		}
		if !validator.IsSHA256(record.Config.FingerPrint) || !record.Published || execution.GetLastKnownState() != pb.Execution_COMPLETE {
			continue
		}
		_, err := s.Store.RuntimeObjectByKey(ctx, runtimeapi.Scope{GroupID: row.GroupID, RunID: row.RunID, InstanceID: row.InstanceID}, "cache-candidate", mlHash([]byte(strconv.FormatInt(row.ID, 10))))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		inventories := map[string]artifactstore.OfficialInventory{}
		for _, output := range record.Outputs {
			if output.Name == "executor-logs" {
				continue
			}
			artifact := &pb.Artifact{}
			if protojson.Unmarshal(output.Artifact, artifact) != nil {
				return nil, nativeFinalConflict()
			}
			if artifact.GetType() == "system.Metrics" || artifact.GetType() == "system.ClassificationMetrics" {
				continue
			}
			inv, err := store.InventoryOfficial(ctx, plan.Policy.PipelineRoot, artifact.GetUri())
			if err != nil {
				return nil, err
			}
			inventories[output.Name] = *inv
		}
		raw, err := json.Marshal(inventories)
		if err != nil {
			return nil, err
		}
		if len(raw) > runtimeapi.MaxMessageBytes/2 {
			return nil, apperr.New(apperr.CodeQuotaExceeded, "缓存清单过大")
		}
		entries = append(entries, model.PipelineRuntimeCache{GroupID: row.GroupID, CacheKey: officialCacheKey(plan.Policy, *plan.Attempt.NamespaceUID, *snap.CreatedBy, record.Config.FingerPrint), RunID: row.RunID, InstanceID: row.InstanceID, ExecutionID: row.ID, ExecutionHash: mlHash(row.Value), Inventory: raw, ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour)})
	}
	return entries, nil
}

func saveOfficialCacheEntries(ctx context.Context, st *repository.Store, entries []model.PipelineRuntimeCache) error {
	if len(entries) == 0 {
		return nil
	}
	return st.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error
}
