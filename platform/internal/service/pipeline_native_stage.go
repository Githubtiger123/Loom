package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const nativeStagingProtocol = "loom-native-staging-v1"
const maxNativeStagingSnapshot = 2 << 20

// Machine-only receipt: staging is not execution, completion or an operation
// acknowledgement. Native worker dispatch invokes the subsequent start stage.
type NativePipelineStagingReceipt struct {
	WorkflowName string
	WorkflowUID  string
	PlanHash     string
	Suspended    bool
}

type nativeStagingInput struct {
	Command MLCommand        `json:"command"`
	Space   MLExecutionSpace `json:"space"`
}

// Small bounded plans/specs are frozen inline in the existing immutable attempt
// snapshot. No object-store URI is fabricated and no mutable summary owns them.
type nativeStagingSnapshot struct {
	SchemaVersion   int                              `json:"schemaVersion"`
	Protocol        string                           `json:"protocol"`
	OperationID     string                           `json:"operationId"`
	CompilerVersion string                           `json:"compilerVersion"`
	Input           nativeStagingInput               `json:"input"`
	Policy          k8s.NativePipelineWorkflowPolicy `json:"policy"`
	Plan            json.RawMessage                  `json:"plan"`
	LocalSpecHash   string                           `json:"localSpecHash"`
	AdmittedSpec    json.RawMessage                  `json:"admittedSpec"`
	RequestHash     string                           `json:"requestHash"`
}

// Canonicalize MySQL JSON without lossy float conversion. This is the staging
// snapshot protocol, not a redefinition of historical KFP package hashes.
func nativeJSONHash(raw []byte) (string, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	return mlHash(encoded), err
}

func nativeStagingInputHash(in nativeStagingInput) string {
	data, _ := json.Marshal(in)
	hash, _ := nativeJSONHash(data)
	return hash
}

// Validate the persisted native command without trusting a caller's Claim.Command.
// Shared by staging and terminal finalization after the execution locks are held.
func (s *Service) nativeSubmissionInput(c *MLCommand, n *mlObject, e *mlExecution) (nativeStagingInput, error) {
	run, ok := n.row.(*model.PipelineRun)
	if !ok || c.Action != MLPipelineSubmit || c.PipelineRun == nil || c.Cluster != s.Cluster ||
		run.Engine != model.PipelineEngineNativeArgo || run.ExecutionCleaned || run.VersionRevision == nil ||
		run.VersionID != c.PipelineRun.VersionID || run.PipelineID != c.PipelineRun.PipelineID ||
		*run.VersionRevision != c.PipelineRun.VersionRevision || c.PipelineRun.VersionRevision == 0 ||
		e.GroupInstanceID == nil || e.NamespaceUID == nil || e.ProfileUID == nil {
		return nativeStagingInput{}, mlExecutionConflict()
	}
	input := nativeStagingInput{Command: *c, Space: MLExecutionSpace{InstanceID: *e.GroupInstanceID, NamespaceUID: *e.NamespaceUID, ProfileUID: *e.ProfileUID}}
	persisted, err := nativeJSONHash(n.state.RequestedSpec)
	if err != nil {
		return nativeStagingInput{}, err
	}
	commandSpec, err := json.Marshal(c.PipelineRun)
	if err != nil {
		return nativeStagingInput{}, err
	}
	commandHash, err := nativeJSONHash(commandSpec)
	if err != nil || commandHash != persisted {
		return nativeStagingInput{}, mlExecutionConflict()
	}
	return input, nil
}

// All callbacks here are SQL-only. Reuse the existing authorization/domain/
// operation/receipt locks and recheck the DB-clock lease at transaction end.
func (s *Service) nativeStagingTx(ctx context.Context, claim *MLExecutionClaim, expected *nativeStagingInput,
	fn func(*repository.Store, nativeStagingInput, *model.PipelineRun) error) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		input, err := s.nativeSubmissionInput(c, n, e)
		if err != nil {
			return err
		}
		if expected != nil && nativeStagingInputHash(*expected) != nativeStagingInputHash(input) {
			return mlExecutionConflict()
		}
		if err := fn(st, input, n.row.(*model.PipelineRun)); err != nil {
			return err
		}
		var count int64
		if err := mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}

func nativeStagingRows(ctx context.Context, st *repository.Store, in nativeStagingInput) (*model.PipelineRunAttempt, *model.PipelineSubmission, error) {
	c := in.Command
	a, err := st.PipelineRunAttempt(ctx, c.GroupID, c.ResourceID, c.InstanceID, true)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		a, err = nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	j := &model.PipelineSubmission{}
	err = st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", c.InstanceID).First(j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		j, err = nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if (a == nil) != (j == nil) {
		return nil, nil, &k8s.Permanent{Reason: "incomplete native staging intent"}
	}
	return a, j, nil
}

