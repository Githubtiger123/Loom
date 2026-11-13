package k8s

import (
	"context"
	"strings"
	"time"

	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PipelineRuntimePod contains live, Kubernetes-verified identity, not labels
// supplied in a runtime HTTP body. The service layer still authorizes the run.
type PipelineRuntimePod struct {
	Namespace, NamespaceUID           string
	Name, UID                         string
	ServiceAccount, ServiceAccountUID string
	WorkflowName, WorkflowUID         string
}

// AuthenticatePipelineRuntimePod accepts only projected Pod-bound SA tokens for
// the runtime audience. Long-lived/unbound SA tokens and user kubeconfigs fail.
// No positive auth cache is used while the runtime contract is being assembled.
func (c *Client) AuthenticatePipelineRuntimePod(ctx context.Context, token string) (*PipelineRuntimePod, error) {
	denied := func() (*PipelineRuntimePod, error) {
		return nil, apperr.New(apperr.CodeUnauthorized, "Pipeline runtime 工作负载身份无法验证")
	}
	if !validator.IsWorkloadBearerToken(token) {
		return denied()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	review, err := c.kube.AuthenticationV1().TokenReviews().Create(ctx, &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{Token: token, Audiences: []string{runtimeapi.Audience}},
	}, metav1.CreateOptions{})
	if err != nil {
		// Some transports include request details in errors. Never wrap the
		// TokenReview request/error into a user response or application log.
		return nil, apperr.New(apperr.CodeUnavailable, "Pipeline runtime 身份校验服务暂不可用")
	}
	if !review.Status.Authenticated || review.Status.Error != "" || len(review.Status.Audiences) != 1 || review.Status.Audiences[0] != runtimeapi.Audience {
		return denied()
	}
	user := review.Status.User
	parts := strings.Split(user.Username, ":")
	if len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" || !validator.IsDNS1123Label(parts[2]) || !validator.IsDNS1123Subdomain(parts[3]) || user.UID == "" {
		return denied()
	}
	podNames := user.Extra["authentication.kubernetes.io/pod-name"]
	podUIDs := user.Extra["authentication.kubernetes.io/pod-uid"]
	if len(podNames) != 1 || len(podUIDs) != 1 || !validator.IsDNS1123Subdomain(podNames[0]) || !validator.IsCatalogID(podUIDs[0]) {
		return denied()
	}
	namespace, err := c.kube.CoreV1().Namespaces().Get(ctx, parts[2], metav1.GetOptions{})
	if err != nil || namespace.DeletionTimestamp != nil || string(namespace.UID) == "" {
		return denied()
	}
	account, err := c.kube.CoreV1().ServiceAccounts(parts[2]).Get(ctx, parts[3], metav1.GetOptions{})
	if err != nil || account.DeletionTimestamp != nil || string(account.UID) != user.UID {
		return denied()
	}
	pod, err := c.kube.CoreV1().Pods(parts[2]).Get(ctx, podNames[0], metav1.GetOptions{})
	if err != nil || string(pod.UID) != podUIDs[0] || pod.DeletionTimestamp != nil || pod.Spec.ServiceAccountName != account.Name {
		return denied()
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "argoproj.io/v1alpha1" || owner.Kind != "Workflow" || !validator.IsDNS1123Subdomain(owner.Name) || string(owner.UID) == "" {
		return denied()
	}
	return &PipelineRuntimePod{Namespace: namespace.Name, NamespaceUID: string(namespace.UID), Name: pod.Name, UID: string(pod.UID),
		ServiceAccount: account.Name, ServiceAccountUID: string(account.UID), WorkflowName: owner.Name, WorkflowUID: string(owner.UID)}, nil
}
