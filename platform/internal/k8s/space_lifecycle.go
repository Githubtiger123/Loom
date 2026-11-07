package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type VerifiedQuota struct {
	HardCPU, HardMemory, HardGPU int64
	UsedCPU, UsedMemory, UsedGPU int64
	ReadStartedAt                time.Time
}

func (c *Client) ReadVerifiedQuota(ctx context.Context, ref SpaceRef) (*VerifiedQuota, error) {
	started := time.Now().UTC()
	if _, err := c.lifecycleSpace(ctx, ref); err != nil {
		return nil, err
	}
	rq, err := c.kube.CoreV1().ResourceQuotas(ref.Name).Get(ctx, c.cfg.QuotaName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	hard, used := []int64{}, []int64{}
	for _, name := range []corev1.ResourceName{corev1.ResourceRequestsCPU, corev1.ResourceRequestsMemory, gpuQuotaResource} {
		h, exists := rq.Spec.Hard[name]
		effective, applied := rq.Status.Hard[name]
		if !exists || !applied || effective.Cmp(h) != 0 {
			return nil, waiting("WAIT_QUOTA", "空间实际额度尚未确认: "+string(name))
		}
		u := rq.Status.Used[name]
		if h.Sign() < 0 || u.Sign() < 0 || h.Cmp(resource.MustParse("1000000000000000")) > 0 || u.Cmp(resource.MustParse("1000000000000000")) > 0 {
			return nil, conflict("空间额度超出可计算范围")
		}
		if name == corev1.ResourceRequestsCPU {
			hard, used = append(hard, h.MilliValue()), append(used, u.MilliValue())
		} else {
			hard, used = append(hard, h.Value()), append(used, u.Value())
		}
	}
	return &VerifiedQuota{HardCPU: hard[0], HardMemory: hard[1], HardGPU: hard[2], UsedCPU: used[0], UsedMemory: used[1], UsedGPU: used[2], ReadStartedAt: started}, nil
}

type WorkspaceRef struct {
	Space                       SpaceRef
	InstanceID, PVCName, PVCUID string
}

// 目录身份与原 Notebook 绑定不可在脱离关联时伪造；目录登记保留原生管理实例标记。
func (c *Client) DeleteRegisteredWorkspace(ctx context.Context, ref WorkspaceRef, guard func() error) error {
	if _, err := c.lifecycleSpace(ctx, ref.Space); err != nil {
		return err
	}
	pvc, err := c.kube.CoreV1().PersistentVolumeClaims(ref.Space.Name).Get(ctx, ref.PVCName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if ref.PVCUID == "" || string(pvc.UID) != ref.PVCUID || pvc.Labels[labelManagedBy] != managedByLoom ||
		pvc.Labels[labelGroupID] != fmt.Sprint(ref.Space.ID) || pvc.Annotations[annotationInstance] != ref.InstanceID ||
		pvc.Annotations["loom.ml/workspace-ownership"] != "EXCLUSIVE" || len(pvc.OwnerReferences) != 0 {
		return conflict("目录 PVC 的 UID、管理实例或独占标记不匹配")
	}
	var next string
	for {
		pods, err := c.kube.CoreV1().Pods(ref.Space.Name).List(ctx, metav1.ListOptions{Limit: 200, Continue: next})
		if err != nil {
			return err
		}
		for _, pod := range pods.Items {
			for _, volume := range pod.Spec.Volumes {
				if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == ref.PVCName {
					return conflict("目录仍被原生 Pod 挂载，拒绝删除")
				}
			}
		}
		next = pods.Continue
		if next == "" {
			break
		}
	}
	if err := guard(); err != nil {
		return err
	}
	if pvc.DeletionTimestamp == nil {
		uid, version := pvc.UID, pvc.ResourceVersion
		if err := c.kube.CoreV1().PersistentVolumeClaims(ref.Space.Name).Delete(ctx, ref.PVCName, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version}}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return waiting("WAIT_WORKSPACE_DELETED", "等待目录 PVC 实际消失")
}

// 核对固定基础设施 Owner 与已登记 UID；生命周期操作不接管同名历史对象。
func (c *Client) lifecycleSpace(ctx context.Context, ref SpaceRef) (*unstructured.Unstructured, error) {
	p, err := c.GetProfile(ctx, ref.Name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, apierrors.NewNotFound(ProfileGVR.GroupResource(), ref.Name)
	}
	owner, _, _ := unstructured.NestedString(p.Object, "spec", "owner", "name")
	if ref.ProfileUID == "" || string(p.GetUID()) != ref.ProfileUID ||
		p.GetLabels()[labelManagedBy] != managedByLoom || p.GetLabels()[labelGroupID] != fmt.Sprint(ref.ID) ||
		p.GetAnnotations()[annotationInstance] != ref.InstanceID || owner != ref.Owner {
		return nil, conflict("空间 Profile 的 UID、管理标记或固定 Owner 不匹配")
	}
	ns, err := c.kube.CoreV1().Namespaces().Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if ref.NamespaceUID == "" || string(ns.UID) != ref.NamespaceUID {
		return nil, conflict("空间 namespace UID 不匹配")
	}
	owned := false
	for _, owner := range ns.OwnerReferences {
		if owner.Kind == "Profile" && string(owner.UID) == ref.ProfileUID && owner.Name == ref.Name {
			owned = true
		}
	}
	if !owned || ns.DeletionTimestamp != nil {
		return nil, conflict("namespace 不属于已登记 Profile 或正在删除")
	}
	return p, nil
}

// UpdateSpaceQuota 只写 Profile 的 quota，让原 controller 落实 ResourceQuota。
// 额度缩小前检查当前 used；实际并发资源准入仍由 Kubernetes ResourceQuota 强制。
func (c *Client) UpdateSpaceQuota(ctx context.Context, ref SpaceRef, in ProfileInput, guard func() error) error {
	p, err := c.lifecycleSpace(ctx, ref)
	if err != nil {
		return err
	}
	rq, err := c.kube.CoreV1().ResourceQuotas(ref.Name).Get(ctx, c.cfg.QuotaName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	want := map[string]string{"requests.cpu": in.CPU, "requests.memory": in.Memory, gpuQuotaResource: fmt.Sprint(in.GPU)}
	if in.Storage != "" {
		want["requests.storage"] = in.Storage
	}
	hard, _, err := unstructured.NestedMap(p.Object, "spec", "resourceQuotaSpec", "hard")
	if err != nil {
		return conflict("Profile quota 格式无效")
	}
	if hard == nil {
		hard = map[string]any{}
	}
	ready := true
	changed := false
	for name, value := range want {
		q, err := resource.ParseQuantity(value)
		if err != nil || q.Sign() < 0 {
			return conflict("额度 %s 格式无效", name)
		}
		used := rq.Status.Used[corev1.ResourceName(name)]
		if used.Cmp(q) > 0 {
			return conflict("额度 %s 低于实际已用量 %s", name, used.String())
		}
		current, exists := hard[name]
		previous, parseErr := resource.ParseQuantity(fmt.Sprint(current))
		if !exists || parseErr != nil || previous.Cmp(q) != 0 {
			hard[name], changed = q.String(), true
		}
		actual, exists := rq.Spec.Hard[corev1.ResourceName(name)]
		effective, applied := rq.Status.Hard[corev1.ResourceName(name)]
		if !exists || !applied || actual.Cmp(q) != 0 || effective.Cmp(q) != 0 {
			ready = false
		}
	}
	if changed {
		if err := guard(); err != nil {
			return err
		}
		if err := unstructured.SetNestedMap(p.Object, hard, "spec", "resourceQuotaSpec", "hard"); err != nil {
			return err
		}
		if _, err := c.dyn.Resource(ProfileGVR).Update(ctx, p, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	if changed || !ready {
		return waiting("WAIT_QUOTA", "已提交 Profile 配额，等待 ResourceQuota 实际生效")
	}
	return nil
}

// DeleteEmptySpace 不递归删除业务资源。发现任一无法识别对象/无法完整发现 API 则拒绝。
// 应先在业务层冻结空间、清空所有工作负载和保留目录，再执行本方法。
func (c *Client) DeleteEmptySpace(ctx context.Context, ref SpaceRef, guard func() error) error {
	p, err := c.GetProfile(ctx, ref.Name)
	if err != nil {
		return err
	}
	ns, nsErr := c.kube.CoreV1().Namespaces().Get(ctx, ref.Name, metav1.GetOptions{})
	if p == nil && apierrors.IsNotFound(nsErr) {
		return nil
	}
	if nsErr != nil && !apierrors.IsNotFound(nsErr) {
		return nsErr
	}
	if p == nil || p.GetDeletionTimestamp() != nil {
		if p != nil && (string(p.GetUID()) != ref.ProfileUID || p.GetAnnotations()[annotationInstance] != ref.InstanceID) {
			return conflict("删除中的 Profile 身份不匹配")
		}
		if nsErr == nil && string(ns.UID) != ref.NamespaceUID {
			return conflict("删除期间 namespace 被替换")
		}
		return waiting("WAIT_SPACE_DELETED", "等待 Profile Controller 清理已确认的空空间")
	}
	if _, err := c.lifecycleSpace(ctx, ref); err != nil {
		return err
	}
	resources, err := c.kube.Discovery().ServerPreferredNamespacedResources()
	if err != nil {
		return fmt.Errorf("无法完整发现空间资源，拒绝删除: %w", err)
	}
	for _, list := range resources {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			return err
		}
		for _, api := range list.APIResources {
			if !api.Namespaced || strings.Contains(api.Name, "/") {
				continue
			}
			canList := false
			for _, verb := range api.Verbs {
				canList = canList || verb == "list"
			}
			if !canList {
				continue
			}
			var next string
			for {
				objects, err := c.dyn.Resource(gv.WithResource(api.Name)).Namespace(ref.Name).List(ctx, metav1.ListOptions{Limit: 200, Continue: next})
				if err != nil {
					return fmt.Errorf("无法核实 %s/%s，拒绝删除: %w", list.GroupVersion, api.Name, err)
				}
				for i := range objects.Items {
					obj := &objects.Items[i]
					if !c.spaceSystemObject(ref, gv.Group, api.Name, obj) {
						return conflict("空间仍有资源 %s/%s（%s），请先显式清理", api.Name, obj.GetName(), list.GroupVersion)
					}
				}
				next = objects.GetContinue()
				if next == "" {
					break
				}
			}
		}
	}
	if err := guard(); err != nil {
		return err
	}
	uid := types.UID(ref.ProfileUID)
	rv := p.GetResourceVersion()
	policy := metav1.DeletePropagationForeground
	if err := c.dyn.Resource(ProfileGVR).Delete(ctx, ref.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &policy}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return waiting("WAIT_SPACE_DELETED", "空空间已提交删除，等待 Profile 与 namespace 消失")
}

func (c *Client) spaceSystemObject(ref SpaceRef, group, resourceName string, obj *unstructured.Unstructured) bool {
	name := obj.GetName()
	if group == "" {
		switch resourceName {
		case "events":
			return true
		case "serviceaccounts":
			return name == "default" || name == "default-editor" || name == "default-viewer"
		case "configmaps":
			return name == "kube-root-ca.crt" || name == "istio-ca-root-cert"
		case "resourcequotas":
			return name == c.cfg.QuotaName
		case "secrets":
			t, _, _ := unstructured.NestedString(obj.Object, "type")
			return t == "kubernetes.io/service-account-token" && (obj.GetAnnotations()["kubernetes.io/service-account.name"] == "default" || obj.GetAnnotations()["kubernetes.io/service-account.name"] == "default-editor" || obj.GetAnnotations()["kubernetes.io/service-account.name"] == "default-viewer")
		}
	}
	if group == "events.k8s.io" && resourceName == "events" {
		return true
	}
	if group == "rbac.authorization.k8s.io" && resourceName == "rolebindings" {
		if name == "namespaceAdmin" || name == "default-editor" || name == "default-viewer" {
			return true
		}
	}
	if group == "security.istio.io" && resourceName == "authorizationpolicies" && name == "ns-owner-access-istio" {
		return c.ownerPolicyMatches(obj, ref.Owner)
	}
	return false
}
