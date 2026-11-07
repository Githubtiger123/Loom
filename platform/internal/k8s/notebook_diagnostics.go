package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func boundedNotebookText(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		r = r[:limit]
	}
	return string(r)
}

func (c *Client) notebookDiagnosticPods(ctx context.Context, ref NotebookRef) ([]corev1.Pod, string, error) {
	if ref.UID == "" {
		return nil, "", conflict("Notebook UID 尚未确认")
	}
	n, err := c.GetNotebook(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	if n == nil {
		return nil, "", conflict("Notebook 对象不存在")
	}
	sts, err := c.notebookStatefulSet(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	uid := ""
	if sts != nil {
		uid = string(sts.UID)
	}
	pods, err := c.NotebookPods(ctx, ref, uid)
	return pods, uid, err
}

// recheckNotebookPod 抵御读取前后重建；不能只凭名字或 label 返回日志/指标。
func (c *Client) recheckNotebookPod(ctx context.Context, ref NotebookRef, pod *corev1.Pod, stsUID string) error {
	pods, currentUID, err := c.notebookDiagnosticPods(ctx, ref)
	if err != nil {
		return err
	}
	if currentUID != stsUID {
		return conflict("查询期间 StatefulSet UID 已变化")
	}
	for _, current := range pods {
		if current.Name == pod.Name && current.UID == pod.UID {
			return nil
		}
	}
	return conflict("查询期间 Pod UID 已变化，请刷新后重试")
}

func (c *Client) NotebookLogs(ctx context.Context, ref NotebookRef, query dto.NotebookLogQuery) (*dto.NotebookLogs, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	pods, stsUID, err := c.notebookDiagnosticPods(ctx, ref)
	if err != nil {
		return nil, err
	}
	var chosen *corev1.Pod
	for i := range pods {
		pod := &pods[i]
		if query.Pod == "" || (pod.Name == query.Pod && string(pod.UID) == query.PodUID) {
			if chosen != nil {
				return nil, conflict("存在多个 Pod，请指定名称与 UID")
			}
			chosen = pod
		}
	}
	if chosen == nil {
		return nil, conflict("目标 Notebook 没有匹配且归属已验证的 Pod")
	}
	mainFound := false
	for _, container := range chosen.Spec.Containers {
		mainFound = mainFound || container.Name == ref.Name
	}
	if !mainFound {
		return nil, conflict("目标 Pod 缺少 Notebook 主容器")
	}
	logCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stream, err := c.logs.Open(logCtx, podlog.Target{Namespace: ref.Namespace, Pod: chosen.Name, PodUID: string(chosen.UID), Container: ref.Name},
		podlog.Options{TailLines: query.TailLines, LimitBytes: query.LimitBytes, SinceSeconds: query.SinceSeconds, Previous: query.Previous, Timestamps: true})
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	data, err := podlog.ReadSnapshot(logCtx, stream)
	if err != nil {
		return nil, err
	}
	if err := c.recheckNotebookPod(ctx, ref, chosen, stsUID); err != nil {
		return nil, err
	}
	return &dto.NotebookLogs{Pod: data.Pod, PodUID: data.PodUID, Container: data.Container, Text: data.Text, Truncated: data.Truncated, ObservedAt: data.ObservedAt}, nil
}

func (c *Client) NotebookEvents(ctx context.Context, ref NotebookRef, limit int) (*dto.NotebookEvents, error) {
	if limit < 1 || limit > 100 {
		return nil, conflict("事件查询上限为 100")
	}
	pods, stsUID, err := c.notebookDiagnosticPods(ctx, ref)
	if err != nil {
		return nil, err
	}
	targets := []ObjectRef{{Kind: "Notebook", Name: ref.Name, UID: ref.UID}}
	if stsUID != "" {
		targets = append(targets, ObjectRef{Kind: "StatefulSet", Name: ref.Name, UID: stsUID})
	}
	for _, pod := range pods {
		targets = append(targets, ObjectRef{Kind: "Pod", Name: pod.Name, UID: string(pod.UID)})
	}
	result := &dto.NotebookEvents{Items: []dto.NotebookEvent{}, ObservedAt: time.Now().UTC()}
	for _, target := range targets {
		list, err := c.kube.CoreV1().Events(ref.Namespace).List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", target.UID).String(), Limit: int64(limit + 1)})
		if err != nil {
			return nil, err
		}
		result.Truncated = result.Truncated || list.Continue != ""
		for _, event := range list.Items {
			if event.InvolvedObject.Namespace != ref.Namespace || event.InvolvedObject.Kind != target.Kind || event.InvolvedObject.Name != target.Name || string(event.InvolvedObject.UID) != target.UID {
				continue
			}
			at := event.LastTimestamp.Time
			if !event.EventTime.IsZero() {
				at = event.EventTime.Time
			}
			if event.Series != nil {
				at = event.Series.LastObservedTime.Time
			}
			if at.IsZero() {
				at = event.CreationTimestamp.Time
			}
			result.Items = append(result.Items, dto.NotebookEvent{Kind: target.Kind, Name: target.Name, UID: target.UID, Type: event.Type,
				Reason: boundedNotebookText(event.Reason, 128), Message: boundedNotebookText(event.Message, 2048), Count: event.Count, At: at})
		}
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].At.After(result.Items[j].At) })
	if len(result.Items) > limit {
		result.Items, result.Truncated = result.Items[:limit], true
	}
	return result, nil
}

