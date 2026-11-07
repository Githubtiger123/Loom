package config

import (
	"fmt"
	"net/url"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// LogArchive is independent of KFP availability, so deleted runs remain readable.
type LogArchive struct {
	Enabled      bool     `yaml:"enabled"`
	Endpoint     string   `yaml:"endpoint"`
	Bucket       string   `yaml:"bucket"`
	Prefix       string   `yaml:"prefix"`
	Region       string   `yaml:"region"`
	AccessKey    string   `yaml:"access_key"`
	SecretKey    string   `yaml:"secret_key"`
	SessionToken string   `yaml:"session_token"`
	Timeout      Duration `yaml:"timeout"`
}

func (c LogArchive) Validate() error {
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("log_archive.endpoint 必须是独立的 HTTP(S) MinIO/S3 服务地址")
	}
	if !validator.IsDNS1123Label(c.Bucket) || len(c.Bucket) < 3 || !validator.IsArchivePath(c.Prefix) ||
		!validator.IsArchiveSegment(c.Region) || c.AccessKey == "" || c.SecretKey == "" {
		return fmt.Errorf("log_archive 需配置 bucket、相对 prefix、region 以及 Secret 注入的 access_key/secret_key")
	}
	if c.Timeout.Std() < time.Second || c.Timeout.Std() > 30*time.Second {
		return fmt.Errorf("log_archive.timeout 必须在 1s–30s 之间")
	}
	return nil
}
