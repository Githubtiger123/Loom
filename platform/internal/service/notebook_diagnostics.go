package service

import (
	"context"
	"errors"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
)

// NotebookReader 仅有只读、受对象 UID 约束的业务查询；*k8s.Client 直接实现。
type NotebookReader interface {
	NotebookEvents(context.Context, k8s.NotebookRef, int) (*dto.NotebookEvents, error)
	NotebookLogs(context.Context, k8s.NotebookRef, dto.NotebookLogQuery) (*dto.NotebookLogs, error)
	NotebookDiagnostics(context.Context, k8s.NotebookRef) (*dto.NotebookDiagnostics, error)
	NotebookUsage(context.Context, k8s.NotebookRef) (*dto.NotebookUsage, error)
}

// 仅在启动装配期调用，不在请求间更换依赖。
func (s *Service) SetNotebookReader(reader NotebookReader) { s.NotebookReader = reader }

func (s *Service) notebookDiagnosticTarget(ctx context.Context, actor, groupID, id uint64, content bool) (*model.Notebook, k8s.NotebookRef, error) {
	n, err := s.readNotebook(ctx, actor, groupID, id, "notebook.read")
	if err != nil {
		return nil, k8s.NotebookRef{}, err
	}
	// 事件和诊断消息也可能含用户内容，不能凭台账 read 就取得文件/进程信息。
	if content {
		if err := notebookAllowed(ctx, s.Store, actor, n, "notebook.connect"); err != nil {
			return nil, k8s.NotebookRef{}, err
		}
	}
	if s.NotebookReader == nil {
		return nil, k8s.NotebookRef{}, apperr.New(apperr.CodeUnavailable, "NOTEBOOK_READER_UNAVAILABLE: 尚未装配 Notebook 只读客户端")
	}
	g, err := s.Store.Group(ctx, groupID, false)
	if err != nil {
		return nil, k8s.NotebookRef{}, domainError(err)
	}
	if n.Cluster != s.Cluster || n.Cluster != g.Cluster || n.Namespace != g.Name || n.UID == nil || *n.UID == "" {
		return nil, k8s.NotebookRef{}, apperr.New(apperr.CodeConflict, "Notebook 尚无可验证的执行身份")
	}
	if n.DeletedAt != nil {
		return nil, k8s.NotebookRef{}, apperr.New(apperr.CodeConflict, "Notebook 已删除")
	}
	ref := k8s.NotebookRef{ID: n.ID, GroupID: n.GroupID, Revision: n.Revision, InstanceID: n.InstanceID, Namespace: n.Namespace, Name: n.Name, ServerType: n.ServerType, UID: *n.UID, WorkspacePVC: n.WorkspacePVC, WorkspaceInstanceID: n.WorkspaceInstanceID}
	if n.WorkspaceUID != nil {
		ref.WorkspaceUID = *n.WorkspaceUID
	}
	return n, ref, nil
}

// 集群请求之后再次授权与核对身份，转移负责人/撤权/新版本不会返回旧请求中的用户内容。
func (s *Service) notebookDiagnosticFence(ctx context.Context, actor uint64, expected *model.Notebook, content bool) error {
	n, _, err := s.notebookDiagnosticTarget(ctx, actor, expected.GroupID, expected.ID, content)
	if err != nil {
		return err
	}
	if n.ServerType != expected.ServerType || n.InstanceID != expected.InstanceID || n.Revision != expected.Revision || n.OwnerRevision != expected.OwnerRevision || n.UID == nil || expected.UID == nil || *n.UID != *expected.UID ||
		n.WorkspaceID != expected.WorkspaceID || n.WorkspaceInstanceID != expected.WorkspaceInstanceID || n.WorkspacePVC != expected.WorkspacePVC || !sameDiagnosticWorkspaceUID(n.WorkspaceUID, expected.WorkspaceUID) {
		return apperr.New(apperr.CodeConflict, "查询期间 Notebook 身份或版本已变化")
	}
	return nil
}

func sameDiagnosticWorkspaceUID(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func notebookReadError(err error) error {
	if err == nil {
		return nil
	}
	var permanent *k8s.Permanent
	if errors.As(err, &permanent) {
		return apperr.New(apperr.CodeConflict, permanent.Reason)
	}
	var app *apperr.Error
	if errors.As(err, &app) {
		return err
	}
	return apperr.Wrap(apperr.CodeUnavailable, "Notebook 查询暂时不可用", err)
}

func (s *Service) NotebookEvents(ctx context.Context, actor, groupID, id uint64, limit int) (*dto.NotebookEvents, error) {
	if limit < 1 || limit > 100 {
		return nil, apperr.New(apperr.CodeInvalid, "事件 limit 必须在 1–100 之间")
	}
	n, ref, err := s.notebookDiagnosticTarget(ctx, actor, groupID, id, true)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result, err := s.NotebookReader.NotebookEvents(readCtx, ref, limit)
	if err != nil {
		return nil, notebookReadError(err)
	}
	if err := s.notebookDiagnosticFence(ctx, actor, n, true); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *Service) NotebookLogs(ctx context.Context, actor, groupID, id uint64, query dto.NotebookLogQuery) (*dto.NotebookLogs, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	n, ref, err := s.notebookDiagnosticTarget(ctx, actor, groupID, id, true)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result, err := s.NotebookReader.NotebookLogs(readCtx, ref, query)
	if err != nil {
		return nil, notebookReadError(err)
	}
	if err := s.notebookDiagnosticFence(ctx, actor, n, true); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *Service) NotebookDiagnostics(ctx context.Context, actor, groupID, id uint64) (*dto.NotebookDiagnostics, error) {
	n, ref, err := s.notebookDiagnosticTarget(ctx, actor, groupID, id, true)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result, err := s.NotebookReader.NotebookDiagnostics(readCtx, ref)
	if err != nil {
		return nil, notebookReadError(err)
	}
	if err := s.notebookDiagnosticFence(ctx, actor, n, true); err != nil {
		return nil, err
	}
	// 返回已通过前后权限/版本检查的负责人快照，不在 fence 后另读一个新负责人。
	result.OwnerUserID = n.OwnerUserID
	return result, nil
}
func (s *Service) NotebookUsage(ctx context.Context, actor, groupID, id uint64) (*dto.NotebookUsage, error) {
	n, ref, err := s.notebookDiagnosticTarget(ctx, actor, groupID, id, false)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result, err := s.NotebookReader.NotebookUsage(readCtx, ref)
	if err != nil {
		return nil, notebookReadError(err)
	}
	if err := s.notebookDiagnosticFence(ctx, actor, n, false); err != nil {
		return nil, err
	}
	return result, nil
}
