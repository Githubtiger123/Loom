package config

import (
	"fmt"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// KServe 固定平台执行身份与部署模式；业务请求不能覆盖这些字段。
type KServe struct {
	Enabled         bool     `yaml:"enabled"`
	ServiceAccount  string   `yaml:"service_account"`
	DeploymentMode  string   `yaml:"deployment_mode"`
	PollInterval    Duration `yaml:"poll_interval"`
	ResyncInterval  Duration `yaml:"resync_interval"`
	SyncConcurrency int      `yaml:"sync_concurrency"`
	SyncQPS         int      `yaml:"sync_qps"`
}

func (c KServe) Validate() error {
	if !c.Enabled {
		return nil
	}
	if !validator.IsDNS1123Label(c.ServiceAccount) {
		return fmt.Errorf("kserve.service_account 必须是有效的 ServiceAccount 名称")
	}
	if c.DeploymentMode != "Standard" {
		return fmt.Errorf("kserve.deployment_mode 首期只允许 Standard")
	}
	if c.PollInterval.Std() < 5*time.Second || c.PollInterval.Std() > time.Minute ||
		c.ResyncInterval.Std() < time.Minute || c.ResyncInterval.Std() > 24*time.Hour ||
		c.SyncConcurrency < 1 || c.SyncConcurrency > 16 || c.SyncQPS < 1 || c.SyncQPS > 100 {
		return fmt.Errorf("KServe 需配置 poll_interval=5–60s、resync_interval=1m–24h、sync_concurrency=1–16、sync_qps=1–100")
	}
	return nil
}
