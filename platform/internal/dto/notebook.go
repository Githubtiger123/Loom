package dto

import (
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// EnvVar 支持明文和 Secret 引用两种形态，Secret 只存引用不存值。
type EnvVar struct {
	Name       string `json:"name"`
	Value      string `json:"value,omitempty"`
	SecretName string `json:"secretName,omitempty"`
	SecretKey  string `json:"secretKey,omitempty"`
}

// CreateNotebookReq 创建 Notebook 实例。
//
// 刻意不包含：镜像完整地址、serviceAccountName、nodeSelector、tolerations、
// command/args、hostPath、privileged —— 这些由平台注入或永久禁止。
type CreateNotebookReq struct {
	Name              string            `json:"name"`
	DisplayName       string            `json:"displayName,omitempty"`
	Description       string            `json:"description,omitempty"`
	ImageID           string            `json:"imageId"`       // 镜像白名单 id
	ServerType        string            `json:"serverType"`    // jupyter / group-one / group-two，创建后固定
	CPU               string            `json:"cpu"`           // "4"
	Memory            string            `json:"memory"`        // "16Gi"
	GPU               int               `json:"gpu"`           // 0/1/2/4
	WorkspaceSize     string            `json:"workspaceSize"` // "50Gi"
	WorkspaceID       uint64            `json:"workspaceId,omitempty"`
	WorkspaceRevision uint64            `json:"workspaceRevision,omitempty"`
	Env               []EnvVar          `json:"env,omitempty"`
	IdleStopMinutes   int               `json:"idleStopMinutes"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// ValidGPUs 一期允许的 GPU 卡数。
var ValidGPUs = map[int]bool{0: true, 1: true, 2: true, 4: true}

// ValidIdleMinutes 一期允许的空闲停止时长，0 表示不自动停止。
var ValidIdleMinutes = map[int]bool{0: true, 60: true, 240: true, 480: true}

// Validate 校验创建实例请求。
func (r *CreateNotebookReq) Validate() error {
	r.ServerType = validator.NotebookServerTypeOrDefault(strings.TrimSpace(r.ServerType))
	if !validator.IsNotebookServerType(r.ServerType) {
		return apperr.New(apperr.CodeInvalid, "serverType 只支持 jupyter、group-one、group-two")
	}
	if (r.WorkspaceID == 0) != (r.WorkspaceRevision == 0) {
		return apperr.New(apperr.CodeInvalid, "复用目录必须同时提供 workspaceId 和 workspaceRevision")
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return apperr.New(apperr.CodeInvalid, "实例名称不能为空")
	}
	// ≤63 是因为 notebook-controller 会创建同名 Service，Service 名上限 63
	if !validator.IsDNS1123Label(r.Name) {
		return apperr.New(apperr.CodeInvalid,
			"实例名称只能包含小写字母、数字和 -，且必须以字母或数字开头结尾，最长 63 个字符")
	}
	r.ImageID = strings.TrimSpace(r.ImageID)
	if !validator.IsCatalogID(r.ImageID) {
		return apperr.New(apperr.CodeInvalid, "必须选择运行镜像")
	}
	if !validator.MaxTextLength(r.DisplayName, 128) || !validator.MaxTextLength(r.Description, 1000) {
		return apperr.New(apperr.CodeInvalid, "显示名最长 128 字，描述最长 1000 字")
	}
	if _, ok := validator.QuantityUnits(r.CPU, 1000); !ok {
		return apperr.New(apperr.CodeInvalid, "CPU 规格格式不正确，示例：4")
	}
	if _, ok := validator.QuantityUnits(r.Memory, 1); !ok {
		return apperr.New(apperr.CodeInvalid, "内存规格格式不正确，示例：16Gi")
	}
	if !ValidGPUs[r.GPU] {
		return apperr.New(apperr.CodeInvalid, "GPU 卡数只能是 0 / 1 / 2 / 4")
	}
	if _, ok := validator.QuantityUnits(r.WorkspaceSize, 1); !ok {
		return apperr.New(apperr.CodeInvalid, "个人目录容量格式不正确，示例：50Gi")
	}
	if !ValidIdleMinutes[r.IdleStopMinutes] {
		return apperr.New(apperr.CodeInvalid, "空闲停止时长只能是 0 / 60 / 240 / 480 分钟")
	}
	if len(r.Labels) > 32 || len(r.Env) > 64 {
		return apperr.New(apperr.CodeInvalid, "最多 32 个标签和 64 个环境变量")
	}
	for key, value := range r.Labels {
		if !validator.IsUserLabelKey(key) {
			return apperr.Newf(apperr.CodeInvalid, "标签名称 %q 不符合 Kubernetes 规则", key)
		}
		if !validator.IsLabelValue(value) {
			return apperr.Newf(apperr.CodeInvalid, "标签 %q 的值不符合 Kubernetes 规则", key)
		}
	}

	seen := make(map[string]struct{}, len(r.Env))
	for i := range r.Env {
		e := &r.Env[i]
		name := strings.TrimSpace(e.Name)
		e.Name = name
		if !validator.IsEnvVarName(name) || len(name) > 253 || len(e.Value) > 16384 {
			return apperr.New(apperr.CodeInvalid, "环境变量名称无效或值超过 16KiB")
		}
		if _, dup := seen[name]; dup {
			return apperr.Newf(apperr.CodeInvalid, "环境变量 %s 重复", name)
		}
		seen[name] = struct{}{}

		hasRef := e.SecretName != "" || e.SecretKey != ""
		if hasRef && (e.SecretName == "" || e.SecretKey == "") {
			return apperr.Newf(apperr.CodeInvalid,
				"环境变量 %s 引用 Secret 时必须同时填写 Secret 名和 key", name)
		}
		if hasRef && e.Value != "" {
			return apperr.Newf(apperr.CodeInvalid,
				"环境变量 %s 不能同时填写明文值和 Secret 引用", name)
		}
		if hasRef && (!validator.IsSecretName(e.SecretName) || !validator.IsSecretKey(e.SecretKey)) {
			return apperr.New(apperr.CodeInvalid, "Secret 名称或 key 无效")
		}
		// 同一空间的 Secret 并不都属于请求人。未接入 Secret 业务授权目录前拒绝引用。
		if hasRef {
			return apperr.New(apperr.CodeInvalid, "尚未接入 Secret 引用授权，请勿引用空间中的任意 Secret")
		}
	}
	return nil
}

// UpdateNotebookReq 只允许业务字段，不允许修改实例身份、镜像配置或底层 PodSpec。
type UpdateNotebookReq struct {
	Revision        uint64             `json:"revision"`
	DisplayName     *string            `json:"displayName,omitempty"`
	Description     *string            `json:"description,omitempty"`
	IdleStopMinutes *int               `json:"idleStopMinutes,omitempty"`
	CPU             *string            `json:"cpu,omitempty"`
	Memory          *string            `json:"memory,omitempty"`
	GPU             *int               `json:"gpu,omitempty"`
	Env             *[]EnvVar          `json:"env,omitempty"`
	Labels          *map[string]string `json:"labels,omitempty"`
}

func (r UpdateNotebookReq) HasChanges() bool {
	return r.DisplayName != nil || r.Description != nil || r.IdleStopMinutes != nil ||
		r.CPU != nil || r.Memory != nil || r.GPU != nil || r.Env != nil || r.Labels != nil
}

func (r UpdateNotebookReq) ChangesRuntime() bool {
	return r.CPU != nil || r.Memory != nil || r.GPU != nil || r.Env != nil || r.Labels != nil
}

func (r UpdateNotebookReq) Apply(spec *CreateNotebookReq) {
	if r.DisplayName != nil {
		spec.DisplayName = *r.DisplayName
	}
	if r.Description != nil {
		spec.Description = *r.Description
	}
	if r.IdleStopMinutes != nil {
		spec.IdleStopMinutes = *r.IdleStopMinutes
	}
	if r.CPU != nil {
		spec.CPU = *r.CPU
	}
	if r.Memory != nil {
		spec.Memory = *r.Memory
	}
	if r.GPU != nil {
		spec.GPU = *r.GPU
	}
	if r.Env != nil {
		spec.Env = *r.Env
	}
	if r.Labels != nil {
		spec.Labels = *r.Labels
	}
}

type NotebookActionReq struct {
	Revision uint64 `json:"revision"`
}

type DeleteNotebookReq struct {
	Revision        uint64 `json:"revision"`
	DeleteWorkspace bool   `json:"deleteWorkspace"`
	ConfirmName     string `json:"confirmName,omitempty"`
}

type RetryNotebookReq struct {
	Revision    uint64 `json:"revision"`
	OperationID string `json:"operationId"`
}

type NotebookListQuery struct {
	After          uint64
	Limit          int
	Scope          string
	Search         string
	Status         string
	ServerType     string
	IncludeDeleted bool
}

var NotebookStatuses = map[string]bool{
	"CREATING": true, "STARTING": true, "RUNNING": true, "STOPPING": true,
	"STOPPED": true, "UPDATING": true, "DELETING": true, "DELETED": true,
	"PENDING": true, "FAILED": true, "ERROR": true, "UNKNOWN": true,
}

func (q *NotebookListQuery) Validate() error {
	if q.Scope == "" {
		q.Scope = "mine"
	}
	if q.Scope != "mine" && q.Scope != "all" && q.Scope != "unowned" {
		return apperr.New(apperr.CodeInvalid, "scope 只支持 mine、all、unowned")
	}
	q.Search = strings.TrimSpace(q.Search)
	if !validator.MaxTextLength(q.Search, 128) || q.Limit < 1 || q.Limit > 200 {
		return apperr.New(apperr.CodeInvalid, "搜索词过长或分页大小无效")
	}
	if q.Status != "" && !NotebookStatuses[q.Status] {
		return apperr.New(apperr.CodeInvalid, "无效的 Notebook 状态")
	}
	if q.ServerType != "" && !validator.IsNotebookServerType(q.ServerType) {
		return apperr.New(apperr.CodeInvalid, "serverType 只支持 jupyter、group-one、group-two")
	}
	return nil
}

// 不直接序列化数据库模型：requested_spec 可能含明文环境变量。
type NotebookView struct {
	ID                 uint64  `json:"id"`
	InstanceID         string  `json:"instanceId"`
	GroupID            uint64  `json:"groupId"`
	Name               string  `json:"name"`
	DisplayName        string  `json:"displayName"`
	Description        string  `json:"description"`
	CreatedBy          *uint64 `json:"createdBy"`
	OwnerUserID        *uint64 `json:"ownerUserId"`
	OwnerRevision      uint64  `json:"ownerRevision"`
	ImageID            string  `json:"imageId"`
	ServerType         string  `json:"serverType"`
	CPUMilli           int64   `json:"cpuMilli"`
	MemoryBytes        int64   `json:"memoryBytes"`
	GPU                uint    `json:"gpu"`
	WorkspaceSizeBytes int64   `json:"workspaceSizeBytes"`
	WorkspaceID        uint64  `json:"workspaceId"`
	WorkspaceOwnership string  `json:"workspaceOwnership"`
	IdleStopMinutes    uint    `json:"idleStopMinutes"`
	NotebookState
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `json:"deletedAt"`
}

type NotebookState struct {
	Status          string     `json:"status"`
	StatusReason    *string    `json:"statusReason"`
	DesiredState    string     `json:"desiredState"`
	ObservedState   string     `json:"observedState"`
	Revision        uint64     `json:"revision"`
	AppliedRevision uint64     `json:"appliedRevision"`
	OperationID     *string    `json:"operationId"`
	ObservedAt      *time.Time `json:"observedAt"`
	LastActiveAt    *time.Time `json:"lastActiveAt"`
	StartedAt       *time.Time `json:"startedAt"`
	StoppedAt       *time.Time `json:"stoppedAt"`
}

type NotebookEnvView struct {
	Name       string `json:"name"`
	HasValue   bool   `json:"hasValue"`
	SecretName string `json:"secretName,omitempty"`
	SecretKey  string `json:"secretKey,omitempty"`
}

type NotebookDetail struct {
	NotebookView
	Env    []NotebookEnvView `json:"env"`
	Labels map[string]string `json:"labels"`
}

type NotebookPage struct {
	Items     []NotebookView `json:"items"`
	NextAfter *uint64        `json:"nextAfter"`
}

type NotebookAccess struct {
	ServerType string     `json:"serverType"`
	Ready      bool       `json:"ready"`
	Reason     string     `json:"reason"`
	URL        string     `json:"url,omitempty"`
	ObservedAt *time.Time `json:"observedAt"`
}
