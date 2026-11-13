package service

import (
	"context"
	"encoding/json"
	"fmt"
	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

type NativeModelPublisher interface {
	Publish(context.Context, uint64, string, string, string, string, artifactstore.NativeModelSource, func() error) (*artifactstore.PublishedModel, error)
}

func (s *Service) RetryModelImport(ctx context.Context, actor uint64, key string, original *model.Operation) (*model.Operation, error) {
	var command modelImportCommand
	if json.Unmarshal(original.Payload, &command) != nil {
		return nil, apperr.New(apperr.CodeConflict, "模型导入命令无法读取")
	}
	return s.mutate(ctx, actor, command.GroupID, key, "model.version.create", "model.pipeline.import.retry", "model_version", original.TargetKey, map[string]string{"operationId": original.ID}, func(st *repository.Store, o *model.Operation) error {
		v, err := st.ModelVersion(ctx, command.GroupID, command.ModelID, command.VersionID, true)
		if err != nil {
			return err
		}
		if v.Revision != 1 || v.DeletedAt != nil || v.ImportOperationID == nil || *v.ImportOperationID != original.ID {
			return apperr.New(apperr.CodeConflict, "版本已修改或归档，不能重试原导入")
		}
		res := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ? AND status = 'FAILED'", original.ID).Updates(map[string]any{"status": "PENDING", "attempt": 0, "next_attempt_at": gorm.Expr("UTC_TIMESTAMP(3)"), "deadline_at": nil, "last_error": nil, "lease_owner": nil, "lease_until": nil, "finished_at": nil})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return apperr.New(apperr.CodeConflict, "导入不是失败状态")
		}
		o.Cluster, o.TargetKey = original.Cluster, original.TargetKey
		return result(o, map[string]string{"operationId": original.ID, "status": "PENDING"})
	}, modelGuard(ctx, actor, command.GroupID, command.ModelID, "model.version.create"), func(st *repository.Store) error {
		if err := (rbac.Checker{Store: st}).Require(ctx, actor, command.GroupID, "model.artifact.create"); err != nil {
			return err
		}
		run, err := st.PipelineRun(ctx, command.GroupID, command.Request.RunID, false)
		if err != nil {
			return err
		}
		return mlAllowed(ctx, st, actor, &run.MLIdentity, "pipeline", "pipeline.read")
	})
}

type modelImportCommand struct {
	Request          dto.ImportPipelineModelReq      `json:"request"`
	GroupID          uint64                          `json:"groupId"`
	ModelID          uint64                          `json:"modelId"`
	VersionID        uint64                          `json:"versionId"`
	NamespaceUID     string                          `json:"namespaceUid"`
	ProducerInstance string                          `json:"producerInstance"`
	Source           artifactstore.NativeModelSource `json:"source"`
}

