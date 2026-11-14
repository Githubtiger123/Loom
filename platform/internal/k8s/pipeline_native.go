package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

var WorkflowGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "workflows"}

const (
	labelPipelineRunID             = "loom.ml/pipeline-run-id"
	annotationPipelineNamespaceUID = "loom.ml/pipeline-namespace-uid"
	annotationPipelinePlanHash     = "loom.ml/pipeline-plan-hash"
	MaxNativeWorkflowNodes         = 4000
)

// Informer callbacks enqueue keys only. The installed official runtime backend
// must recheck durable ownership and live UID before interpreting observations.
func (c *Client) NewPipelineWorkflowInformer() cache.SharedIndexInformer {
	return c.NewResourceInformer(WorkflowGVR, labelManagedBy+"="+managedByLoom)
}

type PipelineWorkflowRef struct {
	GroupID, RunID                                           uint64
	InstanceID, Namespace, NamespaceUID, Name, UID, PlanHash string
}

func (c *Client) ReadPipelineWorkflow(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	return c.dyn.Resource(WorkflowGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// Matching transport identity does not validate runtime semantics or outputs.
func PipelineWorkflowMatches(obj *unstructured.Unstructured, ref PipelineWorkflowRef) bool {
	if obj == nil || ref.UID == "" || ref.PlanHash == "" || ref.NamespaceUID == "" ||
		obj.GetAPIVersion() != "argoproj.io/v1alpha1" || obj.GetKind() != "Workflow" {
		return false
	}
	return obj.GetNamespace() == ref.Namespace && obj.GetName() == ref.Name && string(obj.GetUID()) == ref.UID &&
		obj.GetLabels()[labelManagedBy] == managedByLoom &&
		obj.GetLabels()[labelGroupID] == fmt.Sprint(ref.GroupID) &&
		obj.GetLabels()[labelPipelineRunID] == fmt.Sprint(ref.RunID) &&
		obj.GetAnnotations()[annotationInstance] == ref.InstanceID &&
		obj.GetAnnotations()[annotationPipelineNamespaceUID] == ref.NamespaceUID &&
		obj.GetAnnotations()[annotationPipelinePlanHash] == ref.PlanHash
}
