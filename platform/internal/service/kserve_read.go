package service

import (
	"context"
	"errors"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func kserveReadError(err error) error {
	var permanent *k8s.Permanent
	if errors.As(err, &permanent) {
		return apperr.New(apperr.CodeConflict, permanent.Reason)
	}
	var app *apperr.Error
	if errors.As(err, &app) {
		return err
	}
	return apperr.Wrap(apperr.CodeUnavailable, "KServe 查询暂不可用，请核对集群连接、CRD 和机器权限", err)
}

func kserveReadConflict() error {
	return apperr.New(apperr.CodeConflict, "KServe 服务修订、空间身份或资源归属已变化，请刷新")
}

func (s *Service) kserveReadSpace(ctx context.Context, actor, gid uint64) (*model.Group, error) {
	if err := s.Require(ctx, actor, gid, "kserve.read"); err != nil {
		return nil, err
	}
	if s.KServeClient == nil || !s.KServeConfig.Enabled {
		return nil, apperr.New(apperr.CodeUnavailable, "KServe 尚未启用")
	}
	g, err := s.Store.Group(ctx, gid, false)
	if err != nil {
		return nil, domainError(err)
	}
	if g.Cluster != s.Cluster || g.DeletedAt != nil || g.Status != "ACTIVE" || g.NamespaceUID == nil || g.ProfileUID == nil {
		return nil, apperr.New(apperr.CodeConflict, "空间或集群执行身份尚未就绪")
	}
	exists, err := s.KServeClient.AccessNamespace(ctx, k8s.SpaceRef{ID: g.ID, InstanceID: g.InstanceID, Name: g.Name, Owner: s.InfrastructureOwner, NamespaceUID: *g.NamespaceUID, ProfileUID: *g.ProfileUID})
	if err != nil {
		return nil, kserveReadError(err)
	}
	if !exists {
		return nil, kserveReadConflict()
	}
	return g, nil
}

func (s *Service) kserveReadSpaceFence(ctx context.Context, actor uint64, expected *model.Group) error {
	g, err := s.kserveReadSpace(ctx, actor, expected.ID)
	if err != nil {
		return err
	}
	if g.InstanceID != expected.InstanceID || g.NamespaceUID == nil || expected.NamespaceUID == nil || *g.NamespaceUID != *expected.NamespaceUID ||
		g.ProfileUID == nil || expected.ProfileUID == nil || *g.ProfileUID != *expected.ProfileUID || g.Name != expected.Name || g.Cluster != expected.Cluster || g.Revision != expected.Revision {
		return kserveReadConflict()
	}
	return nil
}

func (s *Service) KServeRuntimes(ctx context.Context, actor, gid uint64) (*dto.KServeRuntimeList, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	g, err := s.kserveReadSpace(ctx, actor, gid)
	if err != nil {
		return nil, err
	}
	out, err := s.KServeClient.KServeRuntimes(ctx, g.Name)
	if err != nil {
		return nil, kserveReadError(err)
	}
	if err := s.kserveReadSpaceFence(ctx, actor, g); err != nil {
		return nil, err
	}
	return out, nil
}

// Preflight has no business or cluster writes and does no object-store probes.
// The registry lookup is authorized exactly as create; dynamic admission checks
// are repeated by the worker, outside all database transaction locks.
func (s *Service) KServePreflight(ctx context.Context, actor, gid uint64, in dto.CreateKServeReq) (*dto.KServePreflight, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if err := s.Require(ctx, actor, gid, "kserve.create"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	g, err := s.kserveReadSpace(ctx, actor, gid)
	if err != nil {
		return nil, err
	}
	spec, err := s.resolveKServeModelSpec(ctx, s.Store, actor, gid, in.KServeSpec)
	if err != nil {
		return nil, err
	}
	out, err := s.KServeClient.KServePreflight(ctx, g.Name, s.KServeConfig.ServiceAccount, s.KServeConfig.StorageSecret, spec)
	if err != nil {
		return nil, kserveReadError(err)
	}
	if err := s.kserveReadSpaceFence(ctx, actor, g); err != nil {
		return nil, err
	}
	if err := s.Require(ctx, actor, gid, "kserve.create"); err != nil {
		return nil, err
	}
	if _, err := s.resolveKServeModelSpec(ctx, s.Store, actor, gid, in.KServeSpec); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) kserveDiagnosticTarget(ctx context.Context, actor, gid, id uint64) (*model.KServeService, *model.Group, k8s.KServeRef, error) {
	n, err := s.KServeService(ctx, actor, gid, id)
	if err != nil {
		return nil, nil, k8s.KServeRef{}, err
	}
	if err := mlAllowed(ctx, s.Store, actor, &n.MLIdentity, "kserve", "kserve.logs"); err != nil {
		return nil, nil, k8s.KServeRef{}, err
	}
	g, err := s.kserveReadSpace(ctx, actor, gid)
	if err != nil {
		return nil, nil, k8s.KServeRef{}, err
	}
	if n.Cluster != s.Cluster || n.Namespace != g.Name || n.DeletedAt != nil || n.ExternalID == nil {
		return nil, nil, k8s.KServeRef{}, apperr.New(apperr.CodeConflict, "服务尚无可核验的集群执行身份")
	}
	ref := k8s.KServeRef{ID: n.ID, GroupID: gid, Revision: n.Revision, InstanceID: n.InstanceID, Namespace: n.Namespace, Name: n.Name, UID: *n.ExternalID}
	return n, g, ref, nil
}

func (s *Service) kserveDiagnosticFence(ctx context.Context, actor uint64, expected *model.KServeService, g *model.Group) error {
	n, err := s.KServeService(ctx, actor, expected.GroupID, expected.ID)
	if err != nil {
		return err
	}
	if err := mlAllowed(ctx, s.Store, actor, &n.MLIdentity, "kserve", "kserve.logs"); err != nil {
		return err
	}
	if n.Revision != expected.Revision || n.InstanceID != expected.InstanceID || n.AppliedRevision != expected.AppliedRevision || n.ExternalID == nil || expected.ExternalID == nil || *n.ExternalID != *expected.ExternalID || n.DeletedAt != nil {
		return kserveReadConflict()
	}
	return s.kserveReadSpaceFence(ctx, actor, g)
}

func (s *Service) KServeDiagnostics(ctx context.Context, actor, gid, id uint64) (*dto.KServeDiagnostics, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	n, g, ref, err := s.kserveDiagnosticTarget(ctx, actor, gid, id)
	if err != nil {
		return nil, err
	}
	out, err := s.KServeClient.KServeDiagnostics(ctx, ref)
	if err != nil {
		return nil, kserveReadError(err)
	}
	if err := s.kserveDiagnosticFence(ctx, actor, n, g); err != nil {
		return nil, err
	}
	out.Revision = n.Revision
	return out, nil
}

func (s *Service) KServeLogs(ctx context.Context, actor, gid, id uint64, q dto.KServeLogQuery) (*podlog.Snapshot, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	n, g, ref, err := s.kserveDiagnosticTarget(ctx, actor, gid, id)
	if err != nil {
		return nil, err
	}
	if q.InstanceID != n.InstanceID || q.Revision != n.Revision {
		return nil, kserveReadConflict()
	}
	out, err := s.KServeClient.KServeLogs(ctx, ref, q)
	if err != nil {
		return nil, kserveReadError(err)
	}
	if err := s.kserveDiagnosticFence(ctx, actor, n, g); err != nil {
		return nil, err
	}
	return out, nil
}
