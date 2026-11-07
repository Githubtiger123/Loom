package dto

import (
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// References are opaque IDs resolved by a trusted adapter, not names of arbitrary
// Kubernetes Secrets. References never carry plaintext credentials or endpoint
// URLs. Public parameter strings are data and must never be proxied by this API.
type MLCredentialRef struct {
	Ref string `json:"ref"`
}

type MLParameterSchema struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"` // STRING / NUMBER / INTEGER / DOUBLE / BOOLEAN / LIST / STRUCT / CREDENTIAL
	Required bool    `json:"required"`
	Default  *string `json:"default,omitempty"` // public, non-credential values only
}

type MLParameterValue struct {
	Name          string           `json:"name"`
	Value         *string          `json:"value,omitempty"`
	CredentialRef *MLCredentialRef `json:"credentialRef,omitempty"`
}

type CreatePipelineReq struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

type PipelineVersionSpec struct {
	PackageRef    string              `json:"packageRef"`
	PackageDigest string              `json:"packageDigest"` // immutable SHA-256
	Parameters    []MLParameterSchema `json:"parameters"`
	PipelineSpec  json.RawMessage     `json:"pipelineSpec,omitempty"` // 官方 KFP v2 IR，不是 Argo CR
}

type CreatePipelineVersionReq struct {
	Revision    uint64 `json:"revision"` // parent definition revision
	Name        string `json:"name"`
	PackageYAML string `json:"packageYaml,omitempty"` // 导入 SDK 编译的单文档 IR YAML
	PipelineVersionSpec
}

type SubmitPipelineRunReq struct {
	Name       string             `json:"name"`
	VersionID  uint64             `json:"versionId"`
	Parameters []MLParameterValue `json:"parameters"`
}

type PipelineRunSpec struct {
	PipelineID uint64 `json:"pipelineId"`
	VersionID  uint64 `json:"versionId"`
	PipelineVersionSpec
	Values []MLParameterValue `json:"values"`
}

type MLResources struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	GPU    int    `json:"gpu"`
}

type TrainerJobSpec struct {
	RuntimeRef  string             `json:"runtimeRef"`
	CodeRef     string             `json:"codeRef"`
	DatasetRefs []string           `json:"datasetRefs"`
	OutputRef   string             `json:"outputRef"`
	Workers     int                `json:"workers"`
	Resources   MLResources        `json:"resources"` // per worker
	Parameters  []MLParameterValue `json:"parameters"`
}

type CreateTrainerJobReq struct {
	Name string `json:"name"`
	TrainerJobSpec
}

type KServeSpec struct {
	RuntimeRef  string            `json:"runtimeRef"`
	ModelRef    string            `json:"modelRef"`
	ModelDigest string            `json:"modelDigest"`
	Protocol    string            `json:"protocol"` // v1 / v2
	MinReplicas int               `json:"minReplicas"`
	MaxReplicas int               `json:"maxReplicas"`
	Resources   MLResources       `json:"resources"`
	Credentials []MLCredentialRef `json:"credentials"`
}

type CreateKServeReq struct {
	Name string `json:"name"`
	KServeSpec
}

// Update creates an immutable config revision and reconciles the current desired
// state. A stopped service stays stopped. Deploy can select any retained revision.
type UpdateKServeReq struct {
	Revision uint64 `json:"revision"`
	KServeSpec
}

type DeployKServeReq struct {
	Revision   uint64 `json:"revision"`
	RevisionID uint64 `json:"revisionId"`
}

type MLActionReq struct {
	Revision uint64 `json:"revision"`
}

type MLListQuery struct {
	After          uint64
	Limit          int
	Scope          string // mine / all / unowned
	IncludeDeleted bool
}

type MLPage[T any] struct {
	Items     []T     `json:"items"`
	NextAfter *uint64 `json:"nextAfter"`
}

func mlInvalid(message string) error { return apperr.New(apperr.CodeInvalid, message) }

