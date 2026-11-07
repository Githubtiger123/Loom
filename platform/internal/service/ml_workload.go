package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type MLAction string

const (
	MLPipelineSubmit MLAction = "pipeline.run.submit"
	MLPipelineStop   MLAction = "pipeline.run.stop"
	MLPipelineDelete MLAction = "pipeline.run.delete"
	MLPipelineEdit   MLAction = "pipeline.run.edit"
	MLTrainerCreate  MLAction = "trainer.create"
	MLTrainerStop    MLAction = "trainer.stop"
	MLTrainerDelete  MLAction = "trainer.delete"
	MLKServeCreate   MLAction = "kserve.create"
	MLKServeUpdate   MLAction = "kserve.update"
	MLKServeDeploy   MLAction = "kserve.deploy"
	MLKServeStop     MLAction = "kserve.stop"
	MLKServeDelete   MLAction = "kserve.delete"
)

// MLCommand is the versioned adapter contract. Each action has exactly one typed
// resource branch; Pipeline edit additionally carries pendingPipelineRun, which
// cannot be applied until the old execution is confirmed absent.
// Ref resolution, code/package validation and runtime policy are adapter
// responsibilities and MUST complete before side effects. No user URL is fetched.
type MLCommand struct {
	SchemaVersion      int                  `json:"schemaVersion"`
	Action             MLAction             `json:"action"`
	ResourceID         uint64               `json:"resourceId"`
	GroupID            uint64               `json:"groupId"`
	CreatedBy          *uint64              `json:"createdBy"`
	RequestedBy        uint64               `json:"requestedBy"`
	InstanceID         string               `json:"instanceId"`
	Cluster            string               `json:"cluster"`
	Namespace          string               `json:"namespace"`
	Name               string               `json:"name"`
	ExternalID         *string              `json:"externalId"`
	Revision           uint64               `json:"revision"`
	DesiredState       string               `json:"desiredState"`
	ConfigRevisionID   uint64               `json:"configRevisionId,omitempty"`
	PipelineRun        *dto.PipelineRunSpec `json:"pipelineRun,omitempty"`
	PendingPipelineRun *dto.PipelineRunSpec `json:"pendingPipelineRun,omitempty"`
	TrainerJob         *dto.TrainerJobSpec  `json:"trainerJob,omitempty"`
	KServe             *dto.KServeSpec      `json:"kserve,omitempty"`
}

func MLExecutorActions() []string {
	return []string{string(MLPipelineSubmit), string(MLPipelineStop), string(MLPipelineDelete), string(MLPipelineEdit), string(MLTrainerCreate), string(MLTrainerStop), string(MLTrainerDelete), string(MLKServeCreate), string(MLKServeUpdate), string(MLKServeDeploy), string(MLKServeStop), string(MLKServeDelete)}
}

// mlObject is an in-memory helper only; the three domain resources have separate
// tables, immutable snapshots and lifecycle entrypoints.
type mlObject struct {
	identity *model.MLIdentity
	state    *model.MLExecutionState
	row      any
	configID *uint64
}

func mlModule(target string) string {
	switch target {
	case "pipeline", "pipeline_run":
		return "pipeline"
	case "trainer_job":
		return "trainer"
	case "kserve_service":
		return "kserve"
	}
	return ""
}

func IsMLTarget(target string) bool { return mlModule(target) != "" }

func mlAllowed(ctx context.Context, st *repository.Store, actor uint64, n *model.MLIdentity, module, permission string) error {
	if err := (rbac.Checker{Store: st}).Require(ctx, actor, n.GroupID, permission); err != nil {
		return err
	}
	if n.CreatedBy != nil && *n.CreatedBy == actor {
		return nil
	}
	return (rbac.Checker{Store: st}).Require(ctx, actor, n.GroupID, module+".read.any")
}

