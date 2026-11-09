package logarchive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/minio/minio-go/v7"
)

const maxNativeObjectBytes int64 = 32 << 20
const maxNativeDecodedBytes int64 = 32 << 20
const maxNativeEntries = 10000

// Argo's archiveLogs creates <keyFormat>/main.log. Prefixes include cluster,
// namespace and immutable Run UUID; neither a caller nor KFP supplies a raw key.
// No ListBuckets/global scan, KFP lookup or DB log index is needed after deletion.
func (r *Reader) argoManifest(ctx context.Context, e Execution) (*Manifest, error) {
	prefix, err := r.executionPrefix(e)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	listCtx, stop := context.WithCancel(ctx)
	defer stop()
	m := &Manifest{Version: 1, Source: "ARGO", Execution: e, Entries: []Entry{}}
	count := 0
	for object := range r.client.ListObjects(listCtx, r.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, archiveError(object.Err)
		}
		count++
		if count > maxNativeEntries {
			return nil, apperr.New(apperr.CodeConflict, "本次执行归档过多，不能返回不完整日志列表")
		}
		if !strings.HasPrefix(object.Key, prefix) {
			return nil, invalidManifest()
		}
		key := strings.TrimPrefix(object.Key, prefix)
		parts := strings.Split(key, "/")
		if len(parts) != 2 || !validator.IsDNS1123Subdomain(parts[0]) || !argoLogName(parts[1]) {
			continue // This is a log-only API, not an arbitrary artifact browser.
		}
		entry := Entry{ID: argoEntryID(key), Pod: parts[0], Container: "main", Key: key,
			SizeBytes: object.Size, ETag: strings.Trim(object.ETag, "\"")}
		if !validArgoEntry(entry) {
			return nil, invalidManifest()
		}
		m.Entries = append(m.Entries, entry)
	}
	if ctx.Err() != nil {
		return nil, archiveError(ctx.Err())
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].ID < m.Entries[j].ID })
	// An empty list means not archived/expired/no real Pod; never claim all logs
	// are complete merely because the bucket listing succeeded.
	return m, nil
}

func argoLogName(name string) bool {
	return name == "main.log" || name == "main.log.gz" || name == "main.log.tgz"
}

func argoEntryID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "argo-" + hex.EncodeToString(sum[:])
}

func validArgoEntry(e Entry) bool {
	parts := strings.Split(e.Key, "/")
	return len(parts) == 2 && len(e.Key) <= 1024 &&
		validator.IsDNS1123Subdomain(parts[0]) && argoLogName(parts[1]) &&
		e.ID == argoEntryID(e.Key) && e.Pod == parts[0] && e.Container == "main" &&
		e.TaskID == "" && e.PodUID == "" && e.SizeBytes >= 0 && validator.IsArchiveETag(e.ETag)
}

func (r *Reader) readArgo(ctx context.Context, e Execution, entry Entry, offset, limit int64) (*Chunk, error) {
	if !validArgoEntry(entry) {
		return nil, invalidManifest()
	}
	if offset < 0 || offset > maxNativeDecodedBytes || limit < 1 || limit > MaxChunkBytes {
		return nil, apperr.New(apperr.CodeInvalid, "归档读取范围无效")
	}
	if entry.SizeBytes > maxNativeObjectBytes {
		return nil, apperr.New(apperr.CodeConflict, "归档日志超过 32 MiB 在线读取上限")
	}
	prefix, err := r.executionPrefix(e)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	select {
	case r.nativeSlots <- struct{}{}:
		defer func() { <-r.nativeSlots }()
	case <-ctx.Done():
		return nil, archiveError(ctx.Err())
	}
	stat, err := r.client.StatObject(ctx, r.bucket, prefix+entry.Key, minio.StatObjectOptions{})
	if err != nil {
		return nil, archiveError(err)
	}
	if stat.Size != entry.SizeBytes || stat.ETag != entry.ETag {
		return nil, invalidManifest()
	}
	options := minio.GetObjectOptions{}
	if err := options.SetMatchETag(entry.ETag); err != nil {
		return nil, invalidManifest()
	}
	object, err := r.client.GetObject(ctx, r.bucket, prefix+entry.Key, options)
	if err != nil {
		return nil, archiveError(err)
	}
	defer object.Close()
	raw, err := io.ReadAll(io.LimitReader(object, maxNativeObjectBytes+1))
	if err != nil {
		return nil, archiveError(err)
	}
	if int64(len(raw)) != entry.SizeBytes {
		return nil, invalidManifest()
	}
	data, err := decodeArgoLog(raw)
	if err != nil {
		return nil, err
	}
	if offset > int64(len(data)) {
		return nil, apperr.New(apperr.CodeInvalid, "offset 超出解压后日志长度")
	}
	end := min(offset+limit, int64(len(data)))
	// Offsets address decoded bytes. Keep continuation on UTF-8 boundaries.
	if end < int64(len(data)) && end > offset {
		start := end - 1
		for start > offset && !utf8.RuneStart(data[start]) {
			start--
		}
		if !utf8.FullRune(data[start:end]) {
			end = start
		}
	}
	if end == offset && end < int64(len(data)) {
		return nil, apperr.New(apperr.CodeInvalid, "limitBytes 太小，无法容纳 UTF-8 字符")
	}
	return &Chunk{Execution: e, EntryID: entry.ID, ETag: entry.ETag, Offset: offset,
		NextOffset: end, SizeBytes: int64(len(data)), EOF: end == int64(len(data)),
		Text: strings.ToValidUTF8(string(data[offset:end]), "\uFFFD")}, nil
}

// Read plaintext, gzip, or a single-file tar+gzip. Never extract files to disk;
// bound both compressed and decoded sizes to reject decompression bombs.
func decodeArgoLog(raw []byte) ([]byte, error) {
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return raw, nil
	}
	z, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, invalidManifest()
	}
	defer z.Close()
	data, err := io.ReadAll(io.LimitReader(z, maxNativeDecodedBytes+1))
	if err != nil || int64(len(data)) > maxNativeDecodedBytes {
		return nil, apperr.New(apperr.CodeConflict, "归档压缩格式无效或解压后超过 32 MiB")
	}
	// Like KFP, detect a real tar header rather than guessing by the .log suffix.
	t := tar.NewReader(bytes.NewReader(data))
	header, err := t.Next()
	if err != nil {
		return data, nil // gzip containing plain text, not tar.
	}
	name := strings.TrimPrefix(header.Name, "./")
	if header.Typeflag != tar.TypeReg || !validator.IsArchivePath(name) ||
		(name != "main.log" && !strings.HasSuffix(name, "/main.log")) {
		return nil, invalidManifest()
	}
	text, err := io.ReadAll(io.LimitReader(t, maxNativeDecodedBytes+1))
	if err != nil || int64(len(text)) > maxNativeDecodedBytes {
		return nil, invalidManifest()
	}
	if _, err := t.Next(); err != io.EOF {
		return nil, invalidManifest()
	}
	return text, nil
}
