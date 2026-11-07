package k8s

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/githubtiger123/loom/platform/internal/apperr"
)

const gpuQuotaResource = "requests.nvidia.com/gpu"

// QuotaUsage 是 ResourceQuota 的只读快照，用于列表页展示"本组还剩多少"。
// 数值统一转成字符串，避免前端再解析 Quantity。
type QuotaUsage struct {
	CPUUsed    string `json:"cpuUsed"`
	CPUHard    string `json:"cpuHard"`
	MemoryUsed string `json:"memoryUsed"`
	MemoryHard string `json:"memoryHard"`
	GPUUsed    string `json:"gpuUsed"`
	GPUHard    string `json:"gpuHard"`
}

// ReadQuota 读当前 namespace 的 ResourceQuota。
// 额度真相在 K8s，不在数据库：数据库里只有历史快照。
func (c *Client) ReadQuota(ctx context.Context, ns string) (*QuotaUsage, error) {
	rq, err := c.kube.CoreV1().ResourceQuotas(ns).Get(ctx, c.cfg.QuotaName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil // 未配置额度
	}
	if err != nil {
		return nil, fmt.Errorf("get resourcequota %s/%s: %w", ns, c.cfg.QuotaName, err)
	}

	pick := func(name corev1.ResourceName) (used, hard string) {
		h, ok := rq.Spec.Hard[name]
		if !ok {
			return "", ""
		}
		u := rq.Status.Used[name]
		return u.String(), h.String()
	}

	cpuU, cpuH := pick(corev1.ResourceRequestsCPU)
	memU, memH := pick(corev1.ResourceRequestsMemory)
	gpuU, gpuH := pick(gpuQuotaResource)

	return &QuotaUsage{
		CPUUsed: cpuU, CPUHard: cpuH,
		MemoryUsed: memU, MemoryHard: memH,
		GPUUsed: gpuU, GPUHard: gpuH,
	}, nil
}

// CheckQuota 预检"本次申请是否放得下"。
//
// 必须读 K8s 的 status.used 而不是数据库：库里的投影总有延迟，
// 用延迟数据预检会让用户"被拒一次、重试又通过"。
func (c *Client) CheckQuota(ctx context.Context, ns, cpu, memory string, gpu int) error {
	rq, err := c.kube.CoreV1().ResourceQuotas(ns).Get(ctx, c.cfg.QuotaName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeK8sRejected, "读取空间额度失败，请稍后重试", err)
	}

	type check struct {
		name  corev1.ResourceName
		label string
		need  string
	}
	checks := []check{
		{corev1.ResourceRequestsCPU, "CPU", cpu},
		{corev1.ResourceRequestsMemory, "内存", memory},
	}
	if gpu > 0 {
		checks = append(checks, check{gpuQuotaResource, "GPU", strconv.Itoa(gpu)})
	}

	for _, ck := range checks {
		hard, limited := rq.Spec.Hard[ck.name]
		if !limited {
			continue // 该维度没设上限
		}
		need, err := resource.ParseQuantity(ck.need)
		if err != nil {
			return apperr.Newf(apperr.CodeInvalid, "%s 规格格式不正确", ck.label)
		}

		used := rq.Status.Used[ck.name]
		sum := used.DeepCopy()
		sum.Add(need)
		if sum.Cmp(hard) <= 0 {
			continue
		}

		remaining := hard.DeepCopy()
		remaining.Sub(used)
		if remaining.Sign() < 0 {
			remaining = *resource.NewQuantity(0, resource.DecimalSI)
		}
		return apperr.Newf(apperr.CodeQuotaExceeded,
			"%s 超出本空间剩余额度（剩余 %s，本次需要 %s），请降低规格或联系空间管理员",
			ck.label, remaining.String(), need.String())
	}
	return nil
}
