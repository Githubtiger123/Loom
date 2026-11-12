package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

var WorkflowGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "workflows"}

const (
	labelPipelineRunID             = "loom.ml/pipeline-run-id"
	annotationPipelineNamespaceUID = "loom.ml/pipeline-namespace-uid"
	annotationPipelinePlanHash     = "loom.ml/pipeline-plan-hash"
	MaxNativeWorkflowNodes         = 4000
)

// Informer caches only Loom-managed Workflows. Its callbacks enqueue keys;
// business ownership is always rechecked against durable execution bindings.
func (c *Client) NewPipelineWorkflowInformer() cache.SharedIndexInformer {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.dyn, 30*time.Second, v1.NamespaceAll, func(opts *v1.ListOptions) {
		opts.LabelSelector = labelManagedBy + "=" + managedByLoom
	})
	return factory.ForResource(WorkflowGVR).Informer()
}

type PipelineWorkflowRef struct {
	GroupID, RunID                                           uint64
	InstanceID, Namespace, NamespaceUID, Name, UID, PlanHash string
	StopToken                                                string
}

type PipelineWorkflowNode struct {
	ID                    string
	Name, Type            string
	TemplateName          string
	TemplateScope         string
	BoundaryID            string
	Phase                 string
	StartedAt, FinishedAt *time.Time
	Outputs               json.RawMessage
	Inputs                json.RawMessage
	Children              []string
	Attempts              []PipelineWorkflowNode
	NeverStarted          bool
	DefinitionKey         string
	ParentNodeID          string
	IterationPath         []int
	RetryIndex            int
	CacheHit              bool
	CacheKey              string
	CacheName             string
}

// A shutdown is a lifecycle transition only with Loom's durable stop token.
// Natural success wins a concurrent stop; an acknowledged failed shutdown is
// projected STOPPED. Raw status remains in the private terminal snapshot.
func ObserveNativePipelineWorkflow(obj *unstructured.Unstructured, ref PipelineWorkflowRef) (*PipelineWorkflowObservation, error) {
	obs, err := ObservePipelineWorkflow(obj)
	if err != nil {
		return nil, err
	}
	shutdown, _, err := unstructured.NestedString(obj.Object, "spec", "shutdown")
	if err != nil {
		return nil, err
	}
	if ref.StopToken != "" && obj.GetAnnotations()[AnnotationPipelineStopToken] == ref.StopToken && shutdown == "Stop" && obs.Phase == "FAILED" {
		obs.Phase = "STOPPED"
		for id, node := range obs.Nodes {
			if node.Name == ref.Name {
				node.Phase = "STOPPED"
				obs.Nodes[id] = node
			}
		}
	}
	return obs, nil
}

type PipelineWorkflowObservation struct {
	UID, ResourceVersion, RawPhase, Phase, SpecHash string
	StartedAt, FinishedAt                           *time.Time
	Nodes                                           map[string]PipelineWorkflowNode
	NodesComplete                                   bool
	NodesReason                                     string
}

// Read is deliberately a live GET after acquiring the existing target lock.
// Informer notifications are coalesced hints, never an ordering/fencing token.
func (c *Client) ReadPipelineWorkflow(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	return c.dyn.Resource(WorkflowGVR).Namespace(namespace).Get(ctx, name, v1.GetOptions{})
}

func PipelineWorkflowMatches(obj *unstructured.Unstructured, ref PipelineWorkflowRef) bool {
	return pipelineWorkflowMetadataMatches(obj, ref) && string(obj.GetUID()) != "" &&
		(ref.UID == "" || string(obj.GetUID()) == ref.UID)
}

func pipelineWorkflowMetadataMatches(obj *unstructured.Unstructured, ref PipelineWorkflowRef) bool {
	if obj == nil || obj.GetAPIVersion() != "argoproj.io/v1alpha1" || obj.GetKind() != "Workflow" {
		return false
	}
	return obj.GetNamespace() == ref.Namespace && obj.GetName() == ref.Name &&
		obj.GetLabels()[labelManagedBy] == managedByLoom &&
		obj.GetLabels()[labelGroupID] == fmt.Sprint(ref.GroupID) &&
		obj.GetLabels()[labelPipelineRunID] == fmt.Sprint(ref.RunID) &&
		obj.GetAnnotations()[annotationInstance] == ref.InstanceID &&
		obj.GetAnnotations()[annotationPipelineNamespaceUID] == ref.NamespaceUID &&
		obj.GetAnnotations()[annotationPipelinePlanHash] == ref.PlanHash
}

func nativeWorkflowPhase(raw string) string {
	switch raw {
	case "Pending":
		return "PENDING"
	case "Running":
		return "RUNNING"
	case "Succeeded":
		return "SUCCEEDED"
	case "Failed", "Error":
		return "FAILED"
	case "Skipped", "Omitted":
		return "SKIPPED"
	default:
		return "UNKNOWN"
	}
}

func workflowTime(obj map[string]any, key string) (*time.Time, error) {
	value, exists := obj[key]
	if !exists || value == nil || value == "" {
		return nil, nil
	}
	raw, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("invalid Workflow timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid Workflow timestamp")
	}
	if t.IsZero() {
		return nil, nil
	}
	t = t.UTC()
	return &t, nil
}

