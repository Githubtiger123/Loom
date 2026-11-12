package k8s

import (
	"context"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func kserveControllerOwner(meta metav1.Object, kind, uid string) bool {
	for _, owner := range meta.GetOwnerReferences() {
		if owner.Kind == kind && string(owner.UID) == uid && owner.Controller != nil && *owner.Controller {
			return true
		}
	}
	return false
}

func (c *Client) kserveDeployment(ctx context.Context, ref KServeRef) (*appsv1.Deployment, error) {
	d, err := c.kube.AppsV1().Deployments(ref.Namespace).Get(ctx, ref.Name+"-predictor", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !kserveControllerOwner(d, "InferenceService", ref.UID) {
		return nil, conflict("KSERVE_DEPLOYMENT_IDENTITY_CONFLICT")
	}
	return d, nil
}

func kserveRolloutMatches(d *appsv1.Deployment, ref KServeRef, hash string) bool {
	for _, annotations := range []map[string]string{d.Annotations, d.Spec.Template.Annotations} {
		if annotations[annotationInstance] != ref.InstanceID || annotations[annotationKServeRevision] != strconv.FormatUint(ref.Revision, 10) || annotations[annotationKServeSpecHash] != hash {
			return false
		}
	}
	return true
}

// KServe Standard uses the predictor Deployment's observedGeneration in ISVC
// status. Compare it to that Deployment, then fence by the propagated Loom
// revision/hash and complete rollout; do not compare different object counters.
func (c *Client) observeKServeStandard(ctx context.Context, n *unstructured.Unstructured) (*KServeObservation, error) {
	obs := ObserveKServeObject(n)
	if n.GetDeletionTimestamp() != nil {
		return obs, nil
	}
	ready := false
	conditions, _, _ := unstructured.NestedSlice(n.Object, "status", "conditions")
	for _, item := range conditions {
		if condition, ok := item.(map[string]any); ok && condition["type"] == "Ready" && condition["status"] == "True" {
			ready = true
		}
	}
	ref := KServeRef{ID: obs.ID, GroupID: obs.GroupID, Revision: obs.Revision, InstanceID: obs.InstanceID, Namespace: obs.Namespace, Name: obs.Name, UID: obs.UID}
	d, err := c.kserveDeployment(ctx, ref)
	if err != nil {
		return nil, err
	}
	if d == nil || d.DeletionTimestamp != nil || !kserveRolloutMatches(d, ref, n.GetAnnotations()[annotationKServeSpecHash]) {
		// A failed status left by the old revision cannot fail a new deployment
		// before the controller has even generated its predictor workload.
		obs.State, obs.Ready = "PENDING", false
		if obs.Reason == "" {
			obs.Reason = "等待 predictor Deployment 应用当前发布修订"
		}
		return obs, nil
	}
	if d.Status.ObservedGeneration < d.Generation {
		obs.State, obs.Ready, obs.Reason = "PENDING", false, "等待 Deployment controller 观测当前滚动发布"
		return obs, nil
	}
	if obs.State == "FAILED" || !ready {
		return obs, nil
	}
	for _, condition := range d.Status.Conditions {
		if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionFalse {
			obs.State, obs.Reason = "FAILED", boundedKServeText(condition.Reason+": "+condition.Message, 1024)
			return obs, nil
		}
	}
	replicas := int32(1)
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	if d.Status.ObservedGeneration < d.Generation || replicas < 1 || d.Status.UpdatedReplicas != replicas || d.Status.Replicas != replicas || d.Status.AvailableReplicas < replicas {
		obs.Reason = fmt.Sprintf("等待 predictor 滚动发布：updated=%d available=%d desired=%d", d.Status.UpdatedReplicas, d.Status.AvailableReplicas, replicas)
		return obs, nil
	}
	obs.Ready, obs.State, obs.Reason = true, "SERVING", ""
	return obs, nil
}

// Diagnostics accepts only the official ISVC -> Deployment -> ReplicaSet -> Pod
// controller-owner chain, never labels alone or a user-supplied namespace.
func (c *Client) kservePods(ctx context.Context, ref KServeRef) ([]corev1.Pod, *appsv1.Deployment, error) {
	n, err := c.GetKServe(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if n == nil || string(n.GetUID()) != ref.UID {
		return nil, nil, conflict("KSERVE_RESOURCE_MISSING")
	}
	d, err := c.kserveDeployment(ctx, ref)
	if err != nil || d == nil {
		return []corev1.Pod{}, d, err
	}
	selector := "app=isvc." + d.Name
	sets, err := c.kube.AppsV1().ReplicaSets(ref.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 300})
	if err != nil {
		return nil, nil, err
	}
	if sets.Continue != "" {
		return nil, nil, conflict("KSERVE_REPLICASET_LIMIT")
	}
	owners := map[string]bool{}
	for i := range sets.Items {
		if kserveControllerOwner(&sets.Items[i], "Deployment", string(d.UID)) {
			owners[string(sets.Items[i].UID)] = true
		}
	}
	list, err := c.kube.CoreV1().Pods(ref.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 300})
	if err != nil {
		return nil, nil, err
	}
	if list.Continue != "" {
		return nil, nil, conflict("KSERVE_POD_LIMIT")
	}
	pods := []corev1.Pod{}
	for _, pod := range list.Items {
		for uid := range owners {
			if kserveControllerOwner(&pod, "ReplicaSet", uid) {
				pods = append(pods, pod)
				break
			}
		}
	}
	return pods, d, nil
}
