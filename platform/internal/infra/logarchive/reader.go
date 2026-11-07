// Package logarchive reads immutable log objects on demand. It never writes a DB index.
package logarchive

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const MaxChunkBytes int64 = 1 << 20
const maxManifestBytes int64 = 2 << 20

type Execution struct {
	Cluster    string `json:"cluster"`
	Namespace  string `json:"namespace"`
	InstanceID string `json:"instanceId"`
}

type Entry struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	Pod       string `json:"pod"`
	PodUID    string `json:"podUID"`
	Container string `json:"container"`
	Key       string `json:"key,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	ETag      string `json:"etag"`
}

func (e Entry) valid() bool {
	return validator.IsArchiveSegment(e.ID) && validator.IsIdempotencyKey(e.TaskID) &&
		validator.IsDNS1123Label(e.Container) && validator.IsDNS1123Subdomain(e.Pod) &&
		validator.IsArchiveSegment(e.PodUID) && validator.IsArchivePath(e.Key) &&
		strings.HasPrefix(e.Key, e.PodUID+"/"+e.Container+"/") &&
		e.SizeBytes >= 0 && e.SizeBytes <= (1<<60) && validator.IsArchiveETag(e.ETag)
}

type Manifest struct {
	Version int `json:"version"`
	Execution
	Complete bool    `json:"complete"`
	Entries  []Entry `json:"entries"`
}

type Chunk struct {
	Execution
	EntryID    string `json:"entryId"`
	ETag       string `json:"etag"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"nextOffset"`
	SizeBytes  int64  `json:"sizeBytes"`
	EOF        bool   `json:"eof"`
	Text       string `json:"text"`
}

type Reader struct {
	client         *minio.Client
	bucket, prefix string
	timeout        time.Duration
}

// New constructs a client only; no network calls or bucket creation at startup.
func New(c config.LogArchive) (*Reader, error) {
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
	return &Reader{client: client, bucket: c.Bucket, prefix: c.Prefix, timeout: c.Timeout.Std()}, nil
}

func (r *Reader) executionPrefix(e Execution) (string, error) {
	if !validator.IsArchiveSegment(e.Cluster) || !validator.IsDNS1123Label(e.Namespace) || !validator.IsArchiveSegment(e.InstanceID) {
		return "", apperr.New(apperr.CodeConflict, "执行归档标识无效")
	}
	return strings.Join([]string{r.prefix, e.Cluster, e.Namespace, e.InstanceID}, "/") + "/", nil
}

func archiveError(err error) error {
	if err == nil {
		return nil
	}
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchObject":
		return apperr.Wrap(apperr.CodeNotFound, "日志尚未归档或已过期清理", err)
	case "PreconditionFailed", "InvalidRange":
		return apperr.Wrap(apperr.CodeConflict, "归档文件已变化，请刷新归档列表", err)
	default:
		return apperr.Wrap(apperr.CodeUnavailable, "日志归档存储暂不可用", err)
	}
}

func invalidManifest() error {
	return apperr.New(apperr.CodeConflict, "日志归档清单无效或不属于该次执行")
}

func (r *Reader) Manifest(ctx context.Context, e Execution) (*Manifest, error) {
	prefix, err := r.executionPrefix(e)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	object, err := r.client.GetObject(ctx, r.bucket, prefix+"manifest.json", minio.GetObjectOptions{})
	if err != nil {
		return nil, archiveError(err)
	}
	defer object.Close()
	data, err := io.ReadAll(io.LimitReader(object, maxManifestBytes+1))
	if err != nil {
		return nil, archiveError(err)
	}
	if int64(len(data)) > maxManifestBytes {
		return nil, invalidManifest()
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil || m.Version != 1 || m.Execution != e || len(m.Entries) > 10000 {
		return nil, invalidManifest()
	}
	ids := map[string]bool{}
	for _, entry := range m.Entries {
		if !entry.valid() || ids[entry.ID] {
			return nil, invalidManifest()
		}
		ids[entry.ID] = true
	}
	if m.Entries == nil {
		m.Entries = []Entry{}
	}
	return &m, nil
}

// Read fetches a bounded byte range from plain UTF-8 objects. ETag pins pagination.
func (r *Reader) Read(ctx context.Context, e Execution, entry Entry, offset, limit int64) (*Chunk, error) {
	if !entry.valid() {
		return nil, invalidManifest()
	}
	if offset < 0 || offset > entry.SizeBytes || limit < 1 || limit > MaxChunkBytes {
		return nil, apperr.New(apperr.CodeInvalid, "归档 offset 或 limitBytes 超出范围")
	}
	prefix, err := r.executionPrefix(e)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	stat, err := r.client.StatObject(ctx, r.bucket, prefix+entry.Key, minio.StatObjectOptions{})
	if err != nil {
		return nil, archiveError(err)
	}
	if stat.Size != entry.SizeBytes || stat.ETag != entry.ETag {
		return nil, invalidManifest()
	}
	chunk := &Chunk{Execution: e, EntryID: entry.ID, ETag: entry.ETag, Offset: offset, NextOffset: offset, SizeBytes: entry.SizeBytes}
	if offset == entry.SizeBytes {
		chunk.EOF = true
		return chunk, nil
	}
	count := min(limit, entry.SizeBytes-offset)
	options := minio.GetObjectOptions{}
	if err := options.SetMatchETag(entry.ETag); err != nil {
		return nil, invalidManifest()
	}
	if err := options.SetRange(offset, offset+count-1); err != nil {
		return nil, invalidManifest()
	}
	object, err := r.client.GetObject(ctx, r.bucket, prefix+entry.Key, options)
	if err != nil {
		return nil, archiveError(err)
	}
	defer object.Close()
	data, err := io.ReadAll(io.LimitReader(object, count+1))
	if err != nil {
		return nil, archiveError(err)
	}
	if int64(len(data)) != count {
		return nil, invalidManifest()
	}
	// Keep continuation offsets on UTF-8 boundaries. Invalid source bytes use replacement characters.
	if offset+count < entry.SizeBytes {
		for len(data) > 0 {
			start := len(data) - 1
			for start > 0 && !utf8.RuneStart(data[start]) {
				start--
			}
			if utf8.FullRune(data[start:]) {
				break
			}
			data = data[:start]
		}
		if len(data) == 0 {
			return nil, apperr.New(apperr.CodeInvalid, "limitBytes 太小，无法容纳 UTF-8 字符")
		}
	}
	chunk.Text = strings.ToValidUTF8(string(data), "\uFFFD")
	chunk.NextOffset = offset + int64(len(data))
	chunk.EOF = chunk.NextOffset == entry.SizeBytes
	return chunk, nil
}
