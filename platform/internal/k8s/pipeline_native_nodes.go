package k8s

import (
	"context"
	"fmt"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Only suspend is a lifecycle field in static-v1. Verify every other admitted
// spec field even after initial UID binding. This is observation, not authority
// to resume, acknowledge submission, or adopt another UID.
func ValidateNativePipelineObservedSpec(obj *unstructured.Unstructured, ref PipelineWorkflowRef, stagedHash string) error {
	if ref.UID == "" || !PipelineWorkflowMatches(obj, ref) || !validator.IsSHA256(stagedHash) {
		return &Permanent{Reason: "native observed Workflow identity conflict"}
	}
	if _, _, err := unstructured.NestedBool(obj.Object, "spec", "suspend"); err != nil {
		return &Permanent{Reason: "native observed suspension is malformed"}
	}
	copy := obj.DeepCopy()
	if value, exists, err := unstructured.NestedString(copy.Object, "spec", "shutdown"); err != nil {
		return err
	} else if exists {
		if value != "Stop" || !validator.IsCanonicalUUID(ref.StopToken) || copy.GetAnnotations()[AnnotationPipelineStopToken] != ref.StopToken {
			return &Permanent{Reason: "native shutdown has no durable authorization"}
		}
		unstructured.RemoveNestedField(copy.Object, "spec", "shutdown")
	}
	if err := unstructured.SetNestedField(copy.Object, true, "spec", "suspend"); err != nil {
		return err
	}
	// Ignore deletionTimestamp for observation: deletion does not erase known
	// execution facts and is not a stop/completion acknowledgement.
	copy.SetDeletionTimestamp(nil)
	return ValidateStagedPipelineWorkflow(copy, ref, stagedHash, true)
}

// Map actual status node IDs through the frozen static invocation/source map.
// Never calculate Argo IDs, use displayName alone, or invent task definitions.
// An unsupported/ambiguous node invalidates this entire observation batch.
func MatchNativeStaticWorkflowNodes(ref PipelineWorkflowRef, plan *pipelineir.Plan, obs *PipelineWorkflowObservation) (map[string]PipelineWorkflowNode, error) {
	bad := func() (map[string]PipelineWorkflowNode, error) {
		return nil, &Permanent{Reason: "native static node source identity conflict"}
	}
	if plan == nil || plan.Digest() != ref.PlanHash || ref.UID == "" || obs == nil || obs.UID != ref.UID || !obs.NodesComplete {
		return bad()
	}
	if plan.Compiler() == pipelineir.AdvancedCompilerVersion {
		return MatchNativeAdvancedWorkflowNodes(ref, plan, obs)
	}
	matched := map[string]PipelineWorkflowNode{}
	if len(obs.Nodes) == 0 {
		return matched, nil
	}
	var root *PipelineWorkflowNode
	byName := map[string]PipelineWorkflowNode{}
	for _, node := range obs.Nodes {
		if node.Name == "" || (node.TemplateScope != "" && node.TemplateScope != "local/"+ref.Name) {
			return bad()
		}
		if _, duplicate := byName[node.Name]; duplicate {
			return bad()
		}
		byName[node.Name] = node
		if node.Name == ref.Name {
			copy := node
			root = &copy
		}
	}
	definition := plan.Root()
	if root == nil || root.Type != "DAG" || root.TemplateName != definition.TemplateName || root.BoundaryID != "" {
		return bad()
	}
	matched[definition.NodeKey] = *root
	consumed := map[string]bool{root.ID: true}
	for _, task := range plan.Tasks() {
		node, exists := byName[ref.Name+"."+task.TaskName]
		if !exists {
			continue // Dependencies may not have scheduled this task yet.
		}
		pythonSkipped := pipelineir.IsPythonCompiler(plan.Compiler()) && node.Phase == "SKIPPED" && (node.Type == "Skipped" || node.Type == "Omitted")
		if task.Retry != nil && node.Type == "Retry" {
			if node.TemplateName != task.TemplateName || node.BoundaryID != root.ID || len(node.Children) > int(task.Retry.Limit)+1 {
				return bad()
			}
			children := map[string]bool{}
			for _, id := range node.Children {
				if children[id] {
					return bad()
				}
				children[id] = true
			}
			for index := 0; index < len(node.Children); index++ {
				attempt, exists := byName[node.Name+"("+strconv.Itoa(index)+")"]
				if !exists || !children[attempt.ID] || consumed[attempt.ID] || attempt.Type != "Pod" || attempt.TemplateName != task.TemplateName || attempt.BoundaryID != root.ID {
					return bad()
				}
				consumed[attempt.ID] = true
				node.Attempts = append(node.Attempts, attempt)
			}
			matched[task.NodeKey] = node
			consumed[node.ID] = true
			continue
		}
		if (node.TemplateName != task.TemplateName && !(pythonSkipped && node.TemplateName == "")) || node.BoundaryID != root.ID ||
			(node.Type != "Pod" && !(node.Type == "Skipped" && node.Phase == "SKIPPED") && !pythonSkipped) {
			return bad()
		}
		matched[task.NodeKey] = node
		consumed[node.ID] = true
	}
	if len(consumed) != len(obs.Nodes) {
		return bad() // Retry, hooks, loops and dynamic/internal nodes are not v1.
	}
	if obs.Phase == "STOPPED" && ref.StopToken != "" {
		for _, task := range plan.Tasks() {
			if _, exists := matched[task.NodeKey]; !exists {
				matched[task.NodeKey] = PipelineWorkflowNode{Name: ref.Name + "." + task.TaskName, TemplateName: task.TemplateName, Type: "NeverStarted", Phase: "STOPPED", NeverStarted: true}
			}
		}
	}
	return matched, nil
}

type PipelineWorkflowPod struct {
	Name, UID, NodeID string
	MainStarted       bool
	MainNeverStarted  bool
}

// The annotation carries the actual invocation; owner UID proves membership.
// This deliberately does not depend on Argo's configurable Pod naming scheme.
// Absence is not deletion proof, and a new UID cannot replace a saved binding.
func (c *Client) ReadNativeStaticWorkflowPods(ctx context.Context, ref PipelineWorkflowRef, nodes map[string]PipelineWorkflowNode) (map[string]PipelineWorkflowPod, error) {
	if ref.UID == "" || !validator.IsDNS1123Subdomain(ref.Name) || !validator.IsDNS1123Label(ref.Namespace) {
		return nil, fmt.Errorf("native Pod observation requires a bound Workflow")
	}
	want := map[string]PipelineWorkflowNode{}
	for _, node := range nodes {
		for _, attempt := range node.Attempts {
			want[attempt.Name] = attempt
		}
		if node.Type == "Pod" {
			want[node.Name] = node
		}
	}
	pods := map[string]PipelineWorkflowPod{}
	if len(want) == 0 {
		return pods, nil
	}
	list, err := c.kube.CoreV1().Pods(ref.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "workflows.argoproj.io/workflow=" + ref.Name, Limit: int64(MaxNativeWorkflowNodes + 1),
	})
	if err != nil {
		return nil, err
	}
	if list.Continue != "" || len(list.Items) > MaxNativeWorkflowNodes {
		return nil, &Permanent{Reason: "native static Workflow Pod limit exceeded"}
	}
	for _, pod := range list.Items {
		owner := metav1.GetControllerOf(&pod)
		if owner == nil || owner.APIVersion != "argoproj.io/v1alpha1" || owner.Kind != "Workflow" ||
			owner.Name != ref.Name || string(owner.UID) != ref.UID {
			continue // Same label alone must never establish ownership.
		}
		node, exists := want[pod.Annotations["workflows.argoproj.io/node-name"]]
		if !exists || !validator.IsCatalogID(string(pod.UID)) {
			return nil, &Permanent{Reason: "native Pod invocation identity conflict"}
		}
		if id := pod.Annotations["workflows.argoproj.io/node-id"]; id != "" && id != node.ID {
			return nil, &Permanent{Reason: "native Pod node identity conflict"}
		}
		if _, duplicate := pods[node.ID]; duplicate {
			return nil, &Permanent{Reason: "multiple Pods claim the same static node"}
		}
		binding := PipelineWorkflowPod{Name: pod.Name, UID: string(pod.UID), NodeID: node.ID}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != "main" {
				continue
			}
			binding.MainStarted = status.State.Running != nil || status.State.Terminated != nil && !status.State.Terminated.StartedAt.IsZero() || status.LastTerminationState.Terminated != nil && !status.LastTerminationState.Terminated.StartedAt.IsZero()
			// A failed Pod with an explicitly unstarted main container cannot
			// have main logs. Waiting/missing status alone is not this proof.
			binding.MainNeverStarted = !binding.MainStarted && string(pod.Status.Phase) == "Failed" && status.ContainerID == "" && (status.State.Waiting != nil || status.State.Terminated != nil)
		}
		pods[node.ID] = binding
	}
	return pods, nil
}
