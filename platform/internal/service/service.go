// Package service 实现业务权限、事务、幂等和审计；外部执行由持久操作承接。
package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Service struct {
	DB                        *gorm.DB
	Store                     *repository.Store
	Cluster                   string
	InfrastructureOwner       string
	QuotaReader               QuotaReader
	NotebookReader            NotebookReader
	NotebookImages            *config.NotebookImageCatalog
	PipelineClient            *kfp.Client
	NativePipelineClient      *k8s.Client
	PipelineMetadata          PipelineMetadataReader
	PipelineArtifacts         PipelineArtifactReader
	PipelineConfig            config.Pipeline
	PipelineVerifier          PipelineSpaceVerifier
	PipelineLogs              PipelineLogReader
	PipelineTerminal          PipelineTerminalClient
	PipelineLogArchivesReader PipelineLogArchiveReader
	NativeLogArchivePolicy    *k8s.NativeLogArchivePolicy
	KServeClient              *k8s.Client
	KServeConfig              config.KServe
	KServeVerifier            KServeSpaceVerifier
}

func New(db *gorm.DB, cluster, owner string) *Service {
	return &Service{DB: db, Store: repository.New(db), Cluster: cluster, InfrastructureOwner: owner}
}

func domainError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperr.New(apperr.CodeNotFound, "目标不存在")
	}
	if repository.IsDuplicate(err) {
		return apperr.New(apperr.CodeConflict, "名称或身份已被占用")
	}
	return err
}

func (s *Service) Require(ctx context.Context, actor, groupID uint64, permission string) error {
	return (rbac.Checker{Store: s.Store}).Require(ctx, actor, groupID, permission)
}

// 所有授权变更统一先锁此行，序列化最后 Owner/管理员检查及权限变更。
// 牺牲部分写吞吐换取明确的锁顺序；禁止在锁内调用外部系统。
func lockAuthorization(ctx context.Context, st *repository.Store) error {
	return st.LockAuthorization(ctx)
}

type mutation func(*repository.Store, *model.Operation) error
type mutationGuard func(*repository.Store) error

func (s *Service) mutate(ctx context.Context, actor, groupID uint64, key, permission, action, targetType, targetKey string, input any, fn mutation, guards ...mutationGuard) (*model.Operation, error) {
	if !validator.IsIdempotencyKey(key) {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 1–64 字符的可见 ASCII Idempotency-Key")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(append([]byte(action+"\x00"+targetType+"\x00"+targetKey+"\x00"), payload...)))
	var operation *model.Operation
	err = repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		if err := lockAuthorization(ctx, st); err != nil {
			return err
		}
		if err := (rbac.Checker{Store: st}).Require(ctx, actor, groupID, permission); err != nil {
			return err
		}
		// 对象级权限在幂等重放时也必须重新判断；guard 不检查请求是否已执行。
		for _, guard := range guards {
			if err := guard(st); err != nil {
				return err
			}
		}
		existing, oldHash, err := st.RequestOperation(ctx, actor, key)
		if err == nil {
			if oldHash != hash {
				return apperr.New(apperr.CodeConflict, "该幂等键已用于不同请求")
			}
			operation = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		operation = &model.Operation{ID: uuid.NewString(), ActorID: actor, Action: action, TargetType: targetType, TargetKey: targetKey, TargetVersion: 1, Status: "SUCCEEDED", Step: "COMPLETED", Payload: payload, Result: json.RawMessage(`{}`)}
		if err := fn(st, operation); err != nil {
			return err
		}
		if err := st.InsertOperation(ctx, operation, key, hash); err != nil {
			return err
		}
		outcome := "ACCEPTED"
		if operation.Status == "SUCCEEDED" {
			outcome = "SUCCEEDED"
		}
		if err := audit(ctx, st, actor, groupID, action, targetType, operation.TargetKey, operation.ID, outcome); err != nil {
			return err
		}
		return st.DB.WithContext(ctx).Model(&model.Audit{}).Where("operation_id = ? AND outcome = ?", operation.ID, outcome).
			Update("after_data", string(operation.Result)).Error
	})
	if err != nil {
		return nil, domainError(err)
	}
	return s.Store.Operation(ctx, operation.ID)
}

func audit(ctx context.Context, st *repository.Store, actor, groupID uint64, action, targetType, target, operation, outcome string) error {
	// 不记录原始请求体，避免身份凭据进入审计快照。
	name := "system"
	if actor != 0 {
		u, err := st.User(ctx, actor, false)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			name = u.Username
		}
	}
	record := &model.Audit{ActorID: &actor, ActorName: name, Action: action, TargetType: targetType, TargetID: target, Outcome: outcome}
	if operation != "" {
		record.OperationID = &operation
	}
	if groupID != 0 {
		record.GroupID = &groupID
	}
	return st.DB.WithContext(ctx).Create(record).Error
}

func result(o *model.Operation, value any) error {
	b, err := json.Marshal(value)
	o.Result = b
	return err
}

func (s *Service) Denied(ctx context.Context, actor uint64, action string) error {
	return audit(ctx, s.Store, actor, 0, action, "request", "", "", "DENIED")
}
