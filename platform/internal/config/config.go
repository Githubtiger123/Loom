// Package config 负责平台配置的加载、默认值与覆盖。
//
// 优先级（后者覆盖前者）：
//
//	代码默认值  <  config.yaml  <  config.local.yaml  <  环境变量  <  命令行 flag
//
// 敏感信息（MySQL 密码、私仓凭证）只放环境变量/Secret，不写进仓库里的 yaml。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是平台配置的根结构。
type Config struct {
	Server   Server   `yaml:"server"`
	DB       DB       `yaml:"db"`
	Redis    Redis    `yaml:"redis"`
	MQ       MQ       `yaml:"mq"`
	K8s      K8s      `yaml:"k8s"`
	Istio    Istio    `yaml:"istio"`
	Identity Identity `yaml:"identity"`
	Auth     Auth     `yaml:"auth"`
	Executor Executor `yaml:"executor"`
	Images   Images   `yaml:"images"`
	Pipeline Pipeline `yaml:"pipeline"`
	Log      Log      `yaml:"log"`
}

// Server 是 HTTP 服务参数。
type Server struct {
	Addr            string   `yaml:"addr"`
	ReadTimeout     Duration `yaml:"read_timeout"`
	WriteTimeout    Duration `yaml:"write_timeout"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
}

// DB 是 MySQL 参数。DSN 通常由环境变量注入。
type DB struct {
	DSN             string   `yaml:"dsn"`
	MaxOpen         int      `yaml:"max_open"`
	MaxIdle         int      `yaml:"max_idle"`
	ConnMaxLifetime Duration `yaml:"conn_max_lifetime"`
	ConnMaxIdleTime Duration `yaml:"conn_max_idle_time"`
	PingTimeout     Duration `yaml:"ping_timeout"`
}

// Redis 是可选缓存，认证和业务授权的权威状态在 MySQL；故障时直查数据库。
type Redis struct {
	Enabled      bool     `yaml:"enabled"`
	Addr         string   `yaml:"addr"`
	Password     string   `yaml:"password"`
	DB           int      `yaml:"db"`
	DialTimeout  Duration `yaml:"dial_timeout"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
	PoolSize     int      `yaml:"pool_size"`
}

