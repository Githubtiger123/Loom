package service

import (
	"context"
	"encoding/json"
	"time"

	wfapi "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"k8s.io/apimachinery/pkg/runtime"
)

// AuthorizePipelineRuntimeWorkload grants a run scope only. Runtime persistence
// must additionally check driver vs launcher role, assigned execution ID, inputs
// and publication rights in its own transaction; this is not a user-session API.
// Only the independent TLS machine listener mounts this API.
func (s *Service) AuthorizePipelineRuntimeWorkload(ctx context.Context, token string) (*runtimeapi.Scope, error) {
	if s.NativePipelineClient == nil {
		return nil, officialRuntimeUnavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pod, err := s.NativePipelineClient.AuthenticatePipelineRuntimePod(ctx, token)
	if err != nil {
		return nil, err
	}
	denied := func() (*runtimeapi.Scope, error) {
		return nil, apperr.New(apperr.CodeForbidden, "工作负载未绑定当前受管 Pipeline 执行")
	}
	attempt, err := s.Store.PipelineRuntimeAttempt(ctx, s.Cluster, pod.Namespace, pod.NamespaceUID, pod.WorkflowName, pod.WorkflowUID)
	if err != nil {
		return denied()
	}
	if attempt.EngineRevision == nil || *attempt.EngineRevision != kfpengine.EngineRelease || attempt.PlanHash == nil || len(attempt.Snapshot) == 0 || len(attempt.Snapshot) > runtimeapi.MaxMessageBytes {
		return denied()
	}
	var snapshot struct {
		RuntimePolicy runtimeapi.FrozenPolicy `json:"runtimePolicy"`
	}
	if json.Unmarshal(attempt.Snapshot, &snapshot) != nil {
		return denied()
	}
	policy := snapshot.RuntimePolicy
	if policy.Protocol != runtimeapi.Protocol || policy.EngineRevision != *attempt.EngineRevision || policy.PlanDigest == "" || policy.PlanDigest != *attempt.PlanHash ||
		policy.ServiceAccount != pod.ServiceAccount || policy.ServiceAccountUID != pod.ServiceAccountUID || policy.GroupInstanceID == "" {
		return denied()
	}
	run, err := s.Store.PipelineRun(ctx, attempt.GroupID, attempt.RunID, false)
	if err != nil || run.Engine != model.PipelineEngineNativeArgo || run.InstanceID != attempt.InstanceID || run.Cluster != s.Cluster || run.Namespace != pod.Namespace ||
		run.ExternalID == nil || *run.ExternalID != pod.WorkflowUID || run.ExecutionCleaned || run.DeletedAt != nil {
		return denied()
	}
	group, err := s.Store.Group(ctx, attempt.GroupID, false)
	if err != nil || group.DeletedAt != nil || group.InstanceID != policy.GroupInstanceID || group.Cluster != s.Cluster || group.Name != pod.Namespace ||
		group.NamespaceUID == nil || *group.NamespaceUID != pod.NamespaceUID {
		return denied()
	}
	ref := k8s.PipelineWorkflowRef{GroupID: attempt.GroupID, RunID: attempt.RunID, InstanceID: attempt.InstanceID,
		Namespace: pod.Namespace, NamespaceUID: pod.NamespaceUID, Name: pod.WorkflowName, UID: pod.WorkflowUID, PlanHash: *attempt.PlanHash}
	workflow, err := s.NativePipelineClient.ReadPipelineWorkflow(ctx, pod.Namespace, pod.WorkflowName)
	if err != nil || !k8s.PipelineWorkflowMatches(workflow, ref) || workflow.GetDeletionTimestamp() != nil || workflow.GetAnnotations()["loom.ml/pipeline-engine-release"] != policy.EngineRevision {
		return denied()
	}
	plan, err := s.readOfficialSubmissionPlan(ctx, attempt)
	observed := &wfapi.Workflow{}
	if err != nil || runtime.DefaultUnstructuredConverter.FromUnstructured(workflow.Object, observed) != nil || kfpengine.VerifyFrozenWorkflow(plan.Workflow, observed) != nil {
		return denied()
	}
	scope := &runtimeapi.Scope{GroupID: attempt.GroupID, RunID: attempt.RunID, InstanceID: attempt.InstanceID, Namespace: pod.Namespace, NamespaceUID: pod.NamespaceUID,
		WorkflowName: pod.WorkflowName, WorkflowUID: pod.WorkflowUID, PodName: pod.Name, PodUID: pod.UID, ServiceAccount: pod.ServiceAccount, ServiceAccountUID: pod.ServiceAccountUID, EngineRevision: policy.EngineRevision}
	if err := s.NativePipelineClient.BindPipelineRuntimeTask(ctx, pod, workflow, policy, scope); err != nil {
		return denied()
	}
	return scope, nil
}
