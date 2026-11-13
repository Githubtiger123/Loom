package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/infra/logarchive"
	"github.com/githubtiger123/loom/platform/internal/model"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

type officialObjectReader interface {
	InventoryOfficial(context.Context, string, string) (*artifactstore.OfficialInventory, error)
	ReadOfficialRange(context.Context, string, string, string, int64, int64) (*artifactstore.Object, int64, error)
}

func (s *Service) officialLogAttempt(ctx context.Context, gid, rid uint64, instance string) (*model.PipelineRunAttempt, bool, error) {
	a, err := s.Store.PipelineRunAttempt(ctx, gid, rid, instance, false)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if a.Engine != model.PipelineEngineNativeArgo {
		return nil, false, nil
	}
	if a.EngineRevision == nil || *a.EngineRevision != kfpengine.EngineRelease {
		return nil, false, officialRuntimeUnavailable()
	}
	return a, true, nil
}

func (s *Service) officialLogManifest(ctx context.Context, e logarchive.Execution, a *model.PipelineRunAttempt) (*logarchive.Manifest, error) {
	reader, ok := s.PipelineArtifacts.(officialObjectReader)
	if !ok {
		return nil, officialRuntimeUnavailable()
	}
	var snapshot officialAttemptSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil || snapshot.RuntimePolicy.Protocol != runtimeapi.Protocol {
		return nil, nativeFinalConflict()
	}
	scope := runtimeapi.Scope{GroupID: a.GroupID, RunID: a.RunID, InstanceID: a.InstanceID}
	rows, err := s.Store.RuntimeObjects(ctx, scope, "execution")
	if err != nil {
		return nil, err
	}
	history, err := s.Store.RuntimeObjects(ctx, scope, "execution-history")
	if err != nil {
		return nil, err
	}
	rows = append(rows, history...)
	m := &logarchive.Manifest{Version: 1, Source: "OFFICIAL_LAUNCHER", Execution: e, Complete: a.WorkflowArchivedAt != nil, Entries: []logarchive.Entry{}}
	for _, row := range rows {
		var record runtimeExecution
		execution := &pb.Execution{}
		if json.Unmarshal(row.Value, &record) != nil || record.Config == nil || protojson.Unmarshal(record.Execution, execution) != nil {
			return nil, nativeFinalConflict()
		}
		if record.Config.ExecutionType != metadata.ContainerExecutionTypeName || record.Config.NotTriggered || execution.GetLastKnownState() == pb.Execution_CACHED {
			continue
		}
		if record.Replayed {
			continue
		}
		found := false
		if record.Published {
			for _, output := range record.Outputs {
				if output.Name != "executor-logs" {
					continue
				}
				artifact := &pb.Artifact{}
				if protojson.Unmarshal(output.Artifact, artifact) != nil || artifact.GetState() != pb.Artifact_LIVE || record.AssignedPodUID == "" {
					return nil, nativeFinalConflict()
				}
				inventory, err := reader.InventoryOfficial(ctx, snapshot.RuntimePolicy.PipelineRoot, artifact.GetUri())
				if err != nil {
					return nil, err
				}
				if inventory.Directory || len(inventory.Files) != 1 {
					return nil, nativeFinalConflict()
				}
				file := inventory.Files[0]
				entryID := mlHash([]byte(fmt.Sprintf("%d/%s/%d", execution.GetId(), record.AssignedPodUID, artifact.GetId())))
				m.Entries = append(m.Entries, logarchive.Entry{ID: entryID, TaskID: fmt.Sprintf("execution-%d", execution.GetId()), Pod: execution.GetCustomProperties()["pod_name"].GetStringValue(), PodUID: record.AssignedPodUID, Container: "main", Key: artifact.GetUri(), SizeBytes: file.Size, ETag: file.ETag})
				found = true
			}
		}
		if !found {
			m.Complete = false
		}
	}
	return m, nil
}

func (s *Service) officialLogChunk(ctx context.Context, e logarchive.Execution, a *model.PipelineRunAttempt, id, etag string, offset, limit int64) (*logarchive.Chunk, error) {
	m, err := s.officialLogManifest(ctx, e, a)
	if err != nil {
		return nil, err
	}
	var selected *logarchive.Entry
	for i := range m.Entries {
		if m.Entries[i].ID == id {
			selected = &m.Entries[i]
			break
		}
	}
	if selected == nil {
		return nil, apperr.New(apperr.CodeNotFound, "该执行没有此已发布日志")
	}
	if selected.ETag != etag {
		return nil, apperr.New(apperr.CodeConflict, "日志 ETag 已变化，请刷新列表")
	}
	var snapshot officialAttemptSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil {
		return nil, nativeFinalConflict()
	}
	reader := s.PipelineArtifacts.(officialObjectReader)
	obj, size, err := reader.ReadOfficialRange(ctx, snapshot.RuntimePolicy.PipelineRoot, selected.Key, etag, offset, limit)
	if err != nil {
		return nil, err
	}
	next := offset + int64(len(obj.Data))
	return &logarchive.Chunk{Execution: e, EntryID: id, ETag: etag, Offset: offset, NextOffset: next, SizeBytes: size, EOF: next == size, Text: strings.ToValidUTF8(string(obj.Data), "�")}, nil
}
