package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
)

type KServePredictResult struct {
	Revision   uint64          `json:"revision"`
	InstanceID string          `json:"instanceId"`
	DurationMS int64           `json:"durationMs"`
	Response   json.RawMessage `json:"response"`
}

func (s *Service) PredictKServe(ctx context.Context, actor, gid, id uint64, in dto.KServePredictReq) (*KServePredictResult, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	n, err := s.KServeService(ctx, actor, gid, id)
	if err != nil {
		return nil, err
	}
	if err := mlAllowed(ctx, s.Store, actor, &n.MLIdentity, "kserve", "kserve.invoke"); err != nil {
		return nil, err
	}
	if n.Revision != in.Revision || n.InstanceID != in.InstanceID || n.ExternalID == nil || n.DesiredState != "SERVING" || !n.KServeReady || n.DeletedAt != nil {
		return nil, apperr.New(apperr.CodeConflict, "服务已变化或尚未就绪，请刷新")
	}
	if s.KServeClient == nil || !s.KServeConfig.Enabled {
		return nil, apperr.New(apperr.CodeUnavailable, "KServe 尚未启用")
	}
	g, err := s.kserveReadSpace(ctx, actor, gid)
	if err != nil {
		return nil, err
	}
	if n.Cluster != g.Cluster || n.Namespace != g.Name || g.NamespaceUID == nil || g.ProfileUID == nil || g.Status != "ACTIVE" {
		return nil, apperr.New(apperr.CodeConflict, "空间身份尚未就绪")
	}
	var spec struct {
		Protocol       string `json:"protocol"`
		DeploymentType string `json:"deploymentType"`
	}
	if json.Unmarshal(n.RequestedSpec, &spec) != nil || spec.DeploymentType != "MODEL" || (spec.Protocol != "v1" && spec.Protocol != "v2") {
		return nil, apperr.New(apperr.CodeInvalid, "在线验收仅支持 MODEL 的 v1/v2 协议")
	}
	start := time.Now()
	data, err := s.KServeClient.PredictKServe(ctx, k8s.KServeRef{ID: n.ID, GroupID: gid, Revision: n.AppliedRevision, InstanceID: n.InstanceID, Namespace: n.Namespace, Name: n.Name, UID: *n.ExternalID}, spec.Protocol, in.Payload)
	if err != nil {
		return nil, err
	}
	current, err := s.KServeService(ctx, actor, gid, id)
	if err != nil {
		return nil, err
	}
	if err := mlAllowed(ctx, s.Store, actor, &current.MLIdentity, "kserve", "kserve.invoke"); err != nil {
		return nil, err
	}
	if current.Revision != n.Revision || current.InstanceID != n.InstanceID || current.AppliedRevision != n.AppliedRevision || current.ExternalID == nil || *current.ExternalID != *n.ExternalID || current.DesiredState != "SERVING" || !current.KServeReady {
		return nil, apperr.New(apperr.CodeConflict, "推理期间服务已变化，丢弃旧响应")
	}
	if err := s.kserveReadSpaceFence(ctx, actor, g); err != nil {
		return nil, err
	}
	return &KServePredictResult{Revision: n.Revision, InstanceID: n.InstanceID, DurationMS: time.Since(start).Milliseconds(), Response: data}, nil
}
