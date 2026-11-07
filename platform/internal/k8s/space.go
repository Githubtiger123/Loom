package k8s

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EnsureSpace 仅创建尚未创建过的空间。已记录 UID 的对象消失时不自动重建。
// 非空 observation 即使伴随等待/错误，也包含已确认的 UID，供执行器持久化恢复。
func (c *Client) EnsureSpace(ctx context.Context, ref SpaceRef, in ProfileInput, create bool) (*SpaceObservation, error) {
	obs := &SpaceObservation{}
	p, err := c.GetProfile(ctx, ref.Name)
	if err != nil {
		return obs, err
	}
	if p == nil {
		if ref.ProfileUID != "" {
			return obs, conflict("Profile %s 已消失，禁止按旧记录自动重建", ref.Name)
		}
		if !create {
			return obs, waiting("WAIT_PROFILE", "等待空间创建任务提交 Profile")
		}
		_, err := c.kube.CoreV1().Namespaces().Get(ctx, ref.Name, metav1.GetOptions{})
		if err == nil {
			return obs, conflict("同名 namespace %s 已存在，拒绝创建 Profile 接管", ref.Name)
		}
		if !apierrors.IsNotFound(err) {
			return obs, err
		}
		in.Name, in.Owner, in.GroupID, in.InstanceID = ref.Name, ref.Owner, ref.ID, ref.InstanceID
		if err := c.CreateProfile(ctx, in); err != nil {
			// 超时或 AlreadyExists 只读取核实，不删除、不盲目重建。
			found, getErr := c.GetProfile(ctx, ref.Name)
			if getErr != nil || found == nil {
				return obs, err
			}
			p = found
		} else {
			p, err = c.GetProfile(ctx, ref.Name)
			if err != nil {
				return obs, err
			}
		}
		if p == nil {
			return obs, waiting("CREATE_PROFILE", "Profile 已提交，等待读取确认")
		}
	}
	owner, _, _ := unstructured.NestedString(p.Object, "spec", "owner", "name")
	kind, _, _ := unstructured.NestedString(p.Object, "spec", "owner", "kind")
	if p.GetLabels()[labelManagedBy] != managedByLoom || p.GetLabels()[labelGroupID] != fmt.Sprint(ref.ID) || p.GetAnnotations()[annotationInstance] != ref.InstanceID || owner != ref.Owner || kind != "User" {
		return obs, conflict("Profile %s 的管理标记、实例或固定 Owner 不匹配", ref.Name)
	}
	if ref.ProfileUID != "" && string(p.GetUID()) != ref.ProfileUID {
		return obs, conflict("Profile %s 已被同名新对象替换", ref.Name)
	}
	obs.ProfileUID = string(p.GetUID())
	if p.GetDeletionTimestamp() != nil {
		return obs, conflict("Profile %s 正在删除", ref.Name)
	}
	ns, err := c.kube.CoreV1().Namespaces().Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return obs, waiting("WAIT_NAMESPACE", "等待 Profile Controller 创建 namespace")
	}
	if err != nil {
		return obs, err
	}
	owned := false
	for _, o := range ns.OwnerReferences {
		if o.Kind == "Profile" && o.Name == ref.Name && o.UID == p.GetUID() {
			owned = true
		}
	}
	if !owned {
		return obs, conflict("namespace %s 不属于目标 Profile UID", ref.Name)
	}
	if ref.NamespaceUID != "" && ref.NamespaceUID != string(ns.UID) {
		return obs, conflict("namespace %s 已被同名新对象替换", ref.Name)
	}
	obs.NamespaceUID = string(ns.UID)
	if ns.DeletionTimestamp != nil || ns.Status.Phase != corev1.NamespaceActive {
		return obs, waiting("WAIT_NAMESPACE", "namespace 尚未处于 ACTIVE 或正在删除")
	}
	for _, role := range []string{"kubeflow-admin", "kubeflow-edit", "kubeflow-view"} {
		r, err := c.kube.RbacV1().ClusterRoles().Get(ctx, role, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return obs, waiting("WAIT_BASE_RESOURCES", "缺少 ClusterRole "+role)
		}
		if err != nil {
			return obs, err
		}
		if len(r.Rules) == 0 {
			return obs, waiting("WAIT_BASE_RESOURCES", "ClusterRole "+role+" 尚无聚合权限")
		}
	}
	for _, entry := range []struct{ name, role string }{{"default-editor", "kubeflow-edit"}, {"default-viewer", "kubeflow-view"}} {
		if _, err := c.kube.CoreV1().ServiceAccounts(ref.Name).Get(ctx, entry.name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
			return obs, waiting("WAIT_BASE_RESOURCES", "等待 ServiceAccount "+entry.name)
		} else if err != nil {
			return obs, err
		}
		rb, err := c.kube.RbacV1().RoleBindings(ref.Name).Get(ctx, entry.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return obs, waiting("WAIT_BASE_RESOURCES", "等待系统绑定 "+entry.name)
		}
		if err != nil {
			return obs, err
		}
		if rb.RoleRef.Name != entry.role || rb.RoleRef.Kind != "ClusterRole" || rb.RoleRef.APIGroup != rbacv1.GroupName || len(rb.Subjects) != 1 || rb.Subjects[0].Kind != "ServiceAccount" || rb.Subjects[0].Name != entry.name || rb.Subjects[0].Namespace != ref.Name {
			return obs, waiting("WAIT_BASE_RESOURCES", "系统 SA 绑定配置不匹配: "+entry.name)
		}
	}
	admin, err := c.kube.RbacV1().RoleBindings(ref.Name).Get(ctx, "namespaceAdmin", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return obs, waiting("WAIT_BASE_RESOURCES", "等待 namespaceAdmin")
	}
	if err != nil {
		return obs, err
	}
	if admin.RoleRef.Name != "kubeflow-admin" || admin.RoleRef.Kind != "ClusterRole" || admin.RoleRef.APIGroup != rbacv1.GroupName || len(admin.Subjects) != 1 || admin.Subjects[0].Kind != "User" || admin.Subjects[0].Name != ref.Owner {
		return obs, waiting("WAIT_BASE_RESOURCES", "固定 Owner 的系统授权不匹配")
	}
	ap, err := c.dyn.Resource(AuthzPolicyGVR).Namespace(ref.Name).Get(ctx, "ns-owner-access-istio", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return obs, waiting("WAIT_BASE_RESOURCES", "等待 Owner 访问策略")
	}
	if err != nil {
		return obs, err
	}
	if !c.ownerPolicyMatches(ap, ref.Owner) {
		return obs, waiting("WAIT_BASE_RESOURCES", "Owner 访问策略与可信网关、身份头或前缀不匹配")
	}
	cpu, cpuErr := resource.ParseQuantity(in.CPU)
	memory, memErr := resource.ParseQuantity(in.Memory)
	if cpuErr != nil || memErr != nil || cpu.Sign() < 0 || memory.Sign() < 0 || in.GPU < 0 {
		return obs, conflict("空间额度配置无效")
	}
	expected := map[corev1.ResourceName]resource.Quantity{corev1.ResourceRequestsCPU: cpu, corev1.ResourceRequestsMemory: memory, gpuQuotaResource: *resource.NewQuantity(int64(in.GPU), resource.DecimalSI)}
	if in.Storage != "" {
		q, err := resource.ParseQuantity(in.Storage)
		if err != nil {
			return obs, conflict("存储额度无效")
		}
		expected[corev1.ResourceRequestsStorage] = q
	}
	rq, err := c.kube.CoreV1().ResourceQuotas(ref.Name).Get(ctx, c.cfg.QuotaName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return obs, waiting("WAIT_BASE_RESOURCES", "等待空间 ResourceQuota")
	}
	if err != nil {
		return obs, err
	}
	obs.Quota = map[string]string{}
	for key, value := range rq.Spec.Hard {
		obs.Quota[string(key)] = value.String()
	}
	for key, want := range expected {
		have, exists := rq.Spec.Hard[key]
		effective, applied := rq.Status.Hard[key]
		if !exists || have.Cmp(want) != 0 || !applied || effective.Cmp(want) != 0 {
			return obs, waiting("WAIT_BASE_RESOURCES", "等待实际额度生效: "+string(key))
		}
	}
	return obs, nil
}

