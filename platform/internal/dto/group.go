// Package dto 定义平台对外接口的请求/响应结构。
//
// 约定：
//   - 与 model 分离，不直接暴露表结构
//   - Validate 只做"不需要 IO 就能判断"的校验，重名、额度等交给 service
//   - 所有错误都返回 *apperr.Error，消息是给用户看的中文
package dto

import (
	"strings"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"

	"github.com/githubtiger123/loom/platform/internal/apperr"
)

// Quota 是组额度，直接映射到 Profile.spec.resourceQuotaSpec.hard。
type Quota struct {
	CPU    string `json:"cpu"`    // "32"
	Memory string `json:"memory"` // "128Gi"
	GPU    int    `json:"gpu"`    // 8
}

// CreateGroupReq 建组 = 建一个 Profile。
type CreateGroupReq struct {
	Name        string `json:"name"`                  // 同时是 Profile 名和 namespace 名
	DisplayName string `json:"displayName,omitempty"` // 仅展示，存 Profile annotation
	Quota       Quota  `json:"quota"`
}

// Validate 校验建组请求。
func (r *CreateGroupReq) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return apperr.New(apperr.CodeInvalid, "空间名不能为空")
	}
	if !validator.IsDNS1123Label(r.Name) {
		return apperr.New(apperr.CodeInvalid,
			"空间名只能包含小写字母、数字和 -，且必须以字母或数字开头结尾，最长 63 个字符")
	}
	if !validator.IsNonNegativeQuantity(r.Quota.CPU) {
		return apperr.New(apperr.CodeInvalid, "CPU 额度格式不正确，示例：8 或 500m")
	}
	if !validator.IsNonNegativeQuantity(r.Quota.Memory) {
		return apperr.New(apperr.CodeInvalid, "内存额度格式不正确，示例：16Gi")
	}
	if r.Quota.GPU < 0 {
		return apperr.New(apperr.CodeInvalid, "GPU 额度不能为负数")
	}
	return nil
}

// CreateGroupResp 返回"已受理"的结果，不代表空间已可用。
type CreateGroupResp struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"` // 固定 Creating，Active 由同步器回填
}

// 组内角色码。
const (
	RoleGroupOwner  = "group_owner"
	RoleGroupEditor = "group_editor"
	RoleGroupViewer = "group_viewer"
)

// AddMemberReq 给组添加成员（一期只支持直接授权给用户）。
type AddMemberReq struct {
	Username string `json:"username"` // 必须与 kubeflow-userid 完全一致
	Role     string `json:"role"`     // group_owner / group_editor / group_viewer
}

// Validate 校验加成员请求。
func (r *AddMemberReq) Validate() error {
	r.Username = strings.TrimSpace(r.Username)
	if r.Username == "" {
		return apperr.New(apperr.CodeInvalid, "成员账号不能为空")
	}
	if !validator.IsLoginEmail(r.Username) {
		return apperr.New(apperr.CodeInvalid, "成员账号必须是登录邮箱，例如 alice@example.com")
	}
	switch r.Role {
	case RoleGroupOwner, RoleGroupEditor, RoleGroupViewer:
	default:
		return apperr.New(apperr.CodeInvalid, "角色只能是 group_owner / group_editor / group_viewer")
	}
	return nil
}
