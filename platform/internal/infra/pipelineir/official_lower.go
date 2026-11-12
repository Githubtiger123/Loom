package pipelineir

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/thirdparty/kfp/pipelinespec"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The schema understands more fields than this first-version execution policy.
// Check populated protobuf fields, not a second handwritten IR JSON schema.
func officialFields(message proto.Message, path string, report *Report, names ...string) {
	if message == nil || !message.ProtoReflect().IsValid() {
		return
	}
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	message.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if !allowed[field.JSONName()] {
			report.add("FIELD_NOT_SUPPORTED", strings.TrimRight(path, "/")+"/"+field.JSONName())
		}
		return true
	})
}

// Port constraints are Loom execution policy, not another IR type definition.
func officialPorts(message proto.Message, path string, report *Report) Ports {
	if message == nil || !message.ProtoReflect().IsValid() {
		return Ports{Parameters: map[string]Parameter{}, Artifacts: map[string]ArtifactPort{}}
	}
	raw, err := protojson.Marshal(message)
	if err != nil {
		report.add("PORT_DEFINITION_INVALID", path)
		return Ports{}
	}
	return pythonPorts(raw, path, report)
}

// lowerOfficialPlan consumes only official types resolved by the upstream
// visitor. It maps the supported subset to Loom's frozen Argo/storage plan;
// parameter resolution at execution time is still a Loom driver adapter.
func lowerOfficialPlan(spec *pipelinespec.PipelineSpec, deploy *pipelinespec.PipelineDeploymentConfig, visitor *officialProjection, report *Report) (*Plan, *Report) {
	report.SupportedFeatures = []string{"PYTHON_COMPONENT", "SCALAR_PARAMETERS", "FILE_ARTIFACTS", "STATIC_DAG", "LOGS", "SCALAR_METRICS"}
	officialFields(spec, "/", report, "pipelineInfo", "sdkVersion", "schemaVersion", "root", "components", "deploymentSpec")
	officialFields(spec.GetPipelineInfo(), "/pipelineInfo", report, "name", "description")
	officialFields(deploy, "/deploymentSpec", report, "executors")
	if spec.GetSchemaVersion() != SchemaVersion || !validator.RequiredText(spec.GetPipelineInfo().GetName(), 128) {
		report.add("INVALID_PIPELINE_SCHEMA", "/")
	}
	root := visitor.root
	officialFields(root, "/root", report, "dag", "inputDefinitions")
	officialFields(root.GetDag(), "/root/dag", report, "tasks")
	rootPorts := officialPorts(root.GetInputDefinitions(), "/root/inputDefinitions", report)
	if len(rootPorts.Artifacts) != 0 {
		report.add("ROOT_ARTIFACT_NOT_SUPPORTED", "/root/inputDefinitions")
	}
	inputs, outputs := map[string]Ports{}, map[string]Ports{}
	for _, name := range sortedKeys(visitor.components) {
		component, container := visitor.components[name], visitor.containers[name]
		path := "/components/" + pointer(name)
		officialFields(component, path, report, "inputDefinitions", "outputDefinitions", "executorLabel")
		inputs[name] = officialPorts(component.GetInputDefinitions(), path+"/inputDefinitions", report)
		outputs[name] = officialPorts(component.GetOutputDefinitions(), path+"/outputDefinitions", report)
		executorPath := "/deploymentSpec/executors/" + pointer(component.GetExecutorLabel())
		officialFields(deploy.GetExecutors()[component.GetExecutorLabel()], executorPath, report, "container")
		officialFields(container, executorPath+"/container", report, "image", "command", "args")
		argv := append(append([]string{}, container.GetCommand()...), container.GetArgs()...)
		if !validator.IsContainerImageReference(container.GetImage()) || len(container.GetCommand()) == 0 || len(argv) > 256 {
			report.add("CONTAINER_OPTIONS_NOT_SUPPORTED", path)
		}
		for _, arg := range argv {
			if len(arg) > 64<<10 || strings.ContainsRune(arg, 0) {
				report.add("ARGV_LIMIT_EXCEEDED", path)
			}
			if strings.Contains(arg, "{{$}}") && arg != "{{$}}" {
				report.add("EXECUTOR_INPUT_MUST_BE_WHOLE_ARGV", path)
			}
			if strings.Contains(arg, "{{$.") {
				report.add("PLACEHOLDER_NOT_SUPPORTED", path)
			}
		}
	}
	// Even unused executors must not smuggle in unsupported execution options.
	for _, label := range sortedKeys(deploy.GetExecutors()) {
		executor := deploy.GetExecutors()[label]
		path := "/deploymentSpec/executors/" + pointer(label)
		officialFields(executor, path, report, "container")
		if executor.GetContainer() == nil {
			report.add("NON_CONTAINER_EXECUTOR", path)
		} else {
			officialFields(executor.GetContainer(), path+"/container", report, "image", "command", "args")
		}
	}
	tasks := root.GetDag().GetTasks()
	names, generated := sortedKeys(tasks), map[string]string{}
	for i, name := range names {
		generated[name] = fmt.Sprintf("task-%03d", i)
	}
	plan := &Plan{data: planData{FormatVersion: 2, CompilerVersion: OfficialCompilerVersion, IRDigest: report.IRDigest,
		Root: source("/root", "DAG", "", "loom-entry"), Tasks: []Task{}}}
	edges := map[string][]string{}
	for _, name := range names {
		task := tasks[name]
		path := "/root/dag/tasks/" + pointer(name)
		officialFields(task, path, report, "taskInfo", "componentRef", "inputs", "dependentTasks", "cachingOptions")
		officialFields(task.GetTaskInfo(), path+"/taskInfo", report, "name")
		officialFields(task.GetComponentRef(), path+"/componentRef", report, "name")
		officialFields(task.GetInputs(), path+"/inputs", report, "parameters", "artifacts")
		officialFields(task.GetCachingOptions(), path+"/cachingOptions", report, "enableCache")
		if task.GetCachingOptions().GetEnableCache() {
			report.add("ADVANCED_POLICY_NOT_SUPPORTED", path+"/cachingOptions")
		}
		if !validator.RequiredText(task.GetTaskInfo().GetName(), 128) {
			report.add("TASK_NAME_INVALID", path)
		}
		componentName := task.GetComponentRef().GetName()
		container := visitor.containers[componentName]
		runtime := &PythonTask{Inputs: inputs[componentName], Outputs: outputs[componentName],
			Parameters: map[string]Binding{}, Artifacts: map[string]Binding{}, RootParameters: rootPorts.Parameters}
		dependencies := map[string]bool{}
		for _, dependency := range task.GetDependentTasks() {
			if dependencies[dependency] {
				report.add("DUPLICATE_DEPENDENCY", path)
			}
			dependencies[dependency] = true
		}
		for _, key := range sortedKeys(task.GetInputs().GetParameters()) {
			input := task.GetInputs().GetParameters()[key]
			bindingPath := path + "/inputs/parameters/" + pointer(key)
			officialFields(input, bindingPath, report, "componentInputParameter", "taskOutputParameter", "runtimeValue")
			definition, declared := runtime.Inputs.Parameters[key]
			binding := Binding{}
			switch value := input.GetKind().(type) {
			case *pipelinespec.TaskInputsSpec_InputParameterSpec_ComponentInputParameter:
				binding.Root = value.ComponentInputParameter
				if binding.Root == "" || rootPorts.Parameters[binding.Root].Type != definition.Type {
					report.add("ROOT_BINDING_INVALID", bindingPath)
				}
			case *pipelinespec.TaskInputsSpec_InputParameterSpec_TaskOutputParameter:
				officialFields(value.TaskOutputParameter, bindingPath+"/taskOutputParameter", report, "producerTask", "outputParameterKey")
				producer, output := value.TaskOutputParameter.GetProducerTask(), value.TaskOutputParameter.GetOutputParameterKey()
				binding.Task, binding.Output = generated[producer], output
				dependencies[producer] = true
				producerType := outputs[tasks[producer].GetComponentRef().GetName()].Parameters[output].Type
				if binding.Task == "" || output == "" || producerType != definition.Type {
					report.add("PARAMETER_BINDING_INVALID", bindingPath)
				}
			case *pipelinespec.TaskInputsSpec_InputParameterSpec_RuntimeValue:
				officialFields(value.RuntimeValue, bindingPath+"/runtimeValue", report, "constant")
				if value.RuntimeValue.GetConstant() == nil {
					report.add("PARAMETER_BINDING_INVALID", bindingPath)
				} else {
					binding.Value, _ = protojson.Marshal(value.RuntimeValue.GetConstant())
					if !ParameterValid(definition.Type, binding.Value) {
						report.add("PARAMETER_BINDING_INVALID", bindingPath)
					}
				}
			default:
				report.add("PARAMETER_BINDING_INVALID", bindingPath)
			}
			if !declared {
				report.add("INPUT_PARAMETER_NOT_DECLARED", bindingPath)
			}
			runtime.Parameters[key] = binding
		}
		for key, definition := range runtime.Inputs.Parameters {
			if _, exists := runtime.Parameters[key]; !exists {
				if len(definition.Default) == 0 {
					report.add("INPUT_PARAMETER_REQUIRED", path+"/inputs")
				} else {
					runtime.Parameters[key] = Binding{Value: definition.Default}
				}
			}
		}
		for _, key := range sortedKeys(task.GetInputs().GetArtifacts()) {
			input := task.GetInputs().GetArtifacts()[key]
			bindingPath := path + "/inputs/artifacts/" + pointer(key)
			officialFields(input, bindingPath, report, "taskOutputArtifact")
			output := input.GetTaskOutputArtifact()
			officialFields(output, bindingPath+"/taskOutputArtifact", report, "producerTask", "outputArtifactKey")
			producer, port := output.GetProducerTask(), output.GetOutputArtifactKey()
			in, declared := runtime.Inputs.Artifacts[key]
			out, found := outputs[tasks[producer].GetComponentRef().GetName()].Artifacts[port]
			if !declared || !found || generated[producer] == "" || in.Type.Title != out.Type.Title && in.Type.Title != "system.Artifact" {
				report.add("ARTIFACT_BINDING_INVALID", bindingPath)
			}
			runtime.Artifacts[key] = Binding{Task: generated[producer], Output: port}
			dependencies[producer] = true
		}
		if len(runtime.Artifacts) != len(runtime.Inputs.Artifacts) {
			report.add("INPUT_ARTIFACT_REQUIRED", path+"/inputs")
		}
		edges[name] = sortedKeys(dependencies)
		invocations := []string{}
		for _, dependency := range edges[name] {
			if generated[dependency] == "" || dependency == name {
				report.add("DEPENDENCY_REFERENCE_INVALID", path)
			}
			invocations = append(invocations, generated[dependency])
		}
		report.DependencyCount += len(invocations)
		plan.data.Tasks = append(plan.data.Tasks, Task{SourceNode: source(path, "CONTAINER", generated[name], generated[name]),
			Dependencies: invocations, Image: container.GetImage(), Command: container.GetCommand(), Args: container.GetArgs(), Runtime: runtime})
	}
	done := map[string]bool{}
	for range len(names) {
		for _, name := range names {
			ready := true
			for _, dependency := range edges[name] {
				ready = ready && done[dependency]
			}
			if ready {
				done[name] = true
			}
		}
	}
	if len(done) != len(names) {
		report.add("DEPENDENCY_CYCLE_OR_INVALID_REFERENCE", "/root/dag/tasks")
	}
	report.TaskCount = len(names)
	if len(report.Issues) > 0 || report.IssuesTruncated {
		return nil, report
	}
	encoded, err := json.Marshal(plan.data)
	if err != nil || len(encoded) > MaxIRBytes {
		report.add("PLAN_SIZE_NOT_SUPPORTED", "/")
		return nil, report
	}
	plan.digest, report.PlanDigest = hash(encoded), hash(encoded)
	report.Compilable = true
	return plan, report
}
