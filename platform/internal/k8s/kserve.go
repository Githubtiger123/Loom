package k8s

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/dto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

var KServeGVR = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1beta1", Resource: "inferenceservices"}

const (
	labelKServeID            = "loom.ml/kserve-service-id"
	annotationKServeRevision = "loom.ml/kserve-revision"
	annotationKServeSpecHash = "loom.ml/kserve-spec-hash"
	annotationDeploymentMode = "serving.kserve.io/deploymentMode"
)

type KServeRef struct {
	ID, GroupID, Revision            uint64
	InstanceID, Namespace, Name, UID string
}

type KServeObservation struct {
	ID                 uint64 `json:"id"`
	GroupID            uint64 `json:"groupId"`
	InstanceID         string `json:"instanceId"`
	Namespace          string `json:"namespace"`
	Name               string `json:"name"`
	UID                string `json:"uid"`
	Revision           uint64 `json:"revision"`
	Generation         int64  `json:"generation"`
	ObservedGeneration int64  `json:"observedGeneration"`
	ResourceVersion    string `json:"resourceVersion"`
	State              string `json:"state"`
	Ready              bool   `json:"ready"`
	URL                string `json:"url,omitempty"`
	Address            string `json:"address,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Absent             bool   `json:"absent"`
}

func kserveMetadata(ref KServeRef, hash string) (map[string]string, map[string]string) {
	return map[string]string{
			labelManagedBy: managedByLoom,
			labelGroupID:   strconv.FormatUint(ref.GroupID, 10),
			labelKServeID:  strconv.FormatUint(ref.ID, 10),
		}, map[string]string{
			annotationInstance:       ref.InstanceID,
			annotationKServeRevision: strconv.FormatUint(ref.Revision, 10),
			annotationKServeSpecHash: hash,
			annotationDeploymentMode: "Standard",
		}
}

func kserveMatches(n *unstructured.Unstructured, ref KServeRef, requireUID bool) bool {
	uid := string(n.GetUID())
	return uid != "" && (!requireUID || ref.UID == uid) &&
		n.GetLabels()[labelManagedBy] == managedByLoom &&
		n.GetLabels()[labelGroupID] == strconv.FormatUint(ref.GroupID, 10) &&
		n.GetLabels()[labelKServeID] == strconv.FormatUint(ref.ID, 10) &&
		n.GetAnnotations()[annotationInstance] == ref.InstanceID
}

func kserveResources(in dto.MLResources) map[string]any {
	values := map[string]any{"cpu": in.CPU, "memory": in.Memory}
	if in.GPU > 0 {
		values["nvidia.com/gpu"] = strconv.Itoa(in.GPU)
	}
	return map[string]any{"requests": values, "limits": values}
}

func BuildKServe(ref KServeRef, in dto.KServeSpec, serviceAccount, specHash string) *unstructured.Unstructured {
	labels, annotations := kserveMetadata(ref, specHash)
	predictor := map[string]any{
		"serviceAccountName": serviceAccount,
		"minReplicas":        int64(in.MinReplicas),
		"maxReplicas":        int64(in.MaxReplicas),
		// Official KServe propagates component metadata to Deployment/Pod.
		// Fence rollout checks to the immutable Loom deployment command.
		"annotations": map[string]any{annotationInstance: ref.InstanceID, annotationKServeRevision: strconv.FormatUint(ref.Revision, 10), annotationKServeSpecHash: specHash},
	}
	if in.DeploymentType == "MODEL" {
		format := map[string]any{"name": in.Model.ModelFormat}
		if in.Model.ModelFormatVersion != "" {
			format["version"] = in.Model.ModelFormatVersion
		}
		model := map[string]any{
			"modelFormat":     format,
			"protocolVersion": in.Protocol,
			"storageUri":      in.Model.StorageURI,
			"resources":       kserveResources(in.Resources),
		}
		if in.Model.Runtime != "" {
			model["runtime"] = in.Model.Runtime
		}
		predictor["model"] = model
	} else {
		container := map[string]any{
			"name": "kserve-container", "image": in.Custom.Image,
			"env":             []any{map[string]any{"name": "PROTOCOL", "value": in.Protocol}},
			"ports":           []any{map[string]any{"name": "http1", "containerPort": int64(in.Custom.Port), "protocol": "TCP"}},
			"readinessProbe":  map[string]any{"httpGet": map[string]any{"path": in.Custom.HealthPath, "port": int64(in.Custom.Port)}},
			"resources":       kserveResources(in.Resources),
			"securityContext": map[string]any{"allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []any{"ALL"}}},
		}
		if len(in.Custom.Command) > 0 {
			container["command"] = stringSliceAny(in.Custom.Command)
		}
		if len(in.Custom.Args) > 0 {
			container["args"] = stringSliceAny(in.Custom.Args)
		}
		predictor["containers"] = []any{container}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "serving.kserve.io/v1beta1", "kind": "InferenceService",
		"metadata": map[string]any{"name": ref.Name, "namespace": ref.Namespace, "labels": labels, "annotations": annotations},
		"spec":     map[string]any{"predictor": predictor},
	}}
}

func stringSliceAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func (c *Client) GetKServe(ctx context.Context, ref KServeRef) (*unstructured.Unstructured, error) {
	n, err := c.dyn.Resource(KServeGVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !kserveMatches(n, ref, ref.UID != "") {
		return nil, conflict("KSERVE_IDENTITY_CONFLICT: 同名 InferenceService 不属于目标 Loom 服务")
	}
	revision, err := strconv.ParseUint(n.GetAnnotations()[annotationKServeRevision], 10, 64)
	if err != nil || revision > ref.Revision {
		return nil, conflict("KSERVE_REVISION_CONFLICT: 集群对象版本超出当前业务目标")
	}
	return n, nil
}

// ReconcileKServe 仅更新 Loom 管理的结构化 spec，使用 resourceVersion 防止覆盖并发写入。
func (c *Client) ReconcileKServe(ctx context.Context, ref KServeRef, in dto.KServeSpec, serviceAccount, specHash string, guard func() error) (*KServeObservation, error) {
	current, err := c.GetKServe(ctx, ref)
	if err != nil {
		return nil, err
	}
	want := BuildKServe(ref, in, serviceAccount, specHash)
	if current == nil {
		if ref.UID != "" {
			return nil, conflict("KSERVE_RESOURCE_MISSING: 已绑定 UID 的 InferenceService 消失，禁止自动重建")
		}
		if err := guard(); err != nil {
			return nil, err
		}
		current, err = c.dyn.Resource(KServeGVR).Namespace(ref.Namespace).Create(ctx, want, metav1.CreateOptions{})
	} else {
		if current.GetDeletionTimestamp() != nil {
			return c.observeKServeStandard(ctx, current)
		}
		annotations := current.GetAnnotations()
		if annotations[annotationKServeRevision] == strconv.FormatUint(ref.Revision, 10) &&
			annotations[annotationKServeSpecHash] == specHash && annotations[annotationDeploymentMode] == "Standard" &&
			nestedString(current.Object, "spec", "predictor", "annotations", annotationInstance) == ref.InstanceID &&
			nestedString(current.Object, "spec", "predictor", "annotations", annotationKServeRevision) == strconv.FormatUint(ref.Revision, 10) &&
			nestedString(current.Object, "spec", "predictor", "annotations", annotationKServeSpecHash) == specHash {
			return c.observeKServeStandard(ctx, current)
		}
		if err := guard(); err != nil {
			return nil, err
		}
		// 保留 controller/webhook 管理的 finalizer 和非 Loom metadata；只替换受控 spec
		// 与本模块拥有的标签/注解，避免完整对象 Update 意外清除控制器状态。
		update := current.DeepCopy()
		update.Object["spec"] = want.Object["spec"]
		labels, annotations := update.GetLabels(), update.GetAnnotations()
		for key, value := range want.GetLabels() {
			labels[key] = value
		}
		for key, value := range want.GetAnnotations() {
			annotations[key] = value
		}
		update.SetLabels(labels)
		update.SetAnnotations(annotations)
		current, err = c.dyn.Resource(KServeGVR).Namespace(ref.Namespace).Update(ctx, update, metav1.UpdateOptions{})
	}
	if err != nil {
		return nil, err
	}
	return c.observeKServeStandard(ctx, current)
}

func (c *Client) DeleteKServe(ctx context.Context, ref KServeRef, guard func() error) (*KServeObservation, error) {
	n, err := c.GetKServe(ctx, ref)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return &KServeObservation{ID: ref.ID, GroupID: ref.GroupID, InstanceID: ref.InstanceID, Namespace: ref.Namespace, Name: ref.Name, UID: ref.UID, Revision: ref.Revision, State: "STOPPED", Absent: true}, nil
	}
	if n.GetDeletionTimestamp() == nil {
		if err := guard(); err != nil {
			return nil, err
		}
		uid := n.GetUID()
		propagation := metav1.DeletePropagationForeground
		if err := c.dyn.Resource(KServeGVR).Namespace(ref.Namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}, PropagationPolicy: &propagation}); err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
	}
	obs := ObserveKServeObject(n)
	obs.State, obs.Ready, obs.Reason = "PENDING", false, "等待 KServe Controller 删除资源"
	return obs, nil
}

func (c *Client) ObserveKServe(ctx context.Context, ref KServeRef) (*KServeObservation, error) {
	n, err := c.GetKServe(ctx, ref)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return &KServeObservation{ID: ref.ID, GroupID: ref.GroupID, InstanceID: ref.InstanceID, Namespace: ref.Namespace, Name: ref.Name, UID: ref.UID, Revision: ref.Revision, State: "STOPPED", Absent: true}, nil
	}
	return c.observeKServeStandard(ctx, n)
}

func (c *Client) WatchKServe(ctx context.Context, resourceVersion string) (watch.Interface, error) {
	return c.dyn.Resource(KServeGVR).Namespace("").Watch(ctx, metav1.ListOptions{
		LabelSelector: labelManagedBy + "=" + managedByLoom, ResourceVersion: resourceVersion, AllowWatchBookmarks: true,
	})
}

func ObserveKServeObject(n *unstructured.Unstructured) *KServeObservation {
	obs := &KServeObservation{Namespace: n.GetNamespace(), Name: n.GetName(), UID: string(n.GetUID()), Generation: n.GetGeneration(),
		ResourceVersion: n.GetResourceVersion(), State: "PENDING", InstanceID: n.GetAnnotations()[annotationInstance]}
	obs.ID, _ = strconv.ParseUint(n.GetLabels()[labelKServeID], 10, 64)
	obs.GroupID, _ = strconv.ParseUint(n.GetLabels()[labelGroupID], 10, 64)
	obs.Revision, _ = strconv.ParseUint(n.GetAnnotations()[annotationKServeRevision], 10, 64)
	obs.ObservedGeneration, _, _ = unstructured.NestedInt64(n.Object, "status", "observedGeneration")
	obs.URL = safeKServeURL(nestedString(n.Object, "status", "url"))
	obs.Address = safeKServeURL(nestedString(n.Object, "status", "address", "url"))
	if n.GetDeletionTimestamp() != nil {
		obs.Reason = "等待 KServe Controller 删除资源"
		return obs
	}
	conditions, _, _ := unstructured.NestedSlice(n.Object, "status", "conditions")
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok || fmt.Sprint(condition["type"]) != "Ready" {
			continue
		}
		status, reason, message := stringValue(condition["status"]), stringValue(condition["reason"]), stringValue(condition["message"])
		obs.Reason = boundedKServeText(strings.TrimSpace(reason+": "+message), 1024)
		if status == "True" {
			// In Standard mode KServe copies Deployment.observedGeneration into
			// ISVC status: it is NOT an ISVC generation acknowledgement. A live
			// owned Deployment rollout check is required before SERVING.
			obs.Reason = "等待核对 predictor Deployment 的当前修订与滚动发布"
		} else if status == "False" && kserveFailureReason(reason) {
			obs.State = "FAILED"
		}
		break
	}
	transition := nestedString(n.Object, "status", "modelStatus", "transitionStatus")
	if transition == "InvalidSpec" || transition == "BlockedByFailedLoad" {
		obs.State, obs.Ready = "FAILED", false
		obs.Reason = boundedKServeText(nestedString(n.Object, "status", "modelStatus", "lastFailureInfo", "reason")+": "+nestedString(n.Object, "status", "modelStatus", "lastFailureInfo", "message"), 1024)
	}
	return obs
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func nestedString(object map[string]any, fields ...string) string {
	value, _, _ := unstructured.NestedString(object, fields...)
	return value
}

func safeKServeURL(value string) string {
	if value == "" || len(value) > 2048 {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func kserveFailureReason(reason string) bool {
	lower := strings.ToLower(reason)
	return strings.Contains(lower, "failed") || strings.Contains(lower, "invalid") || strings.Contains(lower, "error")
}

var kserveMessageURL = regexp.MustCompile(`(?i)(?:https?|s3|gs)://[^\s"<>]+`)

func boundedKServeText(value string, max int) string {
	// Condition/event text can contain signed download URLs. Keep them out of
	// public metadata projections; explicit authorized container logs stay raw.
	value = kserveMessageURL.ReplaceAllString(value, "[URL omitted]")
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")
	runes := []rune(value)
	if len(runes) > max {
		return string(runes[:max])
	}
	return value
}
