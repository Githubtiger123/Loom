package pipelineviz

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
)

type Preview struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	ETag      string `json:"etag"`
	MediaType string `json:"mediaType"`
	Encoding  string `json:"encoding,omitempty"`
	Text      string `json:"text,omitempty"`
	Data      any    `json:"data,omitempty"`
	// HTML must be rendered in an opaque-origin sandbox, never in the app DOM.
	Sandbox string `json:"sandbox,omitempty"`
}
type Table struct {
	Columns   []string   `json:"columns"`
	Rows      [][]string `json:"rows"`
	Truncated bool       `json:"truncated"`
}

func ParsePreview(a mlmd.Artifact, obj *artifactstore.Object) (*Preview, error) {
	p := &Preview{Kind: FileKind(a), Name: obj.Name, ETag: obj.ETag}
	if p.Kind == "IMAGE" {
		p.MediaType = http.DetectContentType(obj.Data)
		switch p.MediaType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			p.Encoding = "base64"
			p.Text = base64.StdEncoding.EncodeToString(obj.Data)
			return p, nil
		default:
			return nil, invalidPreview("图片格式与文件内容不符，仅支持 PNG/JPEG/GIF/WebP")
		}
	}
	if !utf8.Valid(obj.Data) {
		return nil, invalidPreview("预览文件必须是 UTF-8 编码")
	}
	switch p.Kind {
	case "HTML":
		p.MediaType = "text/html"
		p.Text = string(obj.Data)
		p.Sandbox = "allow-scripts"
	case "MARKDOWN":
		p.MediaType = "text/markdown"
		p.Text = string(obj.Data)
	case "TEXT":
		p.MediaType = "text/plain"
		p.Text = string(obj.Data)
	case "JSON", "KFP_PREVIEW":
		p.MediaType = "application/json"
		if json.Unmarshal(obj.Data, &p.Data) != nil {
			return nil, invalidPreview("产物不是有效 JSON")
		}
		if p.Kind == "KFP_PREVIEW" {
			if !validKFPPreview(a.Type, p.Data) {
				return nil, invalidPreview("KFP 数据集/模型预览不符合 v1 格式或超过展示上限")
			}
		}
	case "TABLE":
		p.MediaType = "text/csv"
		t, err := parseTable(obj.Data, strings.ToLower(path.Ext(a.URI)) == ".tsv")
		if err != nil {
			return nil, err
		}
		p.Data = t
	default:
		return nil, invalidPreview("该类型暂不支持内容预览，可下载原文件")
	}
	return p, nil
}

func parseTable(data []byte, tsv bool) (*Table, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	if tsv {
		r.Comma = '\t'
	}
	head, err := r.Read()
	if err == io.EOF {
		return &Table{Columns: []string{}, Rows: [][]string{}}, nil
	}
	if err != nil || !validCells(head) {
		return nil, invalidPreview("CSV/TSV 表头无效；最多 50 列、每格 4096 字符")
	}
	out := &Table{Columns: head, Rows: [][]string{}}
	for {
		row, err := r.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil || !validCells(row) {
			return nil, invalidPreview("CSV/TSV 行格式无效或超过展示上限")
		}
		if len(out.Rows) == 200 {
			out.Truncated = true
			return out, nil
		}
		out.Rows = append(out.Rows, row)
	}
}

func validCells(cells []string) bool {
	if len(cells) > 50 {
		return false
	}
	for _, c := range cells {
		if utf8.RuneCountInString(c) > 4096 {
			return false
		}
	}
	return true
}

func invalidPreview(message string) error { return apperr.New(apperr.CodeInvalid, message) }

