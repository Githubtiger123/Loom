package k8s

import (
	"context"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Freeze once before Delete. OBSERVE_ABSENCE can only observe absence; it must
// never acquire delete authority if an object subsequently appears by name.
type PipelineWorkflowDeletionRequest struct {
	Mode            string `json:"mode"`
	ResourceVersion string `json:"resourceVersion"`
	Phase           string `json:"phase"`
}

type PipelineWorkflowDeletionObservation struct {
	WorkflowAbsent bool
	PodsAbsent     bool
}

func PipelineWorkflowDeletionRequestValid(request PipelineWorkflowDeletionRequest) bool {
	if request.Phase != "SUCCEEDED" && request.Phase != "FAILED" && request.Phase != "STOPPED" {
		return false
	}
	return (request.Mode == "DELETE" && validator.IsKubernetesResourceVersion(request.ResourceVersion)) ||
		(request.Mode == "OBSERVE_ABSENCE" && request.ResourceVersion == "")
}

// This is a terminal-object check, not the SQL archive/authorization gate. The
// service must validate the frozen plan, archive and task evidence separately.
func validateNativeDeletionTarget(obj *unstructured.Unstructured, ref PipelineWorkflowRef, specHash, phase string) error {
	if err := ValidateNativePipelineObservedSpec(obj, ref, specHash); err != nil {
		return err
	}
	suspended, _, err := unstructured.NestedBool(obj.Object, "spec", "suspend")
	if err != nil || suspended {
		return &Permanent{Reason: "native deletion cannot target a suspended Workflow"}
	}
	obs, err := ObserveNativePipelineWorkflow(obj, ref)
	if err != nil {
		return err
	}
	if obs.Phase != phase || !obs.NodesComplete || len(obs.Nodes) == 0 || obs.StartedAt == nil || obs.FinishedAt == nil ||
		obs.FinishedAt.Before(*obs.StartedAt) {
		return &Permanent{Reason: "native deletion requires a matching complete terminal Workflow"}
	}
	return nil
}

// ReconcileNativePipelineDeletion never terminates a running Workflow, removes
// finalizers or deletes Pods directly. Foreground deletion is conditional on
// the exact UID and original RV; changed RVs are not silently rebased.
func (c *Client) ReconcileNativePipelineDeletion(ctx context.Context, ref PipelineWorkflowRef, specHash string,
	request PipelineWorkflowDeletionRequest, savedPods []PipelineWorkflowPod, allowDelete bool, guard func() error) (*PipelineWorkflowDeletionObservation, error) {
	if guard == nil || !validator.IsCatalogID(ref.UID) || !validator.IsDNS1123Label(ref.Namespace) ||
		!validator.IsDNS1123Subdomain(ref.Name) || !validator.IsSHA256(specHash) || !PipelineWorkflowDeletionRequestValid(request) ||
		len(savedPods) > MaxNativeWorkflowNodes {
		return nil, fmt.Errorf("native deletion requires bound identity, frozen request, Pod bindings and execution guard")
	}
	known := map[string]bool{}
	for _, pod := range savedPods {
		if !validator.IsDNS1123Subdomain(pod.Name) || !validator.IsCatalogID(pod.UID) || known[pod.UID] {
			return nil, &Permanent{Reason: "native deletion Pod bindings are invalid"}
		}
		known[pod.UID] = true
	}
	if err := guard(); err != nil {
		return nil, err
	}
	live, err := c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		if request.Mode == "OBSERVE_ABSENCE" {
			return nil, &Permanent{Reason: "native absence-only cleanup found a Workflow; deletion is not authorized"}
		}
		if err := validateNativeDeletionTarget(live, ref, specHash, request.Phase); err != nil {
			return nil, err
		}
		if live.GetDeletionTimestamp() == nil {
			if !allowDelete || live.GetResourceVersion() != request.ResourceVersion {
				return nil, &Permanent{Reason: "native deletion request version changed; automatic rebasing is forbidden"}
			}
			if err := guard(); err != nil {
				return nil, err
			}
			uid := types.UID(ref.UID)
			policy := metav1.DeletePropagationForeground
			deleteErr := c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &request.ResourceVersion}, PropagationPolicy: &policy,
			})
			// Success, timeout, conflict and NotFound all require a fresh GET.
			live, err = c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
			if err != nil && !apierrors.IsNotFound(err) {
				return nil, err
			}
			if err == nil {
				if err := validateNativeDeletionTarget(live, ref, specHash, request.Phase); err != nil {
					return nil, err
				}
				if live.GetDeletionTimestamp() == nil {
					if live.GetResourceVersion() != request.ResourceVersion {
						return nil, &Permanent{Reason: "native deletion result is uncertain; request rebasing is forbidden"}
					}
					if deleteErr != nil {
						return nil, deleteErr
					}
				}
			}
		}
		if err == nil {
			return &PipelineWorkflowDeletionObservation{}, nil // Still present, including Terminating.
		}
	}
	// A missing CR alone is insufficient. Inspect a bounded, unfiltered Pod
	// inventory: labels may have been removed, or a known Pod orphaned. Do not
	// infer absence from a selector or a partial page, even for Succeeded Pods.
	list, err := c.kube.CoreV1().Pods(ref.Namespace).List(ctx, metav1.ListOptions{Limit: 2048})
	if err != nil {
		return nil, err
	}
	if list.Continue != "" || len(list.Items) > 2048 {
		return nil, &Pending{Step: "OBSERVING", Reason: "native cleanup Pod inventory is incomplete"}
	}
	out := &PipelineWorkflowDeletionObservation{WorkflowAbsent: true, PodsAbsent: true}
	for _, pod := range list.Items {
		if known[string(pod.UID)] {
			out.PodsAbsent = false // Even a known Pod with its owner removed remains.
		}
		for _, owner := range pod.OwnerReferences {
			// For negative evidence, even a malformed owner reference carrying
			// this globally unique UID prevents claiming that cleanup finished.
			if string(owner.UID) == ref.UID {
				out.PodsAbsent = false
			}
		}
	}
	// Detect a name-reuse race while inventory was read. Never adopt/delete it.
	live, err = c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if err == nil {
		return nil, &Permanent{Reason: "native Workflow reappeared during absence confirmation"}
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err := guard(); err != nil {
		return nil, err
	}
	return out, nil
}
