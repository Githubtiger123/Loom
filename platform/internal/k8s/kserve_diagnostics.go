package k8s

import (
	"context"
	"sort"
	"time"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

func kserveDiagnosticContainer(name string) bool {
	return name == "storage-initializer" || name == "kserve-container"
}

func (c *Client) KServeDiagnostics(ctx context.Context, ref KServeRef) (*dto.KServeDiagnostics, error) {
	obs, err := c.ObserveKServe(ctx, ref)
	if err != nil {
		return nil, err
	}
	if obs.Absent {
		return nil, conflict("KSERVE_RESOURCE_MISSING")
	}
	pods, d, err := c.kservePods(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := &dto.KServeDiagnostics{ServiceID: ref.ID, InstanceID: ref.InstanceID, AppliedRevision: obs.Revision, UID: ref.UID, State: obs.State, Ready: obs.Ready, Reason: obs.Reason,
		Pods: []dto.KServePodDiagnostic{}, Events: []dto.KServeEvent{}, ObservedAt: time.Now().UTC()}
	targets := []ObjectRef{{Kind: "InferenceService", Name: ref.Name, UID: ref.UID}}
	if d != nil {
		targets = append(targets, ObjectRef{Kind: "Deployment", Name: d.Name, UID: string(d.UID)})
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	if len(pods) > 20 {
		pods = pods[:20]
		out.Truncated = true
	}
	for _, pod := range pods {
		item := dto.KServePodDiagnostic{Name: pod.Name, UID: string(pod.UID), Phase: string(pod.Status.Phase), Containers: []dto.KServeContainerDiagnostic{}}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				item.Ready = condition.Status == corev1.ConditionTrue
			}
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				item.Reason = boundedKServeText(condition.Reason+": "+condition.Message, 1024)
			}
		}
		for _, group := range []struct {
			statuses []corev1.ContainerStatus
			init     bool
		}{{pod.Status.InitContainerStatuses, true}, {pod.Status.ContainerStatuses, false}} {
			for _, status := range group.statuses {
				if !kserveDiagnosticContainer(status.Name) {
					continue
				}
				container := dto.KServeContainerDiagnostic{Name: status.Name, Init: group.init, Ready: status.Ready, Restarts: status.RestartCount, State: "UNKNOWN"}
				if status.State.Running != nil {
					container.State = "RUNNING"
				}
				if status.State.Waiting != nil {
					container.State = "WAITING"
					container.Reason = boundedKServeText(status.State.Waiting.Reason+": "+status.State.Waiting.Message, 1024)
				}
				if status.State.Terminated != nil {
					container.State = "TERMINATED"
					code := status.State.Terminated.ExitCode
					container.ExitCode = &code
					container.Reason = boundedKServeText(status.State.Terminated.Reason, 256)
				}
				item.Containers = append(item.Containers, container)
			}
		}
		out.Pods = append(out.Pods, item)
		targets = append(targets, ObjectRef{Kind: "Pod", Name: pod.Name, UID: string(pod.UID)})
	}
	for _, target := range targets {
		list, err := c.kube.CoreV1().Events(ref.Namespace).List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", target.UID).String(), Limit: 51})
		if err != nil {
			return nil, err
		}
		out.Truncated = out.Truncated || list.Continue != ""
		for _, event := range list.Items {
			if event.InvolvedObject.Kind != target.Kind || event.InvolvedObject.Name != target.Name || string(event.InvolvedObject.UID) != target.UID || event.InvolvedObject.Namespace != ref.Namespace {
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
			out.Events = append(out.Events, dto.KServeEvent{Kind: target.Kind, Name: target.Name, UID: target.UID, Type: event.Type, Reason: boundedKServeText(event.Reason, 128), Message: boundedKServeText(event.Message, 2048), Count: event.Count, At: at})
		}
	}
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].At.After(out.Events[j].At) })
	if len(out.Events) > 50 {
		out.Events = out.Events[:50]
		out.Truncated = true
	}
	freshPods, freshDeployment, err := c.kservePods(ctx, ref)
	if err != nil {
		return nil, err
	}
	if (d == nil) != (freshDeployment == nil) || (d != nil && freshDeployment != nil && d.UID != freshDeployment.UID) {
		return nil, conflict("查询期间 predictor Deployment UID 已变化")
	}
	freshUIDs := map[string]bool{}
	for _, pod := range freshPods {
		freshUIDs[string(pod.UID)] = true
	}
	for _, pod := range pods {
		if !freshUIDs[string(pod.UID)] {
			return nil, conflict("查询期间 predictor Pod UID 已变化")
		}
	}
	return out, nil
}

func (c *Client) KServeLogs(ctx context.Context, ref KServeRef, q dto.KServeLogQuery) (*podlog.Snapshot, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if !kserveDiagnosticContainer(q.Container) {
		return nil, conflict("只允许模型主容器和 storage-initializer 日志")
	}
	check := func() error {
		pods, _, err := c.kservePods(ctx, ref)
		if err != nil {
			return err
		}
		for _, pod := range pods {
			if pod.Name != q.Pod || string(pod.UID) != q.PodUID {
				continue
			}
			for _, container := range append(pod.Spec.Containers, pod.Spec.InitContainers...) {
				if container.Name == q.Container {
					return nil
				}
			}
		}
		return conflict("日志 Pod UID、容器或归属已变化")
	}
	if err := check(); err != nil {
		return nil, err
	}
	stream, err := c.logs.Open(ctx, podlog.Target{Namespace: ref.Namespace, Pod: q.Pod, PodUID: q.PodUID, Container: q.Container}, podlog.Options{TailLines: q.TailLines, LimitBytes: q.LimitBytes, SinceSeconds: q.SinceSeconds, Previous: q.Previous, Timestamps: true})
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	out, err := podlog.ReadSnapshot(ctx, stream)
	if err != nil {
		return nil, err
	}
	if err := check(); err != nil {
		return nil, err
	}
	return out, nil
}
