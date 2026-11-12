// Package artifactstore reads exact MLMD artifact objects from a fixed S3 endpoint.
package artifactstore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const MaxPreviewBytes int64 = 2 << 20
const MaxDownloadBytes int64 = 32 << 20
const MaxKFPPreviewBytes int64 = 64 << 10

type Object struct {
	Data []byte
	Name string
	ETag string
}

type Reader struct {
	client         *minio.Client
	bucket, prefix string
	timeout        time.Duration
	endpoint       string
	slots          chan struct{}
}

func New(c config.ArtifactStore) (*Reader, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, c.SessionToken),
		Secure: u.Scheme == "https", Region: c.Region, BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, err
	}
	return &Reader{client: client, endpoint: u.Host, bucket: c.Bucket, prefix: c.Prefix, timeout: c.Timeout.Std(), slots: make(chan struct{}, 4)}, nil
}

// Read accepts no user-supplied endpoint, key or provider credentials. Namespace
// scope allows cached artifacts from another attempt in the same business group.
// suffix is either empty or one of KFP's two fixed producer preview locations.
func (r *Reader) Read(ctx context.Context, namespace, uri, suffix string, limit int64) (*Object, error) {
	u, err := url.Parse(uri)
	if err != nil || (u.Scheme != "s3" && u.Scheme != "minio") || u.Host != r.bucket ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.ForceQuery ||
		!validator.IsDNS1123Label(namespace) || (suffix != "" && suffix != "/_kfp_preview.json" && suffix != ".kfp-preview.json") {
		return nil, apperr.New(apperr.CodeInvalid, "产物地址不属于已配置的 MinIO/S3 存储")
	}
	key := strings.TrimPrefix(u.Path, "/")
	if suffix != "" {
		key = strings.TrimSuffix(key, "/")
	}
	if !validator.IsArtifactKey(key) || !strings.HasPrefix(key, r.prefix+"/"+namespace+"/") || !validator.IsArtifactKey(key+suffix) {
		return nil, apperr.New(apperr.CodeForbidden, "产物不在当前组的存储目录内")
	}
	return r.readKey(ctx, key+suffix, limit)
}

// ReadNative has a separate exact run/namespace-UID prefix. It does not weaken
// the historical namespace path validation of Read, or accept a remote URL.
func (r *Reader) ReadNative(ctx context.Context, groupID uint64, namespaceUID, instance, endpoint, bucket, key string, limit int64) (*Object, error) {
	prefix := fmt.Sprintf("loom-native/%d/%s/%s/", groupID, namespaceUID, instance)
	if groupID == 0 || !validator.IsCatalogID(namespaceUID) || !validator.IsCanonicalUUID(instance) || endpoint != r.endpoint || bucket != r.bucket ||
		!validator.IsArtifactKey(key) || !strings.HasPrefix(key, prefix) {
		return nil, apperr.New(apperr.CodeForbidden, "原生产物不属于配置的存储或本次执行目录")
	}
	return r.readKey(ctx, key, limit)
}

func (r *Reader) readKey(ctx context.Context, key string, limit int64) (*Object, error) {
	if limit < 1 || limit > MaxDownloadBytes {
		return nil, apperr.New(apperr.CodeInvalid, "产物读取大小无效")
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, apperr.New(apperr.CodeRateLimited, "产物读取繁忙，请稍后重试")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	stat, err := r.client.StatObject(ctx, r.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, storageError(err)
	}
	if stat.Size < 0 || stat.Size > limit {
		return nil, apperr.New(apperr.CodeQuotaExceeded, "产物超过读取上限，请生成较小的预览文件")
	}
	options := minio.GetObjectOptions{}
	if err := options.SetMatchETag(stat.ETag); err != nil {
		return nil, storageError(err)
	}
	obj, err := r.client.GetObject(ctx, r.bucket, key, options)
	if err != nil {
		return nil, storageError(err)
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, limit+1))
	if err != nil {
		return nil, storageError(err)
	}
	if int64(len(data)) != stat.Size {
		return nil, apperr.New(apperr.CodeConflict, "产物大小已变化，请刷新")
	}
	return &Object{Data: data, Name: path.Base(key), ETag: stat.ETag}, nil
}

func storageError(err error) error {
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchObject":
		return apperr.New(apperr.CodeNotFound, "产物文件不存在；目录产物需显式提供 KFP 预览")
	case "PreconditionFailed":
		return apperr.New(apperr.CodeConflict, "产物已变化，请刷新")
	default:
		return apperr.Wrap(apperr.CodeUnavailable, "产物存储暂不可用", err)
	}
}
