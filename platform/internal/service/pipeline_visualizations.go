package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineviz"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type PipelineArtifactReader interface {
	Read(context.Context, string, string, string, int64) (*artifactstore.Object, error)
}

type NativePipelineArtifactReader interface {
	ReadNative(context.Context, uint64, string, string, string, string, string, int64) (*artifactstore.Object, error)
}

type PipelineVisualization struct {
	ArtifactID    string             `json:"artifactId"`
	Name          string             `json:"name"`
	Type          string             `json:"type"`
	State         string             `json:"state"`
	NodeIDs       []string           `json:"nodeIds"`
	Views         []pipelineviz.View `json:"views"`
	ContentStatus string             `json:"contentStatus"`
}

type PipelineVisualizations struct {
	InstanceID string                  `json:"instanceId"`
	Revision   uint64                  `json:"revision"`
	ReadAt     time.Time               `json:"readAt"`
	Metadata   PipelineDataSource      `json:"metadata"`
	Items      []PipelineVisualization `json:"items"`
}

type visualizationScope struct {
	data      *pipelineDAGRead
	artifacts map[string]mlmd.Artifact
	nodes     map[string][]string
}

func (s *Service) readVisualizationScope(ctx context.Context, actor, gid, rid uint64, nodeID, instance string) (*visualizationScope, error) {
	if (nodeID != "" && !validator.IsIdempotencyKey(nodeID)) || (instance != "" && !validator.IsCatalogID(instance)) {
		return nil, apperr.New(apperr.CodeInvalid, "nodeId 或 instanceId 无效")
	}
	d, err := s.readPipelineDAGArtifacts(ctx, actor, gid, rid, nodeID, nodeID == "")
	if err != nil {
		return nil, err
	}
	if instance != "" && instance != d.run.InstanceID {
		return nil, apperr.New(apperr.CodeConflict, "运行已重建，请刷新 DAG")
	}
	out := &visualizationScope{data: d, artifacts: map[string]mlmd.Artifact{}, nodes: map[string][]string{}}
	execution := ""
	if nodeID != "" {
		for _, n := range d.dag.Instances {
			if n.ID == nodeID {
				execution = n.ExecutionID
				break
			}
		}
		if execution == "" {
			if d.graph.byID[nodeID] != nil {
				return out, nil
			} // A definition is not a loop execution.
			return nil, apperr.New(apperr.CodeNotFound, "当前运行中不存在该节点实例")
		}
	}
	if d.metadata == nil {
		return out, nil
	}
	for _, event := range d.metadata.Events {
		// Membership in this run's MLMD execution context is mandatory, including
		// cached/imported artifacts. Never perform a global artifact-ID lookup.
		if d.executions[event.ExecutionID] == nil || (execution != "" && event.ExecutionID != execution) ||
			(!nodeIOEvent(event.Type, true) && !nodeIOEvent(event.Type, false)) {
			continue
		}
		a, exists := d.metadata.Artifacts[event.ArtifactID]
		if !exists {
			continue
		}
		out.artifacts[a.ID] = a
		node := "execution-" + event.ExecutionID
		found := false
		for _, old := range out.nodes[a.ID] {
			if old == node {
				found = true
				break
			}
		}
		if !found {
			out.nodes[a.ID] = append(out.nodes[a.ID], node)
		}
	}
	return out, nil
}

func (s *Service) PipelineRunVisualizations(ctx context.Context, actor, gid, rid uint64, nodeID, instance string) (*PipelineVisualizations, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	scope, err := s.readVisualizationScope(ctx, actor, gid, rid, nodeID, instance)
	if err != nil {
		return nil, err
	}
	d := scope.data
	out := &PipelineVisualizations{InstanceID: d.run.InstanceID, Revision: d.run.Revision, ReadAt: d.dag.ReadAt, Metadata: d.dag.Metadata, Items: []PipelineVisualization{}}
	ids := make([]string, 0, len(scope.artifacts))
	for id := range scope.artifacts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.ParseUint(ids[i], 10, 64)
		b, _ := strconv.ParseUint(ids[j], 10, 64)
		return a < b
	})
	for _, id := range ids {
		a := scope.artifacts[id]
		sort.Strings(scope.nodes[id])
		item := PipelineVisualization{ArtifactID: id, Name: a.Name, Type: a.Type, State: a.State, NodeIDs: scope.nodes[id], Views: pipelineviz.Views(a), ContentStatus: "NOT_CONFIGURED"}
		if name, ok := a.Custom["display_name"].(string); ok && name != "" {
			item.Name = name
		}
		if s.PipelineArtifacts != nil {
			item.ContentStatus = "ON_DEMAND"
		}
		if a.StorageURI == "" {
			item.ContentStatus = "NO_URI"
		}
		if a.State == "DELETED" || a.State == "MARKED_FOR_DELETION" || a.State == "ABANDONED" {
			item.ContentStatus = "UNAVAILABLE"
		}
		out.Items = append(out.Items, item)
	}
	if err := s.checkPipelineDAGRead(ctx, actor, d.run); err != nil {
		return nil, err
	}
	return out, nil
}

