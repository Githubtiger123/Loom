package k8s

import (
	"context"
	"fmt"

	wfapi "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VerifyOfficialPipelineRetry authorizes a newer physical Pod for the SAME Argo
// Retry node. A same-name Pod, unrelated sibling task or merely failed metadata
// cannot transfer ownership. Kubernetes reads occur before the SQL transaction.
func (c *Client) VerifyOfficialPipelineRetry(ctx context.Context, scope runtimeapi.Scope, oldPod, oldUID, oldNode string) error {
	if scope.Role != "LAUNCHER" || oldPod == "" || oldUID == "" || oldNode == "" || oldUID == scope.PodUID || oldNode == scope.NodeID {
		return fmt.Errorf("retry identity is incomplete")
	}
	value, err := c.ReadPipelineWorkflow(ctx, scope.Namespace, scope.WorkflowName)
	if err != nil {
		return err
	}
	if string(value.GetUID()) != scope.WorkflowUID || value.GetDeletionTimestamp() != nil {
		return fmt.Errorf("retry Workflow identity changed")
	}
	workflow, err := typedPipelineWorkflow(value)
	if err != nil {
		return err
	}
	previous, oldOK := workflow.Status.Nodes[oldNode]
	current, newOK := workflow.Status.Nodes[scope.NodeID]
	if !oldOK || !newOK || previous.Type != wfapi.NodeTypePod || current.Type != wfapi.NodeTypePod || current.Name != scope.NodeName || (current.Phase != wfapi.NodePending && current.Phase != wfapi.NodeRunning) ||
		(previous.Phase != wfapi.NodeFailed && previous.Phase != wfapi.NodeError) || previous.FinishedAt.IsZero() {
		return fmt.Errorf("retry predecessor has not failed terminally")
	}
	allowed := false
	for _, node := range workflow.Status.Nodes {
		if node.Type != wfapi.NodeTypeRetry {
			continue
		}
		oldIndex, newIndex := -1, -1
		for i, id := range node.Children {
			if id == oldNode {
				oldIndex = i
			}
			if id == scope.NodeID {
				newIndex = i
			}
		}
		// Argo Retry children are ordered attempts; do not skip a concurrent
		// or unobserved predecessor, or hand back to an earlier Pod.
		if oldIndex >= 0 && newIndex == oldIndex+1 {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("Pod is not the next attempt of the same Retry node")
	}
	pod, err := c.kube.CoreV1().Pods(scope.Namespace).Get(ctx, oldPod, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(pod.UID) != oldUID {
		return fmt.Errorf("retry predecessor Pod was replaced")
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == "main" && status.State.Terminated != nil {
			return nil
		}
	}
	return fmt.Errorf("retry predecessor main container is still active")
}