func (q *MLListQuery) Validate() error {
	if q.Scope == "" {
		q.Scope = "mine"
	}
	if q.Limit < 1 || q.Limit > 200 || (q.Scope != "mine" && q.Scope != "all" && q.Scope != "unowned") {
		return mlInvalid("limit 必须为 1–200，scope 必须为 mine/all/unowned")
	}
	return nil
}

func (r CreatePipelineReq) Validate() error {
	if !validator.IsDNS1123Label(r.Name) || !validator.MaxTextLength(r.DisplayName, 128) || !validator.MaxTextLength(r.Description, 1000) {
		return mlInvalid("名称须为最长 63 字符的 DNS label，显示名/描述最长 128/1000 字")
	}
	return nil
}

func (r PipelineVersionSpec) Validate() error {
	if !validator.IsMLReference(r.PackageRef) || !validator.IsSHA256(r.PackageDigest) || len(r.Parameters) > 64 {
		return mlInvalid("必须提供受管 Pipeline 包引用、SHA256 和最多 64 个参数定义")
	}
	seen := map[string]bool{}
	for _, p := range r.Parameters {
		if !validator.IsCatalogID(p.Name) || seen[p.Name] {
			return mlInvalid("参数名无效或重复")
		}
		seen[p.Name] = true
		if p.Type != "STRING" && p.Type != "NUMBER" && p.Type != "INTEGER" && p.Type != "DOUBLE" && p.Type != "BOOLEAN" && p.Type != "LIST" && p.Type != "STRUCT" && p.Type != "CREDENTIAL" {
			return mlInvalid("参数类型须为 STRING/NUMBER/INTEGER/DOUBLE/BOOLEAN/LIST/STRUCT/CREDENTIAL")
		}
		if validator.IsMLCredentialName(p.Name) && p.Type != "CREDENTIAL" {
			return mlInvalid("凭据参数必须声明为 CREDENTIAL，仅允许受管引用")
		}
		if p.Default != nil && !validator.IsMLPublicValue(p.Type, *p.Default) {
			return mlInvalid("参数默认值类型或长度无效，凭据禁止默认明文")
		}
	}
	return nil
}

func (r CreatePipelineVersionReq) Validate() error {
	if r.Revision == 0 || !validator.IsDNS1123Label(r.Name) {
		return mlInvalid("必须提供父定义 revision 和有效版本名称")
	}
	return r.PipelineVersionSpec.Validate()
}

func validateMLValues(values []MLParameterValue) error {
	if len(values) > 64 {
		return mlInvalid("最多 64 个参数")
	}
	seen := map[string]bool{}
	for _, p := range values {
		if !validator.IsCatalogID(p.Name) || seen[p.Name] || (p.Value == nil) == (p.CredentialRef == nil) {
			return mlInvalid("参数名必须唯一，每个参数仅允许公开值或凭据引用之一")
		}
		seen[p.Name] = true
		if p.Value != nil && (!validator.IsMLPublicValue("STRING", *p.Value) || validator.IsMLCredentialName(p.Name)) {
			return mlInvalid("公开值超过 4KiB 或凭据参数使用了明文")
		}
		if p.CredentialRef != nil && !validator.IsMLReference(p.CredentialRef.Ref) {
			return mlInvalid("凭据引用必须是受管 ID")
		}
	}
	return nil
}

func (r SubmitPipelineRunReq) Validate() error {
	if r.VersionID == 0 || !validator.IsDNS1123Label(r.Name) {
		return mlInvalid("必须提供不可变版本 ID 和有效运行名称")
	}
	return validateMLValues(r.Parameters)
}

