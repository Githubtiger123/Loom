package pipelineir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

const PythonCompilerVersion = "loom-native-python-v1"
const PythonOutputProtocol = "loom-python-output-v1"

// The static-v1 compiler remains intact for persisted attempts. Python-v1 is a
// separate frozen protocol, never an implicit reinterpretation of static-v1.
func AnalyzeVersion(raw json.RawMessage, version string) (*Plan, *Report) {
	if version == CompilerVersion {
		return Analyze(raw)
	}
	if version == PythonCompilerVersion {
		return analyzePythonV1(raw)
	}
	if version == OfficialCompilerVersion {
		return analyzeOfficialV2(raw)
	}
	if version == AdvancedCompilerVersion {
		return AnalyzePython(raw)
	}
	return nil, &Report{Issues: []Issue{{Code: "COMPILER_VERSION_NOT_SUPPORTED", Path: "/"}}}
}

type Parameter struct {
	Type        string          `json:"parameterType"`
	Default     json.RawMessage `json:"defaultValue,omitempty"`
	Optional    bool            `json:"isOptional,omitempty"`
	Description string          `json:"description,omitempty"`
}
type ArtifactType struct {
	Title   string `json:"schemaTitle"`
	Version string `json:"schemaVersion"`
}
type ArtifactPort struct {
	Type        ArtifactType `json:"artifactType"`
	Optional    bool         `json:"isOptional,omitempty"`
	List        bool         `json:"isArtifactList,omitempty"`
	Description string       `json:"description,omitempty"`
}
type Ports struct {
	Parameters map[string]Parameter    `json:"parameters,omitempty"`
	Artifacts  map[string]ArtifactPort `json:"artifacts,omitempty"`
}
type Binding struct {
	Root     string          `json:"root,omitempty"`
	Task     string          `json:"task,omitempty"` // generated, not user-controlled Argo name
	Output   string          `json:"output,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Selector string          `json:"selector,omitempty"`
}
type PythonTask struct {
	Inputs         Ports                `json:"inputs"`
	Outputs        Ports                `json:"outputs"`
	Parameters     map[string]Binding   `json:"parameters"`
	Artifacts      map[string]Binding   `json:"artifacts"`
	RootParameters map[string]Parameter `json:"rootParameters"`
}

var portName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)

func ParameterValid(kind string, raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > 8192 {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return false
	}
	switch kind {
	case "STRING":
		_, ok := v.(string)
		return ok
	case "BOOLEAN":
		_, ok := v.(bool)
		return ok
	case "NUMBER_INTEGER":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Int64()
		return err == nil
	case "NUMBER_DOUBLE":
		_, ok := v.(json.Number)
		return ok
	default:
		return false
	}
}

func pythonPorts(raw json.RawMessage, path string, r *Report) Ports {
	p := Ports{Parameters: map[string]Parameter{}, Artifacts: map[string]ArtifactPort{}}
	if len(raw) != 0 {
		decode(raw, &p, path, r)
	}
	if len(p.Parameters)+len(p.Artifacts) > 32 {
		r.add("PORT_LIMIT_EXCEEDED", path)
	}
	for name, v := range p.Parameters {
		if !portName.MatchString(name) || (v.Type != "STRING" && v.Type != "NUMBER_INTEGER" && v.Type != "NUMBER_DOUBLE" && v.Type != "BOOLEAN") ||
			(len(v.Default) != 0 && !ParameterValid(v.Type, v.Default)) || v.Optional && len(v.Default) == 0 {
			r.add("PARAMETER_DEFINITION_NOT_SUPPORTED", path+"/parameters/"+pointer(name))
		}
	}
	for name, v := range p.Artifacts {
		if !portName.MatchString(name) || v.Optional || v.List || v.Type.Version != "0.0.1" ||
			(v.Type.Title != "system.Artifact" && v.Type.Title != "system.Dataset" && v.Type.Title != "system.Model" && v.Type.Title != "system.Metrics") {
			r.add("ARTIFACT_DEFINITION_NOT_SUPPORTED", path+"/artifacts/"+pointer(name))
		}
	}
	return p
}

// analyzePythonV1 is frozen for legacy attempt replay. New submissions use the
// official protobuf/Visitor entry point and never pass through this IR parser.
func analyzePythonV1(raw json.RawMessage) (*Plan, *Report) {
	r := &Report{CompilerVersion: PythonCompilerVersion, SchemaVersion: SchemaVersion, ValidationMode: "PYTHON_V1_PREFLIGHT",
		ExecutionReason: "OPERATOR_ENABLEMENT_REQUIRED", SupportedFeatures: []string{"PYTHON_COMPONENT", "SCALAR_PARAMETERS", "FILE_ARTIFACTS", "STATIC_DAG", "LOGS", "SCALAR_METRICS"}, Issues: []Issue{}}
	if !validator.IsBoundedJSONObject(raw, MaxIRBytes, 32, 60000) {
		r.add("INVALID_OR_UNBOUNDED_IR", "/")
		return nil, r
	}
	var input spec
	var root component
	var graph dag
	var dep deployment
	if !decode(raw, &input, "/", r) || !decode(input.Root, &root, "/root", r) || !decode(root.DAG, &graph, "/root/dag", r) || !decode(input.DeploymentSpec, &dep, "/deploymentSpec", r) {
		return nil, r
	}
	if input.SchemaVersion != SchemaVersion || !validator.RequiredText(input.PipelineInfo.Name, 128) {
		r.add("INVALID_PIPELINE_SCHEMA", "/")
	}
	rootPorts := pythonPorts(root.InputDefinitions, "/root/inputDefinitions", r)
	if len(rootPorts.Artifacts) > 0 || len(root.OutputDefinitions) > 0 || len(graph.Outputs) > 0 || root.ExecutorLabel != "" {
		r.add("ROOT_OUTPUT_OR_ARTIFACT_NOT_SUPPORTED", "/root")
	}
	if len(graph.Tasks) < 1 || len(graph.Tasks) > MaxTasks || len(input.Components) > MaxTasks || len(dep.Executors) > MaxTasks {
		r.add("GRAPH_SIZE_NOT_SUPPORTED", "/")
		return nil, r
	}
	ins, outs, executors := map[string]Ports{}, map[string]Ports{}, map[string]container{}
	for _, name := range sortedKeys(input.Components) {
		var c component
		path := "/components/" + pointer(name)
		if !decode(input.Components[name], &c, path, r) {
			continue
		}
		ins[name], outs[name] = pythonPorts(c.InputDefinitions, path+"/inputDefinitions", r), pythonPorts(c.OutputDefinitions, path+"/outputDefinitions", r)
		var e executor
		var x container
		if len(c.DAG) > 0 || !decode(dep.Executors[c.ExecutorLabel], &e, "/deploymentSpec/executors/"+pointer(c.ExecutorLabel), r) || len(e.Importer) > 0 || !decode(e.Container, &x, path+"/container", r) {
			r.add("NON_CONTAINER_COMPONENT", path)
			continue
		}
		if !validator.IsContainerImageReference(x.Image) || len(x.Command) == 0 || len(x.Command)+len(x.Args) > 256 || len(x.Env) > 0 || len(x.Resources) > 0 {
			r.add("CONTAINER_OPTIONS_NOT_SUPPORTED", path)
		}
		// Only the whole executor-input placeholder is supported. It is passed
		// as one subprocess argv value, never interpolated into shell source.
		for _, argv := range append(append([]string{}, x.Command...), x.Args...) {
			if len(argv) > 64<<10 || strings.ContainsRune(argv, 0) {
				r.add("ARGV_LIMIT_EXCEEDED", path)
			}
			if strings.Contains(argv, "{{$}}") && argv != "{{$}}" {
				r.add("EXECUTOR_INPUT_MUST_BE_WHOLE_ARGV", path)
			}
			if strings.Contains(argv, "{{$.") {
				r.add("PLACEHOLDER_NOT_SUPPORTED", path)
			}
		}
		executors[name] = x
	}
	names := sortedKeys(graph.Tasks)
	generated := map[string]string{}
	specs := map[string]taskSpec{}
	for i, name := range names {
		generated[name] = fmt.Sprintf("task-%03d", i)
		var t taskSpec
		if decode(graph.Tasks[name], &t, "/root/dag/tasks/"+pointer(name), r) {
			specs[name] = t
		}
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&value)
	canonical, _ := json.Marshal(value)
	r.IRDigest = hash(canonical)
	p := &Plan{data: planData{FormatVersion: 2, CompilerVersion: PythonCompilerVersion, IRDigest: r.IRDigest, Root: source("/root", "DAG", "", "loom-entry"), Tasks: []Task{}}}
	edges := map[string][]string{}
	for _, name := range names {
		t := specs[name]
		path := "/root/dag/tasks/" + pointer(name)
		componentName := t.ComponentRef.Name
		x, ok := executors[componentName]
		if !ok {
			r.add("COMPONENT_REFERENCE_INVALID", path)
			continue
		}
		if len(t.TriggerPolicy)+len(t.RetryPolicy)+len(t.ParameterIterator)+len(t.ArtifactIterator)+len(t.IteratorPolicy) > 0 || t.CachingOptions != nil && t.CachingOptions.EnableCache {
			r.add("ADVANCED_POLICY_NOT_SUPPORTED", path)
		}
		if !validator.RequiredText(t.TaskInfo.Name, 128) {
			r.add("TASK_NAME_INVALID", path)
		}
		rt := &PythonTask{Inputs: ins[componentName], Outputs: outs[componentName], Parameters: map[string]Binding{}, Artifacts: map[string]Binding{}, RootParameters: rootPorts.Parameters}
		var bindings struct {
			Parameters map[string]json.RawMessage `json:"parameters,omitempty"`
			Artifacts  map[string]json.RawMessage `json:"artifacts,omitempty"`
		}
		if len(t.Inputs) > 0 {
			decode(t.Inputs, &bindings, path+"/inputs", r)
		}
		dependencies := map[string]bool{}
		for _, ref := range t.DependentTasks {
			if dependencies[ref] {
				r.add("DUPLICATE_DEPENDENCY", path)
			}
			dependencies[ref] = true
		}
		for _, key := range sortedKeys(bindings.Parameters) {
			var b struct {
				Component *struct {
					Name string `json:"componentInputParameter"`
				} `json:"componentInputParameter,omitempty"`
				Output *struct {
					Task string `json:"producerTask"`
					Key  string `json:"outputParameterKey"`
				} `json:"taskOutputParameter,omitempty"`
				Constant *struct {
					Value json.RawMessage `json:"constant"`
				} `json:"runtimeValue,omitempty"`
			}
			// componentInputParameter is a string in protobuf JSON.
			var rawBinding map[string]json.RawMessage
			if json.Unmarshal(bindings.Parameters[key], &rawBinding) != nil {
				r.add("INPUT_BINDING_INVALID", path)
				continue
			}
			binding := Binding{}
			count := 0
			if v, exists := rawBinding["componentInputParameter"]; exists {
				count++
				if json.Unmarshal(v, &binding.Root) != nil {
					r.add("ROOT_BINDING_INVALID", path)
				}
				delete(rawBinding, "componentInputParameter")
			}
			if v, exists := rawBinding["taskOutputParameter"]; exists {
				count++
				if !decode(v, &b.Output, path, r) || b.Output == nil {
					continue
				}
				binding.Task, binding.Output = generated[b.Output.Task], b.Output.Key
				dependencies[b.Output.Task] = true
				if outs[specs[b.Output.Task].ComponentRef.Name].Parameters[b.Output.Key].Type != rt.Inputs.Parameters[key].Type {
					r.add("PARAMETER_TYPE_MISMATCH", path)
				}
				delete(rawBinding, "taskOutputParameter")
			}
			if v, exists := rawBinding["runtimeValue"]; exists {
				count++
				if !decode(v, &b.Constant, path, r) || b.Constant == nil {
					continue
				}
				binding.Value = b.Constant.Value
				delete(rawBinding, "runtimeValue")
			}
			definition, exists := rt.Inputs.Parameters[key]
			if !exists || count != 1 || len(rawBinding) > 0 || binding.Root == "" && binding.Task == "" && len(binding.Value) == 0 || binding.Root != "" && rootPorts.Parameters[binding.Root].Type != definition.Type || len(binding.Value) > 0 && !ParameterValid(definition.Type, binding.Value) {
				r.add("PARAMETER_BINDING_INVALID", path)
			}
			rt.Parameters[key] = binding
		}
		for key, definition := range rt.Inputs.Parameters {
			if _, exists := rt.Parameters[key]; !exists {
				if len(definition.Default) == 0 {
					r.add("INPUT_PARAMETER_REQUIRED", path)
				} else {
					rt.Parameters[key] = Binding{Value: definition.Default}
				}
			}
		}
		for _, key := range sortedKeys(bindings.Artifacts) {
			var b struct {
				Output struct {
					Task string `json:"producerTask"`
					Key  string `json:"outputArtifactKey"`
				} `json:"taskOutputArtifact"`
			}
			if !decode(bindings.Artifacts[key], &b, path, r) {
				continue
			}
			in, exists := rt.Inputs.Artifacts[key]
			out, found := outs[specs[b.Output.Task].ComponentRef.Name].Artifacts[b.Output.Key]
			if !exists || !found || in.Type.Title != out.Type.Title && in.Type.Title != "system.Artifact" {
				r.add("ARTIFACT_BINDING_INVALID", path)
			}
			rt.Artifacts[key] = Binding{Task: generated[b.Output.Task], Output: b.Output.Key}
			dependencies[b.Output.Task] = true
		}
		if len(rt.Artifacts) != len(rt.Inputs.Artifacts) {
			r.add("INPUT_ARTIFACT_REQUIRED", path)
		}
		deps := sortedKeys(dependencies)
		invocations := []string{}
		for _, ref := range deps {
			if generated[ref] == "" || ref == name {
				r.add("DEPENDENCY_REFERENCE_INVALID", path)
			}
			invocations = append(invocations, generated[ref])
		}
		edges[name] = deps
		r.DependencyCount += len(deps)
		p.data.Tasks = append(p.data.Tasks, Task{SourceNode: source(path, "CONTAINER", generated[name], generated[name]), Dependencies: invocations, Image: x.Image, Command: x.Command, Args: x.Args, Runtime: rt})
	}
	done := map[string]bool{}
	for range len(names) {
		for _, name := range names {
			ready := true
			for _, dep := range edges[name] {
				ready = ready && done[dep]
			}
			if ready {
				done[name] = true
			}
		}
	}
	if len(done) != len(names) {
		r.add("DEPENDENCY_CYCLE_OR_INVALID_REFERENCE", "/root/dag/tasks")
	}
	r.TaskCount = len(names)
	if len(r.Issues) > 0 || r.IssuesTruncated {
		return nil, r
	}
	encoded, _ := json.Marshal(p.data)
	if len(encoded) > MaxIRBytes {
		r.add("PLAN_SIZE_NOT_SUPPORTED", "/")
		return nil, r
	}
	p.digest = hash(encoded)
	r.Compilable = true
	r.PlanDigest = p.digest
	return p, r
}

// Stable port ordinals are the only names used in Argo paths and expressions.
func PortIndex[T any](ports map[string]T, name string) string {
	names := sortedKeys(ports)
	i := sort.SearchStrings(names, name)
	return fmt.Sprintf("p%02d", i)
}