func (c *Client) ownerPolicyMatches(ap *unstructured.Unstructured, owner string) bool {
	rules, _, _ := unstructured.NestedSlice(ap.Object, "spec", "rules")
	ownerFound := false
	for _, item := range rules {
		one := ap.DeepCopy()
		if err := unstructured.SetNestedSlice(one.Object, []any{item}, "spec", "rules"); err != nil {
			return false
		}
		if c.ownerRuleMatches(one, owner) {
			if ownerFound {
				return false
			}
			ownerFound = true
			continue
		}
		// 原 Profile Controller 同时生成同空间互访、探针与 kernel 观测规则。
		// 只认这些完整规则，不能因为名字是系统策略就放过任意额外 ALLOW。
		controllerPrincipal := "cluster.local/ns/kubeflow/sa/notebook-controller-service-account"
		if domain, _, ok := strings.Cut(c.cfg.KFPUIPrincipal, "/ns/"); ok {
			controllerPrincipal = domain + "/ns/kubeflow/sa/notebook-controller-service-account"
		}
		known := []map[string]any{
			{"when": []any{map[string]any{"key": "source.namespace", "values": []any{ap.GetNamespace()}}}},
			{"to": []any{map[string]any{"operation": map[string]any{"paths": []any{"/healthz", "/metrics", "/wait-for-drain"}}}}},
			{"from": []any{map[string]any{"source": map[string]any{"principals": []any{controllerPrincipal}}}}, "to": []any{map[string]any{"operation": map[string]any{"methods": []any{"GET"}, "paths": []any{"*/api/kernels"}}}}},
		}
		recognized := false
		for _, expected := range known {
			recognized = recognized || reflect.DeepEqual(item, expected)
		}
		if !recognized {
			return false
		}
	}
	return ownerFound
}

