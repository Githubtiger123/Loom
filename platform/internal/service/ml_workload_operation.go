package service

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

func mlOperationIdentity(ctx context.Context, st *repository.Store, o *model.Operation) (*model.MLIdentity, error) {
	id, err := strconv.ParseUint(o.TargetKey, 10, 64)
	if err != nil || id == 0 || !IsMLTarget(o.TargetType) {
		return nil, apperr.New(apperr.CodeInvalid, "ML 操作目标无效")
	}
	// Resolve the stored target, not a group ID from an untrusted request/payload.
	switch o.TargetType {
	case "pipeline":
		n := &model.Pipeline{}
		err = st.DB.WithContext(ctx).Where("id = ?", id).First(n).Error
		return &n.MLIdentity, err
	case "pipeline_run":
		n := &model.PipelineRun{}
		err = st.DB.WithContext(ctx).Where("id = ?", id).First(n).Error
		return &n.MLIdentity, err
	case "trainer_job":
		n := &model.TrainerJob{}
		err = st.DB.WithContext(ctx).Where("id = ?", id).First(n).Error
		return &n.MLIdentity, err
	default:
		n := &model.KServeService{}
		err = st.DB.WithContext(ctx).Where("id = ?", id).First(n).Error
		return &n.MLIdentity, err
	}
}

// AuthorizeMLOperation MUST be called before the generic Operation actor/admin
// shortcut. Being the original submitter does not retain revoked group access.
func AuthorizeMLOperation(ctx context.Context, st *repository.Store, actor uint64, o *model.Operation) error {
	n, err := mlOperationIdentity(ctx, st, o)
	if err != nil {
		return domainError(err)
	}
	module := mlModule(o.TargetType)
	return mlAllowed(ctx, st, actor, n, module, module+".read")
}

func (s *Service) RequireMLOperation(ctx context.Context, actor uint64, o *model.Operation) error {
	return AuthorizeMLOperation(ctx, s.Store, actor, o)
}

func mlActionPermission(action string) string {
	switch MLAction(action) {
	case MLPipelineSubmit:
		return "pipeline.run"
	case MLPipelineStop:
		return "pipeline.stop"
	case MLTrainerCreate, MLTrainerStop, MLTrainerDelete, MLKServeCreate, MLKServeUpdate, MLKServeDeploy, MLKServeStop, MLKServeDelete:
		return action
	}
	return ""
}

func authorizeMLRetry(ctx context.Context, st *repository.Store, actor uint64, o *model.Operation) error {
	permission := mlActionPermission(o.Action)
	if permission == "" {
		return apperr.New(apperr.CodeConflict, "此操作不是可重试的 ML 执行动作")
	}
	n, err := mlOperationIdentity(ctx, st, o)
	if err != nil {
		return err
	}
	return mlAllowed(ctx, st, actor, n, mlModule(o.TargetType), permission)
}

