package k8s

import (
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

const (
	annotationNotebookServerType = "notebooks.kubeflow.org/server-type"
	annotationNotebookRewriteURI = "notebooks.kubeflow.org/http-rewrite-uri"
	annotationNotebookHeaders    = "notebooks.kubeflow.org/http-headers-request-set"
)

// 三类环境使用相同外部路径，差异只交由 notebook-controller 渲染到 VirtualService。
// 启动命令由兼容镜像自身提供，不向用户开放 command/args 或任意路由配置。
func notebookServerAnnotations(serverType, namespace, name string) map[string]string {
	serverType = validator.NotebookServerTypeOrDefault(serverType)
	annotations := map[string]string{annotationNotebookServerType: serverType}
	if serverType == "group-one" || serverType == "group-two" {
		annotations[annotationNotebookRewriteURI] = "/"
	}
	if serverType == "group-two" {
		headers, _ := json.Marshal(map[string]string{"X-RStudio-Root-Path": fmt.Sprintf("/notebook/%s/%s/", namespace, name)})
		annotations[annotationNotebookHeaders] = string(headers)
	}
	return annotations
}

func notebookServerAnnotationsMatch(have map[string]string, ref NotebookRef) bool {
	serverType := validator.NotebookServerTypeOrDefault(ref.ServerType)
	if !validator.IsNotebookServerType(serverType) {
		return false
	}
	want := notebookServerAnnotations(serverType, ref.Namespace, ref.Name)
	for _, key := range []string{annotationNotebookServerType, annotationNotebookRewriteURI, annotationNotebookHeaders} {
		value, exists := have[key]
		expected, required := want[key]
		// 旧 Loom 创建的 CR 未写类型，且默认就是 Jupyter 前缀路径。
		if key == annotationNotebookServerType && !exists && serverType == "jupyter" {
			continue
		}
		if exists != required || value != expected {
			return false
		}
	}
	return true
}
