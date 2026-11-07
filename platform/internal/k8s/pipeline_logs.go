package k8s

import (
	"context"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type PipelinePodRef struct {
	Namespace   string
	RunID       string
	Pod         string // 来自已核实身份的 KFP task_details，不是任意用户输入。
	Container   string
	ExecutionID string // KFP 漏填 pod_name 时，按受管 launcher execution ID 精确匹配。
}

// ResolvePipelinePod 校验 Pod -> Workflow UID -> KFP Run 的执行归属。
// 不提供按 namespace 任意读取日志的业务接口；非 Argo/非直接 Workflow owner 明确拒绝。
func (c *Client) ResolvePipelinePod(ctx context.Context, ref PipelinePodRef) (podlog.Target, error) {
	if !validator.IsDNS1123Label(ref.Namespace) || (ref.Pod != "" && !validator.IsSecretName(ref.Pod)) || !validator.IsCatalogID(ref.RunID) {
		return podlog.Target{}, apperr.New(apperr.CodeConflict, "KFP 节点尚无合法 Pod 映射")
	}
	if ref.Pod == "" {
		id, err := strconv.ParseInt(ref.ExecutionID, 10, 64)
		if err != nil || id <= 0 {
			return podlog.Target{}, apperr.New(apperr.CodeConflict, "节点没有 Pod 名称或可验证的 execution ID")
		}
		var next, name string
		for page := 0; page < 5; page++ {
			pods, err := c.kube.CoreV1().Pods(ref.Namespace).List(ctx, metav1.ListOptions{
				LabelSelector: labels.Set{"pipeline/runid": ref.RunID}.String(), Limit: 200, Continue: next,
			})
			if err != nil {
				return podlog.Target{}, apperr.Wrap(apperr.CodeUnavailable, "无法解析节点的在线 Pod", err)
			}
			for _, pod := range pods.Items {
				if !pipelineLauncherMatches(&pod, ref) {
					continue
				}
				if name != "" {
					return podlog.Target{}, apperr.New(apperr.CodeConflict, "同一节点 execution ID 匹配多个 Pod，拒绝猜测执行实例")
				}
				name = pod.Name
			}
			next = pods.Continue
			if next == "" {
				break
			}
		}
		if next != "" || name == "" {
			return podlog.Target{}, apperr.New(apperr.CodeConflict, "未找到唯一节点 Pod，可能尚未启动、命中缓存或已清理")
		}
		ref.Pod = name
	}
	pod, err := c.kube.CoreV1().Pods(ref.Namespace).Get(ctx, ref.Pod, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return podlog.Target{}, apperr.New(apperr.CodeNotFound, "Pod 已清理，在线日志不可读；离线日志需要事先归档")
	}
	if err != nil {
		return podlog.Target{}, apperr.Wrap(apperr.CodeUnavailable, "在线 Pod 不可读；Pod 已清理时需要归档日志", err)
	}
	if pod.Namespace != ref.Namespace || pod.Labels["pipeline/runid"] != ref.RunID || pod.UID == "" {
		return podlog.Target{}, apperr.New(apperr.CodeConflict, "Pod 不属于指定 KFP Run")
	}
	if ref.ExecutionID != "" && !pipelineLauncherMatches(pod, ref) {
		return podlog.Target{}, apperr.New(apperr.CodeConflict, "Pod 的受管 launcher 与节点 execution ID 不匹配")
	}
	for _, owner := range pod.OwnerReferences {
		if owner.APIVersion != "argoproj.io/v1alpha1" || owner.Kind != "Workflow" || owner.Controller == nil || !*owner.Controller {
			continue
		}
		workflow, err := c.dyn.Resource(schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "workflows"}).
			Namespace(ref.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err != nil {
			return podlog.Target{}, apperr.Wrap(apperr.CodeUnavailable, "无法核实 Pod 的 Workflow 归属", err)
		}
		if workflow.GetUID() != owner.UID || workflow.GetNamespace() != ref.Namespace || workflow.GetLabels()["pipeline/runid"] != ref.RunID {
			return podlog.Target{}, apperr.New(apperr.CodeConflict, "Workflow UID 或 KFP Run 归属不一致")
		}
		container := ref.Container
		if container == "" {
			container = "main"
		}
		return podlog.Target{Namespace: ref.Namespace, Pod: ref.Pod, PodUID: string(pod.UID), Container: container}, nil
	}
	return podlog.Target{}, apperr.New(apperr.CodeConflict, "当前只支持归属可验证的 Argo Workflow Pod 日志")
}

// 仅识别本仓库 KFP v2 注入的 launcher 参数，遇到 -- 就停止；不从用户命令猜测关联。
func pipelineLauncherMatches(pod *corev1.Pod, ref PipelinePodRef) bool {
	if pod.Namespace != ref.Namespace || pod.Labels["pipeline/runid"] != ref.RunID {
		return false
	}
	for _, container := range pod.Spec.Containers {
		if container.Name != "main" || len(container.Command) == 0 || container.Command[0] != "/kfp-launcher/launch" {
			continue
		}
		args := append(append([]string{}, container.Command[1:]...), container.Args...)
		run, execution := "", ""
		for i := 0; i < len(args); i++ {
			if args[i] == "--" {
				return run == ref.RunID && execution == ref.ExecutionID
			}
			if args[i] == "--run_id" || args[i] == "--execution_id" {
				if i+1 == len(args) {
					return false
				}
				if args[i] == "--run_id" {
					if run != "" {
						return false
					}
					run = args[i+1]
				} else {
					if execution != "" {
						return false
					}
					execution = args[i+1]
				}
				i++
			}
		}
	}
	return false
}

func (c *Client) OpenPodLogs(ctx context.Context, target podlog.Target, options podlog.Options) (*podlog.Stream, error) {
	return c.logs.Open(ctx, target, options)
}