func loadML(ctx context.Context, st *repository.Store, target string, groupID, id uint64, lock bool) (*mlObject, error) {
	switch target {
	case "pipeline_run":
		n, err := st.PipelineRun(ctx, groupID, id, lock)
		return &mlObject{identity: &n.MLIdentity, state: &n.MLExecutionState, row: n}, err
	case "trainer_job":
		n, err := st.TrainerJob(ctx, groupID, id, lock)
		return &mlObject{identity: &n.MLIdentity, state: &n.MLExecutionState, row: n}, err
	case "kserve_service":
		n, err := st.KServeService(ctx, groupID, id, lock)
		return &mlObject{identity: &n.MLIdentity, state: &n.MLExecutionState, row: n, configID: &n.ConfigRevisionID}, err
	}
	return nil, apperr.New(apperr.CodeInvalid, "无效的 ML 资源类型")
}

func mlGuard(ctx context.Context, actor, groupID, id uint64, target, permission string) mutationGuard {
	return func(st *repository.Store) error {
		n, err := loadML(ctx, st, target, groupID, id, false)
		if err != nil {
			return err
		}
		return mlAllowed(ctx, st, actor, n.identity, mlModule(target), permission)
	}
}

func pipelineGuard(ctx context.Context, actor, groupID, id uint64, permission string) mutationGuard {
	return func(st *repository.Store) error {
		p, err := st.Pipeline(ctx, groupID, id, false)
		if err != nil {
			return err
		}
		return mlAllowed(ctx, st, actor, &p.MLIdentity, "pipeline", permission)
	}
}

func mlGroup(ctx context.Context, st *repository.Store, groupID uint64, cleanup bool) (*model.Group, error) {
	g, err := st.Group(ctx, groupID, true)
	if err != nil {
		return nil, err
	}
	cleanupAllowed := cleanup && (g.Status == "DEGRADED" || g.Status == "DISABLING" || g.Status == "DISABLED")
	if g.DeletedAt != nil || (g.Status != "ACTIVE" && !cleanupAllowed) {
		return nil, apperr.New(apperr.CodeConflict, "空间尚未就绪或正在删除")
	}
	return g, nil
}

func mlNewState(g *model.Group, operationID, desired string, spec json.RawMessage) model.MLExecutionState {
	return model.MLExecutionState{InstanceID: uuid.NewString(), Cluster: g.Cluster, Namespace: g.Name,
		Revision: 1, DesiredState: desired, ObservedState: "UNKNOWN", Status: "PENDING", RequestedSpec: spec, LastOperationID: &operationID}
}

func mlHash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func mlRecord(ctx context.Context, st *repository.Store, o *model.Operation, n *mlObject, event string) error {
	return st.DB.WithContext(ctx).Create(&model.MLWorkloadHistory{GroupID: n.identity.GroupID, TargetType: o.TargetType,
		TargetID: n.identity.ID, Revision: n.state.Revision, OperationID: o.ID, Event: event, Action: o.Action,
		ActorID: o.ActorID, DesiredState: n.state.DesiredState, ObservedState: n.state.ObservedState,
		Status: n.state.Status, Spec: n.state.RequestedSpec}).Error
}

