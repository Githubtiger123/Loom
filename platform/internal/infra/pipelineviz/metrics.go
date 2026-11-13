// Package pipelineviz adapts KFP's system artifact metadata to frontend-neutral views.
// Formats follow kfp/dsl/types/artifact_types.py and MetricsVisualizations.tsx.
package pipelineviz

import (
	"encoding/json"
	"math"
	"path"
	"sort"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
)

type View struct {
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Slice   string `json:"slice,omitempty"`
	Data    any    `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
}

type Scalar struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}
type ROCPoint struct {
	Threshold float64 `json:"confidenceThreshold"`
	FPR       float64 `json:"falsePositiveRate"`
	Recall    float64 `json:"recall"`
}
type Matrix struct {
	Labels []string    `json:"labels"`
	Rows   [][]float64 `json:"rows"`
	Axes   [2]string   `json:"axes"`
}

// Unwrap handles the launcher wrapper for list/struct MLMD values. It does not
// recursively reinterpret arbitrary producer dictionaries as protobuf values.
func Unwrap(v any) any {
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		if x, ok := m["struct"]; ok {
			return x
		}
		if x, ok := m["list"]; ok {
			return x
		}
	}
	return v
}

func metadata(a mlmd.Artifact) map[string]any {
	m := make(map[string]any, len(a.Properties)+len(a.Custom))
	for k, v := range a.Properties {
		m[k] = Unwrap(v)
	}
	for k, v := range a.Custom {
		m[k] = Unwrap(v)
	}
	return m
}

func number(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case json.Number:
		var err error
		f, err = n.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

func invalid(kind string) View {
	return View{Kind: kind, Status: "INVALID", Message: "指标格式无效或超过可视化上限"}
}

// Views never fetches storage: metrics are authoritative in MLMD, not stdout.
func Views(a mlmd.Artifact) []View {
	m := metadata(a)
	out := []View{}
	switch a.Type {
	case "system.Metrics":
		metrics := []Scalar{}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v, ok := number(m[k]); ok {
				if len(metrics) >= 1000 || len(k) > 1024 {
					return []View{invalid("SCALAR")}
				}
				metrics = append(metrics, Scalar{k, v})
			}
		}
		out = append(out, View{Kind: "SCALAR", Status: "AVAILABLE", Data: metrics})
	case "system.ClassificationMetrics":
		out = classification(m)
	case "system.SlicedClassificationMetrics":
		slices, ok := m["evaluationSlices"].([]any)
		if !ok || len(slices) > 20 {
			return []View{invalid("CLASSIFICATION")}
		}
		for _, raw := range slices {
			slice, ok := raw.(map[string]any)
			if !ok {
				return []View{invalid("CLASSIFICATION")}
			}
			name, ok := slice["slice"].(string)
			values, valid := Unwrap(slice["sliceClassificationMetrics"]).(map[string]any)
			if !ok || !valid || len(name) > 1024 {
				return []View{invalid("CLASSIFICATION")}
			}
			for _, v := range classification(values) {
				v.Slice = name
				out = append(out, v)
			}
		}
	default:
		kind := FileKind(a)
		if kind != "" {
			out = append(out, View{Kind: kind, Status: "ON_DEMAND"})
		}
	}
	return out
}

func classification(m map[string]any) []View {
	out := []View{}
	if raw, exists := m["confidenceMetrics"]; exists {
		v := invalid("ROC")
		if list, ok := Unwrap(raw).([]any); ok && len(list) > 0 && len(list) <= 5000 {
			points := make([]ROCPoint, 0, len(list))
			for _, raw := range list {
				p, ok := raw.(map[string]any)
				if !ok {
					break
				}
				t, tOK := number(p["confidenceThreshold"])
				f, fOK := number(p["falsePositiveRate"])
				r, rOK := number(p["recall"])
				if !tOK || !fOK || !rOK || f < 0 || f > 1 || r < 0 || r > 1 {
					break
				}
				points = append(points, ROCPoint{t, f, r})
			}
			if len(points) == len(list) {
				v = View{Kind: "ROC", Status: "AVAILABLE", Data: points}
			}
		}
		out = append(out, v)
	}
	if raw, exists := m["confusionMatrix"]; exists {
		v := invalid("CONFUSION_MATRIX")
		if matrix, ok := parseMatrix(Unwrap(raw)); ok {
			v = View{Kind: "CONFUSION_MATRIX", Status: "AVAILABLE", Data: matrix}
		}
		out = append(out, v)
	}
	return out
}

func parseMatrix(raw any) (Matrix, bool) {
	out := Matrix{Labels: []string{}, Rows: [][]float64{}, Axes: [2]string{"True label", "Predicted label"}}
	m, ok := raw.(map[string]any)
	if !ok {
		return out, false
	}
	labels, ok := m["annotationSpecs"].([]any)
	rows, rowsOK := m["rows"].([]any)
	if !ok || !rowsOK || len(labels) == 0 || len(labels) > 100 || len(rows) != len(labels) {
		return out, false
	}
	for _, raw := range labels {
		label, ok := raw.(map[string]any)
		if !ok {
			return out, false
		}
		name, ok := label["displayName"].(string)
		if !ok || len(name) > 1024 {
			return out, false
		}
		out.Labels = append(out.Labels, name)
	}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			return out, false
		}
		cells, ok := row["row"].([]any)
		if !ok || len(cells) != len(labels) {
			return out, false
		}
		values := make([]float64, 0, len(cells))
		for _, cell := range cells {
			n, ok := number(cell)
			if !ok || n < 0 {
				return out, false
			}
			values = append(values, n)
		}
		out.Rows = append(out.Rows, values)
	}
	return out, true
}

func FileKind(a mlmd.Artifact) string {
	switch a.Type {
	case "system.HTML":
		return "HTML"
	case "system.Markdown":
		return "MARKDOWN"
	case "system.Dataset", "system.Model":
		if _, exists := metadata(a)["_kfp_preview"]; exists {
			return "KFP_PREVIEW"
		}
	case "system.Metrics", "system.ClassificationMetrics", "system.SlicedClassificationMetrics":
		return ""
	}
	switch strings.ToLower(path.Ext(a.URI)) {
	case ".html", ".htm":
		return "HTML"
	case ".md", ".markdown":
		return "MARKDOWN"
	case ".csv", ".tsv":
		return "TABLE"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "IMAGE"
	case ".json":
		return "JSON"
	case ".txt", ".log", ".yaml", ".yml", ".py", ".sh", ".r", ".sql", ".toml", ".ini", ".cfg", ".conf", ".xml", ".jsonl", ".ndjson":
		return "TEXT"
	}
	return ""
}

// PreviewSuffix is derived only from producer metadata, never an HTTP query path.
func PreviewSuffix(a mlmd.Artifact) (string, bool) {
	if a.Type != "system.Dataset" && a.Type != "system.Model" {
		return "", false
	}
	d, ok := metadata(a)["_kfp_preview"].(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := number(d["version"])
	if ok && v == 1 && d["path"] == "_kfp_preview.json" {
		return "/_kfp_preview.json", true
	}
	if ok && v == 2 && d["storage"] == "sibling" {
		return ".kfp-preview.json", true
	}
	return "", false
}
