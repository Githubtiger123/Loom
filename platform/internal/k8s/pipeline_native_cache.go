package k8s

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func NativeCacheConfigMapName(ref PipelineWorkflowRef) string { return "loom-cache-" + ref.InstanceID }

// One isolated cache projection per Workflow; Argo may mutate its hit times,
// but another user's ConfigMap or a different Workflow UID is never adopted.
func (c *Client) EnsureNativeCacheProjection(ctx context.Context, ref PipelineWorkflowRef, seeds map[string]NativeCacheSeed, limits map[string]int32, guard func() error) error {
	if ref.UID == "" || !validator.IsCanonicalUUID(ref.InstanceID) || guard == nil || len(seeds) > 128 {
		return &Permanent{Reason: "invalid native cache projection"}
	}
	name := NativeCacheConfigMapName(ref)
	live, err := c.kube.CoreV1().ConfigMaps(ref.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if err = guard(); err != nil {
			return err
		}
		data := map[string]string{}
		for key, limit := range limits {
			if !validator.IsDNS1123Subdomain(key) || limit < 1 || limit > 16 {
				return &Permanent{Reason: "invalid native iteration limit"}
			}
			data[key] = strconv.Itoa(int(limit))
		}
		for key, seed := range seeds {
			if !validator.IsSHA256(key) || seed.NodeID == "" {
				return &Permanent{Reason: "invalid native cache seed"}
			}
			raw, _ := json.Marshal(map[string]any{"nodeID": seed.NodeID, "outputs": seed.Outputs, "creationTimestamp": seed.CreatedAt, "lastHitTimestamp": seed.CreatedAt})
			data[key] = string(raw)
		}
		controller := true
		_, createErr := c.kube.CoreV1().ConfigMaps(ref.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"workflows.argoproj.io/configmap-type": "Cache", "app.kubernetes.io/managed-by": "loom"}, Annotations: map[string]string{"loom.ml/pipeline-plan-hash": ref.PlanHash}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "argoproj.io/v1alpha1", Kind: "Workflow", Name: ref.Name, UID: types.UID(ref.UID), Controller: &controller}}}, Data: data}, metav1.CreateOptions{})
		live, err = c.kube.CoreV1().ConfigMaps(ref.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil && createErr != nil {
			return createErr
		}
	}
	if err != nil {
		return err
	}
	owner := metav1.GetControllerOf(live)
	if owner == nil || owner.Kind != "Workflow" || owner.APIVersion != "argoproj.io/v1alpha1" || string(owner.UID) != ref.UID || owner.Name != ref.Name || live.Annotations["loom.ml/pipeline-plan-hash"] != ref.PlanHash || live.Labels["workflows.argoproj.io/configmap-type"] != "Cache" {
		return &Permanent{Reason: "native cache projection ownership conflict"}
	}
	for key, limit := range limits {
		if live.Data[key] != strconv.Itoa(int(limit)) {
			return &Permanent{Reason: "native iteration limit conflict"}
		}
	}
	return nil
}