// Project execution facts only. No URI fetching, output publication, guessed
// Pod names or conversion of raw Argo nodes into invented IR task identities.
func ObservePipelineWorkflow(obj *unstructured.Unstructured) (*PipelineWorkflowObservation, error) {
	specHash, err := PipelineWorkflowSpecHash(obj)
	if err != nil {
		return nil, err
	}
	status, _, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil {
		return nil, fmt.Errorf("Workflow status is malformed")
	}
	obs := &PipelineWorkflowObservation{UID: string(obj.GetUID()), ResourceVersion: obj.GetResourceVersion(),
		SpecHash: specHash, Nodes: map[string]PipelineWorkflowNode{}, NodesComplete: true}
	obs.RawPhase, _, err = unstructured.NestedString(status, "phase")
	if err != nil {
		return nil, fmt.Errorf("Workflow phase is malformed")
	}
	switch obs.RawPhase {
	case "", "Pending", "Running", "Succeeded", "Failed", "Error":
	default:
		// Skipped/Omitted are node phases, never Workflow success. Do not let
		// a malformed workflow-level phase bypass the finalization gate.
		return nil, fmt.Errorf("Workflow phase is unsupported")
	}
	obs.Phase = nativeWorkflowPhase(obs.RawPhase)
	if obs.StartedAt, err = workflowTime(status, "startedAt"); err != nil {
		return nil, err
	}
	if obs.FinishedAt, err = workflowTime(status, "finishedAt"); err != nil {
		return nil, err
	}
	compressed, _, err := unstructured.NestedString(status, "compressedNodes")
	if err != nil {
		return nil, fmt.Errorf("Workflow compressedNodes is malformed")
	}
	offload, _, err := unstructured.NestedString(status, "offloadNodeStatusVersion")
	if err != nil {
		return nil, fmt.Errorf("Workflow offload version is malformed")
	}
	if compressed != "" || offload != "" {
		obs.NodesComplete, obs.NodesReason = false, "NODE_STATUS_OFFLOAD_UNSUPPORTED"
		return obs, nil
	}
	nodes, _, err := unstructured.NestedMap(status, "nodes")
	if err != nil {
		return nil, fmt.Errorf("Workflow nodes are malformed")
	}
	if len(nodes) > MaxNativeWorkflowNodes {
		obs.NodesComplete, obs.NodesReason = false, "NODE_STATUS_LIMIT_EXCEEDED"
		return obs, nil
	}
	for id, value := range nodes {
		node, ok := value.(map[string]any)
		if !ok || id == "" || len(id) > 128 {
			return nil, fmt.Errorf("invalid Workflow node")
		}
		nodeID, ok := node["id"].(string)
		if !ok || nodeID != id {
			return nil, fmt.Errorf("Workflow node identity mismatch")
		}
		raw, _, err := unstructured.NestedString(node, "phase")
		if err != nil {
			return nil, fmt.Errorf("Workflow node phase is malformed")
		}
		n := PipelineWorkflowNode{ID: id, Phase: nativeWorkflowPhase(raw)}
		if memo, exists, err := unstructured.NestedMap(node, "memoizationStatus"); err != nil {
			return nil, fmt.Errorf("invalid memoization status")
		} else if exists {
			n.CacheHit, _, err = unstructured.NestedBool(memo, "hit")
			if err != nil {
				return nil, err
			}
			n.CacheKey, _, err = unstructured.NestedString(memo, "key")
			if err != nil {
				return nil, err
			}
			n.CacheName, _, err = unstructured.NestedString(memo, "cacheName")
			if err != nil {
				return nil, err
			}
		}
		if inputs, exists := node["inputs"]; exists {
			n.Inputs, err = json.Marshal(inputs)
			if err != nil || len(n.Inputs) > 128<<10 {
				return nil, fmt.Errorf("native inputs exceed limits")
			}
		}
		if n.Children, _, err = unstructured.NestedStringSlice(node, "children"); err != nil || len(n.Children) > MaxNativeWorkflowNodes {
			return nil, fmt.Errorf("Workflow node children are malformed")
		}
		if outputs, exists := node["outputs"]; exists {
			n.Outputs, err = json.Marshal(outputs)
			if err != nil || len(n.Outputs) > 128<<10 {
				return nil, fmt.Errorf("native outputs exceed limits")
			}
		}
		for _, field := range []struct {
			key string
			out *string
		}{{"name", &n.Name}, {"type", &n.Type}, {"templateName", &n.TemplateName},
			{"templateScope", &n.TemplateScope}, {"boundaryID", &n.BoundaryID}} {
			value, _, err := unstructured.NestedString(node, field.key)
			if err != nil || len(value) > 1024 {
				return nil, fmt.Errorf("Workflow node source identity is malformed")
			}
			*field.out = value
		}
		if value, exists := node["templateRef"]; exists && value != nil {
			return nil, fmt.Errorf("Workflow external template identity is unsupported")
		}
		if n.StartedAt, err = workflowTime(node, "startedAt"); err != nil {
			return nil, err
		}
		if n.FinishedAt, err = workflowTime(node, "finishedAt"); err != nil {
			return nil, err
		}
		obs.Nodes[id] = n
	}
	return obs, nil
}

func PipelineWorkflowSpecHash(obj *unstructured.Unstructured) (string, error) {
	if obj == nil {
		return "", fmt.Errorf("Workflow observation is empty")
	}
	spec, exists, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !exists {
		return "", fmt.Errorf("Workflow spec is missing or malformed")
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest), nil
}
