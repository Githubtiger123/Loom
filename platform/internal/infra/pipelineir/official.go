package pipelineir

import (
	"bytes"
	"encoding/json"
	"fmt"

	upstream "github.com/githubtiger123/loom/platform/internal/thirdparty/kfp/compiler"
	"github.com/githubtiger123/loom/platform/internal/thirdparty/kfp/pipelinespec"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

const OfficialCompilerVersion = "loom-native-kfp-v2"

func IsPythonCompiler(version string) bool {
	return version == PythonCompilerVersion || version == OfficialCompilerVersion || version == AdvancedCompilerVersion
}

func IsSupportedCompiler(version string) bool {
	return version == CompilerVersion || IsPythonCompiler(version)
}

// AnalyzePython uses the official protobuf schema and component visitor. Loom's
// remaining lowering pass is a restricted Argo/storage adapter, not KFP's full
// argocompiler/driver/launcher. Old snapshots dispatch to their original parser.
func analyzeOfficialV2(raw json.RawMessage) (*Plan, *Report) {
	report := &Report{CompilerVersion: OfficialCompilerVersion, SchemaVersion: SchemaVersion,
		ValidationMode: "KFP_PROTO_VISITOR_PREFLIGHT", ExecutionReason: "OPERATOR_ENABLEMENT_REQUIRED", Issues: []Issue{}}
	bad := func(code, path string) (*Plan, *Report) {
		report.add(code, path)
		return nil, report
	}
	if !validator.IsBoundedJSONObject(raw, MaxIRBytes, 32, 60000) {
		return bad("INVALID_OR_UNBOUNDED_IR", "/")
	}
	if !validator.HasLosslessJSONIntegerLiterals(raw) {
		return bad("KFP_NUMBER_PRECISION_LOSS", "/")
	}
	spec := &pipelinespec.PipelineSpec{}
	if err := protojson.Unmarshal(raw, spec); err != nil {
		// Protobuf errors can contain parameter values. Never expose them.
		return bad("KFP_PIPELINE_SPEC_INVALID", "/")
	}
	if spec.GetRoot().GetDag() == nil || len(spec.GetComponents()) > MaxTasks ||
		len(spec.GetRoot().GetDag().GetTasks()) < 1 || len(spec.GetRoot().GetDag().GetTasks()) > MaxTasks {
		return bad("GRAPH_SIZE_OR_ROOT_NOT_SUPPORTED", "/root/dag")
	}
	if _, reserved := spec.GetComponents()[upstream.RootComponentName]; reserved {
		return bad("COMPONENT_NAME_RESERVED", "/components/root")
	}
	// The upstream visitor does not yet report circular component references.
	// First-version policy permits only leaf components, so enforce it BEFORE DFS.
	for _, name := range sortedKeys(spec.GetComponents()) {
		component := spec.GetComponents()[name]
		if component.GetDag() != nil || component.GetExecutorLabel() == "" {
			return bad("NON_CONTAINER_COMPONENT", "/components/"+pointer(name))
		}
	}
	// deploymentSpec is a Struct in PipelineSpec. Unlike upstream's permissive
	// GetDeploymentConfig, reject unknown executor fields instead of losing them.
	deploymentJSON, err := protojson.Marshal(spec.GetDeploymentSpec())
	if err != nil {
		return bad("KFP_DEPLOYMENT_SPEC_INVALID", "/deploymentSpec")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(deploymentJSON, &fields) != nil || fields == nil {
		return bad("KFP_DEPLOYMENT_SPEC_INVALID", "/deploymentSpec")
	}
	// The upstream compiler permits the Any type marker; it is not an executor.
	delete(fields, "@type")
	deploymentJSON, _ = json.Marshal(fields)
	deploy := &pipelinespec.PipelineDeploymentConfig{}
	if protojson.Unmarshal(deploymentJSON, deploy) != nil || len(deploy.GetExecutors()) > MaxTasks {
		return bad("KFP_DEPLOYMENT_SPEC_INVALID", "/deploymentSpec")
	}
	visitor := &officialProjection{components: map[string]*pipelinespec.ComponentSpec{}}
	if upstream.AcceptSpec(spec, deploy, nil, visitor) != nil {
		return bad("KFP_COMPONENT_TRAVERSAL_FAILED", "/root/dag")
	}
	if len(visitor.components) != len(spec.GetComponents()) {
		return bad("UNREACHABLE_COMPONENT_NOT_SUPPORTED", "/components")
	}
	// Preserve source identity, not a protobuf round trip's JSON representation.
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&canonical)
	encoded, _ := json.Marshal(canonical)
	report.IRDigest = hash(encoded)
	return lowerOfficialPlan(spec, deploy, visitor, report)
}

type officialProjection struct {
	components map[string]*pipelinespec.ComponentSpec
	containers map[string]*pipelinespec.PipelineDeploymentConfig_PipelineContainerSpec
	root       *pipelinespec.ComponentSpec
}

var _ upstream.Visitor = (*officialProjection)(nil)

func (v *officialProjection) Container(name string, component *pipelinespec.ComponentSpec, container *pipelinespec.PipelineDeploymentConfig_PipelineContainerSpec) error {
	if v.containers == nil {
		v.containers = map[string]*pipelinespec.PipelineDeploymentConfig_PipelineContainerSpec{}
	}
	v.components[name] = component
	v.containers[name] = container
	return nil
}

func (v *officialProjection) DAG(name string, component *pipelinespec.ComponentSpec, _ *pipelinespec.DagSpec) error {
	if name != upstream.RootComponentName {
		return fmt.Errorf("only the root DAG is supported")
	}
	v.root = component
	return nil
}

func (*officialProjection) Importer(string, *pipelinespec.ComponentSpec, *pipelinespec.PipelineDeploymentConfig_ImporterSpec) error {
	return fmt.Errorf("importer is not supported")
}

func (*officialProjection) Resolver(string, *pipelinespec.ComponentSpec, *pipelinespec.PipelineDeploymentConfig_ResolverSpec) error {
	return fmt.Errorf("resolver is not supported")
}

func (*officialProjection) AddKubernetesSpec(string, *structpb.Struct) error {
	return fmt.Errorf("user Kubernetes configuration is not supported")
}
