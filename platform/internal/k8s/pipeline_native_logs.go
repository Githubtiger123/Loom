package k8s

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Client) resolveNativePipelinePod(ctx context.Context, ref PipelinePodRef) (podlog.Target, error) {
	bad := func() (podlog.Target, error) {
		return podlog.Target{}, apperr.New(apperr.CodeConflict, "原生节点 Pod/Workflow 绑定不匹配")
	}
	w := ref.NativeWorkflow
	if w == nil || w.Namespace != ref.Namespace || w.UID == "" || !validator.IsDNS1123Subdomain(ref.Pod) || !validator.IsCatalogID(ref.NativePodUID) ||
		ref.NativeNodeID == "" || ref.NativeNodeName == "" || ref.Container != "" && ref.Container != "main" {
		return bad()
	}
	workflow, err := c.ReadPipelineWorkflow(ctx, w.Namespace, w.Name)
	if err != nil {
		return podlog.Target{}, err
	}
	if !PipelineWorkflowMatches(workflow, *w) {
		return bad()
	}
	pod, err := c.kube.CoreV1().Pods(ref.Namespace).Get(ctx, ref.Pod, metav1.GetOptions{})
	if err != nil {
		return podlog.Target{}, err
	}
	owner := metav1.GetControllerOf(pod)
	if string(pod.UID) != ref.NativePodUID || owner == nil || owner.APIVersion != "argoproj.io/v1alpha1" || owner.Kind != "Workflow" || owner.Name != w.Name || string(owner.UID) != w.UID ||
		pod.Annotations["workflows.argoproj.io/node-name"] != ref.NativeNodeName {
		return bad()
	}
	if id := pod.Annotations["workflows.argoproj.io/node-id"]; id != "" && id != ref.NativeNodeID {
		return bad()
	}
	return podlog.Target{Namespace: ref.Namespace, Pod: ref.Pod, PodUID: ref.NativePodUID, Container: "main"}, nil
}