// MQ 是 RabbitMQ。一期默认关闭：定时任务与状态同步都用不着消息队列，
// 先留接口，等真有"异步通知/削峰"需求再打开，避免为两个 cron 引入一个 broker。
type MQ struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`
	Exchange string `yaml:"exchange"`
	Prefetch int    `yaml:"prefetch"`
}

// K8s 是集群连接与约定名。
type K8s struct {
	Cluster             string `yaml:"cluster"`
	InfrastructureOwner string `yaml:"infrastructure_owner"`
	// Kubeconfig 为空表示使用 Pod 内的 ServiceAccount（生产默认）。
	Kubeconfig string `yaml:"kubeconfig"`
	// QuotaName 是 profile-controller 创建的 ResourceQuota 名。
	QuotaName string `yaml:"quota_name"`
	// EditorServiceAccount 是 Notebook Pod 使用的 SA，模板里是固定值。
	EditorServiceAccount string `yaml:"editor_service_account"`
}

// Istio 是 AuthorizationPolicy 里要匹配的网关身份。
type Istio struct {
	// IngressPrincipal 必须与本集群 ingressgateway 的 SA 完全一致，
	// 写错不会报错，只会让所有成员的 AuthorizationPolicy 静默失效（全 403）。
	IngressPrincipal string `yaml:"ingress_principal"`
	KFPUIPrincipal   string `yaml:"kfp_ui_principal"`
}

// Identity 必须与 profile-controller 的 USERID_HEADER / USERID_PREFIX 完全一致，
// 否则我们建的 AP 与 controller 建的 AP 互相不认，成员一律 403。
type Identity struct {
	UserIDHeader      string   `yaml:"userid_header"`
	UserIDPrefix      string   `yaml:"userid_prefix"`
	Issuer            string   `yaml:"issuer"`
	SubjectHeader     string   `yaml:"subject_header"`
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
}

// Auth 为可选本地认证。启用后 API 只接受本模块 Bearer，不回退到身份头。
type Auth struct {
	Enabled        bool     `yaml:"enabled"`
	Issuer         string   `yaml:"issuer"`
	Audience       string   `yaml:"audience"`
	PublicOrigin   string   `yaml:"public_origin"`
	NotebookOrigin string   `yaml:"notebook_origin"` // 可选独立 Notebook HTTPS 来源，不共享平台 Refresh Cookie。
	SigningKey     string   `yaml:"signing_key"`     // 至少 32 字节随机值的标准 Base64，只从 Secret 注入。
	AccessTTL      Duration `yaml:"access_ttl"`
	SessionTTL     Duration `yaml:"session_ttl"`
}

// Images 是镜像白名单配置文件路径。
type Images struct {
	Path string `yaml:"path"`
}

// Log 是日志级别。
type Log struct {
	Level string `yaml:"level"` // debug | info | warn | error
}

// Duration 让 yaml 能直接写 "15s" 这样的字符串。
type Duration time.Duration

// UnmarshalYAML 实现 yaml 的自定义解析。
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration 必须是字符串，如 15s: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration 格式不正确 %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// Std 返回标准库的 time.Duration。
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Default 返回一份可直接运行的默认配置。
func Default() *Config {
	return &Config{
		Server: Server{
			Addr:            ":8080",
			ReadTimeout:     Duration(15 * time.Second),
			WriteTimeout:    Duration(60 * time.Second),
			ShutdownTimeout: Duration(10 * time.Second),
		},
		DB: DB{
			MaxOpen:         50,
			MaxIdle:         10,
			ConnMaxLifetime: Duration(30 * time.Minute),
			ConnMaxIdleTime: Duration(10 * time.Minute),
			PingTimeout:     Duration(3 * time.Second),
		},
		Redis: Redis{
			Enabled:      true,
			Addr:         "127.0.0.1:6379",
			DB:           0,
			DialTimeout:  Duration(2 * time.Second),
			ReadTimeout:  Duration(3 * time.Second),
			WriteTimeout: Duration(3 * time.Second),
			PoolSize:     20,
		},
		MQ: MQ{
			Enabled:  false,
			URL:      "amqp://guest:guest@127.0.0.1:5672/",
			Exchange: "loom",
			Prefetch: 10,
		},
		K8s: K8s{
			Cluster:              "default",
			InfrastructureOwner:  "admin",
			QuotaName:            "kf-resource-quota",
			EditorServiceAccount: "default-editor",
		},
		Istio: Istio{
			IngressPrincipal: "cluster.local/ns/istio-system/sa/istio-ingressgateway-service-account",
			KFPUIPrincipal:   "cluster.local/ns/kubeflow/sa/ml-pipeline-ui",
		},
		Identity: Identity{UserIDHeader: "kubeflow-userid", UserIDPrefix: "", SubjectHeader: "oidc-sub", TrustedProxyCIDRs: []string{"127.0.0.1/32", "::1/128"}},
		Auth:     Auth{Audience: "loom-api", AccessTTL: Duration(15 * time.Minute), SessionTTL: Duration(15 * 24 * time.Hour)},
		Images:   Images{Path: "configs/images.yaml"},
		Pipeline: Pipeline{ServiceAccount: "default-editor", Timeout: Duration(15 * time.Second), PollInterval: Duration(15 * time.Second)},
		Log:      Log{Level: "info"},
	}
}

// Load 依次读取配置文件、同目录的 config.local.yaml 和环境变量，最后校验。
//
// path 为空时仅使用默认值和环境变量，不隐式读取当前目录。
// 文件不存在时不报错；直接指定 config.local.yaml 时不重复读取。
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		if err := overlayFile(cfg, path); err != nil {
			return nil, err
		}
		localPath := filepath.Join(filepath.Dir(path), "config.local.yaml")
		if !strings.EqualFold(filepath.Clean(path), localPath) {
			if err := overlayFile(cfg, localPath); err != nil {
				return nil, err
			}
		}
	}

	applyEnv(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func overlayFile(cfg *Config, path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	// 未声明字段保留上一层配置，而不是清零整个嵌套结构。
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	return nil
}
func applyEnv(c *Config) {
	envStr(&c.Server.Addr, "LOOM_SERVER_ADDR")
	envDuration(&c.Server.ReadTimeout, "LOOM_SERVER_READ_TIMEOUT")
	envDuration(&c.Server.WriteTimeout, "LOOM_SERVER_WRITE_TIMEOUT")
	envDuration(&c.Server.ShutdownTimeout, "LOOM_SERVER_SHUTDOWN_TIMEOUT")

	envStr(&c.DB.DSN, "LOOM_DB_DSN")
	envInt(&c.DB.MaxOpen, "LOOM_DB_MAX_OPEN")
	envInt(&c.DB.MaxIdle, "LOOM_DB_MAX_IDLE")
	envDuration(&c.DB.ConnMaxLifetime, "LOOM_DB_CONN_MAX_LIFETIME")
	envDuration(&c.DB.ConnMaxIdleTime, "LOOM_DB_CONN_MAX_IDLE_TIME")
	envDuration(&c.DB.PingTimeout, "LOOM_DB_PING_TIMEOUT")

	envBool(&c.Redis.Enabled, "LOOM_REDIS_ENABLED")
	envStr(&c.Redis.Addr, "LOOM_REDIS_ADDR")
	envStr(&c.Redis.Password, "LOOM_REDIS_PASSWORD")
	envInt(&c.Redis.DB, "LOOM_REDIS_DB")
	envDuration(&c.Redis.DialTimeout, "LOOM_REDIS_DIAL_TIMEOUT")
	envDuration(&c.Redis.ReadTimeout, "LOOM_REDIS_READ_TIMEOUT")
	envDuration(&c.Redis.WriteTimeout, "LOOM_REDIS_WRITE_TIMEOUT")
	envInt(&c.Redis.PoolSize, "LOOM_REDIS_POOL_SIZE")

	envBool(&c.MQ.Enabled, "LOOM_MQ_ENABLED")
	envStr(&c.MQ.URL, "LOOM_MQ_URL")
	envStr(&c.MQ.Exchange, "LOOM_MQ_EXCHANGE")
	envInt(&c.MQ.Prefetch, "LOOM_MQ_PREFETCH")

	envStr(&c.K8s.Kubeconfig, "LOOM_K8S_KUBECONFIG")
	envStr(&c.K8s.Cluster, "LOOM_K8S_CLUSTER")
	envStr(&c.K8s.InfrastructureOwner, "LOOM_K8S_INFRASTRUCTURE_OWNER")
	envStr(&c.K8s.QuotaName, "LOOM_K8S_QUOTA_NAME")
	envStr(&c.K8s.EditorServiceAccount, "LOOM_K8S_EDITOR_SA")

	envStr(&c.Istio.IngressPrincipal, "LOOM_ISTIO_INGRESS_PRINCIPAL")
	envStr(&c.Istio.KFPUIPrincipal, "LOOM_ISTIO_KFP_UI_PRINCIPAL")

	envStr(&c.Identity.UserIDHeader, "LOOM_IDENTITY_USERID_HEADER")
	envStr(&c.Identity.Issuer, "LOOM_IDENTITY_ISSUER")
	envStr(&c.Identity.SubjectHeader, "LOOM_IDENTITY_SUBJECT_HEADER")
	if value, ok := os.LookupEnv("LOOM_IDENTITY_TRUSTED_PROXY_CIDRS"); ok {
		c.Identity.TrustedProxyCIDRs = nil
		for _, cidr := range strings.Split(value, ",") {
			if cidr = strings.TrimSpace(cidr); cidr != "" {
				c.Identity.TrustedProxyCIDRs = append(c.Identity.TrustedProxyCIDRs, cidr)
			}
		}
	}
	// 前缀可能是空字符串，用 LOOM_IDENTITY_USERID_PREFIX_SET=1 显式声明要改成空
	if v, ok := os.LookupEnv("LOOM_IDENTITY_USERID_PREFIX"); ok {
		c.Identity.UserIDPrefix = v
	}

	envStr(&c.Images.Path, "LOOM_IMAGES_PATH")
	envBool(&c.Pipeline.Enabled, "LOOM_PIPELINE_ENABLED")
	envStr(&c.Pipeline.Endpoint, "LOOM_PIPELINE_ENDPOINT")
	envStr(&c.Pipeline.TokenFile, "LOOM_PIPELINE_TOKEN_FILE")
	envStr(&c.Pipeline.ServiceAccount, "LOOM_PIPELINE_SERVICE_ACCOUNT")
	envStr(&c.Pipeline.PipelineRoot, "LOOM_PIPELINE_ROOT")
	envDuration(&c.Pipeline.Timeout, "LOOM_PIPELINE_TIMEOUT")
	envDuration(&c.Pipeline.PollInterval, "LOOM_PIPELINE_POLL_INTERVAL")
	envBool(&c.Auth.Enabled, "LOOM_AUTH_ENABLED")
	envStr(&c.Auth.Issuer, "LOOM_AUTH_ISSUER")
	envStr(&c.Auth.Audience, "LOOM_AUTH_AUDIENCE")
	envStr(&c.Auth.PublicOrigin, "LOOM_AUTH_PUBLIC_ORIGIN")
	envStr(&c.Auth.NotebookOrigin, "LOOM_AUTH_NOTEBOOK_ORIGIN")
	envBool(&c.Executor.Enabled, "LOOM_EXECUTOR_ENABLED")
	envStr(&c.Executor.Addr, "LOOM_EXECUTOR_ADDR")
	envStr(&c.Executor.ServerCert, "LOOM_EXECUTOR_SERVER_CERT")
	envStr(&c.Executor.ServerKey, "LOOM_EXECUTOR_SERVER_KEY")
	envStr(&c.Executor.ClientCA, "LOOM_EXECUTOR_CLIENT_CA")
	if value, ok := os.LookupEnv("LOOM_EXECUTOR_CLIENT_CERT_SHA256"); ok {
		c.Executor.ClientCertSHA256 = nil
		for _, pin := range strings.Split(value, ",") {
			c.Executor.ClientCertSHA256 = append(c.Executor.ClientCertSHA256, strings.TrimSpace(pin))
		}
	}
	envStr(&c.Auth.SigningKey, "LOOM_AUTH_SIGNING_KEY")
	envDuration(&c.Auth.AccessTTL, "LOOM_AUTH_ACCESS_TTL")
	envDuration(&c.Auth.SessionTTL, "LOOM_AUTH_SESSION_TTL")
	envStr(&c.Log.Level, "LOOM_LOG_LEVEL")
}

func envStr(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func envInt(dst *int, key string) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return // 非法值保留原值，由 Validate 兜底
	}
	*dst = n
}

func envBool(dst *bool, key string) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return
	}
	*dst = b
}

func envDuration(dst *Duration, key string) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return
	}
	*dst = Duration(d)
}

// SafeDSN 把密码替换成 ***，用于日志输出。
func (d DB) SafeDSN() string {
	if d.DSN == "" {
		return "<empty>"
	}
	at := strings.LastIndex(d.DSN, "@")
	if at < 0 {
		return "<redacted>"
	}
	// 兼容两种写法：user:pass@tcp(...)/db 和 mysql://user:pass@host/db
	credStart := 0
	if i := strings.Index(d.DSN, "://"); i >= 0 {
		credStart = i + 3
	}
	if at <= credStart {
		return "<redacted>"
	}
	cred := d.DSN[credStart:at]
	if i := strings.Index(cred, ":"); i >= 0 {
		cred = cred[:i] + ":***"
	}
	return d.DSN[:credStart] + cred + d.DSN[at:]
}
