package k8s

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type AccessTarget struct {
	Space           SpaceRef
	UserID          uint64
	Username, Level string
	Version         uint64
	Previous        []ObjectRef
}
type AccessObservation struct {
	Level                   string      `json:"level"`
	Objects                 []ObjectRef `json:"objects"`
	Warning                 string      `json:"warning,omitempty"`
	WarningCode             string      `json:"warningCode,omitempty"`
	InfrastructureOwnerOnly bool        `json:"infrastructureOwnerOnly,omitempty"`
}

// ReconcileAccess 不按业务角色逐个授权，只处理该空间用户聚合后的目标。
// guard 在每次外部写入前检查任务租约和最新业务目标，部分成功不回滚，重试补齐。
func (c *Client) ReconcileAccess(ctx context.Context, t AccessTarget, guard func() error, observeOnly bool) (*AccessObservation, error) {
	obs := &AccessObservation{Level: "unknown", Objects: []ObjectRef{}}
	check := func() error {
		if err := guard(); err != nil {
			return err
		}
		ns, err := c.kube.CoreV1().Namespaces().Get(ctx, t.Space.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if string(ns.UID) != t.Space.NamespaceUID || (ns.DeletionTimestamp != nil && t.Level != "none") {
			return conflict("授权目标 namespace UID 已变更或正在删除")
		}
		return nil
	}
	if t.Level != "none" && t.Level != "view" && t.Level != "edit" {
		return obs, conflict("无效的访问级别 %s", t.Level)
	}
	if t.Space.NamespaceUID == "" {
		return obs, waiting("WAIT_NAMESPACE", "尚未确认 namespace UID")
	}
	if err := check(); err != nil {
		return obs, err
	}
	// 完整分页 LIST 成功后才判定遗漏，不能把某一页或网络错误当成没有对象。
	rbs, err := c.listRoleBindings(ctx, t.Space.Name)
	if err != nil {
		return obs, err
	}
	aps, err := c.listPolicies(ctx, t.Space.Name)
	if err != nil {
		return obs, err
	}
	role := ""
	name := ""
	if t.Level != "none" {
		role = "kubeflow-" + t.Level
		name = BindingName(t.Username, role)
	}
	ownedRB := []*rbacv1.RoleBinding{}
	ownedAP := []*unstructured.Unstructured{}
	for i := range rbs {
		rb := &rbs[i]
		candidate := rb.Name == name || (rb.Labels[labelManagedBy] == managedByLoom && (rb.Labels[labelUserID] == fmt.Sprint(t.UserID) || rb.Annotations[annotationUser] == t.Username))
		if !candidate {
			continue
		}
		if err := verifyRoleBinding(rb, t.Username, rb.RoleRef.Name); err != nil {
			return obs, conflict("%v", err)
		}
		if err := verifyAccessIdentity(rb, t); err != nil {
			return obs, err
		}
		if rb.RoleRef.Name != "kubeflow-edit" && rb.RoleRef.Name != "kubeflow-view" {
			return obs, conflict("未知的 Loom 成员角色 %s", rb.RoleRef.Name)
		}
		if err := verifyPrevious(rb, "RoleBinding", t.Previous); err != nil {
			return obs, err
		}
		ownedRB = append(ownedRB, rb)
	}
	for i := range aps {
		ap := &aps[i]
		candidate := ap.GetName() == name || (ap.GetLabels()[labelManagedBy] == managedByLoom && (ap.GetLabels()[labelUserID] == fmt.Sprint(t.UserID) || ap.GetAnnotations()[annotationUser] == t.Username))
		if !candidate {
			continue
		}
		if err := verifyAuthzPolicy(ap, t.Username, ap.GetAnnotations()[annotationClusterRole]); err != nil {
			return obs, conflict("%v", err)
		}
		if err := verifyAccessIdentity(ap, t); err != nil {
			return obs, err
		}
		if err := verifyPrevious(ap, "AuthorizationPolicy", t.Previous); err != nil {
			return obs, err
		}
		ownedAP = append(ownedAP, ap)
	}
	// 必须先清理非目标（尤其是 edit），清理失败不得继续报告低权限已生效。
	for _, rb := range ownedRB {
		if rb.Name != name {
			if observeOnly {
				return obs, waiting("DRIFTED", "发现旧的成员 RoleBinding")
			}
			if err := check(); err != nil {
				return obs, err
			}
			if err := c.kube.RbacV1().RoleBindings(t.Space.Name).Delete(ctx, rb.Name, guardedDelete(rb)); err != nil && !apierrors.IsNotFound(err) {
				return obs, err
			}
		}
	}
	for _, ap := range ownedAP {
		if ap.GetName() != name {
			if observeOnly {
				return obs, waiting("DRIFTED", "发现旧的成员访问策略")
			}
			if err := check(); err != nil {
				return obs, err
			}
			if err := c.dyn.Resource(AuthzPolicyGVR).Namespace(t.Space.Name).Delete(ctx, ap.GetName(), guardedDelete(ap)); err != nil && !apierrors.IsNotFound(err) {
				return obs, err
			}
		}
	}
	if name != "" {
		var rb *rbacv1.RoleBinding
		var ap *unstructured.Unstructured
		for _, item := range ownedRB {
			if item.Name == name {
				rb = item
			}
		}
		for _, item := range ownedAP {
			if item.GetName() == name {
				ap = item
			}
		}
		if rb == nil {
			if observeOnly {
				return obs, waiting("DRIFTED", "缺少成员 RoleBinding")
			}
			if err := check(); err != nil {
				return obs, err
			}
			want := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: t.Space.Name, Labels: accessLabels(t), Annotations: accessAnnotations(t, role)}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role}, Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: t.Username}}}
			var err error
			rb, err = c.kube.RbacV1().RoleBindings(t.Space.Name).Create(ctx, want, metav1.CreateOptions{})
			if err != nil {
				return obs, err
			}
		}
		want := c.buildAuthzPolicy(t.Space.Name, name, t.Username, role)
		want.SetLabels(accessLabels(t))
		ann := accessAnnotations(t, role)
		ann[annotationRole] = "member"
		ann[annotationClusterRole] = role
		want.SetAnnotations(ann)
		if ap == nil {
			if observeOnly {
				return obs, waiting("DRIFTED", "缺少成员 AuthorizationPolicy")
			}
			if err := check(); err != nil {
				return obs, err
			}
			var err error
			ap, err = c.dyn.Resource(AuthzPolicyGVR).Namespace(t.Space.Name).Create(ctx, want, metav1.CreateOptions{})
			if err != nil {
				return obs, err
			}
		} else if !reflect.DeepEqual(ap.Object["spec"], want.Object["spec"]) {
			if observeOnly {
				return obs, waiting("DRIFTED", "成员 AuthorizationPolicy 内容不匹配")
			}
			if err := check(); err != nil {
				return obs, err
			}
			updated := ap.DeepCopy()
			updated.Object["spec"] = want.Object["spec"]
			annotations := updated.GetAnnotations()
			for key, value := range want.GetAnnotations() {
				annotations[key] = value
			}
			updated.SetAnnotations(annotations)
			var err error
			ap, err = c.dyn.Resource(AuthzPolicyGVR).Namespace(t.Space.Name).Update(ctx, updated, metav1.UpdateOptions{})
			if err != nil {
				return obs, err
			}
		}
		obs.Objects = append(obs.Objects, ObjectRef{Kind: "RoleBinding", Name: rb.Name, UID: string(rb.UID), Role: role}, ObjectRef{Kind: "AuthorizationPolicy", Name: ap.GetName(), UID: string(ap.GetUID()), Role: role})
	}
	if !observeOnly {
		// 重新 LIST 确认删除已完成、两类目标均存在，不能仅凭写调用返回判成功。
		t.Previous = obs.Objects
		return c.ReconcileAccess(ctx, t, guard, true)
	}
	obs.Level = t.Level
	// 探测已知的额外直接授权，不承诺推导 OIDC 组、所有网格策略及既有会话。
	for _, rb := range rbs {
		if rb.Name == name {
			continue
		}
		if t.Username == t.Space.Owner && rb.Name == "namespaceAdmin" && rb.RoleRef.Name == "kubeflow-admin" &&
			rb.RoleRef.Kind == "ClusterRole" && rb.RoleRef.APIGroup == rbacv1.GroupName && len(rb.Subjects) == 1 &&
			rb.Subjects[0].Kind == "User" && rb.Subjects[0].Name == t.Space.Owner && rb.Subjects[0].APIGroup == rbacv1.GroupName {
			continue
		}
		for _, subject := range rb.Subjects {
			if (subject.Kind == "User" && subject.Name == t.Username) || (subject.Kind == "Group" && (subject.Name == "system:authenticated" || subject.Name == "system:unauthenticated")) {
				obs.Warning = "发现额外 RoleBinding " + rb.Name + "，未擅自删除"
				break
			}
		}
	}
	for _, ap := range aps {
		if ap.GetName() == name || (ap.GetName() == "ns-owner-access-istio" && c.ownerPolicyMatches(ap, t.Space.Owner)) {
			continue
		}
		if c.policyMayGrant(ap, t.Username) {
			obs.Warning = "发现额外 AuthorizationPolicy " + ap.GetName() + "，未擅自删除"
		}
	}
	bindings, err := c.listClusterRoleBindings(ctx)
	if err != nil {
		return obs, err
	}
	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if (subject.Kind == "User" && subject.Name == t.Username) || (subject.Kind == "Group" && subject.Name == "system:authenticated") {
				grants, err := c.clusterBindingMayGrant(ctx, binding)
				if err != nil {
					return obs, err
				}
				if grants {
					obs.Warning = "发现额外 ClusterRoleBinding " + binding.Name + "，未擅自删除"
				}
				break
			}
		}
	}
	if t.Username == t.Space.Owner && obs.Warning == "" {
		obs.Warning = "该身份是固定基础设施 Owner；业务撤权不会移除其系统管理员访问"
		obs.WarningCode, obs.InfrastructureOwnerOnly = "INFRASTRUCTURE_OWNER_ACCESS_RETAINED", true
	}
	if obs.Warning != "" {
		obs.Level = "conflict"
	}
	return obs, nil
}

