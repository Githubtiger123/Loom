package k8s

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EnvVar 是环境变量的两种形态：明文值或 Secret 引用（只存引用）。
type EnvVar struct {
	Name       string
	Value      string
	SecretName string
	SecretKey  string
}

// NotebookInput 是"已通过校验与注入"的最终输入。
// 用户改不了的东西（SA、securityContext、nodeSelector）都在这里由平台填好。
type NotebookInput struct {
	Namespace string
	Name      string // DNS-1123，≤63
	Group     string // 组名 = namespace
	Creator   string

	Image      string // 完整镜像地址（含 digest 优先）
	ImageID    string // 白名单 id，仅用于统计与排查
	ServerType string // jupyter / group-one / group-two，决定 HTTP 访问适配，不覆盖镜像启动命令

	CPU     string // "4"
	Memory  string // "16Gi"
	GPU     int    // 0 表示不申请加速卡
	ShmSize string // "/dev/shm" 的 sizeLimit，空则 64Mi

	WorkspacePVC  string // 一期每实例一个 PVC
	WorkspaceSize string

	EditorServiceAccount string

	DisplayName  string
	Description  string
	Env          []EnvVar
	Labels       map[string]string
	NodeSelector map[string]string // 组资源池翻译而来，用户不可见
}

const (
	annotationDisplayName = "platform.example.com/display-name"
	annotationDescription = "platform.example.com/description"
	annotationImageID     = "platform.example.com/image-id"
	annotationCreator     = "platform.example.com/creator"
	labelCreator          = "platform.example.com/creator"
	labelGroup            = "platform.example.com/group"

	defaultShmSize = "64Mi"
	workspacePath  = "/home/jovyan"
)

// BuildNotebook 把平台输入渲染成 Notebook CR。
//
// 这里刻意只暴露"表单能填的字段"，其余全部由平台写入：
// serviceAccountName / securityContext / volumes / volumeMounts / resources。
// K8s RBAC 与 nodeSelector 等危险字段永远不从用户输入取值。
func BuildNotebook(in NotebookInput) *unstructured.Unstructured {
	shm := in.ShmSize
	if shm == "" {
		shm = defaultShmSize
	}
	sa := in.EditorServiceAccount
	if sa == "" {
		sa = "default-editor"
	}

	// requests == limits，保证 Guaranteed，也保证 ResourceQuota 统计准确
	resources := map[string]any{
		"requests": resourceList(in.CPU, in.Memory, in.GPU),
		"limits":   resourceList(in.CPU, in.Memory, in.GPU),
	}

	container := map[string]any{
		"name":            in.Name,
		"image":           in.Image,
		"imagePullPolicy": "IfNotPresent", // 固定，不开放给用户
		"workingDir":      workspacePath,
		"resources":       resources,
		"volumeMounts": []any{
			map[string]any{"name": "workspace", "mountPath": workspacePath},
			map[string]any{"name": "dshm", "mountPath": "/dev/shm"},
		},
	}
	if envs := buildEnv(in.Env); len(envs) > 0 {
		container["env"] = envs
	}

	podSpec := map[string]any{
		"serviceAccountName": sa,
		"securityContext": map[string]any{
			"runAsNonRoot": true,
			"fsGroup":      int64(100),
		},
		"containers": []any{container},
		"volumes": []any{
			map[string]any{
				"name":                  "workspace",
				"persistentVolumeClaim": map[string]any{"claimName": in.WorkspacePVC},
			},
			map[string]any{
				"name": "dshm",
				"emptyDir": map[string]any{
					"medium":    "Memory",
					"sizeLimit": shm,
				},
			},
		},
	}
	if len(in.NodeSelector) > 0 {
		ns := make(map[string]any, len(in.NodeSelector))
		for k, v := range in.NodeSelector {
			ns[k] = v
		}
		podSpec["nodeSelector"] = ns
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubeflow.org/v1beta1",
		"kind":       "Notebook",
		"metadata": map[string]any{
			"name":        in.Name,
			"namespace":   in.Namespace,
			"labels":      buildLabels(in),
			"annotations": buildAnnotations(in),
		},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": podSpec,
			},
		},
	}}
}

func resourceList(cpu, memory string, gpu int) map[string]any {
	rl := map[string]any{
		"cpu":    cpu,
		"memory": memory,
	}
	if gpu > 0 {
		rl["nvidia.com/gpu"] = int64(gpu)
	}
	return rl
}

func buildEnv(envs []EnvVar) []any {
	out := make([]any, 0, len(envs))
	for _, e := range envs {
		if e.SecretName != "" {
			out = append(out, map[string]any{
				"name": e.Name,
				"valueFrom": map[string]any{
					"secretKeyRef": map[string]any{
						"name": e.SecretName,
						"key":  e.SecretKey,
					},
				},
			})
			continue
		}
		out = append(out, map[string]any{"name": e.Name, "value": e.Value})
	}
	return out
}

func buildLabels(in NotebookInput) map[string]any {
	labels := map[string]any{}
	for k, v := range in.Labels {
		labels[k] = v
	}
	// 标签保存原始身份的稳定摘要；完整身份放注解，不将邮箱写入 label。
	// 平台标签最后写，防止被用户覆盖。
	labels[labelCreator] = identityDigest(in.Creator)
	labels[labelGroup] = in.Group
	return labels
}

func buildAnnotations(in NotebookInput) map[string]any {
	ann := map[string]any{
		annotationImageID: in.ImageID,
		annotationCreator: in.Creator,
	}
	for key, value := range notebookServerAnnotations(in.ServerType, in.Namespace, in.Name) {
		ann[key] = value
	}
	if in.DisplayName != "" {
		ann[annotationDisplayName] = in.DisplayName
	}
	if in.Description != "" {
		ann[annotationDescription] = in.Description
	}
	return ann
}
