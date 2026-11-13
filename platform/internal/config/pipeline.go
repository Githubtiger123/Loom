package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// Pipeline retains the KFP service client for existing runs. Official compiler
// policy is separate from runtime readiness; a flag never installs adapters.
type Pipeline struct {
	RuntimeEnabled         bool     `yaml:"runtime_enabled"`
	RuntimeAddr            string   `yaml:"runtime_addr"`
	RuntimeEndpoint        string   `yaml:"runtime_endpoint"`
	RuntimeTLSCert         string   `yaml:"runtime_tls_cert"`
	RuntimeTLSKey          string   `yaml:"runtime_tls_key"`
	RuntimeCAConfigMap     string   `yaml:"runtime_ca_config_map"`
	Enabled                bool     `yaml:"enabled"`
	NativeObserverEnabled  bool     `yaml:"native_observer_enabled"`
	NativeEnabled          bool     `yaml:"native_enabled"`
	NativeCacheEnabled     bool     `yaml:"native_cache_enabled"`
	OfficialDriverImage    string   `yaml:"official_driver_image"`
	OfficialLauncherImage  string   `yaml:"official_launcher_image"`
	OfficialPipelineRoot   string   `yaml:"official_pipeline_root"`
	NativeServiceAccount   string   `yaml:"native_service_account"`
	NativeArtifactEndpoint string   `yaml:"native_artifact_endpoint"`
	NativeArtifactBucket   string   `yaml:"native_artifact_bucket"`
	NativeArtifactRegion   string   `yaml:"native_artifact_region"`
	NativeArtifactSecret   string   `yaml:"native_artifact_secret"`
	NativeArtifactInsecure bool     `yaml:"native_artifact_insecure"`
	NativeLogArchiveSecret string   `yaml:"native_log_archive_secret"`
	NativeParallelism      int64    `yaml:"native_parallelism"`
	NativeCPU              string   `yaml:"native_cpu"`
	NativeMemory           string   `yaml:"native_memory"`
	Endpoint               string   `yaml:"endpoint"`
	TokenFile              string   `yaml:"token_file"`
	ServiceAccount         string   `yaml:"service_account"`
	PipelineRoot           string   `yaml:"pipeline_root"`
	Timeout                Duration `yaml:"timeout"`
	PollInterval           Duration `yaml:"poll_interval"`
	SyncInterval           Duration `yaml:"sync_interval"`
	TerminalSyncInterval   Duration `yaml:"terminal_sync_interval"`
	SyncConcurrency        int      `yaml:"sync_concurrency"`
	SyncQPS                int      `yaml:"sync_qps"`
	// Official metadata-envoy grpc-web endpoint, not the KFP apiserver URL.
	MetadataEndpoint  string `yaml:"metadata_endpoint"`
	MetadataTokenFile string `yaml:"metadata_token_file"`
}

func (p Pipeline) Validate() error {
	if p.NativeObserverEnabled && !p.RuntimeEnabled {
		return fmt.Errorf("官方 Pipeline 观测需同时启用独立 Runtime API，不会回退到 MLMD 或旧执行器")
	}
	if p.NativeCacheEnabled && !p.NativeEnabled && !p.NativeObserverEnabled {
		return fmt.Errorf("官方 Pipeline 缓存需启用原生执行或观测，不适用于外部 KFP 后端")
	}
	if p.RuntimeEnabled {
		endpoint, err := url.Parse(p.RuntimeEndpoint)
		if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "" && endpoint.Path != "/" || endpoint.RawQuery != "" || endpoint.Fragment != "" || p.RuntimeAddr == "" || p.RuntimeTLSCert == "" || p.RuntimeTLSKey == "" || !validator.IsDNS1123Subdomain(p.RuntimeCAConfigMap) {
			return fmt.Errorf("Pipeline runtime 需独立 TLS 地址、Pod 可达的 HTTPS origin、服务端证书/私钥和 CA ConfigMap")
		}
	}
	if p.NativeEnabled {
		if p.PollInterval.Std() < 5*time.Second || p.PollInterval.Std() > time.Minute {
			return fmt.Errorf("原生 Pipeline poll_interval 须为 5–60s")
		}
		if !p.RuntimeEnabled || !p.NativeObserverEnabled || !validator.IsDNS1123Label(p.NativeServiceAccount) || p.NativeParallelism < 1 || p.NativeParallelism > 16 ||
			!validator.IsDigestImageReference(p.OfficialDriverImage) || !validator.IsDigestImageReference(p.OfficialLauncherImage) {
			return fmt.Errorf("官方 Pipeline 需启用 observer、配置执行 SA、并发 1–16，以及固定 sha256 digest 的 driver/launcher 镜像；配置有效不表示 runtime 适配已就绪")
		}
		root, err := url.Parse(p.OfficialPipelineRoot)
		if err != nil || (root.Scheme != "s3" && root.Scheme != "minio") || root.Host == "" || !validator.IsObjectURIWithin(p.OfficialPipelineRoot, strings.TrimRight(p.OfficialPipelineRoot, "/")+"/__loom_policy_probe__") {
			return fmt.Errorf("pipeline.official_pipeline_root 当前仅支持不含凭据的 S3/MinIO 根路径，GS 存储适配尚未接入")
		}
		if !validator.IsDNS1123Subdomain(p.NativeArtifactSecret) || root.Host != p.NativeArtifactBucket || strings.TrimSpace(p.NativeArtifactEndpoint) == "" || strings.Contains(p.NativeArtifactEndpoint, "://") || strings.ContainsAny(p.NativeArtifactEndpoint, "/@?# ") {
			return fmt.Errorf("官方 Pipeline 需配置同桶的 native_artifact_bucket、无 scheme/path 的 native_artifact_endpoint 及空间内凭据 Secret 名称")
		}
	}
	if p.NativeObserverEnabled && (p.SyncConcurrency < 1 || p.SyncConcurrency > 16 || p.SyncQPS < 1 || p.SyncQPS > 100) {
		return fmt.Errorf("原生 Workflow 观测需配置 sync_concurrency=1–16、sync_qps=1–100")
	}
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