func (c *Client) NotebookDiagnostics(ctx context.Context, ref NotebookRef) (*dto.NotebookDiagnostics, error) {
	obs, err := c.ObserveNotebook(ctx, ref)
	if err != nil {
		return nil, err
	}
	result := &dto.NotebookDiagnostics{NotebookID: ref.ID, UID: obs.UID, Revision: ref.Revision, State: obs.State, Reason: obs.Reason,
		Pods: []dto.NotebookPodDiagnostic{}, ObservedAt: time.Now().UTC(), IdleReclamationReason: "KERNEL_ACTIVITY_UNAVAILABLE: 未配置可验证的 kernel 活动源，不依据页面访问或陈旧 annotation 自动回收"}
	if obs.State == "MISSING" {
		return result, nil
	}
	pods, _, err := c.notebookDiagnosticPods(ctx, ref)
	if err != nil {
		return nil, err
	}
	for _, pod := range pods {
		item := dto.NotebookPodDiagnostic{Name: pod.Name, UID: string(pod.UID), Phase: string(pod.Status.Phase), Reason: boundedNotebookText(pod.Status.Reason, 256)}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				item.Ready = condition.Status == corev1.ConditionTrue
			}
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				item.Reason = boundedNotebookText(condition.Reason+": "+condition.Message, 2048)
			}
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != ref.Name {
				continue
			}
			item.Restarts = status.RestartCount
			if status.State.Waiting != nil {
				item.Reason = boundedNotebookText(status.State.Waiting.Reason+": "+status.State.Waiting.Message, 2048)
			}
			if status.State.Terminated != nil {
				item.Reason = boundedNotebookText(fmt.Sprintf("%s (exit %d)", status.State.Terminated.Reason, status.State.Terminated.ExitCode), 256)
			}
		}
		result.Pods = append(result.Pods, item)
	}
	return result, nil
}

// 不引入 metrics/watch 依赖。metrics-server 缺失、拒绝、无 UID 或样本过期均返回 unavailable，绝不返回假零。
func (c *Client) NotebookUsage(ctx context.Context, ref NotebookRef) (*dto.NotebookUsage, error) {
	result := &dto.NotebookUsage{ObservedAt: time.Now().UTC(), GPUReason: "METRICS_UNAVAILABLE: metrics.k8s.io 不提供 GPU 使用量"}
	pods, stsUID, err := c.notebookDiagnosticPods(ctx, ref)
	if err != nil {
		return nil, err
	}
	if len(pods) != 1 {
		result.Reason = "METRICS_UNAVAILABLE: 无唯一的归属已验证 Pod"
		return result, nil
	}
	pod := &pods[0]
	metrics, err := c.dyn.Resource(schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}).Namespace(ref.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		result.Reason = "METRICS_UNAVAILABLE: 指标服务未安装、无访问权限或暂时不可用"
		return result, nil
	}
	if metrics.GetNamespace() != ref.Namespace || metrics.GetName() != pod.Name || metrics.GetUID() != pod.UID {
		result.Reason = "METRICS_UNAVAILABLE: 指标样本未携带可验证的当前 Pod UID"
		return result, nil
	}
	stamp, _, _ := unstructured.NestedString(metrics.Object, "timestamp")
	timestamp, err := time.Parse(time.RFC3339Nano, stamp)
	window, _, _ := unstructured.NestedString(metrics.Object, "window")
	duration, windowErr := time.ParseDuration(window)
	if err != nil || windowErr != nil || duration <= 0 || duration > 2*time.Minute || time.Since(timestamp) > 2*time.Minute || timestamp.After(time.Now().Add(15*time.Second)) || timestamp.Add(-duration).Before(pod.CreationTimestamp.Time) {
		result.Reason = "METRICS_UNAVAILABLE: 样本过期或不属于当前 Pod 生命周期"
		return result, nil
	}
	containers, found, err := unstructured.NestedSlice(metrics.Object, "containers")
	if err != nil || !found {
		result.Reason = "METRICS_UNAVAILABLE: 指标缺少容器用量"
		return result, nil
	}
	var cpu, memory int64
	seen := map[string]bool{}
	for _, value := range containers {
		container, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(container, "name")
		if name == "" || seen[name] {
			result.Reason = "METRICS_UNAVAILABLE: 容器指标身份无效"
			return result, nil
		}
		seen[name] = true
		cpuText, _, _ := unstructured.NestedString(container, "usage", "cpu")
		memoryText, _, _ := unstructured.NestedString(container, "usage", "memory")
		cpuQuantity, cpuErr := resource.ParseQuantity(cpuText)
		memoryQuantity, memoryErr := resource.ParseQuantity(memoryText)
		if cpuErr != nil || memoryErr != nil || cpuQuantity.Sign() < 0 || memoryQuantity.Sign() < 0 {
			result.Reason = "METRICS_UNAVAILABLE: 指标值无效"
			return result, nil
		}
		cpu += cpuQuantity.MilliValue()
		memory += memoryQuantity.Value()
	}
	for _, container := range pod.Spec.Containers {
		if !seen[container.Name] {
			result.Reason = "METRICS_UNAVAILABLE: 容器用量不完整"
			return result, nil
		}
	}
	if err := c.recheckNotebookPod(ctx, ref, pod, stsUID); err != nil {
		return nil, err
	}
	result.Available, result.CPUMilli, result.MemoryBytes, result.Timestamp, result.Window = true, &cpu, &memory, &timestamp, window
	return result, nil
}
