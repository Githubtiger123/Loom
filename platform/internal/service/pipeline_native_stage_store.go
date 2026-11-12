package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func restoreNativeStaging(input nativeStagingInput, operationID string, a *model.PipelineRunAttempt, j *model.PipelineSubmission) (*unstructured.Unstructured, k8s.PipelineWorkflowRef, string, error) {
	var empty k8s.PipelineWorkflowRef
	bad := func() (*unstructured.Unstructured, k8s.PipelineWorkflowRef, string, error) {
		return nil, empty, "", &k8s.Permanent{Reason: "native frozen submission intent is invalid or unsupported"}
	}
	if a == nil || j == nil || input.Command.PipelineRun == nil || !validator.IsBoundedJSONObject(a.Snapshot, maxNativeStagingSnapshot, 64, 200000) {
		return bad()
	}
	var snapshot nativeStagingSnapshot
	d := json.NewDecoder(bytes.NewReader(a.Snapshot))
	d.DisallowUnknownFields()
	if d.Decode(&snapshot) != nil || snapshot.SchemaVersion != 1 || snapshot.Protocol != nativeStagingProtocol ||
		snapshot.OperationID != operationID || !pipelineir.IsSupportedCompiler(snapshot.CompilerVersion) ||
		nativeStagingInputHash(snapshot.Input) != nativeStagingInputHash(input) || a.EngineRevision == nil || *a.EngineRevision != snapshot.CompilerVersion ||
		a.Revision != input.Command.Revision || a.VersionID == nil || *a.VersionID != input.Command.PipelineRun.VersionID ||
		a.VersionRevision == nil || *a.VersionRevision != input.Command.PipelineRun.VersionRevision {
		return bad()
	}
	plan, ref, local, localHash, err := compileNativeVersion(snapshot.Input, snapshot.Policy, snapshot.CompilerVersion)
	if err != nil {
		return bad()
	}
	planData, err := plan.Snapshot()
	if err != nil {
		return bad()
	}
	planHash, err := nativeJSONHash(planData)
	if err != nil {
		return bad()
	}
	frozenPlanHash, err := nativeJSONHash(snapshot.Plan)
	if err != nil || frozenPlanHash != planHash || localHash != snapshot.LocalSpecHash ||
		!validator.IsSHA256(snapshot.RequestHash) || snapshot.RequestHash != j.RequestHash {
		return bad()
	}
	var admitted map[string]any
	if json.Unmarshal(snapshot.AdmittedSpec, &admitted) != nil || admitted == nil {
		return bad()
	}
	local.Object["spec"] = admitted
	if err := k8s.ValidateStagedPipelineWorkflow(local, ref, j.RequestHash, false); err != nil {
		return bad()
	}
	if a.WorkflowUID != nil {
		ref.UID = *a.WorkflowUID
	}
	if stop, err := nativeStopState(a); err != nil {
		return bad()
	} else if stop != nil {
		ref.StopToken = stop.Token
	}
	snapshotHash, err := nativeJSONHash(a.Snapshot)
	if err != nil || nativeStagingIntentUnchanged(a, j, snapshotHash, snapshot.RequestHash, ref, input) != nil {
		return bad()
	}
	return local, ref, snapshotHash, nil
}

func nativeStagingIntentUnchanged(a *model.PipelineRunAttempt, j *model.PipelineSubmission, snapshotHash, requestHash string, expected k8s.PipelineWorkflowRef, input nativeStagingInput) error {
	if a == nil || j == nil || input.Command.PipelineRun == nil {
		return mlExecutionConflict()
	}
	ref, valid := nativePipelineRef(a)
	hash, err := nativeJSONHash(a.Snapshot)
	if !valid || ref != expected || err != nil || hash != snapshotHash ||
		*a.ClusterID != input.Command.Cluster || a.Revision != input.Command.Revision ||
		a.EngineRevision == nil || !pipelineir.IsSupportedCompiler(*a.EngineRevision) ||
		a.VersionID == nil || *a.VersionID != input.Command.PipelineRun.VersionID ||
		a.VersionRevision == nil || *a.VersionRevision != input.Command.PipelineRun.VersionRevision ||
		j.Engine != model.PipelineEngineNativeArgo || j.InstanceID != a.InstanceID || j.RunName != ref.Name ||
		j.ClusterID == nil || *j.ClusterID != *a.ClusterID || j.Namespace == nil || *j.Namespace != ref.Namespace ||
		j.NamespaceUID == nil || *j.NamespaceUID != ref.NamespaceUID || j.ExternalID != nil || j.RequestHash != requestHash ||
		(j.WorkflowUID == nil) != (a.WorkflowUID == nil) || (j.WorkflowUID != nil && (*j.WorkflowUID != ref.UID || !j.Attempted)) {
		return mlExecutionConflict()
	}
	return nil
}

