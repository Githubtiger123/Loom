package artifactstore

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/minio/minio-go/v7"
)

// Inventory contains exact object versions observed by the backend. Keys and
// credentials are never accepted from a browser. ETags are conditional-read
// fences, not cryptographic content hashes or a promise of immutable storage.
type OfficialFile struct {
	Name string `json:"name"`
	Size int64  `json:"sizeBytes"`
	ETag string `json:"etag"`
}

type OfficialInventory struct {
	URI       string         `json:"uri"`
	Directory bool           `json:"directory"`
	Files     []OfficialFile `json:"files"`
}

// Exact conditional read; caller must resolve membership from the inventory.
// root and uri come exclusively from an authorized, frozen runtime artifact.
func (r *Reader) ReadOfficialFile(ctx context.Context, root, uri, etag string, limit int64) (*Object, error) {
	key, err := r.officialKey(root, uri)
	if err != nil {
		return nil, err
	}
	if etag == "" {
		return nil, apperr.New(apperr.CodeInvalid, "缺少文件版本")
	}
	return r.readKeyMatch(ctx, key, limit, etag)
}

func (v OfficialInventory) Digest() string {
	raw, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (r *Reader) officialKey(root, uri string) (string, error) {
	if !validator.IsObjectURIWithin(root, uri) {
		return "", apperr.New(apperr.CodeForbidden, "产物不属于冻结存储目录")
	}
	u, err := url.Parse(uri)
	if err != nil || u.Host != r.bucket || (u.Scheme != "s3" && u.Scheme != "minio") || u.RawPath != "" {
		return "", apperr.New(apperr.CodeForbidden, "产物不属于配置的对象存储")
	}
	key := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	if !validator.IsArtifactKey(key) || !strings.HasPrefix(key, r.prefix+"/") {
		return "", apperr.New(apperr.CodeForbidden, "产物超出配置的存储权限目录")
	}
	return key, nil
}

func (r *Reader) officialIO(ctx context.Context) (context.Context, func(), error) {
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
		return nil, nil, apperr.New(apperr.CodeRateLimited, "产物存储繁忙")
	}
	c, cancel := context.WithTimeout(ctx, r.timeout)
	return c, func() { cancel(); <-r.slots }, nil
}