func compileNativeStaging(in nativeStagingInput, policy k8s.NativePipelineWorkflowPolicy) (*pipelineir.Plan, k8s.PipelineWorkflowRef, *unstructured.Unstructured, string, error) {
	return compileNativeVersion(in, policy, pipelineir.AdvancedCompilerVersion)
}

func compileNativeVersion(in nativeStagingInput, policy k8s.NativePipelineWorkflowPolicy, compiler string) (*pipelineir.Plan, k8s.PipelineWorkflowRef, *unstructured.Unstructured, string, error) {
	c := in.Command
	ref := k8s.PipelineWorkflowRef{GroupID: c.GroupID, RunID: c.ResourceID, InstanceID: c.InstanceID,
		Namespace: c.Namespace, NamespaceUID: in.Space.NamespaceUID, Name: "loom-" + c.InstanceID}
	pack := c.PipelineRun
	if pack == nil || len(pack.PipelineSpec) > pipelineir.MaxIRBytes || !validator.IsSHA256(pack.PackageDigest) ||
		compiler == pipelineir.CompilerVersion && (len(pack.Parameters) > 0 || len(pack.Values) > 0) {
		return nil, ref, nil, "", &k8s.Permanent{Reason: "native staging requires the static parameter-free IR subset"}
	}
	canonical, err := canonicalPipelineIR(pack.PipelineSpec)
	if err != nil || mlHash(canonical) != pack.PackageDigest || pack.PackageRef != "inline-"+pack.PackageDigest[:24] {
		return nil, ref, nil, "", &k8s.Permanent{Reason: "native IR package digest or reference conflict"}
	}
	plan, report := pipelineir.AnalyzeVersion(pack.PipelineSpec, compiler)
	if !report.Compilable || plan == nil {
		return nil, ref, nil, "", &k8s.Permanent{Reason: "native IR semantics are not supported by the staging compiler"}
	}
	ref.PlanHash = plan.Digest()
	if pipelineir.IsPythonCompiler(compiler) {
		policy.Parameters, err = pipelineRuntimeParameters(*pack)
		if err != nil {
			return nil, ref, nil, "", err
		}
	}
	want, hash, err := k8s.BuildNativePipelineWorkflow(ref, plan, policy)
	return plan, ref, want, hash, err
}