// Mirrors the bounded KFP v1 producer preview schema. This does not deserialize
// pickle, load a model, unzip datasets or execute producer code.
func validKFPPreview(artifactType string, raw any) bool {
	m, ok := raw.(map[string]any)
	if !ok || m["version"] != float64(1) {
		return false
	}
	if artifactType == "system.Dataset" {
		if m["kind"] != "dataset" {
			return false
		}
		columns, cOK := m["columns"].([]any)
		rows, rOK := m["rows"].([]any)
		sample, sOK := m["sample"].(map[string]any)
		if !cOK || !rOK || !sOK || len(columns) > 20 || len(rows) > 10 {
			return false
		}
		for _, raw := range columns {
			c, ok := raw.(map[string]any)
			if !ok || !shortText(c["name"]) || !shortText(c["dtype"]) {
				return false
			}
		}
		for _, raw := range rows {
			cells, ok := raw.([]any)
			if !ok || len(cells) != len(columns) || len(columns) == 0 {
				return false
			}
			for _, v := range cells {
				if !scalar(v) {
					return false
				}
			}
		}
		if !shortText(sample["method"]) || !optionalText(m, "format") {
			return false
		}
		for _, k := range []string{"rows_truncated", "columns_truncated", "values_truncated"} {
			if _, ok := sample[k].(bool); !ok {
				return false
			}
		}
		for _, k := range []string{"redacted", "rows_omitted"} {
			if v, exists := sample[k]; exists {
				if _, ok := v.(bool); !ok {
					return false
				}
			}
		}
		if sample["rows_omitted"] == true && len(rows) > 0 {
			return false
		}
		for k, count := range map[string]int{"total_rows": len(rows), "total_columns": len(columns)} {
			if v, exists := m[k]; exists && v != nil {
				n, ok := number(v)
				if !ok || n < float64(count) || n > 9007199254740991 || n != float64(int64(n)) {
					return false
				}
				if n > float64(count) {
					if k == "total_columns" && sample["columns_truncated"] != true {
						return false
					}
					if k == "total_rows" && sample["rows_omitted"] != true && sample["rows_truncated"] != true {
						return false
					}
				}
			}
		}
		return true
	}
	if artifactType != "system.Model" || m["kind"] != "model" {
		return false
	}
	if !optionalText(m, "estimator_class") {
		return false
	}
	if raw, exists := m["framework"]; exists {
		f, ok := raw.(map[string]any)
		if !ok || !shortText(f["name"]) || !optionalText(f, "version") {
			return false
		}
	}
	entries := 0
	for _, k := range []string{"hyperparameters", "structure"} {
		if raw, exists := m[k]; exists {
			section, ok := raw.(map[string]any)
			if !ok || !modelValue(section, 0) {
				return false
			}
			entries += len(section)
		}
	}
	if entries > 100 {
		return false
	}
	if raw, exists := m["notes"]; exists {
		notes, ok := raw.([]any)
		if !ok || len(notes) > 100 {
			return false
		}
		for _, n := range notes {
			if !shortText(n) {
				return false
			}
		}
	}
	return true
}

func shortText(v any) bool                         { s, ok := v.(string); return ok && utf8.RuneCountInString(s) <= 256 }
func optionalText(m map[string]any, k string) bool { v, exists := m[k]; return !exists || shortText(v) }
func scalar(v any) bool {
	switch x := v.(type) {
	case nil, bool:
		return true
	case string:
		return shortText(x)
	case float64:
		_, ok := number(x)
		return ok && (x != math.Trunc(x) || (x >= -9007199254740991 && x <= 9007199254740991))
	default:
		return false
	}
}
func modelValue(v any, depth int) bool {
	switch x := v.(type) {
	case map[string]any:
		if depth >= 4 || len(x) > 100 {
			return false
		}
		for k, v := range x {
			if !shortText(k) || !modelValue(v, depth+1) {
				return false
			}
		}
	case []any:
		if depth >= 4 || len(x) > 100 {
			return false
		}
		for _, v := range x {
			if !modelValue(v, depth+1) {
				return false
			}
		}
	default:
		return scalar(v)
	}
	return true
}
