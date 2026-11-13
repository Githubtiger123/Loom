package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strconv"
	"strings"
)

// BindPipelineRuntimeTask verifies a live Argo Pod node and the concrete runtime
// arguments. This is not isolation from a namespace administrator who can edit
// Workflow status or run arbitrary Pods with the execution service account.
func (c *Client) BindPipelineRuntimeTask(ctx context.Context, identity *PipelineRuntimePod, workflow *unstructured.Unstructured, policy runtimeapi.FrozenPolicy, scope *runtimeapi.Scope) error {
	denied := fmt.Errorf("runtime Pod is not an assigned official task")
	pod, err := c.kube.CoreV1().Pods(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	if err != nil {
		return denied
	}
	if string(pod.UID) != identity.UID || pod.DeletionTimestamp != nil {
		return denied
	}
	nodeID := pod.Annotations["workflows.argoproj.io/node-id"]
	if nodeID == "" {
		nodeID = pod.Labels["workflows.argoproj.io/node-id"]
	}
	if label := pod.Labels["workflows.argoproj.io/node-id"]; label != "" && label != nodeID {
		return denied
	}
	node, found, err := unstructured.NestedMap(workflow.Object, "status", "nodes", nodeID)
	if err != nil || !found || nodeID == "" || node["id"] != nodeID || node["type"] != "Pod" || pod.Labels["workflows.argoproj.io/workflow"] != workflow.GetName() {
		return denied
	}
	template, _ := node["templateName"].(string)
	var image string
	var command, args []string
	for _, container := range pod.Spec.Containers {
		if container.Name == "main" {
			image, command, args = container.Image, container.Command, container.Args
		}
	}
	if len(command) == 0 {
		return denied
	}
	// The official driver uses Command=[driver], Args=[flags]. The component
	// podSpecPatch puts launcher+flags+-- in Command and user argv in Args.
	// Parse the real Kubernetes argv, not just Args.
	argv := append(append([]string{}, command[1:]...), args...)
	flags, _, err := runtimeapi.FlagValues(argv)
	if err != nil {
		return denied
	}
	if flags["run_id"] != identity.WorkflowUID || flags["pipeline_name"] != policy.PipelineName {
		return denied
	}
	scope.NodeID = nodeID
	scope.NodeName, _ = node["name"].(string)
	if scope.NodeName == "" || pod.Annotations["workflows.argoproj.io/node-name"] != scope.NodeName {
		return denied
	}
	scope.IterationIndex = -1
	if len(command) == 1 && image == policy.DriverImage && (command[0] == "driver" || command[0] == "/bin/driver") && strings.HasPrefix(template, "system-") && strings.Contains(template, "driver") {
		switch flags["type"] {
		case "ROOT_DAG", "DAG", "CONTAINER":
			scope.Role = "DRIVER_" + flags["type"]
		default:
			return denied
		}
		scope.TaskName = flags["task_name"]
		if value := flags["dag_execution_id"]; value != "" {
			scope.ParentDAGID, err = strconv.ParseInt(value, 10, 64)
			if err != nil || scope.ParentDAGID < 0 {
				return denied
			}
		}
		if value := flags["iteration_index"]; value != "" {
			scope.IterationIndex, err = strconv.Atoi(value)
			if err != nil || scope.IterationIndex < -1 {
				return denied
			}
		}
		return nil
	}
	if command[0] != "/kfp-launcher/launch" || !strings.Contains(template, "container-impl") {
		return denied
	}
	launcherFound := false
	for _, container := range pod.Spec.InitContainers {
		if container.Image == policy.LauncherImage {
			launcherFound = true
		}
	}
	if !launcherFound || flags["pod_name"] != identity.Name || flags["pod_uid"] != identity.UID {
		return denied
	}
	scope.ExecutionID, err = strconv.ParseInt(flags["execution_id"], 10, 64)
	if err != nil || scope.ExecutionID <= 0 {
		return denied
	}
	// The concrete pod-spec-patch is itself an input on the observed Argo node.
	// It must bind the same assigned ID, not just an arbitrary valid SQL ID.
	inputs, _, _ := unstructured.NestedSlice(node, "inputs", "parameters")
	assigned := false
	for _, input := range inputs {
		item, ok := input.(map[string]any)
		if !ok || item["name"] != "pod-spec-patch" {
			continue
		}
		value, _ := item["value"].(string)
		var patch corev1.PodSpec
		if len(value) > runtimeapi.MaxMessageBytes/2 || json.Unmarshal([]byte(value), &patch) != nil {
			return denied
		}
		for _, container := range patch.Containers {
			if container.Name != "main" {
				continue
			}
			if len(container.Command) == 0 || container.Command[0] != "/kfp-launcher/launch" {
				continue
			}
			patchArgv := append(append([]string{}, container.Command[1:]...), container.Args...)
			expected, _, e := runtimeapi.FlagValues(patchArgv)
			if e == nil && expected["execution_id"] == flags["execution_id"] && expected["run_id"] == identity.WorkflowUID && expected["pipeline_name"] == policy.PipelineName {
				assigned = true
			}
		}
	}
	if !assigned {
		return denied
	}
	scope.Role = "LAUNCHER"
	return nil
}
