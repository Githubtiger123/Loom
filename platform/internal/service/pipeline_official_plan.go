package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	wfapi "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/kubeflow/pipelines/backend/src/v2/objectstore"
	"gorm.io/gorm"
)

type officialAttemptSnapshot struct {
	model.PipelineRun
	RuntimePolicy runtimeapi.FrozenPolicy `json:"runtimePolicy"`
}

type officialSubmissionPlan struct {
	Attempt  *model.PipelineRunAttempt
	Policy   runtimeapi.FrozenPolicy
	Workflow *wfapi.Workflow
}

// prepareOfficialPipelineAttempt freezes everything before any Kubernetes write.
// Recovery reads this persisted plan, not today's deployment configuration. It
// is the backend's preparation primitive, not evidence of execution success.
func (s *Service) prepareOfficialPipelineAttempt(ctx context.Context, claim *MLExecutionClaim) (*officialSubmissionPlan, error) {
	if claim == nil || claim.MLExecutionView == nil || claim.Space == nil || claim.Command.PipelineRun == nil || claim.Command.Action != MLPipelineSubmit || s.NativePipelineClient == nil {
		return nil, mlExecutionInvalid()
	}
	c := claim.Command
	existing, err := s.Store.PipelineRunAttempt(ctx, c.GroupID, c.ResourceID, c.InstanceID, false)
	if err == nil {
		return s.readOfficialSubmissionPlan(ctx, existing)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	accountUID, err := s.NativePipelineClient.PipelineRuntimeAccountUID(ctx, c.Namespace, claim.Space.NamespaceUID, s.PipelineConfig.NativeServiceAccount)
	if err != nil {
		return nil, err
	}
	request, err := s.officialCompileRequest(c.PipelineRun.PipelineVersionSpec, c.PipelineRun.Values, kfpengine.Identity{GroupID: c.GroupID, RunID: c.ResourceID, InstanceID: c.InstanceID, Namespace: c.Namespace, NamespaceUID: claim.Space.NamespaceUID})
	if err != nil {
		return nil, err
	}
	if request.Options.RuntimeEndpoint == "" {
		return nil, officialRuntimeUnavailable()
	}
	root, rootErr := url.Parse(request.Options.PipelineRoot)
	if rootErr != nil || (root.Scheme != "s3" && root.Scheme != "minio") || root.Host != s.PipelineConfig.NativeArtifactBucket || s.PipelineConfig.NativeArtifactSecret == "" {
		return nil, officialRuntimeUnavailable()
	}
	compiled, err := kfpengine.Compile(ctx, request)
	if err != nil {
		return nil, err
	}
	// SessionInfo contains only a namespaced credential reference and operator
	// endpoint settings. It never contains an access key, password or token.
	session := objectstore.SessionInfo{Provider: "s3", Params: map[string]string{
		objectstore.S3ParamFromEnv: "false", objectstore.S3ParamSecretName: s.PipelineConfig.NativeArtifactSecret,
		objectstore.S3ParamAccessKeyKey: "accessKey", objectstore.S3ParamSecretKeyKey: "secretKey",
		objectstore.S3ParamEndpoint: s.PipelineConfig.NativeArtifactEndpoint, objectstore.S3ParamRegion: s.PipelineConfig.NativeArtifactRegion,
		objectstore.S3ParamDisableSSL: strconv.FormatBool(s.PipelineConfig.NativeArtifactInsecure), objectstore.S3ParamForcePathStyle: "true",
	}}
	if _, err := objectstore.ParseBucketConfig(request.Options.PipelineRoot, session); err != nil {
		return nil, err
	}
	storeJSON, err := json.Marshal(session)
	if err != nil {
		return nil, err
	}
	var ir struct {
		PipelineInfo struct {
			Name string `json:"name"`
		} `json:"pipelineInfo"`
	}
	if json.Unmarshal(request.PipelineIR, &ir) != nil || ir.PipelineInfo.Name == "" {
		return nil, nativeFinalConflict()
	}
	policy := runtimeapi.FrozenPolicy{Protocol: runtimeapi.Protocol, GroupInstanceID: claim.Space.InstanceID, ServiceAccount: request.Options.ServiceAccount, ServiceAccountUID: accountUID,
		EngineRevision: compiled.EngineRevision, PlanDigest: compiled.WorkflowDigest, PipelineName: ir.PipelineInfo.Name, PipelineID: c.PipelineRun.PipelineID,
		PipelineRoot: request.Options.PipelineRoot, StoreSessionInfo: string(storeJSON), CacheEnabled: !request.Options.CacheDisabled, DriverImage: request.Options.DriverImage, LauncherImage: request.Options.LauncherImage}
	var out *officialSubmissionPlan
	err = repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, authoritative, target, receipt, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, authoritative, target, receipt, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		run, ok := target.row.(*model.PipelineRun)
		if !ok || run.Engine != model.PipelineEngineNativeArgo || run.ExecutionCleaned || authoritative.Action != MLPipelineSubmit || run.InstanceID != c.InstanceID || run.ExternalID != nil ||
			receipt.NamespaceUID == nil || *receipt.NamespaceUID != claim.Space.NamespaceUID || receipt.GroupInstanceID == nil || *receipt.GroupInstanceID != policy.GroupInstanceID {
			return mlExecutionConflict()
		}
		original, current := *c.PipelineRun, *authoritative.PipelineRun
		original.PipelineSpec, err = canonicalPipelineIR(original.PipelineSpec)
		if err != nil {
			return err
		}
		current.PipelineSpec, err = canonicalPipelineIR(current.PipelineSpec)
		if err != nil {
			return err
		}
		left, _ := json.Marshal(original)
		right, _ := json.Marshal(current)
		if mlHash(left) != mlHash(right) {
			return mlExecutionConflict()
		}
		attempt, err := st.PipelineRunAttempt(ctx, c.GroupID, c.ResourceID, c.InstanceID, true)
		if err == nil {
			out = &officialSubmissionPlan{Attempt: attempt}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		snapshot, err := json.Marshal(officialAttemptSnapshot{PipelineRun: *run, RuntimePolicy: policy})
		if err != nil || len(snapshot) > runtimeapi.MaxMessageBytes {
			return runtimeInvalid()
		}
		planRow := &model.PipelineRuntimeObject{GroupID: c.GroupID, RunID: c.ResourceID, InstanceID: c.InstanceID, Kind: "workflow-plan", ObjectKey: mlHash([]byte("workflow-plan")), WriterPodUID: "loom-control"}
		if err := st.SaveRuntimeObject(ctx, planRow, compiled.Workflow); err != nil {
			return err
		}
		ref := "runtime-object:" + strconv.FormatInt(planRow.ID, 10)
		attempt = &model.PipelineRunAttempt{InstanceID: c.InstanceID, RunID: c.ResourceID, Revision: c.Revision, GroupID: c.GroupID, Snapshot: snapshot, Engine: run.Engine, EngineRevision: &compiled.EngineRevision,
			VersionID: &run.VersionID, VersionRevision: run.VersionRevision, ClusterID: &run.Cluster, Namespace: &run.Namespace, NamespaceUID: &claim.Space.NamespaceUID, WorkflowName: &compiled.Workflow.Name, PlanHash: &compiled.WorkflowDigest, PlanRef: &ref}
		if err := st.DB.WithContext(ctx).Create(attempt).Error; err != nil {
			return err
		}
		out = &officialSubmissionPlan{Attempt: attempt, Policy: policy, Workflow: compiled.Workflow}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out.Workflow == nil {
		return s.readOfficialSubmissionPlan(ctx, out.Attempt)
	}
	return out, nil
}

func (s *Service) readOfficialSubmissionPlan(ctx context.Context, attempt *model.PipelineRunAttempt) (*officialSubmissionPlan, error) {
	if attempt == nil || attempt.Engine != model.PipelineEngineNativeArgo || attempt.EngineRevision == nil || *attempt.EngineRevision != kfpengine.EngineRelease || attempt.PlanHash == nil || attempt.WorkflowName == nil || attempt.Namespace == nil || attempt.NamespaceUID == nil || attempt.PlanRef == nil {
		return nil, nativeFinalConflict()
	}
	var snapshot officialAttemptSnapshot
	if json.Unmarshal(attempt.Snapshot, &snapshot) != nil || snapshot.RuntimePolicy.Protocol != runtimeapi.Protocol || snapshot.RuntimePolicy.EngineRevision != kfpengine.EngineRelease || snapshot.RuntimePolicy.PlanDigest != *attempt.PlanHash || snapshot.ID != attempt.RunID || snapshot.GroupID != attempt.GroupID || snapshot.InstanceID != attempt.InstanceID || snapshot.Namespace != *attempt.Namespace || snapshot.RuntimePolicy.GroupInstanceID == "" || snapshot.RuntimePolicy.ServiceAccountUID == "" {
		return nil, nativeFinalConflict()
	}
	row, err := s.Store.RuntimeObjectByKey(ctx, runtimeapi.Scope{GroupID: attempt.GroupID, RunID: attempt.RunID, InstanceID: attempt.InstanceID}, "workflow-plan", mlHash([]byte("workflow-plan")))
	if err != nil {
		return nil, err
	}
	if *attempt.PlanRef != "runtime-object:"+strconv.FormatInt(row.ID, 10) {
		return nil, nativeFinalConflict()
	}
	workflow := &wfapi.Workflow{}
	if json.Unmarshal(row.Value, workflow) != nil || kfpengine.VerifyFrozenWorkflow(workflow, workflow) != nil || workflow.Name != *attempt.WorkflowName || workflow.Namespace != *attempt.Namespace || workflow.Spec.ServiceAccountName != snapshot.RuntimePolicy.ServiceAccount || workflow.Annotations["loom.ml/pipeline-plan-hash"] != *attempt.PlanHash || workflow.Annotations["loom.ml/instance-id"] != attempt.InstanceID || workflow.Annotations["loom.ml/pipeline-namespace-uid"] != *attempt.NamespaceUID || workflow.Labels["loom.ml/group-id"] != strconv.FormatUint(attempt.GroupID, 10) || workflow.Labels["loom.ml/pipeline-run-id"] != strconv.FormatUint(attempt.RunID, 10) || !strings.HasSuffix(snapshot.RuntimePolicy.PipelineRoot, "/"+strconv.FormatUint(attempt.GroupID, 10)+"/"+*attempt.NamespaceUID+"/"+attempt.InstanceID) {
		return nil, nativeFinalConflict()
	}
	return &officialSubmissionPlan{Attempt: attempt, Policy: snapshot.RuntimePolicy, Workflow: workflow}, nil
}
