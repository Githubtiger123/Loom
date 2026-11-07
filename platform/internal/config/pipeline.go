package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// Pipeline 只连接运维配置的官方 KFP 服务，不接受用户提供端点或执行身份。
type Pipeline struct {
	Enabled              bool     `yaml:"enabled"`
	Endpoint             string   `yaml:"endpoint"`
	TokenFile            string   `yaml:"token_file"`
	ServiceAccount       string   `yaml:"service_account"`
	PipelineRoot         string   `yaml:"pipeline_root"`
	Timeout              Duration `yaml:"timeout"`
	PollInterval         Duration `yaml:"poll_interval"`
	SyncInterval         Duration `yaml:"sync_interval"`
	TerminalSyncInterval Duration `yaml:"terminal_sync_interval"`
	SyncConcurrency      int      `yaml:"sync_concurrency"`
	SyncQPS              int      `yaml:"sync_qps"`
	// Official metadata-envoy grpc-web endpoint, not the KFP apiserver URL.
	MetadataEndpoint  string `yaml:"metadata_endpoint"`
	MetadataTokenFile string `yaml:"metadata_token_file"`
}

func (p Pipeline) Validate() error {
	if !p.Enabled {
		return nil
	}
	u, err := url.Parse(p.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("pipeline.endpoint 必须是无凭据、查询参数和 fragment 的 HTTP(S) 服务地址")
	}
	if !validator.IsDNS1123Label(p.ServiceAccount) || strings.TrimSpace(p.TokenFile) == "" {
		return fmt.Errorf("pipeline 需配置有效的 service_account 与 token_file，不使用 Loom 用户 JWT 访问 KFP")
	}
	if p.MetadataEndpoint != "" {
		metadata, err := url.Parse(p.MetadataEndpoint)
		if err != nil || metadata.Host == "" || (metadata.Scheme != "http" && metadata.Scheme != "https") || metadata.User != nil || metadata.RawQuery != "" || metadata.Fragment != "" {
			return fmt.Errorf("pipeline.metadata_endpoint 必须是无凭据和查询参数的 HTTP(S) metadata-envoy 地址")
		}
	}
	if p.Timeout.Std() < time.Second || p.Timeout.Std() > 20*time.Second || p.PollInterval.Std() < 5*time.Second || p.PollInterval.Std() > time.Minute {
		return fmt.Errorf("pipeline.timeout 须为 1–20s，poll_interval 须为 5–60s")
	}
	if p.SyncInterval.Std() < 5*time.Second || p.SyncInterval.Std() > time.Minute ||
		p.TerminalSyncInterval.Std() < time.Minute || p.TerminalSyncInterval.Std() > 24*time.Hour ||
		p.SyncConcurrency < 1 || p.SyncConcurrency > 16 || p.SyncQPS < 1 || p.SyncQPS > 100 {
		return fmt.Errorf("Pipeline 同步需配置 sync_interval=5–60s、terminal_sync_interval=1m–24h、sync_concurrency=1–16、sync_qps=1–100")
	}
	root, err := url.Parse(p.PipelineRoot)
	if err != nil || (root.Scheme != "s3" && root.Scheme != "gs" && root.Scheme != "minio") || root.Host == "" || root.User != nil || root.RawQuery != "" || root.Fragment != "" {
		return fmt.Errorf("pipeline.pipeline_root 须为不含凭据的 s3/gs/minio 存储根路径")
	}
	return nil
}
