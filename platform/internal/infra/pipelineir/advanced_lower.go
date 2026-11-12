package pipelineir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/thirdparty/kfp/pipelinespec"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type GroupTask struct {
	Parameters map[string]Binding `json:"parameters,omitempty"`
	Artifacts  map[string]Binding `json:"artifacts,omitempty"`
	Iterator   *Iterator          `json:"iterator,omitempty"`
}
type Iterator struct {
	ItemInput   string          `json:"itemInput"`
	Input       string          `json:"input,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
	Parallelism int32           `json:"parallelism,omitempty"`
}

func advancedPorts(message proto.Message, path string, r *Report) Ports {
	p := Ports{Parameters: map[string]Parameter{}, Artifacts: map[string]ArtifactPort{}}
	if message != nil && message.ProtoReflect().IsValid() {
		raw, err := protojson.Marshal(message)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		// Reject schema constraints/metadata that the runtime does not honor,
		// rather than silently discarding populated official protobuf fields.
		if err != nil || decoder.Decode(&p) != nil {
			r.add("PORT_DEFINITION_INVALID", path)
		}
	}
	if len(p.Parameters)+len(p.Artifacts) > 32 {
		r.add("PORT_LIMIT_EXCEEDED", path)
	}
	for name, v := range p.Parameters {
		if !portName.MatchString(name) || !AdvancedParameterValidType(v.Type) || len(v.Default) > 0 && !AdvancedParameterValid(v.Type, v.Default) || v.Optional && len(v.Default) == 0 {
			r.add("PARAMETER_DEFINITION_NOT_SUPPORTED", path+"/parameters/"+pointer(name))
		}
	}
	for name, v := range p.Artifacts {
		if !portName.MatchString(name) || v.Optional || v.List || v.Type.Version != "0.0.1" || (v.Type.Title != "system.Artifact" && v.Type.Title != "system.Dataset" && v.Type.Title != "system.Model" && v.Type.Title != "system.Metrics") {
			r.add("ARTIFACT_DEFINITION_NOT_SUPPORTED", path+"/artifacts/"+pointer(name))
		}
	}
	return p
}

func AdvancedParameterValidType(kind string) bool {
	return kind == "LIST" || kind == "STRUCT" || kind == "STRING" || kind == "NUMBER_INTEGER" || kind == "NUMBER_DOUBLE" || kind == "BOOLEAN"
}
func AdvancedParameterValid(kind string, raw json.RawMessage) bool {
	if kind != "LIST" && kind != "STRUCT" {
		return ParameterValid(kind, raw)
	}
	if len(raw) == 0 || len(raw) > 8192 || !validator.HasLosslessJSONIntegerLiterals(raw) {
		return false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	if kind == "LIST" {
		a, ok := v.([]any)
		return ok && len(a) <= MaxTasks
	}
	_, ok := v.(map[string]any)
	return ok
}

// Component references are checked on the recursion stack before following
// them, fixing the cycle-detection TODO in the upstream visitor. Each invocation
// gets its own definition path; a reused component is never a runtime identity.
func lowerAdvanced(spec *pipelinespec.PipelineSpec, deploy *pipelinespec.PipelineDeploymentConfig, r *Report) (*Plan, *Report) {
	officialFields(spec, "/", r, "pipelineInfo", "sdkVersion", "schemaVersion", "root", "components", "deploymentSpec")
	officialFields(spec.GetPipelineInfo(), "/pipelineInfo", r, "name", "description", "displayName")
	officialFields(deploy, "/deploymentSpec", r, "executors")
	if spec.GetSchemaVersion() != SchemaVersion || !validator.RequiredText(spec.GetPipelineInfo().GetName(), 128) {
		r.add("INVALID_PIPELINE_SCHEMA", "/")
	}
	root := spec.GetRoot()
	if root.GetDag() == nil || len(root.GetDag().GetTasks()) == 0 {
		r.add("ROOT_DAG_REQUIRED", "/root")
		return nil, r
	}
	p := &Plan{data: planData{FormatVersion: 3, CompilerVersion: AdvancedCompilerVersion, IRDigest: r.IRDigest, Root: source("/root", "DAG", "", "loom-entry"), Tasks: []Task{}}}
	rootPorts := advancedPorts(root.GetInputDefinitions(), "/root/inputDefinitions", r)
	p.data.RootInputs = &rootPorts
	if len(rootPorts.Artifacts) > 0 {
		r.add("ROOT_ARTIFACT_NOT_SUPPORTED", "/root")
	}
	rootOutputs := advancedPorts(root.GetOutputDefinitions(), "/root/outputDefinitions", r)
	if len(rootOutputs.Parameters)+len(rootOutputs.Artifacts) > 0 || len(root.GetDag().GetOutputs().GetParameters())+len(root.GetDag().GetOutputs().GetArtifacts()) > 0 {
		r.add("ROOT_OUTPUTS_NOT_SUPPORTED", "/root/outputDefinitions")
	}
	inputs, outputs := map[string]Ports{}, map[string]Ports{}
	for name, c := range spec.GetComponents() {
		inputs[name] = advancedPorts(c.GetInputDefinitions(), "/components/"+pointer(name)+"/inputDefinitions", r)
		outputs[name] = advancedPorts(c.GetOutputDefinitions(), "/components/"+pointer(name)+"/outputDefinitions", r)
	}
	var walk func(*pipelinespec.ComponentSpec, string, string, []string, map[string]bool) bool
	walk = func(component *pipelinespec.ComponentSpec, scope, path string, names []string, stack map[string]bool) bool {
		if len(names) > 16 || len(p.data.Tasks) >= MaxTasks {
			r.add("GRAPH_SIZE_LIMIT", path)
			return false
		}
		officialFields(component, path, r, "dag", "inputDefinitions", "outputDefinitions", "executorLabel")
		officialFields(component.GetDag(), path+"/dag", r, "tasks", "outputs")
		scopePorts := advancedPorts(component.GetInputDefinitions(), path+"/inputDefinitions", r)
		tasks := component.GetDag().GetTasks()
		generated := map[string]string{}
		if len(p.data.Tasks)+len(tasks) > MaxTasks {
			r.add("GRAPH_SIZE_LIMIT", path)
			return false
		}
		for _, name := range sortedKeys(tasks) {
			generated[name] = fmt.Sprintf("task-%03d", len(p.data.Tasks))
			p.data.Tasks = append(p.data.Tasks, Task{})
		}
		base := len(p.data.Tasks) - len(tasks)
		edges := map[string][]string{}
		for index, name := range sortedKeys(tasks) {
			t := tasks[name]
			taskPath := path + "/dag/tasks/" + pointer(name)
			componentName := t.GetComponentRef().GetName()
			child := spec.GetComponents()[componentName]
			if child == nil || stack[componentName] {
				r.add("COMPONENT_REFERENCE_OR_CYCLE", taskPath)
				continue
			}
			officialFields(t, taskPath, r, "taskInfo", "componentRef", "inputs", "dependentTasks", "cachingOptions", "triggerPolicy", "parameterIterator", "iteratorPolicy", "retryPolicy")
			officialFields(t.GetTaskInfo(), taskPath+"/taskInfo", r, "name")
			officialFields(t.GetComponentRef(), taskPath+"/componentRef", r, "name")
			officialFields(t.GetInputs(), taskPath+"/inputs", r, "parameters", "artifacts")
			officialFields(t.GetCachingOptions(), taskPath+"/cachingOptions", r, "enableCache")
			runtime := &PythonTask{Inputs: inputs[componentName], Outputs: outputs[componentName], RootParameters: scopePorts.Parameters, Parameters: map[string]Binding{}, Artifacts: map[string]Binding{}}
			deps := map[string]bool{}
			for _, dependency := range t.GetDependentTasks() {
				if deps[dependency] {
					r.add("DUPLICATE_DEPENDENCY", taskPath)
				}
				deps[dependency] = true
			}
			for port, input := range t.GetInputs().GetParameters() {
				b := Binding{Selector: input.GetParameterExpressionSelector()}
				definition, declared := runtime.Inputs.Parameters[port]
				officialFields(input, taskPath+"/inputs/parameters/"+pointer(port), r, "componentInputParameter", "taskOutputParameter", "runtimeValue", "parameterExpressionSelector")
				if !declared {
					r.add("INPUT_PARAMETER_NOT_DECLARED", taskPath)
				}
				switch value := input.GetKind().(type) {
				case *pipelinespec.TaskInputsSpec_InputParameterSpec_ComponentInputParameter:
					b.Root = value.ComponentInputParameter
					if _, ok := scopePorts.Parameters[b.Root]; !ok {
						r.add("ROOT_BINDING_INVALID", taskPath)
					}
					if b.Selector == "" && scopePorts.Parameters[b.Root].Type != definition.Type {
						r.add("PARAMETER_TYPE_MISMATCH", taskPath)
					}
				case *pipelinespec.TaskInputsSpec_InputParameterSpec_TaskOutputParameter:
					out := value.TaskOutputParameter
					producer := out.GetProducerTask()
					b.Task, b.Output = generated[producer], out.GetOutputParameterKey()
					deps[producer] = true
					if _, ok := outputs[tasks[producer].GetComponentRef().GetName()].Parameters[b.Output]; !ok || b.Task == "" {
						r.add("PARAMETER_BINDING_INVALID", taskPath)
					}
					producerType := outputs[tasks[producer].GetComponentRef().GetName()].Parameters[b.Output].Type
					if tasks[producer].GetParameterIterator() != nil {
						producerType = "LIST"
					}
					if b.Selector == "" && producerType != definition.Type {
						r.add("PARAMETER_TYPE_MISMATCH", taskPath)
					}
				case *pipelinespec.TaskInputsSpec_InputParameterSpec_RuntimeValue:
					officialFields(value.RuntimeValue, taskPath, r, "constant")
					if value.RuntimeValue.GetConstant() == nil {
						r.add("PARAMETER_BINDING_INVALID", taskPath)
					} else {
						b.Value, _ = protojson.Marshal(value.RuntimeValue.GetConstant())
						if b.Selector == "" && !AdvancedParameterValid(definition.Type, b.Value) {
							r.add("PARAMETER_BINDING_INVALID", taskPath)
						}
					}
				default:
					r.add("PARAMETER_BINDING_INVALID", taskPath)
				}
				runtime.Parameters[port] = b
				if _, err := ArgoSelector(b.Selector, "nil"); err != nil {
					r.add("PARAMETER_SELECTOR_NOT_SUPPORTED", taskPath+"/inputs/parameters/"+pointer(port))
				}
			}
			for port, input := range t.GetInputs().GetArtifacts() {
				officialFields(input, taskPath, r, "taskOutputArtifact", "componentInputArtifact")
				b := Binding{}
				if out := input.GetTaskOutputArtifact(); out != nil {
					producer := out.GetProducerTask()
					b.Task, b.Output = generated[producer], out.GetOutputArtifactKey()
					deps[producer] = true
					if _, ok := outputs[tasks[producer].GetComponentRef().GetName()].Artifacts[b.Output]; !ok || b.Task == "" {
						r.add("ARTIFACT_BINDING_INVALID", taskPath)
					}
					if outputs[tasks[producer].GetComponentRef().GetName()].Artifacts[b.Output].Type != runtime.Inputs.Artifacts[port].Type || tasks[producer].GetParameterIterator() != nil {
						r.add("ARTIFACT_TYPE_OR_COLLECTION_NOT_SUPPORTED", taskPath)
					}
				} else {
					b.Root = input.GetComponentInputArtifact()
					if _, ok := scopePorts.Artifacts[b.Root]; !ok {
						r.add("ARTIFACT_BINDING_INVALID", taskPath)
					}
					if scopePorts.Artifacts[b.Root].Type != runtime.Inputs.Artifacts[port].Type {
						r.add("ARTIFACT_TYPE_MISMATCH", taskPath)
					}
				}
				if _, ok := runtime.Inputs.Artifacts[port]; !ok {
					r.add("INPUT_ARTIFACT_NOT_DECLARED", taskPath)
				}
				runtime.Artifacts[port] = b
			}
			taskNames := append(append([]string{}, names...), name)
			kind := "CONTAINER"
			if child.GetDag() != nil {
				kind = "DAG"
			}
			node := Task{SourceNode: source(taskPath, kind, generated[name], generated[name]), Scope: scope, DefinitionNames: taskNames, Runtime: runtime, Cache: t.GetCachingOptions().GetEnableCache(), Retry: lowerRetry(t.GetRetryPolicy(), taskPath+"/retryPolicy", r)}
			if trigger := t.GetTriggerPolicy(); trigger != nil {
				officialFields(trigger, taskPath+"/triggerPolicy", r, "condition")
				node.Condition = trigger.GetCondition()
				if node.Condition == "" || len(node.Condition) > 8192 {
					r.add("CONDITION_INVALID", taskPath)
				}
				if _, err := ArgoCondition(node.Condition, "{}"); err != nil {
					r.add("CONDITION_NOT_SUPPORTED", taskPath+"/triggerPolicy")
				}
			}
			itemInput := ""
			if iterator := t.GetParameterIterator(); iterator != nil {
				officialFields(iterator, taskPath+"/parameterIterator", r, "itemInput", "items")
				officialFields(iterator.GetItems(), taskPath+"/parameterIterator/items", r, "raw", "inputParameter")
				officialFields(t.GetIteratorPolicy(), taskPath+"/iteratorPolicy", r, "parallelismLimit")
				if child.GetDag() == nil {
					r.add("LOOP_REQUIRES_DAG_COMPONENT", taskPath)
				}
				node.Group = &GroupTask{Iterator: &Iterator{ItemInput: iterator.GetItemInput(), Input: iterator.GetItems().GetInputParameter(), Parallelism: t.GetIteratorPolicy().GetParallelismLimit()}}
				i := node.Group.Iterator
				itemInput = i.ItemInput
				if _, ok := runtime.Inputs.Parameters[itemInput]; !ok || i.Parallelism < 0 || i.Parallelism > 16 {
					r.add("ITERATOR_INVALID", taskPath)
				}
				if raw := iterator.GetItems().GetRaw(); raw != "" {
					i.Raw = json.RawMessage(raw)
					if !AdvancedParameterValid("LIST", i.Raw) {
						r.add("ITERATOR_ITEMS_INVALID", taskPath)
					}
					var values []json.RawMessage
					if json.Unmarshal(i.Raw, &values) == nil {
						for _, value := range values {
							if !AdvancedParameterValid(runtime.Inputs.Parameters[itemInput].Type, value) {
								r.add("ITERATOR_ITEM_TYPE_MISMATCH", taskPath)
								break
							}
						}
					}
				} else if _, ok := runtime.Parameters[i.Input]; !ok {
					r.add("ITERATOR_INPUT_REQUIRED", taskPath)
				}
				if i.Input != "" && runtime.Inputs.Parameters[i.Input].Type != "LIST" && runtime.Inputs.Parameters[i.Input].Type != "STRING" {
					r.add("ITERATOR_LIST_REQUIRED", taskPath)
				}
			}
			for port, definition := range runtime.Inputs.Parameters {
				if _, ok := runtime.Parameters[port]; !ok && port != itemInput {
					if len(definition.Default) == 0 {
						r.add("INPUT_PARAMETER_REQUIRED", taskPath)
					} else {
						runtime.Parameters[port] = Binding{Value: definition.Default}
					}
				}
			}
			if len(runtime.Artifacts) != len(runtime.Inputs.Artifacts) {
				r.add("INPUT_ARTIFACT_REQUIRED", taskPath)
			}
			if child.GetDag() != nil {
				if node.Cache || node.Retry != nil {
					r.add("DAG_CACHE_OR_RETRY_NOT_SUPPORTED", taskPath)
				}
				if node.Group == nil {
					node.Group = &GroupTask{}
				}
				node.Group.Parameters = map[string]Binding{}
				node.Group.Artifacts = map[string]Binding{}
				// Record now; recursive expansion appends definitions after peers.
				p.data.Tasks[base+index] = node
				next := map[string]bool{}
				for k, v := range stack {
					next[k] = v
				}
				next[componentName] = true
				walk(child, node.TaskName, taskPath, taskNames, next)
				childNames := map[string]string{}
				for _, sub := range p.data.Tasks {
					if sub.Scope == node.TaskName && len(sub.DefinitionNames) > 0 {
						childNames[sub.DefinitionNames[len(sub.DefinitionNames)-1]] = sub.TaskName
					}
				}
				for port, out := range child.GetDag().GetOutputs().GetParameters() {
					selector := out.GetValueFromParameter()
					if selector == nil {
						r.add("DAG_ONEOF_OUTPUT_NOT_SUPPORTED", taskPath)
						continue
					}
					node.Group.Parameters[port] = Binding{Task: childNames[selector.GetProducerSubtask()], Output: selector.GetOutputParameterKey()}
					if node.Group.Parameters[port].Task == "" {
						r.add("DAG_OUTPUT_INVALID", taskPath)
					}
				}
				for port, out := range child.GetDag().GetOutputs().GetArtifacts() {
					selectors := out.GetArtifactSelectors()
					if len(selectors) != 1 {
						r.add("DAG_ARTIFACT_LIST_NOT_SUPPORTED", taskPath)
						continue
					}
					selector := selectors[0]
					node.Group.Artifacts[port] = Binding{Task: childNames[selector.GetProducerSubtask()], Output: selector.GetOutputArtifactKey()}
					if node.Group.Artifacts[port].Task == "" {
						r.add("DAG_OUTPUT_INVALID", taskPath)
					}
				}
				if len(node.Group.Parameters) != len(runtime.Outputs.Parameters) || len(node.Group.Artifacts) != len(runtime.Outputs.Artifacts) {
					r.add("DAG_OUTPUT_DEFINITION_MISMATCH", taskPath)
				}
				for port, binding := range node.Group.Parameters {
					declared, ok := runtime.Outputs.Parameters[port]
					producer := Task{}
					for _, sub := range p.data.Tasks {
						if sub.TaskName == binding.Task {
							producer = sub
							break
						}
					}
					if !ok || producer.Runtime == nil {
						r.add("DAG_OUTPUT_INVALID", taskPath)
						continue
					}
					output, ok := producer.Runtime.Outputs.Parameters[binding.Output]
					if producer.Group != nil && producer.Group.Iterator != nil {
						output.Type = "LIST"
					}
					if !ok || declared.Type != output.Type {
						r.add("DAG_OUTPUT_TYPE_MISMATCH", taskPath)
					}
				}
				for port, binding := range node.Group.Artifacts {
					declared, ok := runtime.Outputs.Artifacts[port]
					producer := Task{}
					for _, sub := range p.data.Tasks {
						if sub.TaskName == binding.Task {
							producer = sub
							break
						}
					}
					if !ok || producer.Runtime == nil {
						r.add("DAG_OUTPUT_INVALID", taskPath)
						continue
					}
					output, ok := producer.Runtime.Outputs.Artifacts[binding.Output]
					if !ok || declared.Type != output.Type || producer.Group != nil && producer.Group.Iterator != nil {
						r.add("DAG_OUTPUT_TYPE_MISMATCH", taskPath)
					}
				}
			} else {
				container := deploy.GetExecutors()[child.GetExecutorLabel()].GetContainer()
				officialFields(child, taskPath, r, "inputDefinitions", "outputDefinitions", "executorLabel")
				officialFields(deploy.GetExecutors()[child.GetExecutorLabel()], taskPath, r, "container")
				officialFields(container, taskPath, r, "image", "command", "args")
				if container == nil || !validator.IsContainerImageReference(container.GetImage()) || len(container.GetCommand()) == 0 {
					r.add("CONTAINER_INVALID", taskPath)
				}
				node.Image, node.Command, node.Args = container.GetImage(), container.GetCommand(), container.GetArgs()
				if len(node.Command)+len(node.Args) > 256 {
					r.add("ARGV_LIMIT_EXCEEDED", taskPath)
				}
				for _, arg := range append(append([]string{}, node.Command...), node.Args...) {
					if len(arg) > 64<<10 || strings.ContainsRune(arg, 0) || strings.Contains(arg, "{{$}}") && arg != "{{$}}" || strings.Contains(arg, "{{$.") {
						r.add("ARGV_NOT_SUPPORTED", taskPath)
					}
				}
			}
			edges[name] = sortedKeys(deps)
			for _, dependency := range edges[name] {
				if generated[dependency] == "" || dependency == name {
					r.add("DEPENDENCY_REFERENCE_INVALID", taskPath)
				}
				node.Dependencies = append(node.Dependencies, generated[dependency])
			}
			r.DependencyCount += len(node.Dependencies)
			p.data.Tasks[base+index] = node
		}
		done := map[string]bool{}
		for range len(tasks) {
			for name, deps := range edges {
				ready := true
				for _, dep := range deps {
					ready = ready && done[dep]
				}
				if ready {
					done[name] = true
				}
			}
		}
		if len(done) != len(tasks) {
			r.add("DEPENDENCY_CYCLE", path)
		}
		return true
	}
	walk(root, "", "/root", nil, map[string]bool{})
	if len(p.data.Tasks) > MaxTasks {
		r.add("GRAPH_SIZE_LIMIT", "/")
	}
	if len(r.Issues) > 0 || r.IssuesTruncated {
		return nil, r
	}
	encoded, _ := json.Marshal(p.data)
	if len(encoded) > MaxIRBytes {
		r.add("PLAN_SIZE_NOT_SUPPORTED", "/")
		return nil, r
	}
	p.digest = hash(encoded)
	r.PlanDigest = p.digest
	r.Compilable = true
	r.TaskCount = len(p.data.Tasks)
	r.SupportedFeatures = []string{"PYTHON_COMPONENT", "PARAMETERS", "NESTED_DAG", "CONDITION", "PARAMETER_LOOP", "CACHE", "RETRY_POLICY", "DIRECTORY_ARTIFACTS", "ONLINE_STOP", "LOGS", "SCALAR_METRICS", "AUTOMATIC_LOG_ARCHIVE"}
	return p, r
}