func queueML(ctx context.Context, st *repository.Store, o *model.Operation, n *mlObject) error {
	c := MLCommand{SchemaVersion: 1, Action: MLAction(o.Action), ResourceID: n.identity.ID, GroupID: n.identity.GroupID,
		CreatedBy: n.identity.CreatedBy, RequestedBy: o.ActorID, InstanceID: n.state.InstanceID, Cluster: n.state.Cluster, Namespace: n.state.Namespace,
		Name: n.identity.Name, ExternalID: n.state.ExternalID, Revision: n.state.Revision, DesiredState: n.state.DesiredState}
	switch o.TargetType {
	case "pipeline_run":
		c.PipelineRun = &dto.PipelineRunSpec{}
		if err := json.Unmarshal(n.state.RequestedSpec, c.PipelineRun); err != nil {
			return err
		}
		if c.Action == MLPipelineEdit {
			c.PendingPipelineRun = &dto.PipelineRunSpec{}
			if err := json.Unmarshal(n.row.(*model.PipelineRun).PendingSpec, c.PendingPipelineRun); err != nil {
				return err
			}
		}
	case "trainer_job":
		c.TrainerJob = &dto.TrainerJobSpec{}
		if err := json.Unmarshal(n.state.RequestedSpec, c.TrainerJob); err != nil {
			return err
		}
	case "kserve_service":
		c.KServe, c.ConfigRevisionID = &dto.KServeSpec{}, *n.configID
		if err := json.Unmarshal(n.state.RequestedSpec, c.KServe); err != nil {
			return err
		}
	default:
		return apperr.New(apperr.CodeInvalid, "资源不能交给执行器")
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	o.Payload, o.Cluster, o.TargetKey, o.TargetVersion = b, c.Cluster, strconv.FormatUint(c.ResourceID, 10), c.Revision
	o.Status, o.Step = "PENDING", "WAIT_EXECUTOR"
	if err := mlRecord(ctx, st, o, n, "INTENT"); err != nil {
		return err
	}
	return result(o, map[string]any{"resourceId": c.ResourceID, "groupId": c.GroupID, "revision": c.Revision,
		"status": "PENDING", "desiredState": c.DesiredState, "observedState": n.state.ObservedState,
		"appliedRevision": n.state.AppliedRevision, "executionPending": true})
}

func (s *Service) CreatePipeline(ctx context.Context, actor, groupID uint64, key string, in dto.CreatePipelineReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "pipeline.create", "pipeline.create", "pipeline", fmt.Sprintf("%d/%s", groupID, in.Name), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			p := &model.Pipeline{MLIdentity: model.MLIdentity{GroupID: groupID, Name: in.Name, CreatedBy: &actor}, DisplayName: in.DisplayName, Description: in.Description, Revision: 1}
			if err := st.DB.WithContext(ctx).Create(p).Error; err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.Payload = g.Cluster, strconv.FormatUint(p.ID, 10), json.RawMessage(`{}`)
			return result(o, p)
		})
}

func (s *Service) CreatePipelineVersion(ctx context.Context, actor, groupID, id uint64, key string, in dto.CreatePipelineVersionReq) (*model.Operation, error) {
	if err := normalizePipelinePackage(&in); err != nil {
		return nil, err
	}
	if s.PipelineClient != nil && len(in.PipelineSpec) == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "内置 KFP 模式需提交 packageYaml 或 pipelineSpec，不能只保存未解析的 packageRef")
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "pipeline.version.create", "pipeline.version.create", "pipeline", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			p, err := st.Pipeline(ctx, groupID, id, true)
			if err != nil {
				return err
			}
			if in.Revision != p.Revision || p.Revision == ^uint64(0) || p.DeletedAt != nil {
				return apperr.New(apperr.CodeConflict, "Pipeline 定义版本已变化或不可修改")
			}
			b, err := json.Marshal(in.PipelineVersionSpec)
			if err != nil {
				return err
			}
			v := &model.PipelineVersion{GroupID: groupID, PipelineID: id, Name: in.Name, Spec: b, SpecHash: mlHash(b), CreatedBy: &actor}
			if err := st.DB.WithContext(ctx).Create(v).Error; err != nil {
				return err
			}
			if err := savePipelineVersionRevision(ctx, st, v, actor); err != nil {
				return err
			}
			nextRevision := p.Revision + 1
			if err := st.UpdateML(ctx, p, groupID, id, p.Revision, map[string]any{"revision": nextRevision}); err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion, o.Payload = g.Cluster, strconv.FormatUint(id, 10), nextRevision, json.RawMessage(`{}`)
			return result(o, map[string]any{"pipelineId": id, "revision": nextRevision, "version": v})
		}, pipelineGuard(ctx, actor, groupID, id, "pipeline.version.create"))
}

