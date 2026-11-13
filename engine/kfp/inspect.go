package kfpengine

import (
	"encoding/json"
	"fmt"

	"github.com/kubeflow/pipelines/api/v2alpha1/go/pipelinespec"
	"google.golang.org/protobuf/encoding/protojson"
)

// Inspect validates the upstream schema and structural bounds only. It never
// reports full compilation or runtime readiness without actually compiling.
func Inspect(raw json.RawMessage) *Report {
	report := &Report{CompilerVersion: CompilerVersion, ValidationMode: "OFFICIAL_SCHEMA_ONLY", ExecutionReason: "OFFICIAL_RUNTIME_NOT_CONFIGURED_OR_UNVERIFIED", SupportedFeatures: []string{}, Issues: []Issue{}}
	spec, err := decode(raw)
	if err != nil {
		report.Issues = append(report.Issues, Issue{Code: "KFP_PIPELINE_SPEC_INVALID", Path: "/"})
		return report
	}
	report.SchemaVersion = spec.GetSchemaVersion()
	report.TaskCount, report.DependencyCount, err = countDefinitions(spec)
	if err != nil {
		report.Issues = append(report.Issues, Issue{Code: "INVALID_OR_UNBOUNDED_COMPONENT_GRAPH", Path: "/components"})
		return report
	}
	digest, err := pipelineIRDigest(raw)
	if err != nil {
		report.Issues = append(report.Issues, Issue{Code: "KFP_PIPELINE_SPEC_INVALID", Path: "/"})
		return report
	}
	report.IRDigest = digest
	return report
}

// Match the platform's protobuf Struct/SQL JSON numeric semantics while
// sorting object keys; protobuf JSON output order is not a digest contract.
func pipelineIRDigest(raw json.RawMessage) (string, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return sum(canonical), nil
}

func decode(raw json.RawMessage) (*pipelinespec.PipelineSpec, error) {
	if len(raw) == 0 || len(raw) > MaxIRBytes {
		return nil, fmt.Errorf("pipeline IR exceeds size limit")
	}
	spec := &pipelinespec.PipelineSpec{}
	if err := protojson.Unmarshal(raw, spec); err != nil {
		return nil, err
	}
	if spec.GetRoot() == nil || spec.GetPipelineInfo().GetName() == "" || len(spec.GetComponents()) > MaxTasks || spec.GetComponents()["root"] != nil {
		return nil, fmt.Errorf("invalid pipeline root or component count")
	}
	return spec, nil
}

// This is a resource/cycle guard, not another semantic IR compiler. Definitions
// are counted once; expanded loop iterations remain the official driver's job.
func countDefinitions(spec *pipelinespec.PipelineSpec) (tasks, dependencies int, err error) {
	state := map[string]uint8{}
	var visit func(string, *pipelinespec.ComponentSpec, int) error
	visit = func(name string, component *pipelinespec.ComponentSpec, depth int) error {
		if component == nil || depth > MaxDepth || state[name] == 1 {
			return fmt.Errorf("invalid component reference, nesting, or cycle")
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		for _, task := range component.GetDag().GetTasks() {
			tasks++
			dependencies += len(task.GetDependentTasks())
			if tasks > MaxTasks {
				return fmt.Errorf("too many task definitions")
			}
			if err := visit(task.GetComponentRef().GetName(), spec.GetComponents()[task.GetComponentRef().GetName()], depth+1); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	err = visit("root", spec.GetRoot(), 0)
	return
}
