package service

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineviz"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type PipelineArtifactReader interface {
	Read(context.Context, string, string, string, int64) (*artifactstore.Object, error)
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
	obj, err := s.PipelineArtifacts.Read(ctx, scope.data.run.Namespace, a.StorageURI, suffix, limit)
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