func (s *Service) resolveModelImport(ctx context.Context, actor, gid uint64, in dto.ImportPipelineModelReq) (*modelImportCommand, error) {
	before, err := s.PipelineRun(ctx, actor, gid, in.RunID)
	if err != nil {
		return nil, err
	}
	if before.Engine != model.PipelineEngineNativeArgo {
		return nil, apperr.New(apperr.CodeInvalid, "训练产物直接注册目前要求 NATIVE_ARGO 运行")
	}
	scope, err := s.readVisualizationScope(ctx, actor, gid, in.RunID, "", in.InstanceID)
	if err != nil {
		return nil, err
	}
	run := scope.data.run
	if run.Engine != model.PipelineEngineNativeArgo {
		return nil, apperr.New(apperr.CodeInvalid, "训练产物直接注册目前要求 NATIVE_ARGO 运行")
	}
	output := false
	if scope.data.metadata != nil {
		for _, event := range scope.data.metadata.Events {
			if event.ArtifactID == in.ArtifactID && scope.data.executions[event.ExecutionID] != nil && nodeIOEvent(event.Type, false) {
				output = true
				break
			}
		}
	}
	if !output {
		return nil, apperr.New(apperr.CodeNotFound, "当前运行没有此已发布输出产物")
	}
	currentAttempt, err := s.Store.PipelineRunAttempt(ctx, gid, run.ID, run.InstanceID, false)
	if err != nil {
		return nil, err
	}
	if currentAttempt.EngineRevision != nil && *currentAttempt.EngineRevision == kfpengine.EngineRelease {
		if currentAttempt.NamespaceUID == nil || currentAttempt.WorkflowArchivedAt == nil || currentAttempt.Phase == nil || *currentAttempt.Phase != "SUCCEEDED" {
			return nil, apperr.New(apperr.CodeConflict, "请等待官方运行成功并完成产物归档后登记模型")
		}
		id, err := strconv.ParseInt(in.ArtifactID, 10, 64)
		if err != nil || id <= 0 {
			return nil, nativeFinalConflict()
		}
		row, err := s.Store.RuntimeObject(ctx, runtimeapi.Scope{GroupID: gid, RunID: run.ID, InstanceID: run.InstanceID}, id, "artifact")
		if err != nil {
			return nil, domainError(err)
		}
		var record runtimeapi.ArtifactRecord
		artifact := &pb.Artifact{}
		var snapshot officialAttemptSnapshot
		if json.Unmarshal(row.Value, &record) != nil || record.Name == "executor-logs" || protojson.Unmarshal(record.Artifact, artifact) != nil || artifact.GetState() != pb.Artifact_LIVE || json.Unmarshal(currentAttempt.Snapshot, &snapshot) != nil {
			return nil, nativeFinalConflict()
		}
		if err = s.checkPipelineDAGRead(ctx, actor, run); err != nil {
			return nil, err
		}
		return &modelImportCommand{Request: in, GroupID: gid, NamespaceUID: *currentAttempt.NamespaceUID, ProducerInstance: run.InstanceID, Source: artifactstore.NativeModelSource{Format: "OFFICIAL_V1", Root: snapshot.RuntimePolicy.PipelineRoot, URI: artifact.GetUri()}}, nil
	}
	row := &model.PipelineArtifact{}
	if err := s.DB.WithContext(ctx).Where("id = ? AND group_id = ? AND state = 'PUBLISHED'", in.ArtifactID, gid).First(row).Error; err != nil {
		return nil, domainError(err)
	}
	a, err := s.Store.PipelineRunAttempt(ctx, gid, run.ID, run.InstanceID, false)
	if err != nil {
		return nil, err
	}
	if a.NamespaceUID == nil || row.ProducerInstanceID == nil {
		return nil, nativeFinalConflict()
	}
	producer := &model.PipelineRunAttempt{}
	if err := s.DB.WithContext(ctx).Where("group_id = ? AND instance_id = ?", gid, *row.ProducerInstanceID).First(producer).Error; err != nil {
		return nil, domainError(err)
	}
	if producer.NamespaceUID == nil || *producer.NamespaceUID != *a.NamespaceUID {
		return nil, nativeFinalConflict()
	}
	var object artifactstore.NativeModelSource
	if json.Unmarshal(row.ObjectRef, &object) != nil || object.Format != "TAR_GZIP_V1" {
		return nil, apperr.New(apperr.CodeInvalid, "产物没有受支持的归档清单，请重新生成产物")
	}
	if err := s.checkPipelineDAGRead(ctx, actor, run); err != nil {
		return nil, err
	}
	return &modelImportCommand{Request: in, GroupID: gid, NamespaceUID: *a.NamespaceUID, ProducerInstance: *row.ProducerInstanceID, Source: object}, nil
}

func (s *Service) ImportPipelineModel(ctx context.Context, actor, gid, mid uint64, key string, in dto.ImportPipelineModelReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, gid, key, "model.version.create", "model.pipeline.import", "model_version", fmt.Sprintf("%d/%d/%s", gid, mid, in.Name), in, func(st *repository.Store, o *model.Operation) error {
		if s.ModelPublisher == nil {
			return apperr.New(apperr.CodeUnavailable, "model_store 尚未配置，不能将归档直接作为模型发布")
		}
		g, err := mlGroup(ctx, st, gid, false)
		if err != nil {
			return err
		}
		parent, err := st.RegisteredModel(ctx, gid, mid, true)
		if err != nil {
			return err
		}
		if err := activeRegisteredModel(parent, in.Revision); err != nil {
			return err
		}
		// Resolution performs database-only reads for the native engine, not object
		// storage I/O. All external copying is handled after the durable commit.
		resolver := *s
		resolver.Store, resolver.DB = st, st.DB
		command, err := resolver.resolveModelImport(ctx, actor, gid, in)
		if err != nil {
			return err
		}
		if g.NamespaceUID == nil || *g.NamespaceUID != command.NamespaceUID {
			return nativeFinalConflict()
		}
		artifactID, _ := strconv.ParseUint(in.ArtifactID, 10, 64)
		n := &model.ModelVersion{GroupID: gid, ModelID: mid, Name: in.Name, Description: in.Description, SourcePipelineRunID: &in.RunID, SourcePipelineInstanceID: &in.InstanceID, SourcePipelineArtifactID: &artifactID, ImportOperationID: &o.ID, CreatedBy: &actor, Revision: 1, Status: "DRAFT"}
		if err := st.DB.WithContext(ctx).Create(n).Error; err != nil {
			return err
		}
		if err := st.UpdateML(ctx, parent, gid, mid, parent.Revision, map[string]any{"revision": parent.Revision + 1}); err != nil {
			return err
		}
		command.ModelID, command.VersionID = mid, n.ID
		o.Payload, err = json.Marshal(command)
		if err != nil {
			return err
		}
		o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(n.ID, 10), 1
		o.Status, o.Step = "PENDING", "MODEL_EXPORT_PENDING"
		return result(o, map[string]any{"version": n, "modelRevision": parent.Revision + 1})
	}, modelGuard(ctx, actor, gid, mid, "model.version.create"), func(st *repository.Store) error {
		if err := (rbac.Checker{Store: st}).Require(ctx, actor, gid, "model.artifact.create"); err != nil {
			return err
		}
		run, err := st.PipelineRun(ctx, gid, in.RunID, false)
		if err != nil {
			return err
		}
		return mlAllowed(ctx, st, actor, &run.MLIdentity, "pipeline", "pipeline.read")
	})
}