func (s *Service) SubmitPipelineRun(ctx context.Context, actor, groupID, pipelineID uint64, key string, in dto.SubmitPipelineRunReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "pipeline.run", string(MLPipelineSubmit), "pipeline_run", fmt.Sprintf("%d/%d/%s", groupID, pipelineID, in.Name), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			p, err := st.Pipeline(ctx, groupID, pipelineID, true)
			if err != nil {
				return err
			}
			if p.DeletedAt != nil {
				return apperr.New(apperr.CodeConflict, "Pipeline 已删除")
			}
			v, err := st.PipelineVersion(ctx, groupID, pipelineID, in.VersionID)
			if err != nil {
				return err
			}
			if in.VersionRevision != 0 && in.VersionRevision != v.Revision {
				return apperr.New(apperr.CodeConflict, "Pipeline 版本已被编辑，请刷新")
			}
			var version dto.PipelineVersionSpec
			if err := json.Unmarshal(v.Spec, &version); err != nil {
				return err
			}
			if s.PipelineClient != nil && len(version.PipelineSpec) == 0 {
				return apperr.New(apperr.CodeInvalid, "内置 KFP 执行需要导入官方 v2 IR；旧 packageRef 尚无受管解析器")
			}
			values, err := dto.BindPipelineParameters(version.Parameters, in.Parameters)
			if err != nil {
				return err
			}
			b, err := json.Marshal(dto.PipelineRunSpec{PipelineID: pipelineID, VersionID: v.ID, VersionRevision: v.Revision, PipelineVersionSpec: version, Values: values})
			if err != nil {
				return err
			}
			n := &model.PipelineRun{MLIdentity: model.MLIdentity{GroupID: groupID, Name: in.Name, CreatedBy: &actor}, MLExecutionState: mlNewState(g, o.ID, "RUNNING", b), PipelineID: pipelineID, VersionID: v.ID}
			if err := st.DB.WithContext(ctx).Create(n).Error; err != nil {
				return err
			}
			return queueML(ctx, st, o, &mlObject{identity: &n.MLIdentity, state: &n.MLExecutionState, row: n})
		}, pipelineGuard(ctx, actor, groupID, pipelineID, "pipeline.run"))
}

func (s *Service) CreateTrainerJob(ctx context.Context, actor, groupID uint64, key string, in dto.CreateTrainerJobReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "trainer.create", string(MLTrainerCreate), "trainer_job", fmt.Sprintf("%d/%s", groupID, in.Name), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			b, err := json.Marshal(in.TrainerJobSpec)
			if err != nil {
				return err
			}
			n := &model.TrainerJob{MLIdentity: model.MLIdentity{GroupID: groupID, Name: in.Name, CreatedBy: &actor}, MLExecutionState: mlNewState(g, o.ID, "RUNNING", b)}
			if err := st.DB.WithContext(ctx).Create(n).Error; err != nil {
				return err
			}
			return queueML(ctx, st, o, &mlObject{identity: &n.MLIdentity, state: &n.MLExecutionState, row: n})
		})
}

func (s *Service) CreateKServe(ctx context.Context, actor, groupID uint64, key string, in dto.CreateKServeReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "kserve.create", string(MLKServeCreate), "kserve_service", fmt.Sprintf("%d/%s", groupID, in.Name), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			resolved, err := s.resolveKServeModelSpec(ctx, st, actor, groupID, in.KServeSpec)
			if err != nil {
				return err
			}
			b, err := json.Marshal(resolved)
			if err != nil {
				return err
			}
			n := &model.KServeService{MLIdentity: model.MLIdentity{GroupID: groupID, Name: in.Name, CreatedBy: &actor}, MLExecutionState: mlNewState(g, o.ID, "SERVING", b), SyncEnabled: true}
			if err := st.DB.WithContext(ctx).Create(n).Error; err != nil {
				return err
			}
			v := &model.KServeRevision{GroupID: groupID, ServiceID: n.ID, Revision: 1, Spec: b, SpecHash: mlHash(b), CreatedBy: &actor}
			if err := st.DB.WithContext(ctx).Create(v).Error; err != nil {
				return err
			}
			n.ConfigRevisionID = v.ID
			if err := st.UpdateML(ctx, n, groupID, n.ID, 1, map[string]any{"config_revision_id": v.ID}); err != nil {
				return err
			}
			return queueML(ctx, st, o, &mlObject{identity: &n.MLIdentity, state: &n.MLExecutionState, row: n, configID: &n.ConfigRevisionID})
		})
}