// PrepareMLRetry runs inside mutate's transaction, AFTER LockAuthorization and
// current-actor authorization, BEFORE resetting the original operation. Callers
// should use RetryMLOperation, which performs that authorization on replay too.
func PrepareMLRetry(ctx context.Context, st *repository.Store, original *model.Operation) error {
	if mlActionPermission(original.Action) == "" {
		return apperr.New(apperr.CodeConflict, "此操作不是可重试的 ML 执行动作")
	}
	if original.Status != "FAILED" {
		return apperr.New(apperr.CodeConflict, "只能重试失败操作")
	}
	var command MLCommand
	if err := json.Unmarshal(original.Payload, &command); err != nil {
		return err
	}
	cleanup := command.DesiredState == "STOPPED" || command.DesiredState == "DELETED"
	if _, err := mlGroup(ctx, st, command.GroupID, cleanup); err != nil {
		return err
	}
	n, err := loadML(ctx, st, original.TargetType, command.GroupID, command.ResourceID, true)
	if err != nil {
		return err
	}
	if n.identity.DeletedAt != nil || command.SchemaVersion != 1 || command.Action != MLAction(original.Action) ||
		command.RequestedBy != original.ActorID ||
		original.TargetKey != strconv.FormatUint(n.identity.ID, 10) || original.TargetVersion != n.state.Revision ||
		command.Revision != n.state.Revision || command.InstanceID != n.state.InstanceID ||
		command.DesiredState != n.state.DesiredState || command.Cluster != n.state.Cluster ||
		original.Cluster != n.state.Cluster || command.Namespace != n.state.Namespace || command.Name != n.identity.Name ||
		n.state.LastOperationID == nil || *n.state.LastOperationID != original.ID {
		return apperr.New(apperr.CodeConflict, "操作已过期，不能覆盖最新业务意图")
	}
	busy, err := st.HasMLOperation(ctx, original.TargetType, n.identity.ID)
	if err != nil {
		return err
	}
	if busy {
		return apperr.New(apperr.CodeConflict, "该资源已有未完成操作")
	}
	if command.ExternalID != nil && (n.state.ExternalID == nil || *command.ExternalID != *n.state.ExternalID) {
		return apperr.New(apperr.CodeConflict, "外部实例身份已经变化")
	}
	if original.TargetType != "kserve_service" && n.state.DesiredState == "RUNNING" &&
		(n.state.ObservedState == "SUCCEEDED" || n.state.ObservedState == "FAILED" || n.state.ObservedState == "STOPPED") {
		return apperr.New(apperr.CodeConflict, "已结束的作业不可通过操作重试重新运行，请提交新作业")
	}
	// A timed-out create may have a verified external ID recorded by the adapter.
	// Persist it for the retry rather than submitting another external instance.
	command.ExternalID = n.state.ExternalID
	b, err := json.Marshal(command)
	if err != nil {
		return err
	}
	if err := st.DB.WithContext(ctx).Model(n.row).Where("group_id = ? AND id = ? AND revision = ?", n.identity.GroupID, n.identity.ID, n.state.Revision).
		Updates(map[string]any{"status": "PENDING", "status_reason": nil}).Error; err != nil {
		return err
	}
	if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ? AND status = ?", original.ID, "FAILED").
		Updates(map[string]any{"step": "WAIT_EXECUTOR", "payload": string(b)}).Error; err != nil {
		return err
	}
	original.Payload, original.Step = b, "WAIT_EXECUTOR"
	return nil
}

// RetryMLOperation is the ML branch of the existing generic Retry endpoint.
// It records the retry request through mutate and returns the original queued op.
func (s *Service) RetryMLOperation(ctx context.Context, actor uint64, key, id string) (*model.Operation, error) {
	original, err := s.Store.Operation(ctx, id)
	if err != nil {
		return nil, domainError(err)
	}
	n, err := mlOperationIdentity(ctx, s.Store, original)
	if err != nil {
		return nil, domainError(err)
	}
	permission := mlActionPermission(original.Action)
	if permission == "" {
		return nil, apperr.New(apperr.CodeConflict, "此 ML 操作不支持重试")
	}
	guard := func(st *repository.Store) error {
		o, err := st.Operation(ctx, id)
		if err != nil {
			return err
		}
		return authorizeMLRetry(ctx, st, actor, o)
	}
	_, err = s.mutate(ctx, actor, n.GroupID, key, permission, original.Action+".retry", original.TargetType, original.TargetKey,
		map[string]string{"operationId": id}, func(st *repository.Store, receipt *model.Operation) error {
			o, err := st.Operation(ctx, id)
			if err != nil {
				return err
			}
			if err := PrepareMLRetry(ctx, st, o); err != nil {
				return err
			}
			r := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ? AND status = ?", id, "FAILED").Updates(map[string]any{
				"status": "PENDING", "attempt": 0, "next_attempt_at": gorm.Expr("UTC_TIMESTAMP(3)"), "deadline_at": nil,
				"last_error": nil, "lease_owner": nil, "lease_until": nil, "finished_at": nil,
			})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return apperr.New(apperr.CodeConflict, "操作状态已经变化")
			}
			workload, err := loadML(ctx, st, o.TargetType, n.GroupID, n.ID, false)
			if err != nil {
				return err
			}
			workload.state.Status = "PENDING"
			if err := mlRecord(ctx, st, o, workload, "RETRY"); err != nil {
				return err
			}
			receipt.Cluster, receipt.TargetVersion, receipt.Payload = o.Cluster, o.TargetVersion, json.RawMessage(`{}`)
			return result(receipt, map[string]any{"operationId": id, "resourceId": workload.identity.ID, "revision": o.TargetVersion, "status": "PENDING"})
		}, guard)
	if err != nil {
		return nil, err
	}
	return s.Store.Operation(ctx, id)
}
