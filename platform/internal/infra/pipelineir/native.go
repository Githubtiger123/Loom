// Package pipelineir builds bounded, deterministic native execution plans using
// the official KFP IR schema/Visitor and a restricted Loom Argo adapter. Legacy
// static/Python parsers remain frozen for replay; this is not the full KFP runtime.
package pipelineir

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

const (
	CompilerVersion = "loom-native-static-v1"
	SchemaVersion   = "2.1.0"
	MaxIRBytes      = 512 << 10
	MaxTasks        = 128
	maxIssues       = 64
)

type Issue struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

type Report struct {
	CompilerVersion   string   `json:"compilerVersion"`
	SchemaVersion     string   `json:"schemaVersion"`
	ValidationMode    string   `json:"validationMode"`
	Compilable        bool     `json:"compilable"`
	Runnable          bool     `json:"runnable"`
	ExecutionReason   string   `json:"executionReason"`
	SupportedFeatures []string `json:"supportedFeatures"`
	Issues            []Issue  `json:"issues"`
	IssuesTruncated   bool     `json:"issuesTruncated"`
	IRDigest          string   `json:"irDigest,omitempty"`
	PlanDigest        string   `json:"planDigest,omitempty"`
	TaskCount         int      `json:"taskCount"`
	DependencyCount   int      `json:"dependencyCount"`
}

// SourceNode identifies a static definition and its generated Argo invocation.
// It is not a runtime node/Pod UID, nor an algorithm for guessing either.
type SourceNode struct {
	NodeKey        string `json:"nodeKey"`
	DefinitionPath string `json:"definitionPath"`
	NodeKind       string `json:"nodeKind"`
	TaskName       string `json:"taskName,omitempty"`
	TemplateName   string `json:"templateName"`
}

type Task struct {
	SourceNode
	Dependencies    []string     `json:"dependencies"`
	Image           string       `json:"image"`
	Command         []string     `json:"command"`
	Args            []string     `json:"args"`
	Runtime         *PythonTask  `json:"runtime,omitempty"`
	Retry           *RetryPolicy `json:"retry,omitempty"`
	Scope           string       `json:"scope,omitempty"`
	DefinitionNames []string     `json:"definitionNames,omitempty"`
	Group           *GroupTask   `json:"group,omitempty"`
	Condition       string       `json:"condition,omitempty"`
	Cache           bool         `json:"cache,omitempty"`
}

type planData struct {
	FormatVersion   int        `json:"formatVersion"`
	CompilerVersion string     `json:"compilerVersion"`
	IRDigest        string     `json:"irDigest"`
	Root            SourceNode `json:"root"`
	Tasks           []Task     `json:"tasks"`
	RootInputs      *Ports     `json:"rootInputs,omitempty"`
}

// Plan cannot be constructed from an arbitrary user manifest. Only Analyze
// sets its private contents. Accessors return copies, preserving its digest.
type Plan struct {
	data   planData
	digest string
}

func (p *Plan) Digest() string {
	if p == nil {
		return ""
	}
	return p.digest
}

func (p *Plan) Snapshot() (json.RawMessage, error) {
	if p == nil || p.digest == "" {
		return nil, fmt.Errorf("native plan is not compiled")
	}
	return json.Marshal(p.data)
}

func (p *Plan) Root() SourceNode { return p.data.Root }

func (p *Plan) Compiler() string { return p.data.CompilerVersion }

func (p *Plan) RootPorts() Ports {
	if p.data.RootInputs == nil {
		return Ports{}
	}
	raw, _ := json.Marshal(p.data.RootInputs)
	var ports Ports
	_ = json.Unmarshal(raw, &ports)
	return ports
}

func (p *Plan) Tasks() []Task {
	rows := make([]Task, len(p.data.Tasks))
	for i, task := range p.data.Tasks {
		rows[i] = task
		rows[i].Dependencies = append([]string{}, task.Dependencies...)
		rows[i].Command = append([]string{}, task.Command...)
		rows[i].Args = append([]string{}, task.Args...)
		rows[i].DefinitionNames = append([]string(nil), task.DefinitionNames...)
		if task.Group != nil {
			raw, _ := json.Marshal(task.Group)
			var group GroupTask
			_ = json.Unmarshal(raw, &group)
			rows[i].Group = &group
		}
		if task.Retry != nil {
			copy := *task.Retry
			rows[i].Retry = &copy
		}
		if task.Runtime != nil {
			raw, _ := json.Marshal(task.Runtime)
			var runtime PythonTask
			_ = json.Unmarshal(raw, &runtime)
			rows[i].Runtime = &runtime
		}
	}
	return rows
}

