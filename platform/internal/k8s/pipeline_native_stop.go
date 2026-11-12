package k8s

import (
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const AnnotationPipelineStopToken = "loom.ml/pipeline-stop-token"

// RequestNativePipelineStop never deletes Pods/CRs or changes other spec fields.
// UID+resourceVersion tests make a retry safe after an uncertain PATCH response.
func (c *Client) RequestNativePipelineStop(ctx context.Context, ref PipelineWorkflowRef, specHash string, guard func() error) error {
	if !validator.IsCanonicalUUID(ref.StopToken) || ref.UID == "" || guard == nil {
		return &Permanent{Reason: "native stop requires durable authorization"}
	}
	obj, err := c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return err
	}
	if err = ValidateNativePipelineObservedSpec(obj, ref, specHash); err != nil {
		return err
	}
	obs, err := ObserveNativePipelineWorkflow(obj, ref)
	if err != nil {
		return err
	}
	if obs.Phase == "SUCCEEDED" || obs.Phase == "FAILED" || obs.Phase == "STOPPED" {
		return nil
	}
	shutdown, _, _ := unstructured.NestedString(obj.Object, "spec", "shutdown")
	if shutdown == "Stop" {
		return nil
	}
	if err = guard(); err != nil {
		return err
	}
	patch, _ := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": ref.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": obj.GetResourceVersion()},
		{"op": "add", "path": "/metadata/annotations/loom.ml~1pipeline-stop-token", "value": ref.StopToken},
		{"op": "add", "path": "/spec/shutdown", "value": "Stop"},
		{"op": "add", "path": "/spec/suspend", "value": false},
	})
	_, err = c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Patch(ctx, ref.Name, types.JSONPatchType, patch, metav1.PatchOptions{})
	return err
}
