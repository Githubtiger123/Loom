// Package k8s 是平台对 Kubernetes 的唯一出口。
//
// 约束：
//   - 只做"对象怎么建、怎么改、怎么查"，不写业务判断
//   - service 层不允许直接拼 YAML，一律走这里的函数
//   - mapper.go 是表单 ↔ CR 的映射，严格限制用户可配置字段
package k8s

import (
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// GVR 常量：Kubeflow 与 Istio 的 CRD 全部走 dynamic client，
// 避免为了一个字段把整个 CRD 的 Go 类型搬进仓库。
var (
	ProfileGVR = schema.GroupVersionResource{
		Group: "kubeflow.org", Version: "v1", Resource: "profiles",
	}
	AuthzPolicyGVR = schema.GroupVersionResource{
		Group: "security.istio.io", Version: "v1beta1", Resource: "authorizationpolicies",
	}
)

// Config 是 k8s 层的静态配置，来自 config.yaml。
type Config struct {
	// IngressPrincipal 是 ingressgateway 的 SPIFFE 身份，
	// 必须与集群里实际的 SA 一致，否则所有成员的 AuthorizationPolicy 都不生效。
	IngressPrincipal string
	// KFPUIPrincipal 允许 KFP UI 代表用户访问空间内的服务。
	KFPUIPrincipal string
	// QuotaName 是 profile-controller 创建的 ResourceQuota 名。
	QuotaName string
	// EditorServiceAccount 是 Pod 使用的 SA，Notebook 模板里是固定的。
	EditorServiceAccount string
	// UserIDHeader / UserIDPrefix 必须与 profile-controller 完全一致，
	// 因为 AuthorizationPolicy 是按这个头名和前缀匹配身份的。
	UserIDHeader string
	UserIDPrefix string
}

func (c Config) withDefaults() Config {
	if c.IngressPrincipal == "" {
		c.IngressPrincipal = "cluster.local/ns/istio-system/sa/istio-ingressgateway-service-account"
	}
	if c.KFPUIPrincipal == "" {
		c.KFPUIPrincipal = "cluster.local/ns/kubeflow/sa/ml-pipeline-ui"
	}
	if c.QuotaName == "" {
		c.QuotaName = "kf-resource-quota"
	}
	if c.EditorServiceAccount == "" {
		c.EditorServiceAccount = "default-editor"
	}
	if c.UserIDHeader == "" {
		c.UserIDHeader = "kubeflow-userid"
	}
	return c
}

// Client 封装平台需要的三类客户端。
type Client struct {
	cfg  Config
	kube kubernetes.Interface
	dyn  dynamic.Interface
	logs *podlog.Reader
}

// New 用给定 rest.Config 构造客户端（本地开发用 kubeconfig）。
func New(restCfg *rest.Config, cfg Config) (*Client, error) {
	kube, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	// 普通 API 保留短超时；follow 日志不能被普通客户端的 10 秒总超时截断。
	logConfig := rest.CopyConfig(restCfg)
	logConfig.Timeout = 0
	logClient, err := kubernetes.NewForConfig(logConfig)
	if err != nil {
		return nil, fmt.Errorf("build pod log client: %w", err)
	}
	return &Client{cfg: cfg.withDefaults(), kube: kube, dyn: dyn, logs: podlog.New(logClient)}, nil
}

// NewInCluster 使用 Pod 内的 ServiceAccount。
func NewInCluster(cfg Config) (*Client, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	return New(restCfg, cfg)
}