func hash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func (r *Report) add(code, path string) {
	if len(r.Issues) >= maxIssues {
		r.IssuesTruncated = true
		return
	}
	if len(path) > 512 {
		path = "/"
	}
	r.Issues = append(r.Issues, Issue{Code: code, Path: path})
}

func pointer(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Never return decoder errors: they may echo user values or credential-shaped
// field names. The authorized caller receives bounded paths and reason codes.
func decode(raw json.RawMessage, out any, path string, r *Report) bool {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		r.add("INVALID_OBJECT", path)
		return false
	}
	if !exactFields(raw, reflect.TypeOf(out), path, r) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		r.add("UNSUPPORTED_OR_INVALID_FIELD", path)
		return false
	}
	return true
}

// encoding/json accepts case-insensitive struct field aliases. KFP protobuf
// field names in this subset are exact: do not silently let Root overwrite
// root or accept unknown casing as a supported extension.
func exactFields(raw json.RawMessage, t reflect.Type, path string, r *Report) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		r.add("INVALID_OBJECT", path)
		return false
	}
	known := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		known[strings.Split(f.Tag.Get("json"), ",")[0]] = f.Type
	}
	valid := true
	for _, key := range sortedKeys(fields) {
		fieldPath := strings.TrimRight(path, "/") + "/" + pointer(key)
		ft, ok := known[key]
		if !ok {
			r.add("FIELD_NOT_SUPPORTED", fieldPath)
			valid = false
			continue
		}
		if !exactFields(fields[key], ft, fieldPath, r) {
			valid = false
		}
	}
	return valid
}

type spec struct {
	PipelineInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	} `json:"pipelineInfo"`
	SchemaVersion  string                     `json:"schemaVersion"`
	SDKVersion     string                     `json:"sdkVersion,omitempty"`
	Root           json.RawMessage            `json:"root"`
	Components     map[string]json.RawMessage `json:"components"`
	DeploymentSpec json.RawMessage            `json:"deploymentSpec"`
}

type component struct {
	InputDefinitions  json.RawMessage `json:"inputDefinitions,omitempty"`
	OutputDefinitions json.RawMessage `json:"outputDefinitions,omitempty"`
	DAG               json.RawMessage `json:"dag,omitempty"`
	ExecutorLabel     string          `json:"executorLabel,omitempty"`
}

type dag struct {
	Tasks   map[string]json.RawMessage `json:"tasks"`
	Outputs json.RawMessage            `json:"outputs,omitempty"`
}

type taskSpec struct {
	TaskInfo struct {
		Name string `json:"name"`
	} `json:"taskInfo"`
	ComponentRef struct {
		Name string `json:"name"`
	} `json:"componentRef"`
	DependentTasks []string `json:"dependentTasks,omitempty"`
	CachingOptions *struct {
		EnableCache bool `json:"enableCache,omitempty"`
	} `json:"cachingOptions,omitempty"`
	Inputs            json.RawMessage `json:"inputs,omitempty"`
	TriggerPolicy     json.RawMessage `json:"triggerPolicy,omitempty"`
	RetryPolicy       json.RawMessage `json:"retryPolicy,omitempty"`
	ParameterIterator json.RawMessage `json:"parameterIterator,omitempty"`
	ArtifactIterator  json.RawMessage `json:"artifactIterator,omitempty"`
	IteratorPolicy    json.RawMessage `json:"iteratorPolicy,omitempty"`
}

type deployment struct {
	Executors map[string]json.RawMessage `json:"executors"`
}

type executor struct {
	Container json.RawMessage `json:"container,omitempty"`
	Importer  json.RawMessage `json:"importer,omitempty"`
}

type container struct {
	Image     string          `json:"image"`
	Command   []string        `json:"command,omitempty"`
	Args      []string        `json:"args,omitempty"`
	Env       json.RawMessage `json:"env,omitempty"`
	Resources json.RawMessage `json:"resources,omitempty"`
}

func noPorts(raw json.RawMessage, path string, r *Report) {
	if len(raw) == 0 {
		return
	}
	var ports struct {
		Parameters map[string]json.RawMessage `json:"parameters,omitempty"`
		Artifacts  map[string]json.RawMessage `json:"artifacts,omitempty"`
	}
	if decode(raw, &ports, path, r) && (len(ports.Parameters) > 0 || len(ports.Artifacts) > 0) {
		r.add("PORTS_REQUIRE_RUNTIME", path)
	}
}