func (c *Client) clusterBindingMayGrant(ctx context.Context, binding rbacv1.ClusterRoleBinding) (bool, error) {
	role, err := c.kube.RbacV1().ClusterRoles().Get(ctx, binding.RoleRef.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	for _, rule := range role.Rules {
		for _, res := range rule.Resources {
			// 所有人默认具有的 API discovery 和自我权限查询不是空间工作负载授权。
			if res != "selfsubjectreviews" && res != "selfsubjectaccessreviews" && res != "selfsubjectrulesreviews" {
				return true, nil
			}
		}
	}
	return false, nil
}

func (c *Client) listClusterRoleBindings(ctx context.Context) ([]rbacv1.ClusterRoleBinding, error) {
	items := []rbacv1.ClusterRoleBinding{}
	next := ""
	for {
		page, err := c.kube.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{Limit: 200, Continue: next})
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		next = page.Continue
		if next == "" {
			return items, nil
		}
	}
}

func accessLabels(t AccessTarget) map[string]string {
	return map[string]string{labelManagedBy: managedByLoom, labelGroupID: fmt.Sprint(t.Space.ID), labelUserID: fmt.Sprint(t.UserID)}
}
func accessAnnotations(t AccessTarget, role string) map[string]string {
	return map[string]string{annotationUser: t.Username, annotationRole: role, annotationInstance: t.Space.InstanceID, annotationVersion: strconv.FormatUint(t.Version, 10)}
}
func verifyAccessIdentity(obj metav1.Object, t AccessTarget) error {
	labels := obj.GetLabels()
	if labels[labelGroupID] != fmt.Sprint(t.Space.ID) || labels[labelUserID] != fmt.Sprint(t.UserID) || obj.GetAnnotations()[annotationInstance] != t.Space.InstanceID {
		return conflict("对象 %s 不属于目标空间实例和用户，拒绝接管", obj.GetName())
	}
	return nil
}
func verifyPrevious(obj metav1.Object, kind string, previous []ObjectRef) error {
	for _, old := range previous {
		if old.Kind == kind && old.Name == obj.GetName() && old.UID != "" && old.UID != string(obj.GetUID()) {
			return conflict("%s %s 已被同名新 UID 对象替换", kind, obj.GetName())
		}
	}
	return nil
}
func (c *Client) listRoleBindings(ctx context.Context, ns string) ([]rbacv1.RoleBinding, error) {
	items := []rbacv1.RoleBinding{}
	next := ""
	for {
		page, err := c.kube.RbacV1().RoleBindings(ns).List(ctx, metav1.ListOptions{Limit: 200, Continue: next})
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		next = page.Continue
		if next == "" {
			return items, nil
		}
	}
}
func (c *Client) listPolicies(ctx context.Context, ns string) ([]unstructured.Unstructured, error) {
	items := []unstructured.Unstructured{}
	next := ""
	for {
		page, err := c.dyn.Resource(AuthzPolicyGVR).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 200, Continue: next})
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		next = page.GetContinue()
		if next == "" {
			return items, nil
		}
	}
}
func (c *Client) policyMayGrant(ap unstructured.Unstructured, user string) bool {
	action, _, _ := unstructured.NestedString(ap.Object, "spec", "action")
	if action != "" && action != "ALLOW" {
		return false
	}
	if ap.GetAnnotations()[annotationUser] == user {
		return true
	}
	rules, _, _ := unstructured.NestedSlice(ap.Object, "spec", "rules")
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		conditions, _ := rule["when"].([]any)
		hasIdentityConstraint := false
		for _, condition := range conditions {
			m, ok := condition.(map[string]any)
			if !ok {
				continue
			}
			if m["key"] != fmt.Sprintf("request.headers[%s]", c.cfg.UserIDHeader) {
				continue
			}
			hasIdentityConstraint = true
			values, _ := m["values"].([]any)
			for _, v := range values {
				if text, ok := v.(string); ok && (text == c.cfg.UserIDPrefix+user || strings.Contains(text, "*")) {
					return true
				}
			}
		}
		if !hasIdentityConstraint {
			return true
		}
	}
	return false
}