func (r *Reader) InventoryOfficial(ctx context.Context, root, uri string) (*OfficialInventory, error) {
	key, err := r.officialKey(root, uri)
	if err != nil {
		return nil, err
	}
	ctx, done, err := r.officialIO(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	result := &OfficialInventory{URI: uri, Files: []OfficialFile{}}
	stat, err := r.client.StatObject(ctx, r.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		if stat.Size < 0 || stat.ETag == "" {
			return nil, apperr.New(apperr.CodeConflict, "产物对象清单无效")
		}
		result.Files = append(result.Files, OfficialFile{Name: path.Base(key), Size: stat.Size, ETag: stat.ETag})
		return result, nil
	}
	code := minio.ToErrorResponse(err).Code
	if code != "NoSuchKey" && code != "NoSuchObject" {
		return nil, storageError(err)
	}
	result.Directory = true
	for file := range r.client.ListObjects(ctx, r.bucket, minio.ListObjectsOptions{Prefix: key + "/", Recursive: true}) {
		if file.Err != nil {
			return nil, storageError(file.Err)
		}
		name := strings.TrimPrefix(file.Key, key+"/")
		if strings.HasSuffix(name, "/") && file.Size == 0 {
			continue
		}
		if len(result.Files) >= 4096 || name == "" || !validator.IsArtifactKey(name) || file.Size < 0 || file.ETag == "" {
			return nil, apperr.New(apperr.CodeQuotaExceeded, "目录清单无效或超过 4096 个文件")
		}
		result.Files = append(result.Files, OfficialFile{Name: name, Size: file.Size, ETag: file.ETag})
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(result.Files) == 0 {
		return nil, apperr.New(apperr.CodeNotFound, "产物文件或目录尚未上传")
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Name < result.Files[j].Name })
	return result, nil
}

// ReadOfficialRange performs a real bounded Range request, not a whole-object
// download for every log chunk. The caller selected URI and ETag from metadata.
func (r *Reader) ReadOfficialRange(ctx context.Context, root, uri, etag string, offset, limit int64) (*Object, int64, error) {
	key, err := r.officialKey(root, uri)
	if err != nil {
		return nil, 0, err
	}
	if offset < 0 || limit < 1 || limit > 1<<20 || etag == "" {
		return nil, 0, apperr.New(apperr.CodeInvalid, "日志范围无效")
	}
	ctx, done, err := r.officialIO(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer done()
	stat, err := r.client.StatObject(ctx, r.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, 0, storageError(err)
	}
	if stat.ETag != etag || offset > stat.Size {
		return nil, 0, apperr.New(apperr.CodeConflict, "日志文件已变化，请刷新")
	}
	if offset == stat.Size {
		return &Object{Name: path.Base(key), ETag: etag, Data: []byte{}}, stat.Size, nil
	}
	count := min(limit, stat.Size-offset)
	opts := minio.GetObjectOptions{}
	if err = opts.SetMatchETag(etag); err != nil {
		return nil, 0, err
	}
	if err = opts.SetRange(offset, offset+count-1); err != nil {
		return nil, 0, err
	}
	obj, err := r.client.GetObject(ctx, r.bucket, key, opts)
	if err != nil {
		return nil, 0, storageError(err)
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, count+1))
	if err != nil {
		return nil, 0, storageError(err)
	}
	if int64(len(data)) != count {
		return nil, 0, apperr.New(apperr.CodeConflict, "日志大小变化")
	}
	// Trim only an incomplete trailing rune, not arbitrary binary/invalid
	// bytes earlier in the chunk. Always advance even for a 1-byte request.
	if offset+count < stat.Size && len(data) > 1 {
		start := len(data) - 1
		for start > 0 && !utf8.RuneStart(data[start]) && len(data)-start < 4 {
			start--
		}
		if start > 0 && !utf8.FullRune(data[start:]) {
			data = data[:start]
		}
	}
	return &Object{Name: path.Base(key), ETag: etag, Data: data}, stat.Size, nil
}

// DownloadOfficial returns a regular file or a bounded ZIP directory. The
// inventory is rechecked after reading. ETags are observed consistency fences,
// not an object-lock guarantee against concurrent writes or ETag-preserving ABA.
func (r *Reader) DownloadOfficial(ctx context.Context, root, uri string, limit int64) (*Object, error) {
	if limit < 1 || limit > MaxDownloadBytes {
		return nil, apperr.New(apperr.CodeInvalid, "产物下载上限无效")
	}
	inv, err := r.InventoryOfficial(ctx, root, uri)
	if err != nil {
		return nil, err
	}
	if !inv.Directory {
		return r.ReadOfficial(ctx, root, uri, "", limit)
	}
	var total int64
	for _, f := range inv.Files {
		if f.Size < 0 || f.Size > limit-total {
			return nil, apperr.New(apperr.CodeQuotaExceeded, "目录超过下载上限")
		}
		total += f.Size
	}
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for _, file := range inv.Files {
		obj, err := r.ReadOfficial(ctx, root, strings.TrimRight(uri, "/")+"/"+file.Name, "", max(file.Size, 1))
		if err != nil {
			return nil, err
		}
		if obj.ETag != file.ETag {
			return nil, apperr.New(apperr.CodeConflict, "目录文件已变化")
		}
		entry, err := zw.CreateHeader(&zip.FileHeader{Name: file.Name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write(obj.Data); err != nil {
			return nil, err
		}
		if buffer.Len() > int(limit)+(2<<20) {
			return nil, apperr.New(apperr.CodeQuotaExceeded, "目录归档过大")
		}
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > int(limit)+(2<<20) {
		return nil, apperr.New(apperr.CodeQuotaExceeded, "目录归档过大")
	}
	fresh, err := r.InventoryOfficial(ctx, root, uri)
	if err != nil {
		return nil, err
	}
	if fresh.Digest() != inv.Digest() {
		return nil, apperr.New(apperr.CodeConflict, "目录清单已变化")
	}
	return &Object{Data: buffer.Bytes(), Name: path.Base(strings.TrimRight(uri, "/")) + ".zip", ETag: inv.Digest()}, nil
}

// CopyOfficial copies only a previously frozen inventory to the destination
// attempt root. Source If-Match fences every file; no cross-run URI escapes.
func (r *Reader) CopyOfficial(ctx context.Context, sourceRoot string, inv OfficialInventory, destinationRoot, destination string) (*OfficialInventory, error) {
	if r.cacheCopyClient == nil || !strings.HasPrefix(destination, strings.TrimRight(destinationRoot, "/")+"/__cache/") {
		return nil, apperr.New(apperr.CodeUnavailable, "缓存复制需要独立的前缀受限读写凭据")
	}
	source, err := r.officialKey(sourceRoot, inv.URI)
	if err != nil {
		return nil, err
	}
	target, err := r.officialKey(destinationRoot, destination)
	if err != nil {
		return nil, err
	}
	if len(inv.Files) == 0 || len(inv.Files) > 4096 {
		return nil, apperr.New(apperr.CodeInvalid, "缓存清单无效")
	}
	err = func() error {
		copyCtx, done, err := r.officialIO(ctx)
		if err != nil {
			return err
		}
		defer done()
		for _, file := range inv.Files {
			if !validator.IsArtifactKey(file.Name) || file.ETag == "" || file.Size < 0 {
				return apperr.New(apperr.CodeInvalid, "缓存文件无效")
			}
			from, to := source, target
			if inv.Directory {
				from += "/" + file.Name
				to += "/" + file.Name
			}
			dst := minio.CopyDestOptions{Bucket: r.bucket, Object: to}
			src := minio.CopySrcOptions{Bucket: r.bucket, Object: from, MatchETag: file.ETag}
			if file.Size > 5<<30 {
				_, err = r.cacheCopyClient.ComposeObject(copyCtx, dst, src)
			} else {
				_, err = r.cacheCopyClient.CopyObject(copyCtx, dst, src)
			}
			if err != nil {
				return storageError(err)
			}
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	return r.InventoryOfficial(ctx, destinationRoot, destination)
}

func (r *Reader) CacheCopyConfigured() bool { return r.cacheCopyClient != nil }