func source(path, kind, taskName, template string) SourceNode {
	// Static-only v1: root parent, no iterations and retryIndex=0. Dynamic
	// identity must use a new protocol before loops/retries can be accepted.
	identity, _ := json.Marshal([]any{"loom-static-node-v1", path, "/root", []int{}, 0})
	return SourceNode{NodeKey: hash(identity), DefinitionPath: path, NodeKind: kind, TaskName: taskName, TemplateName: template}
}

// Analyze accepts only a flat DAG of literal container invocations, without
// ports, caching, retries, conditions, loops, importers or platform extensions.
// It does no I/O and returns no partial Plan if any semantic issue is found.
func Analyze(raw json.RawMessage) (*Plan, *Report) {
	r := &Report{CompilerVersion: CompilerVersion, SchemaVersion: SchemaVersion, ValidationMode: "STATIC_SUBSET_PREFLIGHT",
		ExecutionReason: "NATIVE_EXECUTION_NOT_ENABLED", SupportedFeatures: []string{"STATIC_DAG", "DEPENDENCIES", "LITERAL_CONTAINER_ARGV"}, Issues: []Issue{}}
	if len(raw) == 0 {
		r.add("PACKAGE_IR_REQUIRED", "/")
		return nil, r
	}
	if !validator.IsBoundedJSONObject(raw, MaxIRBytes, 32, 60000) {
		r.add("INVALID_OR_UNBOUNDED_IR", "/")
		return nil, r
	}
	var input spec
	if !decode(raw, &input, "/", r) {
		return nil, r
	}
	if input.SchemaVersion != SchemaVersion {
		r.add("SCHEMA_VERSION_NOT_SUPPORTED", "/schemaVersion")
	}
	if !validator.RequiredText(input.PipelineInfo.Name, 128) {
		r.add("PIPELINE_NAME_INVALID", "/pipelineInfo/name")
	}
	var root component
	var graph dag
	var deploy deployment
	if !decode(input.Root, &root, "/root", r) || !decode(root.DAG, &graph, "/root/dag", r) ||
		!decode(input.DeploymentSpec, &deploy, "/deploymentSpec", r) {
		return nil, r
	}
	noPorts(root.InputDefinitions, "/root/inputDefinitions", r)
	noPorts(root.OutputDefinitions, "/root/outputDefinitions", r)
	if root.ExecutorLabel != "" || len(graph.Outputs) > 0 {
		r.add("ROOT_SEMANTICS_NOT_SUPPORTED", "/root")
	}
	if len(graph.Tasks) < 1 || len(graph.Tasks) > MaxTasks || len(input.Components) < 1 || len(input.Components) > MaxTasks || len(deploy.Executors) < 1 || len(deploy.Executors) > MaxTasks {
		r.add("GRAPH_SIZE_NOT_SUPPORTED", "/")
		return nil, r
	}
	// Normalize without float conversion; this compiler's digest protocol is
	// independent of the existing KFP packageDigest protocol.
	var canonical any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&canonical) != nil {
		r.add("INVALID_IR", "/")
		return nil, r
	}
	encoded, _ := json.Marshal(canonical)
	r.IRDigest = hash(encoded)
	components := map[string]component{}
	for _, key := range sortedKeys(input.Components) {
		path := "/components/" + pointer(key)
		var c component
		if !decode(input.Components[key], &c, path, r) {
			continue
		}
		noPorts(c.InputDefinitions, path+"/inputDefinitions", r)
		noPorts(c.OutputDefinitions, path+"/outputDefinitions", r)
		if len(c.DAG) > 0 || c.ExecutorLabel == "" {
			r.add("NESTED_OR_NON_CONTAINER_COMPONENT", path)
		}
		components[key] = c
	}
	executors := map[string]container{}
	for _, key := range sortedKeys(deploy.Executors) {
		path := "/deploymentSpec/executors/" + pointer(key)
		var e executor
		var c container
		if !decode(deploy.Executors[key], &e, path, r) {
			continue
		}
		if len(e.Importer) > 0 {
			r.add("IMPORTER_REQUIRES_RUNTIME", path+"/importer")
		}
		if !decode(e.Container, &c, path+"/container", r) {
			continue
		}
		if !validator.IsContainerImageReference(c.Image) {
			r.add("CONTAINER_IMAGE_INVALID", path+"/container/image")
		}
		if len(c.Env) > 0 || len(c.Resources) > 0 {
			r.add("CONTAINER_OPTIONS_NOT_SUPPORTED", path+"/container")
		}
		if len(c.Command)+len(c.Args) > 256 {
			r.add("ARGV_LIMIT_EXCEEDED", path+"/container")
		}
		for _, text := range append(append([]string{c.Image}, c.Command...), c.Args...) {
			if len(text) > 8192 || strings.ContainsRune(text, '\x00') {
				r.add("ARGV_VALUE_INVALID", path+"/container")
				break
			}
			if strings.Contains(text, "{{") || strings.Contains(text, "}}") {
				r.add("PLACEHOLDER_REQUIRES_RUNTIME", path+"/container")
				break
			}
		}
		executors[key] = c
	}
	for _, key := range sortedKeys(components) {
		if _, ok := executors[components[key].ExecutorLabel]; !ok {
			r.add("EXECUTOR_REFERENCE_INVALID", "/components/"+pointer(key)+"/executorLabel")
		}
	}
	names := sortedKeys(graph.Tasks)
	generated := map[string]string{}
	for i, name := range names {
		generated[name] = fmt.Sprintf("task-%03d", i)
	}
	plan := &Plan{data: planData{FormatVersion: 1, CompilerVersion: CompilerVersion, IRDigest: r.IRDigest,
		Root: source("/root", "DAG", "", "loom-entry"), Tasks: []Task{}}}
	edges := map[string][]string{}
	for _, name := range names {
		path := "/root/dag/tasks/" + pointer(name)
		var task taskSpec
		if !decode(graph.Tasks[name], &task, path, r) {
			continue
		}
		if !validator.RequiredText(name, 128) || !validator.RequiredText(task.TaskInfo.Name, 128) {
			r.add("TASK_NAME_INVALID", path+"/taskInfo")
		}
		noPorts(task.Inputs, path+"/inputs", r)
		for _, option := range []struct {
			name string
			raw  json.RawMessage
		}{{"triggerPolicy", task.TriggerPolicy}, {"retryPolicy", task.RetryPolicy}, {"parameterIterator", task.ParameterIterator}, {"artifactIterator", task.ArtifactIterator}, {"iteratorPolicy", task.IteratorPolicy}} {
			if len(option.raw) > 0 {
				r.add("TASK_POLICY_REQUIRES_RUNTIME", path+"/"+option.name)
			}
		}
		if task.CachingOptions != nil && task.CachingOptions.EnableCache {
			r.add("CACHE_NOT_SUPPORTED", path+"/cachingOptions")
		}
		component, ok := components[task.ComponentRef.Name]
		if !ok {
			r.add("COMPONENT_REFERENCE_INVALID", path+"/componentRef")
		}
		c, ok := executors[component.ExecutorLabel]
		if !ok {
			r.add("EXECUTOR_REFERENCE_INVALID", path+"/componentRef")
		}
		deps := append([]string{}, task.DependentTasks...)
		sort.Strings(deps)
		if len(deps) > MaxTasks {
			r.add("DEPENDENCY_LIMIT_EXCEEDED", path+"/dependentTasks")
			continue
		}
		invocations := []string{}
		for i, dep := range deps {
			if _, exists := generated[dep]; !exists || dep == name || (i > 0 && deps[i-1] == dep) {
				r.add("DEPENDENCY_REFERENCE_INVALID", path+"/dependentTasks")
			}
			invocations = append(invocations, generated[dep])
		}
		edges[name] = deps
		r.DependencyCount += len(deps)
		plan.data.Tasks = append(plan.data.Tasks, Task{SourceNode: source(path, "CONTAINER", generated[name], generated[name]),
			Dependencies: invocations, Image: c.Image, Command: append([]string{}, c.Command...), Args: append([]string{}, c.Args...)})
	}
	// Bounded topological validation. Dependencies must be earlier-completable;
	// input map order never affects the serialized plan or generated templates.
	complete := map[string]bool{}
	for range len(names) {
		changed := false
		for _, name := range names {
			if complete[name] {
				continue
			}
			ready := true
			for _, dep := range edges[name] {
				ready = ready && complete[dep]
			}
			if ready {
				complete[name], changed = true, true
			}
		}
		if !changed {
			break
		}
	}
	if len(complete) != len(names) {
		r.add("DEPENDENCY_CYCLE_OR_INVALID_REFERENCE", "/root/dag/tasks")
	}
	r.TaskCount = len(names)
	if len(r.Issues) > 0 || r.IssuesTruncated {
		return nil, r
	}
	encoded, err := json.Marshal(plan.data)
	if err != nil || len(encoded) > MaxIRBytes {
		r.add("PLAN_SIZE_NOT_SUPPORTED", "/")
		return nil, r
	}
	plan.digest, r.Compilable = hash(encoded), true
	r.PlanDigest = plan.digest
	return plan, r
}
