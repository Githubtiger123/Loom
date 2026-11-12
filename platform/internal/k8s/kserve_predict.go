package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PredictKServe never follows the URL projected by the controller. It uses the
// Kubernetes Service proxy for the fixed Standard-mode predictor, verifying the
// CR UID and the Service controller owner before sending user inference input.
func (c *Client) PredictKServe(ctx context.Context, ref KServeRef, protocol string, payload json.RawMessage) (json.RawMessage, error) {
	cr, err := c.GetKServe(ctx, ref)
	if err != nil {
		return nil, err
	}
	if cr == nil {
		return nil, apperr.New(apperr.CodeConflict, "模型服务尚未就绪")
	}
	observation, err := c.observeKServeStandard(ctx, cr)
	if err != nil {
		return nil, err
	}
	if !observation.Ready || observation.Revision != ref.Revision {
		return nil, apperr.New(apperr.CodeConflict, "模型服务尚未就绪")
	}
	svc, err := c.kube.CoreV1().Services(ref.Namespace).Get(ctx, ref.Name+"-predictor", metav1.GetOptions{})
	if err != nil {
		return nil, apperr.New(apperr.CodeUnavailable, "无法读取 predictor Service")
	}
	owned := false
	for _, owner := range svc.OwnerReferences {
		if string(owner.UID) == ref.UID && owner.Kind == "InferenceService" && owner.Controller != nil && *owner.Controller {
			owned = true
		}
	}
	if !owned || svc.Spec.Type == corev1.ServiceTypeExternalName || svc.Spec.Selector["app"] != "isvc."+svc.Name {
		return nil, apperr.New(apperr.CodeConflict, "predictor Service 归属不匹配")
	}
	port := int32(0)
	for _, candidate := range svc.Spec.Ports {
		if candidate.Port == 80 && candidate.Protocol == corev1.ProtocolTCP {
			port = 80
		}
	}
	if port == 0 {
		return nil, apperr.New(apperr.CodeUnavailable, "predictor Service 未提供 HTTP 80 端口")
	}
	modelPath := ref.Name + ":predict"
	version := "v1"
	if protocol == "v2" {
		modelPath = ref.Name
		version = "v2"
	}
	if c.predict == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "推理传输未初始化")
	}
	request := c.predict.CoreV1().RESTClient().Post().Namespace(ref.Namespace).Resource("services").Name(fmt.Sprintf("http:%s:%d", svc.Name, port)).SubResource("proxy").Suffix(version, "models", modelPath)
	if protocol == "v2" {
		request = request.Suffix("infer")
	}
	stream, err := request.MaxRetries(0).SetHeader("Content-Type", "application/json").Body([]byte(payload)).Stream(ctx)
	if err != nil {
		return nil, apperr.New(apperr.CodeUnavailable, "推理请求未成功；请核对模型协议、输入维度和 Service proxy/Istio 访问权限")
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || !json.Valid(data) {
		return nil, apperr.New(apperr.CodeUnavailable, "推理响应不是有效 JSON 或超过 1 MiB")
	}
	return json.RawMessage(data), nil
}
