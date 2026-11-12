package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/infra/logarchive"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/service"
)

// newBusinessService 在启动装配期绑定只读集群依赖，绝不在事务锁内调用集群。
func newBusinessService(base *service.Service, client *k8s.Client) *service.Service {
	base.SetQuotaReader(boundQuotaReader{client: client, owner: base.InfrastructureOwner})
	base.PipelineVerifier = boundPipelineVerifier{client: client, owner: base.InfrastructureOwner}
	base.KServeVerifier = boundKServeVerifier{client: client, owner: base.InfrastructureOwner}
	if client != nil {
		base.SetNotebookReader(client)
		base.PipelineLogs = client
		base.PipelineTerminal = client
		base.NativePipelineClient = client
		base.KServeClient = client
	}
	return base
}

func configureKServe(base *service.Service, cfg config.KServe) {
	base.KServeConfig = cfg
}

func configurePipeline(base *service.Service, cfg config.Pipeline) {
	base.PipelineConfig = cfg
	if cfg.Enabled {
		base.PipelineClient = kfp.New(cfg.Endpoint, cfg.TokenFile, cfg.Timeout.Std())
		if cfg.MetadataEndpoint != "" {
			base.PipelineMetadata = mlmd.New(cfg.MetadataEndpoint, cfg.MetadataTokenFile, cfg.Timeout.Std())
		}
	}
}

func configureLogArchive(base *service.Service, cfg config.LogArchive) error {
	if !cfg.Enabled {
		if base.PipelineConfig.NativeEnabled {
			return fmt.Errorf("native pipelines require log_archive.enabled=true and format=argo")
		}
		return nil
	}
	reader, err := logarchive.New(cfg)
	if err != nil {
		return err
	}
	base.PipelineLogArchivesReader = reader
	if cfg.Format == "argo" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil {
			return err
		}
		secret := base.PipelineConfig.NativeLogArchiveSecret
		if secret == "" {
			secret = base.PipelineConfig.NativeArtifactSecret
		}
		base.NativeLogArchivePolicy = &k8s.NativeLogArchivePolicy{Endpoint: u.Host, Bucket: cfg.Bucket, Region: cfg.Region, Insecure: u.Scheme == "http", Secret: secret, Prefix: cfg.Prefix, Cluster: base.Cluster}
	} else if base.PipelineConfig.NativeEnabled {
		return fmt.Errorf("native pipeline automatic logs require log_archive.format=argo")
	}
	return nil
}

func configureArtifactStore(base *service.Service, cfg config.ArtifactStore) error {
	if !cfg.Enabled {
		return nil
	}
	reader, err := artifactstore.New(cfg)
	if err != nil {
		return err
	}
	base.PipelineArtifacts = reader
	return nil
}

func configureModelStore(base *service.Service, cfg *config.Config) error {
	if !cfg.ModelStore.Enabled {
		return nil
	}
	publisher, err := artifactstore.NewModelPublisher(cfg.ArtifactStore, cfg.ModelStore)
	if err != nil {
		return err
	}
	base.ModelPublisher = publisher
	return nil
}

type boundPipelineVerifier struct {
	client *k8s.Client
	owner  string
}

type boundKServeVerifier struct {
	client *k8s.Client
	owner  string
}

func (v boundKServeVerifier) VerifyKServeSpace(ctx context.Context, claim *service.MLExecutionClaim) error {
	if v.client == nil || claim.Space == nil {
		return apperr.New(apperr.CodeUnavailable, "集群或推理服务空间身份尚未就绪")
	}
	exists, err := v.client.AccessNamespace(ctx, k8s.SpaceRef{ID: claim.Command.GroupID, InstanceID: claim.Space.InstanceID,
		Name: claim.Command.Namespace, Owner: v.owner, ProfileUID: claim.Space.ProfileUID, NamespaceUID: claim.Space.NamespaceUID})
	if err != nil {
		return err
	}
	if !exists {
		return apperr.New(apperr.CodeConflict, "推理服务空间已消失，禁止写入 KServe")
	}
	return nil
}

func (v boundPipelineVerifier) VerifyPipelineSpace(ctx context.Context, claim *service.MLExecutionClaim) error {
	if v.client == nil || claim.Space == nil {
		return apperr.New(apperr.CodeUnavailable, "集群或执行空间身份尚未就绪")
	}
	exists, err := v.client.AccessNamespace(ctx, k8s.SpaceRef{ID: claim.Command.GroupID, InstanceID: claim.Space.InstanceID,
		Name: claim.Command.Namespace, Owner: v.owner, ProfileUID: claim.Space.ProfileUID, NamespaceUID: claim.Space.NamespaceUID})
	if err != nil {
		return err
	}
	if !exists {
		return apperr.New(apperr.CodeConflict, "执行空间已消失，禁止跨空间提交")
	}
	return nil
}

type boundQuotaReader struct {
	client *k8s.Client
	owner  string
}

func (q boundQuotaReader) ReadGroupQuota(ctx context.Context, command service.GroupLifecycleCommand) (*service.QuotaSnapshot, error) {
	if q.client == nil || command.ProfileUID == nil || command.NamespaceUID == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "集群或空间身份尚未就绪")
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	obs, err := q.client.ReadVerifiedQuota(readCtx, k8s.SpaceRef{ID: command.GroupID, InstanceID: command.InstanceID,
		Name: command.Namespace, Owner: q.owner, ProfileUID: *command.ProfileUID, NamespaceUID: *command.NamespaceUID})
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "无法读取已确认的实际空间额度", err)
	}
	return &service.QuotaSnapshot{GroupID: command.GroupID, GroupRevision: command.Revision,
		NamespaceUID: *command.NamespaceUID, ReadStartedAt: obs.ReadStartedAt,
		Hard: dto.ResourceBudget{CPUMilli: obs.HardCPU, MemoryBytes: obs.HardMemory, GPU: obs.HardGPU},
		Used: dto.ResourceBudget{CPUMilli: obs.UsedCPU, MemoryBytes: obs.UsedMemory, GPU: obs.UsedGPU}}, nil
}