// BindPipelineParameters freezes defaults and rejects unknown/missing inputs.
func BindPipelineParameters(schema []MLParameterSchema, values []MLParameterValue) ([]MLParameterValue, error) {
	if err := validateMLValues(values); err != nil {
		return nil, err
	}
	supplied := map[string]MLParameterValue{}
	for _, v := range values {
		supplied[v.Name] = v
	}
	bound := []MLParameterValue{}
	for _, p := range schema {
		v, ok := supplied[p.Name]
		if !ok {
			if p.Default != nil {
				v, ok = MLParameterValue{Name: p.Name, Value: p.Default}, true
			} else if p.Required {
				return nil, mlInvalid("缺少必填 Pipeline 参数")
			} else {
				continue
			}
		}
		if (p.Type == "CREDENTIAL" && v.CredentialRef == nil) ||
			(p.Type != "CREDENTIAL" && (v.Value == nil || !validator.IsMLPublicValue(p.Type, *v.Value))) {
			return nil, mlInvalid("Pipeline 参数与不可变版本 schema 不匹配")
		}
		bound = append(bound, v)
		delete(supplied, p.Name)
	}
	if len(supplied) != 0 {
		return nil, mlInvalid("包含版本 schema 未声明的 Pipeline 参数")
	}
	return bound, nil
}

func (r MLResources) Validate() error {
	cpu, okCPU := validator.QuantityUnits(r.CPU, 1000)
	memory, okMemory := validator.QuantityUnits(r.Memory, 1)
	if len(r.CPU) > 32 || len(r.Memory) > 32 || !okCPU || !okMemory || cpu > 1024000 || memory > 4*1024*1024*1024*1024 || r.GPU < 0 || r.GPU > 64 {
		return mlInvalid("每副本资源须为正数，最多 1024 CPU、4Ti 内存、64 GPU")
	}
	return nil
}

func (r TrainerJobSpec) Validate() error {
	if !validator.IsMLReference(r.RuntimeRef) || !validator.IsMLReference(r.CodeRef) || !validator.IsMLReference(r.OutputRef) || r.Workers < 1 || r.Workers > 128 || len(r.DatasetRefs) > 16 {
		return mlInvalid("须提供 Runtime/代码/产物受管引用，worker 为 1–128，数据集最多 16 项")
	}
	seen := map[string]bool{}
	for _, ref := range r.DatasetRefs {
		if !validator.IsMLReference(ref) || seen[ref] {
			return mlInvalid("数据集引用无效或重复")
		}
		seen[ref] = true
	}
	if err := r.Resources.Validate(); err != nil {
		return err
	}
	return validateMLValues(r.Parameters)
}

func (r CreateTrainerJobReq) Validate() error {
	if !validator.IsDNS1123Label(r.Name) {
		return mlInvalid("训练名称须为最长 63 字符的 DNS label")
	}
	return r.TrainerJobSpec.Validate()
}

func (r KServeSpec) Validate() error {
	if !validator.IsMLReference(r.RuntimeRef) || !validator.IsMLReference(r.ModelRef) || !validator.IsSHA256(r.ModelDigest) || (r.Protocol != "v1" && r.Protocol != "v2") || r.MinReplicas < 0 || r.MaxReplicas < 1 || r.MinReplicas > r.MaxReplicas || r.MaxReplicas > 128 || len(r.Credentials) > 16 {
		return mlInvalid("模型/Runtime/摘要/协议无效，副本须为 0–128 且 max≥1，凭据引用最多 16 项")
	}
	seen := map[string]bool{}
	for _, c := range r.Credentials {
		if !validator.IsMLReference(c.Ref) || seen[c.Ref] {
			return mlInvalid("凭据引用无效或重复")
		}
		seen[c.Ref] = true
	}
	return r.Resources.Validate()
}

func (r CreateKServeReq) Validate() error {
	if !validator.IsDNS1123Label(r.Name) {
		return mlInvalid("服务名称须为最长 63 字符的 DNS label")
	}
	return r.KServeSpec.Validate()
}

func (r UpdateKServeReq) Validate() error {
	if r.Revision == 0 {
		return mlInvalid("必须提供 revision")
	}
	return r.KServeSpec.Validate()
}
