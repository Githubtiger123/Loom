package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	wfapi "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// Namespace and SA UIDs are observed separately from names. Recreating either
// identity cannot inherit permission to resume an existing frozen execution.
func (c *Client) PipelineRuntimeAccountUID(ctx context.Context, namespace, namespaceUID, account string) (string, error) {
	ns, err := c.kube.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if string(ns.UID) != namespaceUID || ns.DeletionTimestamp != nil {
		return "", fmt.Errorf("Pipeline namespace identity changed")
	}
	sa, err := c.kube.CoreV1().ServiceAccounts(namespace).Get(ctx, account, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if sa.UID == "" || sa.DeletionTimestamp != nil {
		return "", fmt.Errorf("Pipeline service account is not active")
	}
	return string(sa.UID), nil
}

func typedPipelineWorkflow(value *unstructured.Unstructured) (*wfapi.Workflow, error) {
	if value == nil {
		return nil, fmt.Errorf("Workflow is required")
	}
	workflow := &wfapi.Workflow{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(value.Object, workflow); err != nil {
		return nil, err
	}
	return workflow, nil
}

// EnsureOfficialPipelineSuspended is not an authorization API. The operation
// adapter must hold a live lease/fence and persist the immutable plan first.
// Once a UID is known, a missing Workflow is NEVER recreated.
func (c *Client) EnsureOfficialPipelineSuspended(ctx context.Context, plan *wfapi.Workflow, ref PipelineWorkflowRef, accountUID string) (*unstructured.Unstructured, error) {
	if plan == nil || plan.Name != ref.Name || plan.Namespace != ref.Namespace || ref.InstanceID == "" || accountUID == "" {
		return nil, fmt.Errorf("frozen Pipeline identity required")
	}
	if err := kfpengine.VerifyFrozenWorkflow(plan, plan); err != nil {
		return nil, err
	}
	uid, err := c.PipelineRuntimeAccountUID(ctx, ref.Namespace, ref.NamespaceUID, plan.Spec.ServiceAccountName)
	if err != nil {
		return nil, err
	}
	if uid != accountUID {
		return nil, fmt.Errorf("Pipeline service account UID changed")
	}
	workflow, err := c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if apierrors.IsNotFound(err) && ref.UID == "" {
		object, e := runtime.DefaultUnstructuredConverter.ToUnstructured(plan)
		if e != nil {
			return nil, e
		}
		workflow, err = c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Create(ctx, &unstructured.Unstructured{Object: object}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			workflow, err = c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
		}
	}
	if err != nil {
		return nil, err
	}
	typed, err := typedPipelineWorkflow(workflow)
	if err != nil {
		return nil, err
	}
	if ref.UID == "" {
		if typed.Spec.Suspend == nil || !*typed.Spec.Suspend || typed.Spec.Shutdown != "" || len(typed.Status.Nodes) != 0 {
			return nil, fmt.Errorf("unbound Workflow already started")
		}
		ref.UID = string(typed.UID)
	}
	if !PipelineWorkflowMatches(workflow, ref) || workflow.GetDeletionTimestamp() != nil {
		return nil, fmt.Errorf("Pipeline Workflow UID or ownership changed")
	}
	if err := kfpengine.VerifyFrozenWorkflow(plan, typed); err != nil {
		return nil, err
	}
	return workflow, nil
}

// ControlOfficialPipeline performs a single UID+resourceVersion-guarded patch.
// Resume is allowed only after the caller durably bound ref.UID. Stop requests
// Argo's graceful shutdown; STOPPED still requires a later terminal observation.
func (c *Client) ControlOfficialPipeline(ctx context.Context, plan *wfapi.Workflow, ref PipelineWorkflowRef, accountUID string, resume bool) (*unstructured.Unstructured, error) {
	if ref.UID == "" {
		return nil, fmt.Errorf("Workflow UID must be durably bound before control")
	}
	workflow, err := c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return nil, err
	}
	if !PipelineWorkflowMatches(workflow, ref) || workflow.GetDeletionTimestamp() != nil {
		return nil, fmt.Errorf("Workflow control identity changed")
	}
	typed, err := typedPipelineWorkflow(workflow)
	if err != nil {
		return nil, err
	}
	if err := kfpengine.VerifyFrozenWorkflow(plan, typed); err != nil {
		return nil, err
	}
	if resume {
		uid, err := c.PipelineRuntimeAccountUID(ctx, ref.Namespace, ref.NamespaceUID, plan.Spec.ServiceAccountName)
		if err != nil {
			return nil, err
		}
		if uid != accountUID || typed.Spec.Shutdown != "" {
			return nil, fmt.Errorf("Workflow cannot be resumed with changed identity or shutdown")
		}
		if typed.Spec.Suspend == nil || !*typed.Spec.Suspend {
			return workflow, nil
		}
	} else if typed.Status.Phase == wfapi.WorkflowSucceeded || typed.Status.Phase == wfapi.WorkflowFailed || typed.Status.Phase == wfapi.WorkflowError {
		return workflow, nil
	}
	patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": ref.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": workflow.GetResourceVersion()}}
	if resume {
		patch = append(patch, map[string]any{"op": "add", "path": "/spec/suspend", "value": false})
	} else {
		patch = append(patch, map[string]any{"op": "add", "path": "/spec/shutdown", "value": "Stop"}, map[string]any{"op": "add", "path": "/spec/suspend", "value": false})
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	return c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Patch(ctx, ref.Name, types.JSONPatchType, raw, metav1.PatchOptions{})
}

// DeleteOfficialPipeline is called only after the service has durably archived
// final evidence. A same-name replacement must never inherit a deletion intent.
func (c *Client) DeleteOfficialPipeline(ctx context.Context, plan *wfapi.Workflow, ref PipelineWorkflowRef) error {
	if ref.UID == "" {
		return fmt.Errorf("bound Workflow UID is required")
	}
	value, err := c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !PipelineWorkflowMatches(value, ref) {
		return fmt.Errorf("Workflow deletion identity changed")
	}
	workflow, err := typedPipelineWorkflow(value)
	if err != nil {
		return err
	}
	if err = kfpengine.VerifyFrozenWorkflow(plan, workflow); err != nil {
		return err
	}
	if workflow.Status.Phase != wfapi.WorkflowSucceeded && workflow.Status.Phase != wfapi.WorkflowFailed && workflow.Status.Phase != wfapi.WorkflowError {
		return fmt.Errorf("Workflow must terminate before deletion")
	}
	uid, version := types.UID(ref.UID), value.GetResourceVersion()
	propagation := metav1.DeletePropagationForeground
	return c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version}, PropagationPolicy: &propagation,
	})
}