func writableML(ctx context.Context, st *repository.Store, groupID, id, revision uint64, target string, supersede bool) (*mlObject, error) {
	n, err := loadML(ctx, st, target, groupID, id, true)
	if err != nil {
		return nil, err
	}
	if revision == 0 || revision != n.state.Revision || revision == ^uint64(0) || n.identity.DeletedAt != nil || n.state.DesiredState == "DELETED" {
		return nil, apperr.New(apperr.CodeConflict, "资源已删除、正在删除或 revision 已变化")
	}
	busy, err := st.HasMLOperation(ctx, target, id)
	if err != nil {
		return nil, err
	}
	if busy && !supersede {
		return nil, apperr.New(apperr.CodeConflict, "有未完成操作，请等待、停止或查询操作单")
	}
	if busy {
		if err := st.SupersedeMLOperations(ctx, target, id); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func saveMLIntent(ctx context.Context, st *repository.Store, o *model.Operation, n *mlObject) error {
	previous := n.state.Revision
	n.state.Revision++
	n.state.Status, n.state.StatusReason, n.state.LastOperationID = "PENDING", nil, &o.ID
	fields := map[string]any{"revision": n.state.Revision, "status": "PENDING", "status_reason": nil, "last_operation_id": o.ID,
		"desired_state": n.state.DesiredState, "requested_spec": string(n.state.RequestedSpec)}
	if o.TargetType == "pipeline_run" {
		fields["sync_next_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
	}
	if o.TargetType == "kserve_service" {
		fields["sync_enabled"], fields["sync_next_at"] = n.state.DesiredState == "SERVING", gorm.Expr("UTC_TIMESTAMP(3)")
	}
	if n.configID != nil {
		fields["config_revision_id"] = *n.configID
	}
	if err := st.UpdateML(ctx, n.row, n.identity.GroupID, n.identity.ID, previous, fields); err != nil {
		return err
	}
	return queueML(ctx, st, o, n)
}

func (s *Service) UpdateKServe(ctx context.Context, actor, groupID, id uint64, key string, in dto.UpdateKServeReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "kserve.update", "kserve.config.update", "kserve_service", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			n, err := writableML(ctx, st, groupID, id, in.Revision, "kserve_service", false)
			if err != nil {
				return err
			}
			resolved, err := s.resolveKServeModelSpec(ctx, st, actor, groupID, in.KServeSpec)
			if err != nil {
				return err
			}
			b, err := json.Marshal(resolved)
			if err != nil {
				return err
			}
			nextRevision := n.state.Revision + 1
			v := &model.KServeRevision{GroupID: groupID, ServiceID: id, Revision: nextRevision, Spec: b, SpecHash: mlHash(b), CreatedBy: &actor}
			if err := st.DB.WithContext(ctx).Create(v).Error; err != nil {
				return err
			}
			if err := st.UpdateML(ctx, n.row, groupID, id, n.state.Revision, map[string]any{"revision": nextRevision}); err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(id, 10), nextRevision
			if err := st.DB.WithContext(ctx).Create(&model.MLWorkloadHistory{GroupID: groupID, TargetType: "kserve_service",
				TargetID: id, Revision: nextRevision, OperationID: o.ID, Event: "CONFIG_REVISION_SAVED", Action: o.Action,
				ActorID: actor, DesiredState: n.state.DesiredState, ObservedState: n.state.ObservedState, Status: n.state.Status, Spec: b}).Error; err != nil {
				return err
			}
			return result(o, map[string]any{"resourceId": id, "groupId": groupID, "revision": nextRevision,
				"configRevision": v, "published": false, "activeConfigRevisionId": *n.configID})
		}, mlGuard(ctx, actor, groupID, id, "kserve_service", "kserve.update"))
}

func (s *Service) DeployKServe(ctx context.Context, actor, groupID, id uint64, key string, in dto.DeployKServeReq) (*model.Operation, error) {
	if in.Revision == 0 || in.RevisionID == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision 和要部署的 revisionId")
	}
	return s.mutate(ctx, actor, groupID, key, "kserve.deploy", string(MLKServeDeploy), "kserve_service", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			if _, err := mlGroup(ctx, st, groupID, false); err != nil {
				return err
			}
			n, err := writableML(ctx, st, groupID, id, in.Revision, "kserve_service", false)
			if err != nil {
				return err
			}
			v, err := st.KServeRevision(ctx, groupID, id, in.RevisionID)
			if err != nil {
				return err
			}
			var spec dto.KServeSpec
			if err := json.Unmarshal(v.Spec, &spec); err != nil {
				return apperr.Wrap(apperr.CodeInternal, "KServe 配置修订无法读取", err)
			}
			if err := s.verifyKServeArtifactSnapshot(ctx, st, actor, groupID, spec); err != nil {
				return err
			}
			*n.configID, n.state.RequestedSpec, n.state.DesiredState = v.ID, v.Spec, "SERVING"
			return saveMLIntent(ctx, st, o, n)
		}, mlGuard(ctx, actor, groupID, id, "kserve_service", "kserve.deploy"))
}

