package config

import (
	"fmt"
	"net/url"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// ArtifactStore uses a dedicated read-only account, separate from archived logs.
// Optional CacheCopy credentials are a different, prefix-limited reader/writer;
// they do not widen the primary account. Official attempts use frozen UID roots.
type ArtifactStore struct {
	CacheCopyAccessKey    string   `yaml:"cache_copy_access_key"`
	CacheCopySecretKey    string   `yaml:"cache_copy_secret_key"`
	CacheCopySessionToken string   `yaml:"cache_copy_session_token"`
	Enabled               bool     `yaml:"enabled"`
	Endpoint              string   `yaml:"endpoint"`
	Bucket                string   `yaml:"bucket"`
	Prefix                string   `yaml:"prefix"`
	Region                string   `yaml:"region"`
	AccessKey             string   `yaml:"access_key"`
	SecretKey             string   `yaml:"secret_key"`
	SessionToken          string   `yaml:"session_token"`
	Timeout               Duration `yaml:"timeout"`
}

func (c ArtifactStore) Validate() error {
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return fmt.Errorf("artifact_store.endpoint 必须是独立的 HTTP(S) MinIO/S3 服务地址")
	}
	if !validator.IsDNS1123Label(c.Bucket) || len(c.Bucket) < 3 || !validator.IsArchivePath(c.Prefix) ||
		!validator.IsArchiveSegment(c.Region) || c.AccessKey == "" || c.SecretKey == "" {
		return fmt.Errorf("artifact_store 需配置 bucket、相对 prefix、region 以及 access_key/secret_key")
	}
	if c.Timeout.Std() < time.Second || c.Timeout.Std() > 30*time.Second {
		return fmt.Errorf("artifact_store.timeout 必须在 1s–30s 之间")
	}
	if (c.CacheCopyAccessKey == "") != (c.CacheCopySecretKey == "") {
		return fmt.Errorf("缓存复制凭据 access key/secret key 必须成对配置，不能复用只读凭据")
	}
	if (c.CacheCopyAccessKey != "" && c.CacheCopyAccessKey == c.AccessKey) || (c.CacheCopySessionToken != "" && c.CacheCopyAccessKey == "") {
		return fmt.Errorf("缓存复制需独立账户，session token 不能脱离复制凭据配置")
	}
	return nil
}

func defaultArtifactStore() ArtifactStore {
	return ArtifactStore{Prefix: "loom/pipelines", Region: "us-east-1", Timeout: Duration(15 * time.Second)}
}
