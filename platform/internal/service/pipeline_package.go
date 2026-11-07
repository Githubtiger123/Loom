package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gopkg.in/yaml.v3"
)

// normalizePipelinePackage 不执行用户 Python，只读取 SDK 已编译的 KFP v2 IR。
// 单文档/参数入口是当前受支持范围；平台扩展、多文档和根 artifact 输入明确拒绝。
func normalizePipelinePackage(in *dto.CreatePipelineVersionReq) error {
	if in.PackageYAML == "" && len(in.PipelineSpec) == 0 {
		return nil
	} // 保留旧受管引用契约给外部适配器
	invalid := func() error {
		return apperr.New(apperr.CodeInvalid, "需要合法的单文档 KFP v2 IR，当前不支持根 artifact 输入或多文档平台扩展")
	}
	if in.PackageYAML != "" && len(in.PipelineSpec) != 0 {
		return invalid()
	}
	var spec map[string]any
	if in.PackageYAML != "" {
		if len(in.PackageYAML) > 512<<10 {
			return invalid()
		}
		decoder := yaml.NewDecoder(strings.NewReader(in.PackageYAML))
		if decoder.Decode(&spec) != nil {
			return invalid()
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return invalid()
		}
	} else {
		if len(in.PipelineSpec) > 512<<10 || json.Unmarshal(in.PipelineSpec, &spec) != nil {
			return invalid()
		}
	}
	canonical, err := json.Marshal(spec)
	if err != nil || len(canonical) > 512<<10 {
		return invalid()
	}
	canonical, err = canonicalPipelineIR(canonical)
	if err != nil {
		return invalid()
	}
	// 再按 JSON 读取，统一 YAML 整数/浮点数等表示，保证摘要和默认值一致。
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if decoder.Decode(&spec) != nil {
		return invalid()
	}
	info, _ := spec["pipelineInfo"].(map[string]any)
	root, _ := spec["root"].(map[string]any)
	dag, _ := root["dag"].(map[string]any)
	tasks, _ := dag["tasks"].(map[string]any)
	components, _ := spec["components"].(map[string]any)
	if info["name"] == nil || spec["schemaVersion"] == nil || len(tasks) == 0 || len(components) == 0 || spec["apiVersion"] != nil || spec["kind"] != nil || spec["platformSpec"] != nil {
		return invalid()
	}
	inputs, _ := root["inputDefinitions"].(map[string]any)
	if artifacts, ok := inputs["artifacts"].(map[string]any); ok && len(artifacts) != 0 {
		return invalid()
	}
	parameters, _ := inputs["parameters"].(map[string]any)
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	schema := []dto.MLParameterSchema{}
	types := map[string]string{"STRING": "STRING", "NUMBER_INTEGER": "INTEGER", "NUMBER_DOUBLE": "DOUBLE", "BOOLEAN": "BOOLEAN", "LIST": "LIST", "STRUCT": "STRUCT"}
	for _, name := range names {
		parameter, ok := parameters[name].(map[string]any)
		if !ok {
			return invalid()
		}
		typeName, _ := parameter["parameterType"].(string)
		kind := types[typeName]
		if kind == "" {
			return invalid()
		}
		optional, _ := parameter["isOptional"].(bool)
		entry := dto.MLParameterSchema{Name: name, Type: kind, Required: !optional}
		if value, exists := parameter["defaultValue"]; exists {
			var text string
			if kind == "STRING" {
				var ok bool
				text, ok = value.(string)
				if !ok {
					return invalid()
				}
			} else {
				encoded, err := json.Marshal(value)
				if err != nil {
					return invalid()
				}
				text = string(encoded)
			}
			entry.Default, entry.Required = &text, false
		}
		schema = append(schema, entry)
	}
	digest := mlHash(canonical)
	if in.PackageDigest != "" && in.PackageDigest != digest {
		return apperr.New(apperr.CodeInvalid, "packageDigest 与规范化 IR 的 SHA256 不一致")
	}
	if in.PackageRef != "" && in.PackageRef != "inline-"+digest[:24] {
		return apperr.New(apperr.CodeInvalid, "内嵌 IR 的 packageRef 由平台生成，请勿混用外部引用")
	}
	in.PipelineVersionSpec = dto.PipelineVersionSpec{PackageRef: "inline-" + digest[:24], PackageDigest: digest, Parameters: schema, PipelineSpec: canonical}
	in.PackageYAML = "" // 不将两份重复包写入幂等 payload
	return in.PipelineVersionSpec.Validate()
}

// MySQL JSON 会规范化对象空白、键顺序和数字表示，不能直接 hash RawMessage 字节。
// 与 KFP google.protobuf.Struct 一致按 JSON 数值语义规范化后再计算摘要。
func canonicalPipelineIR(raw json.RawMessage) (json.RawMessage, error) {
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil || spec == nil {
		return nil, fmt.Errorf("IR JSON 无效")
	}
	return json.Marshal(spec)
}

func pipelineRuntimeParameters(spec dto.PipelineRunSpec) (map[string]any, error) {
	schema := map[string]string{}
	for _, p := range spec.Parameters {
		schema[p.Name] = p.Type
	}
	values := map[string]any{}
	for _, p := range spec.Values {
		if p.Value == nil || p.CredentialRef != nil {
			return nil, fmt.Errorf("受管凭据解析尚未接入")
		}
		kind := schema[p.Name]
		if !validator.IsMLPublicValue(kind, *p.Value) {
			return nil, fmt.Errorf("运行参数与版本类型不匹配")
		}
		switch kind {
		case "STRING":
			values[p.Name] = *p.Value
		case "INTEGER":
			value, err := strconv.ParseInt(*p.Value, 10, 64)
			if err != nil {
				return nil, err
			}
			values[p.Name] = value
		case "NUMBER", "DOUBLE":
			value, err := strconv.ParseFloat(*p.Value, 64)
			if err != nil {
				return nil, err
			}
			values[p.Name] = value
		default:
			var value any
			if err := json.Unmarshal([]byte(*p.Value), &value); err != nil {
				return nil, err
			}
			values[p.Name] = value
		}
	}
	return values, nil
}
