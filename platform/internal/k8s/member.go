package k8s

import (
	"context"
	"errors"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// 用户和角色从注解读取，不能从摘要名称反解。
	// 注解只描述授权对象，不得用于反向生成数据库里的业务授权。
	annotationUser        = "user"
	annotationRole        = "role"
	annotationClusterRole = "platform.example.com/cluster-role"
	labelManagedBy        = "app.kubernetes.io/managed-by"
	managedByLoom         = "loom"
)

// BindingName 对完整身份与角色组合取摘要；名称为 62 字符，符合 DNS label。
// 不再复用 kfam 的有损名称，历史绑定需要显式核验迁移，不能自动接管。
func BindingName(user, clusterRole string) string {
	return "loom-user-" + identityDigest(user+"\x00"+clusterRole)
}

// EnsureMemberBinding 给成员建"两条同名对象"：
//
//	RoleBinding          → kubectl / client-go 能不能操作对象
//	AuthorizationPolicy  → 浏览器能不能打开页面
//
// 缺前者：容器里没权限；缺后者：页面 403。两条必须一起存在。
func (c *Client) EnsureMemberBinding(ctx context.Context, ns, user, clusterRole string) error {
	name := BindingName(user, clusterRole)

	created, err := c.upsertRoleBinding(ctx, ns, name, user, clusterRole)
	if err != nil {
		return err
	}

	if err := c.upsertAuthzPolicy(ctx, ns, name, user, clusterRole); err != nil {
		// 回滚：只删"本次新建"的 RoleBinding，避免把已有授权误删
		if created != nil {
			rollbackErr := c.kube.RbacV1().RoleBindings(ns).Delete(ctx, name, guardedDelete(created))
			if rollbackErr != nil && !apierrors.IsNotFound(rollbackErr) {
				return errors.Join(fmt.Errorf("upsert authorizationpolicy %s: %w", name, err),
					fmt.Errorf("rollback rolebinding %s: %w", name, rollbackErr))
			}
		}
		return fmt.Errorf("upsert authorizationpolicy %s: %w", name, err)
	}
	return nil
}

// DeleteMemberBinding 同时删两条对象，名字相同。
func (c *Client) DeleteMemberBinding(ctx context.Context, ns, user, clusterRole string) error {
	name := BindingName(user, clusterRole)
	// 先核验两条对象，任一归属不符都不执行删除。
	rb, err := c.kube.RbacV1().RoleBindings(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get rolebinding %s: %w", name, err)
	}
	if err == nil {
		if err := verifyRoleBinding(rb, user, clusterRole); err != nil {
			return err
		}
	} else {
		rb = nil
	}
	ri := c.dyn.Resource(AuthzPolicyGVR).Namespace(ns)
	ap, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get authorizationpolicy %s: %w", name, err)
	}
	if err == nil {
		if err := verifyAuthzPolicy(ap, user, clusterRole); err != nil {
			return err
		}
	} else {
		ap = nil
	}
	if rb != nil {
		if err := c.kube.RbacV1().RoleBindings(ns).Delete(ctx, name, guardedDelete(rb)); err != nil &&
			!apierrors.IsNotFound(err) {
			return fmt.Errorf("delete rolebinding %s: %w", name, err)
		}
	}
	if ap != nil {
		if err := ri.Delete(ctx, name, guardedDelete(ap)); err != nil &&
			!apierrors.IsNotFound(err) {
			return fmt.Errorf("delete authorizationpolicy %s: %w", name, err)
		}
	}
	return nil
}