func (s *Service) changeMLState(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq, target string, action MLAction, permission, desired string) (*model.Operation, error) {
	if in.Revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision")
	}
	return s.mutate(ctx, actor, groupID, key, permission, string(action), target, fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			if _, err := mlGroup(ctx, st, groupID, true); err != nil {
				return err
			}
			n, err := writableML(ctx, st, groupID, id, in.Revision, target, true)
			if err != nil {
				return err
			}
			if desired == "STOPPED" && target != "kserve_service" && (n.state.ObservedState == "SUCCEEDED" || n.state.ObservedState == "FAILED") {
				return apperr.New(apperr.CodeConflict, "作业已经结束，不可再停止；历史保持终态")
			}
			if target == "pipeline_run" {
				// 显式停止/删除替代编辑时取消未应用的新配置。
				if err := st.DB.WithContext(ctx).Model(n.row).Where("id = ?", id).Update("pending_spec", nil).Error; err != nil {
					return err
				}
			}
			n.state.DesiredState = desired
			return saveMLIntent(ctx, st, o, n)
		}, mlGuard(ctx, actor, groupID, id, target, permission))
}

func (s *Service) StopPipelineRun(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	return s.changeMLState(ctx, actor, groupID, id, key, in, "pipeline_run", MLPipelineStop, "pipeline.stop", "STOPPED")
}

func (s *Service) StopTrainerJob(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	return s.changeMLState(ctx, actor, groupID, id, key, in, "trainer_job", MLTrainerStop, "trainer.stop", "STOPPED")
}

func (s *Service) DeleteTrainerJob(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	return s.changeMLState(ctx, actor, groupID, id, key, in, "trainer_job", MLTrainerDelete, "trainer.delete", "DELETED")
}

func (s *Service) StopKServe(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	return s.changeMLState(ctx, actor, groupID, id, key, in, "kserve_service", MLKServeStop, "kserve.stop", "STOPPED")
}

func (s *Service) DeleteKServe(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	return s.changeMLState(ctx, actor, groupID, id, key, in, "kserve_service", MLKServeDelete, "kserve.delete", "DELETED")
}
