package kfpengine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/kubeflow/pipelines/api/v2alpha1/go/pipelinespec"
	"github.com/kubeflow/pipelines/backend/src/v2/compiler/argocompiler"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

// Compile delegates semantic compilation to the pinned upstream argocompiler.
// Only operator policy and durable Loom identity are applied to its output.
func Compile(ctx context.Context, request Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateOptions(request); err != nil {
		return nil, err
	}
	spec, err := decode(request.PipelineIR)
	if err != nil {
		return nil, err
	}
	if _, _, err := countDefinitions(spec); err != nil {
		return nil, err
	}
	// Kubernetes platform extensions are a separate policy surface. Do not
	// silently enable arbitrary resource creation by passing user platformSpec.
	for _, component := range spec.GetComponents() {
		if len(component.GetSinglePlatformSpecs()) != 0 {
			return nil, fmt.Errorf("component platform extensions require an explicit policy adapter")
		}
	}
	if len(spec.GetRoot().GetSinglePlatformSpecs()) != 0 {
		return nil, fmt.Errorf("root platform extensions require an explicit policy adapter")
	}
	deployment := &pipelinespec.PipelineDeploymentConfig{}
	if raw, err := protojson.Marshal(spec.GetDeploymentSpec()); err != nil {
		return nil, err
	} else if err := protojson.Unmarshal(raw, deployment); err != nil {
		return nil, err
	}
	for _, executor := range deployment.GetExecutors() {
		if executor.GetContainer() == nil {
			return nil, fmt.Errorf("non-container executors require an explicit runtime policy adapter")
		}
		if err := validateContainerPolicy(executor.GetContainer()); err != nil {
			return nil, err
		}
		if !request.Options.CacheDisabled {
			parts := strings.Split(executor.GetContainer().GetImage(), "@sha256:")
			if len(parts) != 2 || len(parts[1]) != 64 || strings.Trim(parts[1], "0123456789abcdef") != "" {
				return nil, fmt.Errorf("cache-enabled executions require digest-pinned component images")
			}
		}
	}
	var pipelineSpec structpb.Struct
	if err := protojson.Unmarshal(request.PipelineIR, &pipelineSpec); err != nil {
		return nil, err
	}
	parameters, err := structpb.NewStruct(request.Parameters)
	if err != nil {
		return nil, fmt.Errorf("invalid runtime parameters: %w", err)
	}
	job := &pipelinespec.PipelineJob{PipelineSpec: &pipelineSpec, RuntimeConfig: &pipelinespec.PipelineJob_RuntimeConfig{
		ParameterValues: parameters.GetFields(), GcsOutputDirectory: request.Options.PipelineRoot,
	}}
	workflow, err := argocompiler.Compile(job, nil, &argocompiler.Options{
		DriverImage: request.Options.DriverImage, LauncherImage: request.Options.LauncherImage,
		PipelineRoot: request.Options.PipelineRoot, CacheDisabled: request.Options.CacheDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("official argocompiler: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	identity := request.Identity
	workflow.Name, workflow.GenerateName, workflow.Namespace = "loom-"+identity.InstanceID, "", identity.Namespace
	workflow.Spec.ServiceAccountName = request.Options.ServiceAccount
	workflow.Spec.Suspend = ptr(true)
	workflow.Spec.Parallelism = ptr(request.Options.Parallelism)
	// Workflow TTL is owned by Loom's archive/cleanup lifecycle.
	workflow.Spec.TTLStrategy = nil
	if workflow.Labels == nil {
		workflow.Labels = map[string]string{}
	}
	if workflow.Annotations == nil {
		workflow.Annotations = map[string]string{}
	}
	workflow.Labels["app.kubernetes.io/managed-by"] = "loom"
	workflow.Labels["loom.ml/group-id"] = fmt.Sprint(identity.GroupID)
	workflow.Labels["loom.ml/pipeline-run-id"] = fmt.Sprint(identity.RunID)
	workflow.Annotations["loom.ml/instance-id"] = identity.InstanceID
	workflow.Annotations["loom.ml/pipeline-namespace-uid"] = identity.NamespaceUID
	workflow.Annotations["loom.ml/pipeline-compiler"] = CompilerVersion
	workflow.Annotations["loom.ml/pipeline-engine-release"] = EngineRelease
	encoded, err := json.Marshal(workflow.Spec)
	if err != nil || len(encoded) > MaxIRBytes {
		return nil, fmt.Errorf("compiled Workflow exceeds manifest limit")
	}
	irDigest, err := pipelineIRDigest(request.PipelineIR)
	if err != nil {
		return nil, err
	}
	workflow.TypeMeta = metav1.TypeMeta{APIVersion: "argoproj.io/v1alpha1", Kind: "Workflow"}
	digest := sum(encoded)
	workflow.Annotations["loom.ml/pipeline-plan-hash"] = digest
	result := &Result{Workflow: workflow, IRDigest: irDigest, WorkflowDigest: digest, EngineRevision: EngineRelease}
	if request.Options.RuntimeEndpoint != "" {
		if err := BindRuntimeTransport(result, RuntimeTransportPolicy{Endpoint: request.Options.RuntimeEndpoint, CAConfigMap: request.Options.RuntimeCAConfigMap}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func refreshDigest(result *Result) error {
	raw, err := json.Marshal(result.Workflow.Spec)
	if err != nil || len(raw) > MaxIRBytes {
		return fmt.Errorf("transport-bound Workflow exceeds manifest limit")
	}
	result.WorkflowDigest = sum(raw)
	result.Workflow.Annotations["loom.ml/pipeline-plan-hash"] = result.WorkflowDigest
	return nil
}

func ptr[T any](value T) *T  { return &value }
func sum(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func validateContainerPolicy(container *pipelinespec.PipelineDeploymentConfig_PipelineContainerSpec) error {
	if strings.HasPrefix(container.GetImage(), "argostub/") {
		return fmt.Errorf("Kubernetes platform operations require an explicit runtime policy adapter")
	}
	for _, env := range container.GetEnv() {
		if strings.HasPrefix(strings.ToUpper(env.GetName()), "LOOM_RUNTIME_") {
			return fmt.Errorf("component environment cannot override runtime transport policy")
		}
	}
	return nil
}

func validateOptions(request Request) error {
	id, opts := request.Identity, request.Options
	if id.GroupID == 0 || id.RunID == 0 || id.InstanceID == "" || len(k8svalidation.IsDNS1123Label(id.NamespaceUID)) != 0 ||
		len(k8svalidation.IsDNS1123Label("loom-"+id.InstanceID)) != 0 || len(k8svalidation.IsDNS1123Label(id.Namespace)) != 0 ||
		len(k8svalidation.IsDNS1123Subdomain(opts.ServiceAccount)) != 0 || opts.Parallelism < 1 || opts.Parallelism > 16 {
		return fmt.Errorf("invalid official compiler identity or execution policy")
	}
	for _, image := range []string{opts.DriverImage, opts.LauncherImage} {
		parts := strings.Split(image, "@sha256:")
		if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 || strings.Trim(parts[1], "0123456789abcdef") != "" || strings.ContainsAny(image, " \t\r\n") {
			return fmt.Errorf("driver and launcher images must be explicitly configured with sha256 digests")
		}
	}
	root, err := url.Parse(opts.PipelineRoot)
	if err != nil || root.Host == "" || root.User != nil || root.RawQuery != "" || root.Fragment != "" || (root.Scheme != "s3" && root.Scheme != "gs" && root.Scheme != "minio") {
		return fmt.Errorf("official pipeline root must be an operator-configured object-store URI")
	}
	return nil
}
