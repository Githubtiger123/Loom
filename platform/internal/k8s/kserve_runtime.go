package k8s

import (
	"context"
	"encoding/json"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	servingRuntimeGVR        = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1alpha1", Resource: "servingruntimes"}
	clusterServingRuntimeGVR = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1alpha1", Resource: "clusterservingruntimes"}
)

// Model/protocol matching follows KServe's ModelSpec.RuntimeSupportsModel and
// ServingRuntimeSpec.IsProtocolVersionSupported (Apache-2.0). The controller
// remains responsible for priority/defaulting and the final runtime selection.
func runtimeSupports(r dto.KServeRuntime, spec dto.KServeSpec) bool {
	if spec.Model == nil || r.Disabled || r.MultiModel || r.MultiNode {
		return false
	}
	if spec.Model.Runtime != "" && spec.Model.Runtime != r.Name {
		return false
	}
	protocolOK := len(r.Protocols) == 0
	for _, protocol := range r.Protocols {
		protocolOK = protocolOK || protocol == spec.Protocol
	}
	if !protocolOK {
		return false
	}
	for _, format := range r.Formats {
		if format.Name == spec.Model.ModelFormat && (spec.Model.ModelFormatVersion == "" || spec.Model.ModelFormatVersion == format.Version) &&
			(spec.Model.Runtime != "" || format.AutoSelect) {
			return true
		}
	}
	return false
}

func publicKServeRuntime(n *unstructured.Unstructured, scope string) (dto.KServeRuntime, error) {
	var spec struct {
		Disabled              bool                    `json:"disabled"`
		MultiModel            bool                    `json:"multiModel"`
		WorkerSpec            json.RawMessage         `json:"workerSpec"`
		ProtocolVersions      []string                `json:"protocolVersions"`
		SupportedModelFormats []dto.KServeModelFormat `json:"supportedModelFormats"`
	}
	b, err := json.Marshal(n.Object["spec"])
	if err != nil {
		return dto.KServeRuntime{}, err
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		return dto.KServeRuntime{}, err
	}
	if spec.ProtocolVersions == nil {
		spec.ProtocolVersions = []string{}
	}
	if spec.SupportedModelFormats == nil {
		spec.SupportedModelFormats = []dto.KServeModelFormat{}
	}
	return dto.KServeRuntime{Name: n.GetName(), Scope: scope, Disabled: spec.Disabled, MultiModel: spec.MultiModel,
		MultiNode: len(spec.WorkerSpec) > 0 && string(spec.WorkerSpec) != "null", Protocols: spec.ProtocolVersions, Formats: spec.SupportedModelFormats}, nil
}

func (c *Client) KServeRuntimes(ctx context.Context, namespace string) (*dto.KServeRuntimeList, error) {
	out := &dto.KServeRuntimeList{Namespace: namespace, Items: []dto.KServeRuntime{}, ObservedAt: time.Now().UTC()}
	// Namespace-scoped runtime names shadow cluster-scoped names for explicit
	// selection, exactly as GetServingRuntime does in the official controller.
	for _, source := range []struct {
		gvr   schema.GroupVersionResource
		scope string
	}{{servingRuntimeGVR, "NAMESPACE"}, {clusterServingRuntimeGVR, "CLUSTER"}} {
		runtimeNamespace := namespace
		if source.scope == "CLUSTER" {
			runtimeNamespace = ""
		}
		token := ""
		for {
			list, err := c.dyn.Resource(source.gvr).Namespace(runtimeNamespace).List(ctx, metav1.ListOptions{Limit: 200, Continue: token})
			if apierrors.IsNotFound(err) && source.scope == "CLUSTER" {
				break
			}
			if err != nil {
				return nil, err
			}
			for i := range list.Items {
				runtime, err := publicKServeRuntime(&list.Items[i], source.scope)
				if err != nil {
					return nil, err
				}
				out.Items = append(out.Items, runtime)
				if len(out.Items) > 1000 {
					return nil, apperr.New(apperr.CodeUnavailable, "Runtime 数量超过安全读取上限")
				}
			}
			token = list.GetContinue()
			if token == "" {
				break
			}
		}
	}
	return out, nil
}

func (c *Client) KServePreflight(ctx context.Context, namespace, account, storageSecret string, spec dto.KServeSpec) (*dto.KServePreflight, error) {
	out := &dto.KServePreflight{Ready: true, Checks: []dto.KServeCheck{}, Runtimes: []dto.KServeRuntime{}, StorageAccess: "NOT_VERIFIED", ObservedAt: time.Now().UTC()}
	check := func(code string, passed bool, message string) {
		out.Checks = append(out.Checks, dto.KServeCheck{Code: code, Passed: passed, Message: message})
		out.Ready = out.Ready && passed
	}
	_, err := c.dyn.Resource(KServeGVR).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 1, LabelSelector: labelManagedBy + "=" + managedByLoom})
	if apierrors.IsNotFound(err) {
		check("KSERVE_API_MISSING", false, "InferenceService API 尚未安装")
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	check("KSERVE_API", true, "InferenceService API 可读取；不代表 Controller/webhook 已健康")
	sa, err := c.kube.CoreV1().ServiceAccounts(namespace).Get(ctx, account, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		check("SERVICE_ACCOUNT_MISSING", false, "配置的模型执行 ServiceAccount 不存在")
	} else if err != nil {
		return nil, err
	} else {
		check("SERVICE_ACCOUNT", true, "模型执行 ServiceAccount 存在")
		if spec.DeploymentType == "MODEL" && storageSecret != "" {
			linked := false
			for _, secret := range sa.Secrets {
				linked = linked || secret.Name == storageSecret
			}
			check("STORAGE_SECRET_BINDING", linked, "检查运维配置的存储 Secret 是否关联；不读取 Secret 值或验证实际存储权限")
		}
	}
	if spec.DeploymentType == "CUSTOM" {
		return out, nil
	}
	runtimes, err := c.KServeRuntimes(ctx, namespace)
	if apierrors.IsNotFound(err) {
		check("RUNTIME_API_MISSING", false, "ServingRuntime API 尚未安装")
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	shadow := map[string]bool{}
	for _, runtime := range runtimes.Items {
		if runtime.Scope == "NAMESPACE" {
			shadow[runtime.Name] = true
		}
	}
	for _, runtime := range runtimes.Items {
		if spec.Model.Runtime != "" && runtime.Scope == "CLUSTER" && shadow[runtime.Name] {
			continue
		}
		if runtimeSupports(runtime, spec) {
			out.Runtimes = append(out.Runtimes, runtime)
		}
	}
	check("SUPPORTING_RUNTIME", len(out.Runtimes) > 0, "检查可用单模型、单节点 Runtime 的格式、版本、协议和 autoSelect；最终选择由 KServe 决定")
	return out, nil
}
