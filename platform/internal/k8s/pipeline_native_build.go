package k8s

import (
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Operator-supplied policy, never decoded from the Pipeline IR. The builder is
// pure; the internal staging primitive can persist/create its suspended result.
// Only the configured worker policy may supply storage and execution identity.
type NativePipelineWorkflowPolicy struct {
	ServiceAccount   string                     `json:"serviceAccount"`
	Parallelism      int64                      `json:"parallelism"`
	CPU              string                     `json:"cpu"`
	Memory           string                     `json:"memory"`
	ArtifactEndpoint string                     `json:"artifactEndpoint,omitempty"`
	ArtifactBucket   string                     `json:"artifactBucket,omitempty"`
	ArtifactRegion   string                     `json:"artifactRegion,omitempty"`
	ArtifactInsecure bool                       `json:"artifactInsecure,omitempty"`
	ArtifactSecret   string                     `json:"artifactSecret,omitempty"`
	Parameters       map[string]any             `json:"parameters,omitempty"`
	LogArchive       *NativeLogArchivePolicy    `json:"logArchive,omitempty"`
	CacheSeeds       map[string]NativeCacheSeed `json:"cacheSeeds,omitempty"`
}

type NativeCacheSeed struct {
	SourceTaskID     uint64          `json:"sourceTaskId"`
	SourceInstanceID string          `json:"sourceInstanceId"`
	NodeID           string          `json:"nodeID"`
	Outputs          json.RawMessage `json:"outputs"`
	CreatedAt        string          `json:"creationTimestamp"`
}

// Credentials stay in the namespace-local Secret consumed by Argo's executor.
type NativeLogArchivePolicy struct {
	Endpoint string `json:"endpoint"`
	Bucket   string `json:"bucket"`
	Region   string `json:"region"`
	Insecure bool   `json:"insecure"`
	Secret   string `json:"secret"`
	Prefix   string `json:"prefix"`
	Cluster  string `json:"cluster"`
}

type nativeWorkflowSpec struct {
	Entrypoint         string                   `json:"entrypoint"`
	ServiceAccountName string                   `json:"serviceAccountName"`
	Parallelism        int64                    `json:"parallelism"`
	Suspend            bool                     `json:"suspend"`
	Templates          []nativeWorkflowTemplate `json:"templates"`
}

type nativeWorkflowTemplate struct {
	Name      string             `json:"name"`
	Metadata  nativeTemplateMeta `json:"metadata"`
	DAG       *nativeWorkflowDAG `json:"dag,omitempty"`
	Container *corev1.Container  `json:"container,omitempty"`
}

type nativeTemplateMeta struct {
	Annotations map[string]string `json:"annotations"`
}

type nativeWorkflowDAG struct {
	FailFast bool                 `json:"failFast"`
	Tasks    []nativeWorkflowTask `json:"tasks"`
}

type nativeWorkflowTask struct {
	Name         string   `json:"name"`
	Template     string   `json:"template"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// BuildNativePipelineWorkflow renders only compiler-owned templates and literal
// argv. It does not accept arbitrary PodSpec/Workflow fields, substitute shell
// strings, create CRs, guess runtime node IDs, or enable independent TTL/GC.
func BuildNativePipelineWorkflow(ref PipelineWorkflowRef, plan *pipelineir.Plan, policy NativePipelineWorkflowPolicy) (*unstructured.Unstructured, string, error) {
	instance, err := uuid.Parse(ref.InstanceID)
	if err != nil || instance.String() != ref.InstanceID || ref.GroupID == 0 || ref.RunID == 0 ||
		!validator.IsDNS1123Label(ref.Namespace) || !validator.IsCatalogID(ref.NamespaceUID) || ref.Name != "loom-"+ref.InstanceID || ref.UID != "" ||
		plan == nil || plan.Digest() == "" || ref.PlanHash != plan.Digest() || !validator.IsDNS1123Subdomain(policy.ServiceAccount) ||
		policy.Parallelism < 1 || policy.Parallelism > 16 || !validator.IsPositiveQuantity(policy.CPU) || !validator.IsPositiveQuantity(policy.Memory) {
		return nil, "", fmt.Errorf("invalid native Workflow compilation identity or policy")
	}
	cpu, _ := resource.ParseQuantity(policy.CPU)
	memory, _ := resource.ParseQuantity(policy.Memory)
	resources := corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: memory}
	tasks := plan.Tasks()
	root := plan.Root()
	graph := &nativeWorkflowDAG{FailFast: true, Tasks: []nativeWorkflowTask{}}
	meta := func(nodeKey string) nativeTemplateMeta {
		return nativeTemplateMeta{Annotations: map[string]string{"loom.ml/pipeline-node-key": nodeKey}}
	}
	spec := nativeWorkflowSpec{Entrypoint: root.TemplateName, ServiceAccountName: policy.ServiceAccount, Parallelism: policy.Parallelism, Suspend: true,
		Templates: []nativeWorkflowTemplate{{Name: root.TemplateName, Metadata: meta(root.NodeKey), DAG: graph}}}
	for _, task := range tasks {
		graph.Tasks = append(graph.Tasks, nativeWorkflowTask{Name: task.TaskName, Template: task.TemplateName, Dependencies: task.Dependencies})
		allowEscalation, runAsNonRoot := false, true
		container := &corev1.Container{Name: "main", Image: task.Image, Command: task.Command, Args: task.Args,
			ImagePullPolicy: corev1.PullIfNotPresent, Resources: corev1.ResourceRequirements{Requests: resources.DeepCopy(), Limits: resources.DeepCopy()},
			SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &allowEscalation, RunAsNonRoot: &runAsNonRoot,
				Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}}
		spec.Templates = append(spec.Templates, nativeWorkflowTemplate{Name: task.TemplateName, Metadata: meta(task.NodeKey), Container: container})
	}
	encoded, err := json.Marshal(spec)
	if err != nil || len(encoded) > pipelineir.MaxIRBytes {
		return nil, "", fmt.Errorf("native Workflow specification exceeds compilation limits")
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, "", err
	}
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "argoproj.io/v1alpha1", "kind": "Workflow", "spec": fields}}
	obj.SetName(ref.Name)
	obj.SetNamespace(ref.Namespace)
	obj.SetLabels(map[string]string{labelManagedBy: managedByLoom, labelGroupID: fmt.Sprint(ref.GroupID), labelPipelineRunID: fmt.Sprint(ref.RunID)})
	obj.SetAnnotations(map[string]string{annotationInstance: ref.InstanceID, annotationPipelineNamespaceUID: ref.NamespaceUID,
		annotationPipelinePlanHash: ref.PlanHash, "loom.ml/pipeline-compiler": plan.Compiler()})
	if pipelineir.IsPythonCompiler(plan.Compiler()) {
		build := buildNativePythonTemplates
		if plan.Compiler() == pipelineir.AdvancedCompilerVersion {
			build = buildAdvancedPythonTemplates
		}
		if err := build(obj, ref, plan, policy); err != nil {
			return nil, "", err
		}
	}
	// Hash the actual unstructured representation using the same protocol as
	// observation, rather than the typed struct's field-order encoding.
	digest, err := PipelineWorkflowSpecHash(obj)
	return obj, digest, err
}
