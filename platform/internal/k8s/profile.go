package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ProfileInput 是建组的全部输入。
//
// Owner 是 Profile 的必填字段，但**不代表业务上的组负责人**：
// 业务归属在 tb_role_binding / tb_group 里，K8s 侧只是为了让 controller 有主体可绑。
type ProfileInput struct {
	GroupID     uint64
	InstanceID  string
	Name        string
	Owner       string // 登录邮箱，必须与 kubeflow-userid 完全一致
	DisplayName string
	CPU         string // "32"
	Memory      string // "128Gi"
	GPU         int    // 0 表示禁止申请 GPU，而不是不限额
	Storage     string // 可选，组共享存储上限，如 "2Ti"
}

// CreateProfile 提交 Profile CR。
//
// 返回 AlreadyExists 时由 service 决定是"重试"还是"提示名称占用"——
// 这里不做猜测，保持基础设施层无业务判断。
func (c *Client) CreateProfile(ctx context.Context, in ProfileInput) error {
	hard := map[string]any{
		"requests.cpu":    in.CPU,
		"requests.memory": in.Memory,
		gpuQuotaResource:  int64(in.GPU),
	}
	if in.Storage != "" {
		hard["requests.storage"] = in.Storage
	}

	annotations := map[string]any{}
	labels := map[string]any{}
	if in.GroupID > 0 && in.InstanceID != "" {
		labels[labelManagedBy] = managedByLoom
		labels[labelGroupID] = fmt.Sprint(in.GroupID)
		annotations[annotationInstance] = in.InstanceID
	}
	if in.DisplayName != "" {
		annotations[annotationDisplayName] = in.DisplayName
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubeflow.org/v1",
		"kind":       "Profile",
		"metadata": map[string]any{
			"name":        in.Name,
			"annotations": annotations,
			"labels":      labels,
		},
		"spec": map[string]any{
			"owner": map[string]any{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "User",
				"name":     in.Owner,
			},
			"resourceQuotaSpec": map[string]any{
				"hard": hard,
			},
		},
	}}

	if _, err := c.dyn.Resource(ProfileGVR).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create profile %s: %w", in.Name, err)
	}
	return nil
}

// GetProfile 读取 Profile，用于同步器回填 tb_group。
func (c *Client) GetProfile(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	obj, err := c.dyn.Resource(ProfileGVR).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get profile %s: %w", name, err)
	}
	return obj, nil
}
