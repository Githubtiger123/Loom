package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/logarchive"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/google/uuid"
)

type PipelineLogArchiveReader interface {
	Manifest(context.Context, logarchive.Execution) (*logarchive.Manifest, error)
	Read(context.Context, logarchive.Execution, logarchive.Entry, int64, int64) (*logarchive.Chunk, error)
}

// The current business object authorizes all its attempts, including soft-deleted runs.
// Historical access deliberately does not depend on live KFP, Pod or namespace existence.
func (s *Service) pipelineArchiveExecution(ctx context.Context, actor, groupID, id uint64, instance string) (logarchive.Execution, error) {
	var empty logarchive.Execution
	user, err := s.Store.User(ctx, actor, false)
	if err != nil {
		return empty, domainError(err)
	}
	if user.Status != "ACTIVE" || user.DeletedAt != nil {
		return empty, apperr.New(apperr.CodeForbidden, "用户已停用，不能读取日志")
	}
	run, err := s.PipelineRun(ctx, actor, groupID, id)
	if err != nil {
		return empty, err
	}
	if err := mlAllowed(ctx, s.Store, actor, &run.MLIdentity, "pipeline", "pipeline.logs"); err != nil {
		return empty, err
	}
	if instance == "" {
		instance = run.InstanceID
	}
	parsed, err := uuid.Parse(instance)
	if err != nil || parsed.String() != instance {
		return empty, apperr.New(apperr.CodeInvalid, "instanceId 必须是执行记录返回的 UUID")
	}
	// Editing keeps the old ID until manual restart; prefer its original archived snapshot.
	var attempts []model.PipelineRunAttempt
	if err := s.DB.WithContext(ctx).Where("group_id = ? AND run_id = ? AND instance_id = ?", groupID, id, instance).
		Limit(1).Find(&attempts).Error; err != nil {
		return empty, err
	}
	if len(attempts) != 0 {
		var old model.PipelineRun
		if json.Unmarshal(attempts[0].Snapshot, &old) != nil || old.ID != id || old.GroupID != groupID || old.InstanceID != instance {
			return empty, apperr.New(apperr.CodeConflict, "历史执行快照身份不一致")
		}
		run = &old
	} else if instance != run.InstanceID {
		return empty, apperr.New(apperr.CodeNotFound, "该运行没有此执行实例")
	}
	if run.Cluster != s.Cluster || run.Namespace == "" {
		return empty, apperr.New(apperr.CodeConflict, "该执行不属于当前配置的归档集群")
	}
	return logarchive.Execution{Cluster: run.Cluster, Namespace: run.Namespace, InstanceID: instance}, nil
}

func (s *Service) PipelineLogArchives(ctx context.Context, actor, groupID, id uint64, instance string) (*logarchive.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	e, err := s.pipelineArchiveExecution(ctx, actor, groupID, id, instance)
	if err != nil {
		return nil, err
	}
	if s.PipelineLogArchivesReader == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "离线日志归档尚未启用")
	}
	m, err := s.PipelineLogArchivesReader.Manifest(ctx, e)
	if err != nil {
		return nil, err
	}
	if fresh, err := s.pipelineArchiveExecution(ctx, actor, groupID, id, e.InstanceID); err != nil {
		return nil, err
	} else if fresh != e {
		return nil, apperr.New(apperr.CodeConflict, "执行归档身份已变化")
	}
	// Storage keys stay server-side. The client selects the opaque entry ID only.
	for i := range m.Entries {
		m.Entries[i].Key = ""
	}
	return m, nil
}

func (s *Service) PipelineLogArchiveContent(ctx context.Context, actor, groupID, id uint64, instance, entryID, etag string, offset, limit int64) (*logarchive.Chunk, error) {
	if offset < 0 || limit < 1 || limit > logarchive.MaxChunkBytes {
		return nil, apperr.New(apperr.CodeInvalid, "offset 必须非负，limitBytes 必须为 1–1048576")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	e, err := s.pipelineArchiveExecution(ctx, actor, groupID, id, instance)
	if err != nil {
		return nil, err
	}
	if s.PipelineLogArchivesReader == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "离线日志归档尚未启用")
	}
	m, err := s.PipelineLogArchivesReader.Manifest(ctx, e)
	if err != nil {
		return nil, err
	}
	var selected *logarchive.Entry
	for i := range m.Entries {
		if m.Entries[i].ID == entryID {
			selected = &m.Entries[i]
			break
		}
	}
	if selected == nil {
		return nil, apperr.New(apperr.CodeNotFound, "该执行没有此归档日志")
	}
	if etag != selected.ETag {
		return nil, apperr.New(apperr.CodeConflict, "归档 ETag 已变化，请刷新列表")
	}
	chunk, err := s.PipelineLogArchivesReader.Read(ctx, e, *selected, offset, limit)
	if err != nil {
		return nil, err
	}
	// Pin the selected attempt while rechecking current business permissions after IO.
	if fresh, err := s.pipelineArchiveExecution(ctx, actor, groupID, id, e.InstanceID); err != nil {
		return nil, err
	} else if fresh != e {
		return nil, apperr.New(apperr.CodeConflict, "执行归档身份已变化")
	}
	return chunk, nil
}
