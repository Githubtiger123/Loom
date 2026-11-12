package artifactstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"path"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
)

// DecodeNativeFile keeps single-file download/preview semantics. Directory
// downloads remain a tar.gz; no archive member is ever extracted to disk.
func DecodeNativeFile(object *Object, kind string, decodedSize int64, entries int, limit int64) error {
	bad := func() error { return apperr.New(apperr.CodeConflict, "原生产物归档格式或大小无效") }
	if object == nil || decodedSize < 0 || entries < 1 || entries > 10000 {
		return bad()
	}
	if kind == "DIRECTORY" {
		object.Name = "artifact.tar.gz"
		return nil
	}
	if kind != "FILE" || entries != 1 || decodedSize > limit {
		return bad()
	}
	gz, err := gzip.NewReader(bytes.NewReader(object.Data))
	if err != nil {
		return bad()
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	header, err := reader.Next()
	if err != nil || header.Typeflag != tar.TypeReg || path.Clean(header.Name) != "data" || strings.Contains(header.Name, "\\") || header.Size != decodedSize {
		return bad()
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) != decodedSize {
		return bad()
	}
	if _, err = reader.Next(); err != io.EOF {
		return bad()
	}
	// Consume the gzip trailer so truncated/corrupted streams cannot pass on
	// the strength of a valid first tar entry alone.
	if _, err = io.Copy(io.Discard, io.LimitReader(gz, 1<<20)); err != nil {
		return bad()
	}
	object.Data = data
	return nil
}