func registerNativeStaticDefinitions(ctx context.Context, st *repository.Store, in nativeStagingInput, plan *pipelineir.Plan) error {
	c := in.Command
	create := func(source pipelineir.SourceNode, parent *uint64) (*model.PipelineTaskExecution, error) {
		protocol, sealed := "loom-static-empty-v1", true
		if pipelineir.IsPythonCompiler(plan.Compiler()) {
			protocol = pipelineir.OutputProtocol(plan.Compiler())
			sealed = parent == nil
		}
		runtime, _ := json.Marshal(map[string]any{"schemaVersion": 1, "staticSource": source,
			"outputsSealed": sealed, "outputProtocol": protocol})
		// The compiler proves that this subset declares no ports. Sealing empty
		// IO is not execution success; phase still depends on trusted observation.
		io := json.RawMessage(`{"schemaVersion":1,"parameters":{},"artifacts":{}}`)
		row := &model.PipelineTaskExecution{GroupID: c.GroupID, RunID: c.ResourceID, InstanceID: c.InstanceID,
			NodeKey: source.NodeKey, DefinitionPath: source.DefinitionPath, ParentID: parent,
			IterationPath: json.RawMessage(`[]`), NodeKind: source.NodeKind, Phase: "PENDING", Inputs: io, Outputs: io, RuntimeState: runtime}
		return row, st.DB.WithContext(ctx).Create(row).Error
	}
	root, err := create(plan.Root(), nil)
	if err != nil {
		return err
	}
	if plan.Compiler() == pipelineir.AdvancedCompilerVersion {
		return nil
	}
	for _, task := range plan.Tasks() {
		if _, err := create(task.SourceNode, &root.ID); err != nil {
			return err
		}
	}
	return nil
}

// UID persistence is an observation of an already-created suspended object, not
// an execution grant. It survives lease loss and a newer stop/delete intent,
// but never changes the Run projection, operation receipt or another instance.
func (s *Service) bindNativeStagedWorkflow(ctx context.Context, in nativeStagingInput, ref k8s.PipelineWorkflowRef, snapshotHash, requestHash, uid string) error {
	if !validator.IsCatalogID(uid) {
		return &k8s.Permanent{Reason: "native Workflow UID is invalid"}
	}
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		g, err := st.Group(ctx, in.Command.GroupID, true)
		if err != nil {
			return err
		}
		run, err := st.PipelineRun(ctx, in.Command.GroupID, in.Command.ResourceID, true)
		if err != nil {
			return err
		}
		if g.InstanceID != in.Space.InstanceID || g.NamespaceUID == nil || *g.NamespaceUID != in.Space.NamespaceUID ||
			g.ProfileUID == nil || *g.ProfileUID != in.Space.ProfileUID || g.DeletedAt != nil ||
			g.Cluster != s.Cluster || g.Name != ref.Namespace || run.Cluster != s.Cluster || run.Namespace != ref.Namespace || run.Engine != model.PipelineEngineNativeArgo {
			return mlExecutionConflict()
		}
		a, j, err := nativeStagingRows(ctx, st, in)
		if err != nil {
			return err
		}
		if err := nativeStagingIntentUnchanged(a, j, snapshotHash, requestHash, ref, in); err != nil {
			return err
		}
		if !j.Attempted {
			return mlExecutionConflict()
		}
		if ref.UID != "" && ref.UID != uid {
			return mlExecutionConflict()
		}
		if a.ObservationVersion == ^uint64(0) {
			return mlExecutionConflict()
		}
		summary := map[string]any{}
		if (len(a.RuntimeSummary) != 0 && json.Unmarshal(a.RuntimeSummary, &summary) != nil) || summary == nil {
			return mlExecutionConflict()
		}
		if _, started := summary["start"]; started {
			return mlExecutionConflict() // Staging cannot erase/relabel a start checkpoint.
		}
		summary["stagingProtocol"], summary["stagingState"] = nativeStagingProtocol, "STAGED_SUSPENDED"
		encoded, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		if ref.UID == "" {
			r := st.DB.WithContext(ctx).Model(j).Where("instance_id = ? AND workflow_uid IS NULL AND attempted = ?", j.InstanceID, true).Update("workflow_uid", uid)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return mlExecutionConflict()
			}
		}
		query := st.DB.WithContext(ctx).Model(a).Where("instance_id = ? AND observation_version = ?", a.InstanceID, a.ObservationVersion)
		if ref.UID == "" {
			query = query.Where("workflow_uid IS NULL")
		} else {
			query = query.Where("workflow_uid = ?", uid)
		}
		r := query.Updates(map[string]any{"workflow_uid": uid, "runtime_summary": string(encoded), "observation_version": gorm.Expr("observation_version + 1")})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}
