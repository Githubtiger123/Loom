package artifactstore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/minio/minio-go/v7"
)

const MaxModelBytes int64 = 10 << 30

// NativeModelSource is a server-resolved immutable transport manifest, never a
// browser-supplied endpoint/key. Publication uses separate, prefix-limited creds.
type NativeModelSource struct {
	Root        string `json:"root,omitempty"`
	URI         string `json:"uri,omitempty"`
	Endpoint    string `json:"endpoint"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Format      string `json:"format"`
	Kind        string `json:"kind"`
	DecodedSize int64  `json:"decodedSize"`
	Entries     int    `json:"entries"`
}

type PublishedModel struct {
	URI, Digest string
	Size        uint64
}
type ModelPublisher struct{ source, target *Reader }

func NewModelPublisher(source, target config.ArtifactStore) (*ModelPublisher, error) {
	a, err := New(source)
	if err != nil {
		return nil, err
	}
	b, err := New(target)
	if err != nil {
		return nil, err
	}
	return &ModelPublisher{source: a, target: b}, nil
}

func (p *ModelPublisher) Publish(ctx context.Context, groupID uint64, namespaceUID, instance, operationID, fileName string, source NativeModelSource, guard func() error) (*PublishedModel, error) {
	if source.Format == "OFFICIAL_V1" {
		return p.publishOfficial(ctx, groupID, namespaceUID, instance, operationID, fileName, source, guard)
	}
	bad := func() error {
		return apperr.New(apperr.CodeInvalid, "模型归档不安全、摘要/大小不一致或超过 10 GiB/10000 项上限")
	}
	prefix := fmt.Sprintf("loom-native/%d/%s/%s/", groupID, namespaceUID, instance)
	if groupID == 0 || !validator.IsCatalogID(namespaceUID) || !validator.IsCanonicalUUID(instance) || !validator.IsCanonicalUUID(operationID) ||
		source.Endpoint != p.source.endpoint || source.Bucket != p.source.bucket || !validator.IsArtifactKey(source.Key) || !strings.HasPrefix(source.Key, prefix) ||
		!validator.IsSHA256(source.SHA256) || source.Size < 1 || source.Size > MaxModelBytes || source.DecodedSize < 0 || source.DecodedSize > MaxModelBytes ||
		source.Entries < 1 || source.Entries > 10000 || source.Format != "TAR_GZIP_V1" || (source.Kind != "FILE" && source.Kind != "DIRECTORY") ||
		!validator.IsModelRegistryName(fileName) {
		return nil, bad()
	}
	select {
	case p.source.slots <- struct{}{}:
		defer func() { <-p.source.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, apperr.New(apperr.CodeRateLimited, "模型导入繁忙，请稍后重试")
	}
	if err := guard(); err != nil {
		return nil, err
	}
	stat, err := p.source.client.StatObject(ctx, source.Bucket, source.Key, minio.StatObjectOptions{})
	if err != nil {
		return nil, storageError(err)
	}
	if stat.Size != source.Size {
		return nil, bad()
	}
	opts := minio.GetObjectOptions{}
	if err = opts.SetMatchETag(stat.ETag); err != nil {
		return nil, storageError(err)
	}
	object, err := p.source.client.GetObject(ctx, source.Bucket, source.Key, opts)
	if err != nil {
		return nil, storageError(err)
	}
	defer object.Close()
	// Only the compressed object is spooled, at a private random temporary path.
	// No user archive member is extracted to the host filesystem.
	tmp, err := os.CreateTemp("", "loom-model-*.tar.gz")
	if err != nil {
		return nil, apperr.New(apperr.CodeUnavailable, "模型导入临时存储不可用")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, digest), io.LimitReader(object, source.Size+1))
	if err != nil {
		return nil, storageError(err)
	}
	if n != source.Size || fmt.Sprintf("%x", digest.Sum(nil)) != source.SHA256 {
		return nil, bad()
	}
	// Validate the entire archive before publishing even the first serving file.
	var fileDigest string
	files := 0
	walk := func(visit func(*tar.Header, io.Reader) error) error {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return err
		}
		gz, err := gzip.NewReader(tmp)
		if err != nil {
			return bad()
		}
		defer gz.Close()
		// Bound all decompressed tar headers/padding too, not only file payloads.
		decodedStream := io.LimitReader(gz, MaxModelBytes+(64<<20)+1)
		tr := tar.NewReader(decodedStream)
		seen := map[string]byte{}
		var decoded int64
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return bad()
			}
			name := h.Name
			if h.Typeflag == tar.TypeDir {
				name = strings.TrimSuffix(name, "/")
			}
			if len(seen) >= 10000 || len(name) > 1024 || strings.Contains(name, "\\") || path.Clean(name) != name || (name != "data" && !strings.HasPrefix(name, "data/")) ||
				(h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) || h.Size < 0 || (h.Typeflag == tar.TypeDir && h.Size != 0) {
				return bad()
			}
			if _, ok := seen[name]; ok {
				return bad()
			}
			for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
				if kind, ok := seen[parent]; !ok || kind != tar.TypeDir {
					return bad()
				}
			}
			if name == "data" && ((source.Kind == "FILE" && h.Typeflag != tar.TypeReg) || (source.Kind == "DIRECTORY" && h.Typeflag != tar.TypeDir)) {
				return bad()
			}
			if source.Kind == "FILE" && name != "data" {
				return bad()
			}
			seen[name] = h.Typeflag
			decoded += h.Size
			if decoded > MaxModelBytes || decoded > source.DecodedSize {
				return bad()
			}
			if h.Typeflag == tar.TypeReg {
				if err := visit(h, tr); err != nil {
					return err
				}
			}
		}
		padding, err := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1))
		if err != nil || padding > 1<<20 || decoded != source.DecodedSize || len(seen) != source.Entries {
			return bad()
		}
		if _, ok := seen["data"]; !ok {
			return bad()
		}
		return nil
	}
	err = walk(func(h *tar.Header, r io.Reader) error {
		files++
		hash := sha256.New()
		n, err := io.Copy(hash, r)
		if err != nil || n != h.Size {
			return bad()
		}
		if source.Kind == "FILE" {
			fileDigest = fmt.Sprintf("%x", hash.Sum(nil))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if files == 0 {
		return nil, bad()
	}
	root := fmt.Sprintf("%s/%d/%s/%s/%s", p.target.prefix, groupID, namespaceUID, operationID, source.SHA256)
	err = walk(func(h *tar.Header, r io.Reader) error {
		if err := guard(); err != nil {
			return err
		}
		relative := strings.TrimPrefix(h.Name, "data/")
		if source.Kind == "FILE" {
			relative = fileName
		}
		key := root + "/" + relative
		if !validator.IsArtifactKey(key) {
			return bad()
		}
		_, err := p.target.client.PutObject(ctx, p.target.bucket, key, r, h.Size, minio.PutObjectOptions{ContentType: "application/octet-stream", PartSize: 16 << 20, NumThreads: 1})
		if err != nil {
			return storageError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	uri := "s3://" + p.target.bucket + "/" + root + "/"
	// A versioned prefix is used even for files: KServe runtimes expect a model
	// directory with its framework-specific filename (e.g. model.joblib).
	return &PublishedModel{URI: uri, Digest: fileDigest, Size: uint64(source.DecodedSize)}, nil
}