// StageNativePipelineSubmission implements durable intent -> suspended Create
// -> verified UID binding, not resume/run. It accepts only an already claimed
// NATIVE_ARGO intent; a caller cannot turn a KFP Run into a native Run here.
func (s *Service) StageNativePipelineSubmission(ctx context.Context, claim *MLExecutionClaim, policy k8s.NativePipelineWorkflowPolicy) (*NativePipelineStagingReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if s.NativePipelineClient == nil || s.PipelineVerifier == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "原生暂存依赖尚未配置")
	}
	if claim == nil || claim.MLExecutionView == nil || !mlExecutionLeaseValid(s.PipelinePeer(), pipelineLease(claim)) {
		return nil, mlExecutionInvalid()
	}
	// Do not trust caller-supplied Command/Space. Read the immutable operation
	// and bound receipt under the existing lease before deriving a target lock.
	var input nativeStagingInput
	if err := s.nativeStagingTx(ctx, claim, nil, func(_ *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error { input = in; return nil }); err != nil {
		return nil, err
	}
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(input.Command.ResourceID))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	guard := func() error {
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return err
		}
		if _, err := s.RenewMLExecution(ctx, s.PipelinePeer(), claim.OperationID, pipelineLease(claim)); err != nil {
			return err
		}
		return s.PipelineVerifier.VerifyPipelineSpace(ctx, &MLExecutionClaim{MLExecutionView: &MLExecutionView{Space: &input.Space}, Command: input.Command})
	}
	if err := guard(); err != nil {
		return nil, err
	}
	var attempt *model.PipelineRunAttempt
	var journal *model.PipelineSubmission
	if err := s.nativeStagingTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error {
		var err error
		attempt, journal, err = nativeStagingRows(ctx, st, in)
		return err
	}); err != nil {
		return nil, err
	}
	if attempt == nil {
		policy.CacheSeeds, err = s.nativeCacheSeeds(ctx, input)
		if err != nil {
			return nil, err
		}
		plan, ref, local, localHash, err := compileNativeStaging(input, policy)
		if err != nil {
			return nil, err
		}
		admitted, requestHash, err := s.NativePipelineClient.AdmitStagedPipelineWorkflow(ctx, ref, local)
		if err != nil {
			return nil, err
		}
		planData, err := plan.Snapshot()
		if err != nil {
			return nil, err
		}
		spec, err := json.Marshal(admitted.Object["spec"])
		if err != nil {
			return nil, err
		}
		snapshot, err := json.Marshal(nativeStagingSnapshot{SchemaVersion: 1, Protocol: nativeStagingProtocol, OperationID: claim.OperationID,
			CompilerVersion: plan.Compiler(), Input: input, Policy: policy, Plan: planData, LocalSpecHash: localHash, AdmittedSpec: spec, RequestHash: requestHash})
		if err != nil || len(snapshot) > maxNativeStagingSnapshot {
			return nil, &k8s.Permanent{Reason: "native staging snapshot exceeds persistence limits"}
		}
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return nil, err
		}
		if err := s.nativeStagingTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error {
			a, j, err := nativeStagingRows(ctx, st, in)
			if err != nil {
				return err
			}
			if a != nil || j != nil {
				return mlExecutionConflict()
			}
			c, phase, compiler := in.Command, "PENDING", plan.Compiler()
			summary, _ := json.Marshal(map[string]any{"schemaVersion": 1, "stagingProtocol": nativeStagingProtocol, "stagingState": "PREPARED"})
			attempt = &model.PipelineRunAttempt{InstanceID: c.InstanceID, RunID: c.ResourceID, GroupID: c.GroupID, Revision: c.Revision,
				Snapshot: snapshot, Engine: model.PipelineEngineNativeArgo, EngineRevision: &compiler,
				VersionID: &c.PipelineRun.VersionID, VersionRevision: &c.PipelineRun.VersionRevision,
				ClusterID: &c.Cluster, Namespace: &c.Namespace, NamespaceUID: &in.Space.NamespaceUID,
				WorkflowName: &ref.Name, PlanHash: &ref.PlanHash, Phase: &phase, RuntimeSummary: summary}
			journal = &model.PipelineSubmission{InstanceID: c.InstanceID, RunName: ref.Name, RequestHash: requestHash,
				Engine: model.PipelineEngineNativeArgo, ClusterID: &c.Cluster, Namespace: &c.Namespace, NamespaceUID: &in.Space.NamespaceUID}
			if err := st.DB.WithContext(ctx).Create(attempt).Error; err != nil {
				return err
			}
			if err := st.DB.WithContext(ctx).Create(journal).Error; err != nil {
				return err
			}
			return registerNativeStaticDefinitions(ctx, st, in, plan)
		}); err != nil {
			return nil, err
		}
	}
	// Always restore the frozen compiler/policy/plan/spec, including after a
	// crash or config change. Never repeat dry-run for a persisted intent.
	want, ref, snapshotHash, err := restoreNativeStaging(input, claim.OperationID, attempt, journal)
	if err != nil {
		return nil, err
	}
	if err := s.nativeStagingTx(ctx, claim, &input, func(st *repository.Store, in nativeStagingInput, _ *model.PipelineRun) error {
		a, j, err := nativeStagingRows(ctx, st, in)
		if err != nil {
			return err
		}
		if err := nativeStagingIntentUnchanged(a, j, snapshotHash, journal.RequestHash, ref, in); err != nil {
			return err
		}
		if j.Attempted {
			return nil
		}
		r := st.DB.WithContext(ctx).Model(j).Where("instance_id = ? AND attempted = ? AND workflow_uid IS NULL", j.InstanceID, false).Update("attempted", true)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := guard(); err != nil {
		return nil, err
	}
	live, err := s.NativePipelineClient.EnsureStagedPipelineWorkflow(ctx, ref, want, journal.RequestHash, guard)
	if err != nil {
		return nil, err
	}
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return nil, err
	}
	if err := s.PipelineVerifier.VerifyPipelineSpace(ctx, &MLExecutionClaim{MLExecutionView: &MLExecutionView{Space: &input.Space}, Command: input.Command}); err != nil {
		return nil, err
	}
	// Persist an already-verified external fact even if authorization/lease was
	// lost after Create. This never grants resume authority or projects success.
	if err := s.bindNativeStagedWorkflow(ctx, input, ref, snapshotHash, journal.RequestHash, string(live.GetUID())); err != nil {
		return nil, err
	}
	return &NativePipelineStagingReceipt{WorkflowName: ref.Name, WorkflowUID: string(live.GetUID()), PlanHash: ref.PlanHash, Suspended: true}, nil
}