func (c *Client) ownerRuleMatches(ap *unstructured.Unstructured, owner string) bool {
	action, _, _ := unstructured.NestedString(ap.Object, "spec", "action")
	selector, _, _ := unstructured.NestedMap(ap.Object, "spec", "selector")
	targets, _, _ := unstructured.NestedSlice(ap.Object, "spec", "targetRefs")
	if (action != "" && action != "ALLOW") || len(selector) > 0 || len(targets) > 0 {
		return false
	}
	rules, _, _ := unstructured.NestedSlice(ap.Object, "spec", "rules")
	if len(rules) != 1 {
		return false
	}
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok || len(rule) != 2 {
			continue
		}
		conditions, _ := rule["when"].([]any)
		from, _ := rule["from"].([]any)
		if len(conditions) != 1 || len(from) != 1 {
			continue
		}
		cond, ok := conditions[0].(map[string]any)
		if !ok || len(cond) != 2 {
			continue
		}
		values, _ := cond["values"].([]any)
		if cond["key"] != fmt.Sprintf("request.headers[%s]", c.cfg.UserIDHeader) || len(values) != 1 || values[0] != c.cfg.UserIDPrefix+owner {
			continue
		}
		f, ok := from[0].(map[string]any)
		if !ok || len(f) != 1 {
			continue
		}
		source, _ := f["source"].(map[string]any)
		principals, _ := source["principals"].([]any)
		seen := map[string]bool{}
		for _, p := range principals {
			if s, ok := p.(string); ok {
				if s != c.cfg.IngressPrincipal && s != c.cfg.KFPUIPrincipal {
					return false
				}
				seen[s] = true
			} else {
				return false
			}
		}
		if len(source) == 1 && seen[c.cfg.IngressPrincipal] && seen[c.cfg.KFPUIPrincipal] {
			return true
		}
	}
	return false
}

// AccessNamespace 不依赖额度和默认资源就绪，避免基础资源故障阻断撤权。
// 返回 false 只表示一次成功 GET 已确认 namespace 不存在，不表示网络错误。
func (c *Client) AccessNamespace(ctx context.Context, ref SpaceRef) (bool, error) {
	ns, err := c.kube.CoreV1().Namespaces().Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ref.NamespaceUID == "" || ref.ProfileUID == "" {
		return true, waiting("WAIT_NAMESPACE", "等待空间任务确认 namespace 和 Profile UID")
	}
	if string(ns.UID) != ref.NamespaceUID {
		return true, conflict("namespace %s UID 已变更", ref.Name)
	}
	for _, owner := range ns.OwnerReferences {
		if owner.Kind == "Profile" && owner.Name == ref.Name && string(owner.UID) == ref.ProfileUID {
			return true, nil
		}
	}
	return true, conflict("namespace %s ownerReference 不匹配", ref.Name)
}