func (s *Service) ExecuteModelImport(ctx context.Context, c *repository.Claim, guard func() error) error {
	if !time.Now().Before(c.Deadline) {
		return &k8s.Permanent{Reason: "模型导入超过操作期限，请核对存储后显式重试"}
	}
	ctx, cancel := context.WithDeadline(ctx, c.Deadline)
	defer cancel()
	var command modelImportCommand
	if json.Unmarshal(c.Payload, &command) != nil {
		return &k8s.Permanent{Reason: "模型导入命令无效"}
	}
	in := command.Request
	check := func(st *repository.Store) error {
		group, err := mlGroup(ctx, st, command.GroupID, false)
		if err != nil {
			return err
		}
		if group.NamespaceUID == nil || *group.NamespaceUID != command.NamespaceUID {
			return nativeFinalConflict()
		}
		parent, err := st.RegisteredModel(ctx, command.GroupID, command.ModelID, false)
		if err != nil {
			return err
		}
		if parent.DeletedAt != nil || parent.Status != "ACTIVE" {
			return apperr.New(apperr.CodeConflict, "模型已归档")
		}
		for _, permission := range []string{"model.version.create", "model.artifact.create"} {
			if err := modelAllowed(ctx, st, c.ActorID, parent, permission); err != nil {
				return err
			}
		}
		v, err := st.ModelVersion(ctx, command.GroupID, command.ModelID, command.VersionID, false)
		if err != nil {
			return err
		}
		if v.ImportOperationID == nil || *v.ImportOperationID != c.ID || v.Revision != 1 || v.DeletedAt != nil {
			return apperr.New(apperr.CodeConflict, "模型版本已修改或归档，导入不能覆盖新内容")
		}
		run, err := st.PipelineRun(ctx, command.GroupID, in.RunID, false)
		if err != nil {
			return err
		}
		if run.InstanceID != in.InstanceID || run.DeletedAt != nil {
			return nativeFinalConflict()
		}
		return mlAllowed(ctx, st, c.ActorID, &run.MLIdentity, "pipeline", "pipeline.read")
	}
	if err := check(s.Store); err != nil {
		return &k8s.Permanent{Reason: "导入目标、来源或当前权限已变化"}
	}
	if s.ModelPublisher == nil {
		return fmt.Errorf("模型导入存储尚未启用")
	}
	fresh, err := s.resolveModelImport(ctx, c.ActorID, command.GroupID, in)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(fresh.Source)
	b, _ := json.Marshal(command.Source)
	if string(a) != string(b) || fresh.NamespaceUID != command.NamespaceUID || fresh.ProducerInstance != command.ProducerInstance {
		return &k8s.Permanent{Reason: "导入来源清单已变化"}
	}
	publishGuard := func() error {
		if err := guard(); err != nil {
			return err
		}
		return check(s.Store)
	}
	published, err := s.ModelPublisher.Publish(ctx, command.GroupID, command.NamespaceUID, command.ProducerInstance, c.ID, in.FileName, command.Source, publishGuard)
	if err != nil {
		return err
	}
	if err := guard(); err != nil {
		return err
	}
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		if err := st.Fence(ctx, c); err != nil {
			return err
		}
		if err := check(st); err != nil {
			return &k8s.Permanent{Reason: "模型导入完成前目标或权限已变化"}
		}
		artifact := &model.ModelArtifact{GroupID: command.GroupID, ModelID: command.ModelID, VersionID: command.VersionID, Name: in.ArtifactName, URI: published.URI, ModelFormat: in.ModelFormat, ModelFormatVersion: in.ModelFormatVersion, Digest: published.Digest, SizeBytes: &published.Size, StorageType: "S3", CreatedBy: &c.ActorID, Revision: 1, Status: "ACTIVE"}
		if err := st.DB.WithContext(ctx).Create(artifact).Error; err != nil {
			return err
		}
		if err := st.UpdateML(ctx, &model.ModelVersion{}, command.GroupID, command.VersionID, 1, map[string]any{"revision": 2}); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"modelId": command.ModelID, "versionId": command.VersionID, "artifact": artifact, "versionRevision": 2})
		if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", c.ID).Update("result", string(data)).Error; err != nil {
			return err
		}
		return st.Finish(ctx, c, "SUCCEEDED", "MODEL_EXPORTED", "", 0, false)
	})
}