type PipelineArtifactResult struct {
	InstanceID string                `json:"instanceId"`
	Revision   uint64                `json:"revision"`
	ArtifactID string                `json:"artifactId"`
	Preview    *pipelineviz.Preview  `json:"preview,omitempty"`
	Object     *artifactstore.Object `json:"-"`
}

func (s *Service) PipelineArtifactContent(ctx context.Context, actor, gid, rid uint64, nodeID, instance, artifactID string, download bool) (*PipelineArtifactResult, error) {
	if instance == "" || !validator.IsMetadataID(artifactID) {
		return nil, apperr.New(apperr.CodeInvalid, "请提供列表返回的 instanceId 和 artifactId")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	scope, err := s.readVisualizationScope(ctx, actor, gid, rid, nodeID, instance)
	if err != nil {
		return nil, err
	}
	if scope.data.dag.Metadata.Status != "AVAILABLE" {
		return nil, apperr.New(apperr.CodeUnavailable, "该运行的 MLMD 产物元数据尚不可读")
	}
	a, ok := scope.artifacts[artifactID]
	if !ok {
		return nil, apperr.New(apperr.CodeNotFound, "该运行/节点没有此产物关联")
	}
	if a.State == "DELETED" || a.State == "MARKED_FOR_DELETION" || a.State == "ABANDONED" {
		return nil, apperr.New(apperr.CodeNotFound, "产物已被清理")
	}
	if s.PipelineArtifacts == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "产物对象存储读取尚未启用")
	}
	suffix := ""
	limit := artifactstore.MaxDownloadBytes
	if !download {
		limit = artifactstore.MaxPreviewBytes
		kind := pipelineviz.FileKind(a)
		if kind == "" {
			return nil, apperr.New(apperr.CodeInvalid, "该产物不支持普通预览，可下载原文件")
		}
		if kind == "KFP_PREVIEW" {
			var valid bool
			suffix, valid = pipelineviz.PreviewSuffix(a)
			if !valid {
				return nil, apperr.New(apperr.CodeInvalid, "_kfp_preview 声明无效")
			}
			limit = artifactstore.MaxKFPPreviewBytes
		}
	}
	var obj *artifactstore.Object
	if scope.data.run.Engine == model.PipelineEngineNativeArgo {
		obj, err = s.readNativeArtifactContent(ctx, scope.data.run, artifactID, suffix, limit)
		if obj != nil {
			archiveName := obj.Name == "artifact.tar.gz"
			zipName := strings.HasSuffix(obj.Name, ".zip")
			obj.Name = a.Name
			if obj.Name == "" {
				obj.Name = "artifact"
			}
			if archiveName {
				if !download {
					return nil, apperr.New(apperr.CodeInvalid, "目录产物请下载 tar.gz 归档")
				}
				obj.Name += ".tar.gz"
			}
			if zipName {
				obj.Name += ".zip"
			}
		}
	} else {
		obj, err = s.PipelineArtifacts.Read(ctx, scope.data.run.Namespace, a.StorageURI, suffix, limit)
	}
	if err != nil {
		return nil, err
	}
	out := &PipelineArtifactResult{InstanceID: scope.data.run.InstanceID, Revision: scope.data.run.Revision, ArtifactID: a.ID}
	if download {
		out.Object = obj
	} else {
		out.Preview, err = pipelineviz.ParsePreview(a, obj)
		if err != nil {
			return nil, err
		}
	}
	// Buffer first, then recheck membership and attempt identity before delivering bytes.
	if err := s.checkPipelineDAGRead(ctx, actor, scope.data.run); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) readNativeArtifactContent(ctx context.Context, run *model.PipelineRun, artifactID, suffix string, limit int64) (*artifactstore.Object, error) {
	attempt, err := s.Store.PipelineRunAttempt(ctx, run.GroupID, run.ID, run.InstanceID, false)
	if err != nil {
		return nil, err
	}
	if attempt.EngineRevision != nil && *attempt.EngineRevision == kfpengine.EngineRelease {
		return s.readOfficialArtifactContent(ctx, run, attempt, artifactID, suffix, limit)
	}
	reader, ok := s.PipelineArtifacts.(NativePipelineArtifactReader)
	if !ok {
		return nil, apperr.New(apperr.CodeUnavailable, "原生产物存储读取尚未配置")
	}
	row := &model.PipelineArtifact{}
	if err := s.DB.WithContext(ctx).Where("id = ? AND group_id = ? AND state = ?", artifactID, run.GroupID, "PUBLISHED").First(row).Error; err != nil {
		return nil, domainError(err)
	}
	a, err := s.Store.PipelineRunAttempt(ctx, run.GroupID, run.ID, run.InstanceID, false)
	if err != nil {
		return nil, err
	}
	if a.NamespaceUID == nil {
		return nil, nativeFinalConflict()
	}
	producerInstance := run.InstanceID
	if row.ProducerInstanceID == nil {
		return nil, nativeFinalConflict()
	}
	if *row.ProducerInstanceID != run.InstanceID {
		// Caller already resolved artifact membership through the authorized
		// task outputs. Recheck the producer's immutable space UID as well.
		producer := &model.PipelineRunAttempt{}
		err := s.DB.WithContext(ctx).Where("group_id = ? AND instance_id = ?", run.GroupID, *row.ProducerInstanceID).First(producer).Error
		if err != nil {
			return nil, err
		}
		if producer.NamespaceUID == nil || *producer.NamespaceUID != *a.NamespaceUID {
			return nil, nativeFinalConflict()
		}
		producerInstance = *row.ProducerInstanceID
	}
	var object struct {
		Endpoint    string `json:"endpoint"`
		Bucket      string `json:"bucket"`
		Key         string `json:"key"`
		SHA256      string `json:"sha256"`
		Size        int64  `json:"size"`
		Format      string `json:"format"`
		Kind        string `json:"kind"`
		DecodedSize int64  `json:"decodedSize"`
		Entries     int    `json:"entries"`
	}
	if json.Unmarshal(row.ObjectRef, &object) != nil || !validator.IsSHA256(object.SHA256) {
		return nil, nativeFinalConflict()
	}
	readLimit := limit
	if object.Format != "" {
		readLimit = artifactstore.MaxDownloadBytes
	}
	obj, err := reader.ReadNative(ctx, run.GroupID, *a.NamespaceUID, producerInstance, object.Endpoint, object.Bucket, object.Key, readLimit)
	if err != nil {
		return nil, err
	}
	if int64(len(obj.Data)) != object.Size || fmt.Sprintf("%x", sha256.Sum256(obj.Data)) != object.SHA256 {
		return nil, apperr.New(apperr.CodeConflict, "产物内容与发布摘要不一致")
	}
	if object.Format != "" {
		if object.Format != "TAR_GZIP_V1" {
			return nil, nativeFinalConflict()
		}
		if err := artifactstore.DecodeNativeFile(obj, object.Kind, object.DecodedSize, object.Entries, limit); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

func (s *Service) readOfficialArtifactContent(ctx context.Context, run *model.PipelineRun, attempt *model.PipelineRunAttempt, artifactID, suffix string, limit int64) (*artifactstore.Object, error) {
	reader, ok := s.PipelineArtifacts.(interface {
		ReadOfficial(context.Context, string, string, string, int64) (*artifactstore.Object, error)
	})
	if !ok {
		return nil, apperr.New(apperr.CodeUnavailable, "官方运行产物读取尚未配置")
	}
	id, err := strconv.ParseInt(artifactID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != artifactID {
		return nil, nativeFinalConflict()
	}
	scope := runtimeapi.Scope{GroupID: run.GroupID, RunID: run.ID, InstanceID: run.InstanceID}
	row, err := s.Store.RuntimeObject(ctx, scope, id, "artifact")
	if err != nil {
		return nil, domainError(err)
	}
	var artifact runtimeapi.ArtifactRecord
	parsed := &pb.Artifact{}
	if json.Unmarshal(row.Value, &artifact) != nil || protojson.Unmarshal(artifact.Artifact, parsed) != nil || parsed.GetId() != id {
		return nil, nativeFinalConflict()
	}
	producer, err := s.Store.RuntimeObject(ctx, scope, row.ParentID, "execution")
	if err != nil {
		return nil, err
	}
	var execution runtimeExecution
	if json.Unmarshal(producer.Value, &execution) != nil || !execution.Published {
		return nil, nativeFinalConflict()
	}
	member := false
	for _, output := range execution.Outputs {
		published := &pb.Artifact{}
		if protojson.Unmarshal(output.Artifact, published) != nil {
			return nil, nativeFinalConflict()
		}
		if published.GetId() == id {
			member = true
		}
	}
	var frozen struct {
		RuntimePolicy runtimeapi.FrozenPolicy `json:"runtimePolicy"`
	}
	if !member || json.Unmarshal(attempt.Snapshot, &frozen) != nil || frozen.RuntimePolicy.Protocol != runtimeapi.Protocol || !validator.IsObjectURIWithin(frozen.RuntimePolicy.PipelineRoot, parsed.GetUri()) {
		return nil, nativeFinalConflict()
	}
	if suffix == "" && limit == artifactstore.MaxDownloadBytes {
		if directoryReader, ok := s.PipelineArtifacts.(interface {
			DownloadOfficial(context.Context, string, string, int64) (*artifactstore.Object, error)
		}); ok {
			return directoryReader.DownloadOfficial(ctx, frozen.RuntimePolicy.PipelineRoot, parsed.GetUri(), limit)
		}
	}
	return reader.ReadOfficial(ctx, frozen.RuntimePolicy.PipelineRoot, parsed.GetUri(), suffix, limit)
}
