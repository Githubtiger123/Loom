package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// Validate 在启动时一次性把所有配置问题报出来，而不是跑到一半才发现。
//
// 这里只做"静态可判断"的检查；与集群实际状态的一致性（比如
// profile-controller 的 USERID_PREFIX 是否与本服务一致）由运行时对账负责。
func (c *Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	if c.Executor.Enabled && (strings.TrimSpace(c.Executor.Addr) == "" || c.Executor.Addr == c.Server.Addr ||
		c.Executor.ServerCert == "" || c.Executor.ServerKey == "" || c.Executor.ClientCA == "" || len(c.Executor.ClientCertSHA256) == 0) {
		add("executor 需配置独立监听地址、服务端证书/私钥、客户端 CA 和客户端证书 SHA256 pin")
	}

	if strings.TrimSpace(c.Server.Addr) == "" {
		add("server.addr 不能为空，示例 :8080")
	}
	if c.Server.ReadTimeout.Std() <= 0 {
		add("server.read_timeout 必须大于 0")
	}
	if c.Server.WriteTimeout.Std() <= 0 {
		add("server.write_timeout 必须大于 0")
	}
	if c.Server.ShutdownTimeout.Std() <= 0 {
		add("server.shutdown_timeout 必须大于 0")
	}

	// 三个角色（api/sync/cron）都依赖业务库
	if strings.TrimSpace(c.DB.DSN) == "" {
		add("db.dsn 不能为空，建议通过环境变量 LOOM_DB_DSN 注入")
	} else if !strings.Contains(c.DB.DSN, "/") {
		add("db.dsn 缺少库名，示例 user:pass@tcp(127.0.0.1:3306)/loom?parseTime=true")
	}
	if c.DB.MaxOpen <= 0 {
		add("db.max_open 必须大于 0")
	}
	if c.DB.MaxIdle < 0 || c.DB.MaxIdle > c.DB.MaxOpen {
		add("db.max_idle 必须在 0 与 db.max_open 之间")
	}
	if c.DB.ConnMaxLifetime.Std() <= 0 {
		add("db.conn_max_lifetime 必须大于 0（建议 30m，避免被中间件掐连接）")
	}
	if c.DB.ConnMaxIdleTime.Std() <= 0 {
		add("db.conn_max_idle_time 必须大于 0（建议 10m）")
	}
	if c.DB.PingTimeout.Std() <= 0 {
		add("db.ping_timeout 必须大于 0")
	}

	if c.Redis.Enabled {
		if strings.TrimSpace(c.Redis.Addr) == "" {
			add("redis.enabled=true 时 redis.addr 不能为空")
		}
		if c.Redis.PoolSize <= 0 {
			add("redis.pool_size 必须大于 0")
		}
		if c.Redis.DialTimeout.Std() <= 0 || c.Redis.ReadTimeout.Std() <= 0 || c.Redis.WriteTimeout.Std() <= 0 {
			add("redis 的 dial/read/write 超时必须大于 0")
		}
	}

	if c.MQ.Enabled {
		if !strings.HasPrefix(c.MQ.URL, "amqp://") && !strings.HasPrefix(c.MQ.URL, "amqps://") {
			add("mq.url 必须以 amqp:// 或 amqps:// 开头")
		}
		if strings.TrimSpace(c.MQ.Exchange) == "" {
			add("mq.exchange 不能为空")
		}
		if c.MQ.Prefetch <= 0 {
			add("mq.prefetch 必须大于 0")
		}
	}

	if strings.TrimSpace(c.K8s.QuotaName) == "" {
		add("k8s.quota_name 不能为空，默认 kf-resource-quota")
	}
	if len(c.K8s.Cluster) < 1 || len(c.K8s.Cluster) > 64 {
		add("k8s.cluster 必须为 1–64 字符")
	}
	if strings.TrimSpace(c.K8s.InfrastructureOwner) == "" || len(c.K8s.InfrastructureOwner) > 255 {
		add("k8s.infrastructure_owner 必须是实际固定管理身份，最长 255 字符")
	}
	for _, cidr := range c.Identity.TrustedProxyCIDRs {
		if !validator.IsTrustedProxyCIDR(cidr) {
			add("identity.trusted_proxy_cidrs 含无效或过宽的可信网段 %q", cidr)
		}
	}
	if c.Identity.Issuer != "" {
		if len(c.Identity.TrustedProxyCIDRs) == 0 {
			add("启用身份接入必须配置可信网关地址")
		}
		if !validator.IsLowerHeaderName(c.Identity.SubjectHeader) || c.Identity.SubjectHeader == c.Identity.UserIDHeader {
			add("subject_header 必须配置为独立的稳定身份头")
		}
	}
	if strings.TrimSpace(c.K8s.EditorServiceAccount) == "" {
		add("k8s.editor_service_account 不能为空，默认 default-editor")
	}

	// 这两项写错的最典型后果是"不报错，但所有人 403"
	if strings.TrimSpace(c.Istio.IngressPrincipal) == "" {
		add("istio.ingress_principal 不能为空")
	} else if !strings.Contains(c.Istio.IngressPrincipal, "/sa/") {
		add("istio.ingress_principal 必须是 SPIFFE 身份，形如 cluster.local/ns/istio-system/sa/<sa-name>")
	}
	if strings.TrimSpace(c.Istio.KFPUIPrincipal) == "" {
		add("istio.kfp_ui_principal 不能为空")
	} else if !strings.Contains(c.Istio.KFPUIPrincipal, "/sa/") {
		add("istio.kfp_ui_principal 必须是 SPIFFE 身份，形如 cluster.local/ns/kubeflow/sa/ml-pipeline-ui")
	}

	if strings.TrimSpace(c.Identity.UserIDHeader) == "" {
		add("identity.userid_header 不能为空，默认 kubeflow-userid")
	} else if !validator.IsLowerHeaderName(c.Identity.UserIDHeader) {
		add("identity.userid_header 必须全小写（HTTP 头名规范），当前为 %q", c.Identity.UserIDHeader)
	}

	if strings.TrimSpace(c.Images.Path) == "" {
		add("images.path 不能为空，示例 configs/images.yaml")
	}

	switch strings.ToLower(strings.TrimSpace(c.Log.Level)) {
	case "debug", "info", "warn", "warning", "error":
	default:
		add("log.level 只能是 debug/info/warn/error，当前为 %q", c.Log.Level)
	}

	if len(problems) > 0 {
		return fmt.Errorf("配置校验失败：\n  - %s", strings.Join(problems, "\n  - "))
	}
	if err := c.Pipeline.Validate(); err != nil {
		return err
	}
	if err := c.KServe.Validate(); err != nil {
		return err
	}
	if err := c.LogArchive.Validate(); err != nil {
		return err
	}
	return c.validateAuth()
}

// SlogLevel 把配置里的字符串转成 slog 级别。
func (l Log) SlogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(l.Level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