// 仅新建时返回对象，供失败回滚使用原始 UID/resourceVersion 做条件删除。
func (c *Client) upsertRoleBinding(ctx context.Context, ns, name, user, clusterRole string) (*rbacv1.RoleBinding, error) {
	want := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      map[string]string{labelManagedBy: managedByLoom},
			Annotations: map[string]string{annotationUser: user, annotationRole: clusterRole},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRole,
		},
		Subjects: []rbacv1.Subject{{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "User",
			Name:     user,
		}},
	}

	// 角色参与名称摘要；这里只处理同身份、同角色的幂等重放。
	cur, err := c.kube.RbacV1().RoleBindings(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		created, err := c.kube.RbacV1().RoleBindings(ns).Create(ctx, want, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("create rolebinding %s: %w", name, err)
		}
		return created, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rolebinding %s: %w", name, err)
	}

	// 已存在但归属或主体不符时拒绝接管；匹配时无需重写任何元数据。
	if err := verifyRoleBinding(cur, user, clusterRole); err != nil {
		return nil, err
	}
	return nil, nil
}

func (c *Client) upsertAuthzPolicy(ctx context.Context, ns, name, user, clusterRole string) error {
	want := c.buildAuthzPolicy(ns, name, user, clusterRole)
	ri := c.dyn.Resource(AuthzPolicyGVR).Namespace(ns)

	cur, err := ri.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = ri.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}

	if err := verifyAuthzPolicy(cur, user, clusterRole); err != nil {
		return err
	}
	// 从已有对象复制，保留 ownerReferences、标签和第三方注解。
	updated := cur.DeepCopy()
	updated.Object["spec"] = want.Object["spec"]
	_, err = ri.Update(ctx, updated, metav1.UpdateOptions{})
	return err
}

func verifyRoleBinding(rb *rbacv1.RoleBinding, user, clusterRole string) error {
	if rb.Labels[labelManagedBy] != managedByLoom || rb.Annotations[annotationUser] != user ||
		rb.Annotations[annotationRole] != clusterRole ||
		rb.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: clusterRole}) ||
		len(rb.Subjects) != 1 || rb.Subjects[0] != (rbacv1.Subject{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}) {
		return fmt.Errorf("rolebinding %s/%s ownership or subject mismatch", rb.Namespace, rb.Name)
	}
	return nil
}

func verifyAuthzPolicy(ap *unstructured.Unstructured, user, clusterRole string) error {
	ann := ap.GetAnnotations()
	if ap.GetLabels()[labelManagedBy] != managedByLoom || ann[annotationUser] != user ||
		ann[annotationRole] != "member" || ann[annotationClusterRole] != clusterRole {
		return fmt.Errorf("authorizationpolicy %s/%s ownership mismatch", ap.GetNamespace(), ap.GetName())
	}
	return nil
}

func guardedDelete(obj metav1.Object) metav1.DeleteOptions {
	uid, rv := obj.GetUID(), obj.GetResourceVersion()
	return metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}
}

func (c *Client) buildAuthzPolicy(ns, name, user, clusterRole string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "security.istio.io/v1beta1",
		"kind":       "AuthorizationPolicy",
		"metadata": map[string]any{
			"name":        name,
			"namespace":   ns,
			"labels":      map[string]any{labelManagedBy: managedByLoom},
			"annotations": map[string]any{annotationUser: user, annotationRole: "member", annotationClusterRole: clusterRole},
		},
		"spec": map[string]any{
			// action=ALLOW 且 selector 为空：作用于本 ns 所有工作负载。
			// 注意：namespace 里一旦存在 ALLOW 策略，未匹配的请求一律拒绝，
			// 所以每个成员都必须有一条，漏一条就是 403。
			"action": "ALLOW",
			"rules": []any{map[string]any{
				"when": []any{map[string]any{
					// 头名与前缀必须与 profile-controller 一致，
					// 否则我们建的 AP 与它建的 AP 互相不认，成员一律 403
					"key":    fmt.Sprintf("request.headers[%s]", c.cfg.UserIDHeader),
					"values": []any{c.cfg.UserIDPrefix + user},
				}},
				"from": []any{map[string]any{
					"source": map[string]any{
						"principals": []any{c.cfg.IngressPrincipal, c.cfg.KFPUIPrincipal},
					},
				}},
			}},
		},
	}}
}
