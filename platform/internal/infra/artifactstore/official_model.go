package artifactstore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/minio/minio-go/v7"
)

// The official launcher uploads raw files/directories, not Loom v3 tarballs.
// Copy only the server-resolved output, with per-file If-Match and a final
// inventory recheck. Incomplete copies are never registered as ModelArtifact.
func (p *ModelPublisher) publishOfficial(ctx context.Context, gid uint64, namespaceUID, instance, operationID, fileName string, source NativeModelSource, guard func() error) (*PublishedModel, error) {
	if gid == 0 || !validator.IsCatalogID(namespaceUID) || !validator.IsCanonicalUUID(instance) || !validator.IsCanonicalUUID(operationID) || !validator.IsModelRegistryName(fileName) || !strings.HasSuffix(strings.TrimRight(source.Root, "/"), fmt.Sprintf("/%d/%s/%s", gid, namespaceUID, instance)) {
		return nil, apperr.New(apperr.CodeInvalid, "官方模型来源身份无效")
	}
	if err := guard(); err != nil {
		return nil, err
	}
	key, err := p.source.officialKey(source.Root, source.URI)
	if err != nil {
		return nil, err
	}
	inv, err := p.source.InventoryOfficial(ctx, source.Root, source.URI)
	if err != nil {
		return nil, err
	}
	var size int64
	for _, file := range inv.Files {
		if file.Size < 0 || file.Size > MaxModelBytes-size {
			return nil, apperr.New(apperr.CodeQuotaExceeded, "模型产物超过 10 GiB")
		}
		size += file.Size
	}
	root := fmt.Sprintf("%s/%d/%s/%s/%s", p.target.prefix, gid, namespaceUID, operationID, inv.Digest())
	contentHash := sha256.New()
	for _, file := range inv.Files {
		if err := guard(); err != nil {
			return nil, err
		}
		from, relative := key, fileName
		if inv.Directory {
			from += "/" + file.Name
			relative = file.Name
		}
		to := root + "/" + relative
		if !validator.IsArtifactKey(to) {
			return nil, apperr.New(apperr.CodeInvalid, "模型文件名无效")
		}
		digest, err := p.copyOfficialModelFile(ctx, from, to, file)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(contentHash, "%s\x00%d\x00%x\n", relative, file.Size, digest)
	}
	fresh, err := p.source.InventoryOfficial(ctx, source.Root, source.URI)
	if err != nil {
		return nil, err
	}
	if fresh.Digest() != inv.Digest() {
		return nil, apperr.New(apperr.CodeConflict, "复制期间模型来源清单已变化")
	}
	if err := guard(); err != nil {
		return nil, err
	}
	return &PublishedModel{URI: "s3://" + p.target.bucket + "/" + root + "/", Digest: fmt.Sprintf("%x", contentHash.Sum(nil)), Size: uint64(size)}, nil
}

// Model streaming uses the worker's bounded operation context, not the short
// preview timeout. Release slots per file before the final inventory read.
func (p *ModelPublisher) copyOfficialModelFile(ctx context.Context, from, to string, file OfficialFile) ([]byte, error) {
	select {
	case p.source.slots <- struct{}{}:
		defer func() { <-p.source.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, apperr.New(apperr.CodeRateLimited, "模型导入繁忙，请稍后重试")
	}
	select {
	case p.target.slots <- struct{}{}:
		defer func() { <-p.target.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, apperr.New(apperr.CodeRateLimited, "模型存储繁忙，请稍后重试")
	}
	opts := minio.GetObjectOptions{}
	if err := opts.SetMatchETag(file.ETag); err != nil {
		return nil, err
	}
	obj, err := p.source.client.GetObject(ctx, p.source.bucket, from, opts)
	if err != nil {
		return nil, storageError(err)
	}
	defer obj.Close()
	hash := sha256.New()
	limited := &io.LimitedReader{R: obj, N: file.Size + 1}
	info, err := p.target.client.PutObject(ctx, p.target.bucket, to, io.TeeReader(limited, hash), file.Size, minio.PutObjectOptions{ContentType: "application/octet-stream", PartSize: 16 << 20, NumThreads: 1})
	if err != nil {
		return nil, storageError(err)
	}
	tail, err := io.ReadAll(io.LimitReader(obj, 1))
	if err != nil {
		return nil, storageError(err)
	}
	if info.Size != file.Size || limited.N != 1 || len(tail) != 0 {
		return nil, apperr.New(apperr.CodeConflict, "模型源文件大小与冻结清单不一致")
	}
	return hash.Sum(nil), nil
}
