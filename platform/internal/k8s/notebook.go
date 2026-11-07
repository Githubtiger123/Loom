package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var NotebookGVR = schema.GroupVersionResource{Group: "kubeflow.org", Version: "v1beta1", Resource: "notebooks"}
var notebookVirtualServiceGVR = schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1alpha3", Resource: "virtualservices"}

const (
	labelNotebookID            = "loom.ml/notebook-id"
	annotationNotebookRevision = "loom.ml/runtime-revision"
	annotationNotebookHash     = "loom.ml/runtime-hash"
	annotationWorkspaceOwner   = "loom.ml/workspace-ownership"
	NotebookStopAnnotation     = "kubeflow-resource-stopped"
)

// NotebookRef 的 UID 一旦存在，绝不因同名 GET 结果变化而重新绑定。
type NotebookRef struct {
	ID, GroupID, Revision            uint64
	InstanceID, Namespace, Name, UID string
	ServerType                       string
	WorkspacePVC, WorkspaceUID       string
	// 已授权 attachment 对应资产的原生实例标识；复用不重写 PVC 的原 Notebook 标签。
	WorkspaceInstanceID string
}

// 只保存安全的观测摘要，不持久化 PodSpec 或环境变量；该结构也用于删除后确认依赖消失。
type NotebookObservation struct {
	UID             string            `json:"uid"`
	Revision        uint64            `json:"revision"`
	State           string            `json:"state"`
	Reason          string            `json:"reason,omitempty"`
	StatefulSetUID  string            `json:"statefulSetUid,omitempty"`
	PodUIDs         map[string]string `json:"podUids,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	BudgetAccounted bool              `json:"budgetAccounted,omitempty"`
}

func notebookObjectMatches(meta metav1.Object, ref NotebookRef, uid string) bool {
	return meta.GetUID() != "" && (uid == "" || string(meta.GetUID()) == uid) &&
		meta.GetLabels()[labelManagedBy] == managedByLoom && meta.GetLabels()[labelNotebookID] == strconv.FormatUint(ref.ID, 10) &&
		meta.GetLabels()[labelGroupID] == strconv.FormatUint(ref.GroupID, 10) && meta.GetAnnotations()[annotationInstance] == ref.InstanceID
}

func notebookMetadata(ref NotebookRef) (map[string]string, map[string]string) {
	return map[string]string{labelManagedBy: managedByLoom, labelGroupID: strconv.FormatUint(ref.GroupID, 10), labelNotebookID: strconv.FormatUint(ref.ID, 10)},
		map[string]string{annotationInstance: ref.InstanceID}
}

func notebookWorkspaceMatches(pvc *corev1.PersistentVolumeClaim, ref NotebookRef) bool {
	if ref.WorkspaceInstanceID == "" {
		return notebookObjectMatches(pvc, ref, ref.WorkspaceUID)
	}
	return ref.WorkspaceUID != "" && string(pvc.UID) == ref.WorkspaceUID && pvc.Labels[labelManagedBy] == managedByLoom &&
		pvc.Labels[labelGroupID] == strconv.FormatUint(ref.GroupID, 10) && pvc.Annotations[annotationInstance] == ref.WorkspaceInstanceID
}

// EnsureNotebookWorkspace 不给 PVC 设置 Notebook ownerReference；停止和默认删除必须保留数据。
func (c *Client) EnsureNotebookWorkspace(ctx context.Context, ref NotebookRef, size int64, guard func() error) (*corev1.PersistentVolumeClaim, error) {
	pvcs := c.kube.CoreV1().PersistentVolumeClaims(ref.Namespace)
	pvc, err := pvcs.Get(ctx, ref.WorkspacePVC, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if ref.WorkspaceUID != "" {
			return nil, conflict("WORKSPACE_MISSING: 已记录的工作目录消失，禁止自动重建")
		}
		if size <= 0 {
			return nil, conflict("工作目录容量无效")
		}
		if err := guard(); err != nil {
			return nil, err
		}
		labels, annotations := notebookMetadata(ref)
		annotations[annotationWorkspaceOwner] = "EXCLUSIVE"
		pvc, err = pvcs.Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: ref.WorkspacePVC, Namespace: ref.Namespace, Labels: labels, Annotations: annotations},
			Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(size, resource.BinarySI)}}}}, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if !notebookWorkspaceMatches(pvc, ref) || pvc.Annotations[annotationWorkspaceOwner] != "EXCLUSIVE" || len(pvc.OwnerReferences) != 0 {
		return nil, conflict("WORKSPACE_IDENTITY_CONFLICT: 工作目录不是目标实例的 Loom 独占目录")
	}
	if pvc.DeletionTimestamp != nil {
		return nil, conflict("工作目录正在删除")
	}
	requested := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if requested.Cmp(*resource.NewQuantity(size, resource.BinarySI)) < 0 {
		return nil, conflict("工作目录容量小于业务请求，禁止隐式替换或缩容")
	}
	if pvc.Status.Phase == corev1.ClaimLost {
		return pvc, conflict("WORKSPACE_LOST: 工作目录底层存储丢失")
	}
	return pvc, nil
}

func (c *Client) GetNotebook(ctx context.Context, ref NotebookRef) (*unstructured.Unstructured, error) {
	n, err := c.dyn.Resource(NotebookGVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !notebookObjectMatches(n, ref, ref.UID) {
		return nil, conflict("NOTEBOOK_IDENTITY_CONFLICT: Notebook UID 或 Loom 实例归属不匹配")
	}
	v, err := strconv.ParseUint(n.GetAnnotations()[annotationNotebookRevision], 10, 64)
	if err != nil || v > ref.Revision {
		return nil, conflict("NOTEBOOK_REVISION_CONFLICT: 集群对象版本超出当前目标")
	}
	return n, nil
}

// ReconcileNotebook 使用 GET/resourceVersion 更新整个受控模板，不接受外部任意 PodSpec。
func (c *Client) ReconcileNotebook(ctx context.Context, ref NotebookRef, in NotebookInput, desired string, guard func() error) (*NotebookObservation, error) {
	in.ServerType = validator.NotebookServerTypeOrDefault(in.ServerType)
	if !validator.IsNotebookServerType(in.ServerType) || in.ServerType != validator.NotebookServerTypeOrDefault(ref.ServerType) {
		return nil, conflict("NOTEBOOK_SERVER_TYPE_CONFLICT: 实例类型无效或与执行目标不一致")
	}
	in.EditorServiceAccount = c.cfg.EditorServiceAccount
	n, err := c.GetNotebook(ctx, ref)
	if err != nil {
		return nil, err
	}
	if n == nil && ref.UID != "" {
		return &NotebookObservation{State: "MISSING", Reason: "Notebook 对象丢失"}, conflict("NOTEBOOK_MISSING: 禁止重建已记录 UID 的对象")
	}
	if n == nil && desired != "RUNNING" {
		return nil, conflict("NOTEBOOK_MISSING: 无可停止或更新的 Notebook")
	}
	want := BuildNotebook(in)
	labels, annotations := notebookMetadata(ref)
	for k, v := range want.GetLabels() {
		labels[k] = v
	}
	// Loom 管理标签最终覆盖用户标签。
	labels[labelManagedBy], labels[labelGroupID], labels[labelNotebookID] = managedByLoom, strconv.FormatUint(ref.GroupID, 10), strconv.FormatUint(ref.ID, 10)
	for k, v := range want.GetAnnotations() {
		annotations[k] = v
	}
	annotations[annotationNotebookRevision] = strconv.FormatUint(ref.Revision, 10)
	template, _, _ := unstructured.NestedMap(want.Object, "spec", "template")
	data, _ := json.Marshal(template)
	annotations[annotationNotebookHash] = fmt.Sprintf("%x", sha256.Sum256(data))
	if desired == "STOPPED" {
		annotations[NotebookStopAnnotation] = time.Now().UTC().Format(time.RFC3339)
	}
	want.SetLabels(labels)
	want.SetAnnotations(annotations)
	if n == nil {
		// 防止社区 controller 对已有同名对象进行接管。
		if err := c.notebookChildrenAbsent(ctx, ref, nil); err != nil {
			return nil, err
		}
		if err := guard(); err != nil {
			return nil, err
		}
		n, err = c.dyn.Resource(NotebookGVR).Namespace(ref.Namespace).Create(ctx, want, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
	} else {
		if n.GetDeletionTimestamp() != nil {
			return nil, conflict("Notebook 正在删除")
		}
		currentTemplate, _, _ := unstructured.NestedMap(n.Object, "spec", "template")
		_, stopped := n.GetAnnotations()[NotebookStopAnnotation]
		if n.GetAnnotations()[annotationNotebookRevision] != annotations[annotationNotebookRevision] || !reflect.DeepEqual(currentTemplate, template) || stopped != (desired == "STOPPED") || !mapContains(n.GetAnnotations(), annotations, NotebookStopAnnotation) || !mapContains(n.GetLabels(), labels, "") || !notebookServerAnnotationsMatch(n.GetAnnotations(), ref) {
			if err := guard(); err != nil {
				return nil, err
			}
			if err := unstructured.SetNestedMap(n.Object, template, "spec", "template"); err != nil {
				return nil, err
			}
			mergedAnnotations := n.GetAnnotations()
			for _, key := range []string{annotationDisplayName, annotationDescription, annotationNotebookServerType, annotationNotebookRewriteURI, annotationNotebookHeaders} {
				if _, exists := annotations[key]; !exists {
					delete(mergedAnnotations, key)
				}
			}
			for k, v := range annotations {
				if k != NotebookStopAnnotation || !stopped {
					mergedAnnotations[k] = v
				}
			}
			if desired == "RUNNING" {
				delete(mergedAnnotations, NotebookStopAnnotation)
			}
			n.SetAnnotations(mergedAnnotations)
			n.SetLabels(labels)
			n, err = c.dyn.Resource(NotebookGVR).Namespace(ref.Namespace).Update(ctx, n, metav1.UpdateOptions{})
			if err != nil {
				return nil, err
			}
		}
	}
	ref.UID = string(n.GetUID())
	return c.observeNotebookObject(ctx, ref, n)
}

func mapContains(have, want map[string]string, ignored string) bool {
	for k, v := range want {
		if k != ignored && have[k] != v {
			return false
		}
	}
	return true
}

func controllerOwned(meta metav1.Object, kind, name, uid string) bool {
	if uid == "" {
		return false
	}
	for _, o := range meta.GetOwnerReferences() {
		if o.Controller != nil && *o.Controller && o.Kind == kind && o.Name == name && string(o.UID) == uid {
			return true
		}
	}
	return false
}

func (c *Client) notebookStatefulSet(ctx context.Context, ref NotebookRef) (*appsv1.StatefulSet, error) {
	sts, err := c.kube.AppsV1().StatefulSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !controllerOwned(sts, "Notebook", ref.Name, ref.UID) {
		return nil, conflict("StatefulSet 不属于已确认的 Notebook UID")
	}
	return sts, nil
}

// NotebookPods 仅接受 Notebook -> StatefulSet -> Pod 的 controller UID 链，名称/label 不能替代归属验证。
func (c *Client) NotebookPods(ctx context.Context, ref NotebookRef, stsUID string) ([]corev1.Pod, error) {
	list, err := c.kube.CoreV1().Pods(ref.Namespace).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return nil, err
	}
	if list.Continue != "" {
		return nil, conflict("Pod 数量超出安全查询上限")
	}
	pods := []corev1.Pod{}
	for _, pod := range list.Items {
		if controllerOwned(&pod, "StatefulSet", ref.Name, stsUID) {
			pods = append(pods, pod)
			continue
		}
		if pod.Labels["statefulset"] == ref.Name || pod.Name == ref.Name+"-0" {
			return nil, conflict("Pod controller UID 与目标 StatefulSet 不匹配")
		}
	}
	return pods, nil
}

func (c *Client) ObserveNotebook(ctx context.Context, ref NotebookRef) (*NotebookObservation, error) {
	n, err := c.GetNotebook(ctx, ref)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return &NotebookObservation{UID: ref.UID, State: "MISSING", Reason: "NOTEBOOK_MISSING: 对象消失，未自动重建"}, nil
	}
	obs, err := c.observeNotebookObject(ctx, ref, n)
	if err != nil {
		return obs, err
	}
	if ref.WorkspacePVC != "" && ref.WorkspaceUID != "" {
		pvc, getErr := c.kube.CoreV1().PersistentVolumeClaims(ref.Namespace).Get(ctx, ref.WorkspacePVC, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			obs.State, obs.Reason = "ERROR", "WORKSPACE_MISSING: 已登记的工作目录消失"
			return obs, nil
		}
		if getErr != nil {
			return obs, getErr
		}
		if !notebookWorkspaceMatches(pvc, ref) {
			return obs, conflict("WORKSPACE_IDENTITY_CONFLICT: 工作目录 UID 或资产实例标识变化")
		}
		if pvc.DeletionTimestamp != nil || pvc.Status.Phase == corev1.ClaimLost {
			obs.State, obs.Reason = "ERROR", "WORKSPACE_UNAVAILABLE: 工作目录正在删除或存储已丢失"
		}
	}
	return obs, nil
}

// StopNotebook 不依赖镜像解析或 PVC 创建；即使创建尚未落地，也能安全停止旧意图。
func (c *Client) StopNotebook(ctx context.Context, ref NotebookRef, guard func() error) (*NotebookObservation, error) {
	n, err := c.GetNotebook(ctx, ref)
	if err != nil {
		return nil, err
	}
	if n == nil {
		if err := c.notebookChildrenAbsent(ctx, ref, nil); err != nil {
			return nil, err
		}
		return &NotebookObservation{UID: ref.UID, Revision: ref.Revision, State: "STOPPED", Reason: "已确认 Notebook 及工作负载不存在"}, nil
	}
	annotations := n.GetAnnotations()
	_, stopped := annotations[NotebookStopAnnotation]
	if !stopped || annotations[annotationNotebookRevision] != strconv.FormatUint(ref.Revision, 10) {
		if err := guard(); err != nil {
			return nil, err
		}
		if !stopped {
			annotations[NotebookStopAnnotation] = time.Now().UTC().Format(time.RFC3339)
		}
		annotations[annotationNotebookRevision] = strconv.FormatUint(ref.Revision, 10)
		n.SetAnnotations(annotations)
		n, err = c.dyn.Resource(NotebookGVR).Namespace(ref.Namespace).Update(ctx, n, metav1.UpdateOptions{})
		if err != nil {
			return nil, err
		}
	}
	return c.observeNotebookObject(ctx, ref, n)
}

func (c *Client) observeNotebookObject(ctx context.Context, ref NotebookRef, n *unstructured.Unstructured) (*NotebookObservation, error) {
	obs := &NotebookObservation{UID: string(n.GetUID()), State: "PENDING", ResourceVersion: n.GetResourceVersion(), PodUIDs: map[string]string{}}
	obs.Revision, _ = strconv.ParseUint(n.GetAnnotations()[annotationNotebookRevision], 10, 64)
	ref.UID = obs.UID
	sts, err := c.notebookStatefulSet(ctx, ref)
	if err != nil {
		return obs, err
	}
	if sts != nil {
		obs.StatefulSetUID = string(sts.UID)
	}
	pods, err := c.NotebookPods(ctx, ref, obs.StatefulSetUID)
	if err != nil {
		return obs, err
	}
	for _, pod := range pods {
		obs.PodUIDs[pod.Name] = string(pod.UID)
	}
	if n.GetDeletionTimestamp() != nil {
		obs.State, obs.Reason = "DELETING", "等待 Controller 与垃圾回收删除资源"
		return obs, nil
	}
	_, stopped := n.GetAnnotations()[NotebookStopAnnotation]
	if stopped {
		if len(pods) == 0 && (sts == nil || (sts.Spec.Replicas != nil && *sts.Spec.Replicas == 0 && sts.Status.ObservedGeneration >= sts.Generation && sts.Status.Replicas == 0 && sts.Spec.Template.Annotations[annotationNotebookRevision] == strconv.FormatUint(ref.Revision, 10) && sts.Spec.Template.Annotations[annotationNotebookHash] == n.GetAnnotations()[annotationNotebookHash])) {
			obs.State = "STOPPED"
		} else {
			obs.State, obs.Reason = "STOPPING", "等待 StatefulSet 缩容到 0 且 Pod 消失"
		}
		return obs, nil
	}
	if !notebookServerAnnotationsMatch(n.GetAnnotations(), ref) {
		return obs, conflict("NOTEBOOK_SERVER_TYPE_CONFLICT: CR 类型或 HTTP 访问适配与业务记录不一致")
	}
	if sts == nil {
		obs.Reason = "等待 notebook-controller 创建 StatefulSet"
		return obs, nil
	}
	// 先检查 Pod 故障再检查 ReadyReplicas，否则崩溃/拉取失败会一直被“等待就绪”遮住。
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil || pod.Annotations[annotationNotebookRevision] != strconv.FormatUint(ref.Revision, 10) {
			continue
		}
		if pod.Status.Phase == corev1.PodFailed {
			obs.State, obs.Reason = "ERROR", boundedNotebookText(pod.Status.Reason, 256)
			return obs, nil
		}
		statuses := append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...)
		statuses = append(statuses, pod.Status.ContainerStatuses...)
		for _, status := range statuses {
			if status.State.Waiting != nil {
				switch status.State.Waiting.Reason {
				case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "CreateContainerConfigError", "InvalidImageName", "RunContainerError":
					obs.State, obs.Reason = "ERROR", boundedNotebookText(status.State.Waiting.Reason+": "+status.State.Waiting.Message, 1000)
					return obs, nil
				}
			}
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				obs.Reason = boundedNotebookText(condition.Reason+": "+condition.Message, 1000)
				return obs, nil
			}
		}
	}
	if sts.Status.ObservedGeneration < sts.Generation || sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 || sts.Status.ReadyReplicas != 1 || sts.Status.CurrentRevision != sts.Status.UpdateRevision || sts.Spec.Template.Annotations[annotationNotebookRevision] != strconv.FormatUint(ref.Revision, 10) || sts.Spec.Template.Annotations[annotationNotebookHash] != n.GetAnnotations()[annotationNotebookHash] {
		obs.Reason = "等待目标版本的 StatefulSet 就绪"
		return obs, nil
	}
	ready := 0
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil || pod.Annotations[annotationNotebookRevision] != strconv.FormatUint(ref.Revision, 10) || pod.Annotations[annotationNotebookHash] != n.GetAnnotations()[annotationNotebookHash] {
			continue
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Waiting != nil && (status.State.Waiting.Reason == "ImagePullBackOff" || status.State.Waiting.Reason == "ErrImagePull" || status.State.Waiting.Reason == "CrashLoopBackOff" || status.State.Waiting.Reason == "CreateContainerConfigError") {
				obs.State, obs.Reason = "ERROR", status.State.Waiting.Reason
				return obs, nil
			}
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				ready++
			}
		}
	}
	if ready == 1 && len(pods) == 1 && obs.Revision == ref.Revision {
		svc, err := c.kube.CoreV1().Services(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			obs.Reason = "等待 Notebook Service"
			return obs, nil
		}
		if err != nil {
			return obs, err
		}
		if !controllerOwned(svc, "Notebook", ref.Name, ref.UID) {
			return obs, conflict("Service 不属于目标 Notebook UID")
		}
		if svc.DeletionTimestamp == nil && svc.Spec.Selector["statefulset"] == ref.Name {
			obs.State = "RUNNING"
			obs.BudgetAccounted, err = c.notebookBudgetAccounted(ctx, ref)
			if err != nil {
				return obs, err
			}
		}
	} else {
		obs.Reason = "等待目标版本 Pod Ready"
	}
	return obs, nil
}

// 保守核算：确认本实例 Pod 已存在，并在有界完整快照内检查 quota.used 已覆盖所有非终态 Pod 请求。
// 新版 Pod 级资源、restartable init 尚未覆盖时保持 HELD，不冒充 ACCOUNTED。
func (c *Client) notebookBudgetAccounted(ctx context.Context, ref NotebookRef) (bool, error) {
	pods, err := c.kube.CoreV1().Pods(ref.Namespace).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return false, err
	}
	if pods.Continue != "" {
		return false, nil
	}
	total := corev1.ResourceList{}
	found := false
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}
		if pod.Spec.Resources != nil {
			return false, nil
		}
		if pod.Annotations[annotationInstance] == ref.InstanceID && pod.Annotations[annotationNotebookRevision] == strconv.FormatUint(ref.Revision, 10) {
			found = true
		}
		requests := corev1.ResourceList{}
		for _, container := range pod.Spec.Containers {
			for k, v := range container.Resources.Requests {
				q := requests[k]
				q.Add(v)
				requests[k] = q
			}
		}
		for _, container := range pod.Spec.InitContainers {
			if container.RestartPolicy != nil {
				return false, nil
			}
			for k, v := range container.Resources.Requests {
				q := requests[k]
				if v.Cmp(q) > 0 {
					requests[k] = v.DeepCopy()
				}
			}
		}
		for k, v := range pod.Spec.Overhead {
			q := requests[k]
			q.Add(v)
			requests[k] = q
		}
		for k, v := range requests {
			q := total[k]
			q.Add(v)
			total[k] = q
		}
	}
	if !found {
		return false, nil
	}
	rq, err := c.kube.CoreV1().ResourceQuotas(ref.Namespace).Get(ctx, c.cfg.QuotaName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	for _, pair := range [][2]corev1.ResourceName{{corev1.ResourceCPU, corev1.ResourceRequestsCPU}, {corev1.ResourceMemory, corev1.ResourceRequestsMemory}, {"nvidia.com/gpu", "requests.nvidia.com/gpu"}} {
		want := total[pair[0]]
		if want.IsZero() {
			continue
		}
		used, ok := rq.Status.Used[pair[1]]
		if !ok || used.Cmp(want) < 0 {
			return false, nil
		}
	}
	return true, nil
}

// notebookChildrenAbsent 也用于创建前的同名冲突检查，previous 保存删除前捕获的 StatefulSet UID。
func (c *Client) notebookChildrenAbsent(ctx context.Context, ref NotebookRef, previous *NotebookObservation) error {
	sts, err := c.notebookStatefulSet(ctx, ref)
	if err != nil {
		return err
	}
	if sts != nil {
		return waiting("WAIT_GC", "等待 Notebook StatefulSet 删除")
	}
	stsUID := ""
	if previous != nil {
		stsUID = previous.StatefulSetUID
	}
	pods, err := c.NotebookPods(ctx, ref, stsUID)
	if err != nil {
		return err
	}
	if len(pods) > 0 {
		return waiting("WAIT_GC", "等待 Notebook Pod 删除")
	}
	svc, err := c.kube.CoreV1().Services(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err == nil {
		if !controllerOwned(svc, "Notebook", ref.Name, ref.UID) {
			return conflict("同名 Service 归属不匹配")
		}
		return waiting("WAIT_GC", "等待 Notebook Service 删除")
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	vs, err := c.dyn.Resource(notebookVirtualServiceGVR).Namespace(ref.Namespace).Get(ctx, "notebook-"+ref.Namespace+"-"+ref.Name, metav1.GetOptions{})
	if err == nil {
		if !controllerOwned(vs, "Notebook", ref.Name, ref.UID) {
			return conflict("同名 VirtualService 归属不匹配")
		}
		return waiting("WAIT_GC", "等待 Notebook VirtualService 删除")
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Client) DeleteNotebook(ctx context.Context, ref NotebookRef, previous *NotebookObservation, guard func() error, checkpoint func(*NotebookObservation) error) error {
	n, err := c.GetNotebook(ctx, ref)
	if err != nil {
		return err
	}
	if n != nil {
		ref.UID = string(n.GetUID())
		obs, err := c.observeNotebookObject(ctx, ref, n)
		if err != nil {
			return err
		}
		if previous != nil && obs.StatefulSetUID == "" {
			obs.StatefulSetUID = previous.StatefulSetUID
		}
		if err := checkpoint(obs); err != nil {
			return err
		}
		if n.GetDeletionTimestamp() == nil {
			if err := guard(); err != nil {
				return err
			}
			uid, rv, foreground := n.GetUID(), n.GetResourceVersion(), metav1.DeletePropagationForeground
			if err := c.dyn.Resource(NotebookGVR).Namespace(ref.Namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &foreground}); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
		return waiting("WAIT_NOTEBOOK_DELETE", "已申请删除，等待 Notebook 和派生资源实际消失")
	}
	return c.notebookChildrenAbsent(ctx, ref, previous)
}

func (c *Client) DeleteNotebookWorkspace(ctx context.Context, ref NotebookRef, guard func() error) error {
	if ref.WorkspacePVC == "" || ref.WorkspaceUID == "" {
		return conflict("删除工作目录需要已持久化的 PVC UID")
	}
	pvc, err := c.kube.CoreV1().PersistentVolumeClaims(ref.Namespace).Get(ctx, ref.WorkspacePVC, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !notebookWorkspaceMatches(pvc, ref) || pvc.Annotations[annotationWorkspaceOwner] != "EXCLUSIVE" || len(pvc.OwnerReferences) != 0 {
		return conflict("只可删除 Loom 独占且 UID 匹配的工作目录")
	}
	// 同 namespace 的其他 Pod 即使未写入 Loom DB，也不能在挂载期间删除 PVC。
	pods, err := c.kube.CoreV1().Pods(ref.Namespace).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		return err
	}
	if pods.Continue != "" {
		return conflict("无法在有界查询内证明工作目录未被挂载")
	}
	for _, pod := range pods.Items {
		for _, v := range pod.Spec.Volumes {
			if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == ref.WorkspacePVC {
				return conflict("工作目录仍被 Pod 引用，拒绝删除")
			}
		}
	}
	notebooks, err := c.dyn.Resource(NotebookGVR).Namespace(ref.Namespace).List(ctx, metav1.ListOptions{Limit: 200})
	if err != nil {
		return err
	}
	if notebooks.GetContinue() != "" {
		return conflict("无法在有界查询内证明工作目录未被 Notebook 引用")
	}
	for _, notebook := range notebooks.Items {
		volumes, _, nestedErr := unstructured.NestedSlice(notebook.Object, "spec", "template", "spec", "volumes")
		if nestedErr != nil {
			return conflict("无法核实空间内 Notebook 的目录引用")
		}
		for _, value := range volumes {
			volume, ok := value.(map[string]any)
			if !ok {
				return conflict("无法核实空间内 Notebook 的目录引用")
			}
			claim, _, _ := unstructured.NestedString(volume, "persistentVolumeClaim", "claimName")
			if claim == ref.WorkspacePVC {
				return conflict("工作目录仍被 Notebook CR 引用，拒绝删除")
			}
		}
	}
	if pvc.DeletionTimestamp == nil {
		if err := guard(); err != nil {
			return err
		}
		uid, rv := types.UID(ref.WorkspaceUID), pvc.ResourceVersion
		if err := c.kube.CoreV1().PersistentVolumeClaims(ref.Namespace).Delete(ctx, ref.WorkspacePVC, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return waiting("WAIT_WORKSPACE_DELETE", "等待工作目录 PVC 实际消失")
}
