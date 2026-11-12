package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// SyncNativePipelineWorkflow is a machine-only observation entrypoint. An
// informer key never creates/adopts a Run, attempt, task or submission journal.
func (s *Service) SyncNativePipelineWorkflow(ctx context.Context, namespace, name string) error {
	if !validator.IsDNS1123Label(namespace) || !validator.IsDNS1123Subdomain(name) {
		return nil
	}
	rows := []model.PipelineRunAttempt{}
	err := s.DB.WithContext(ctx).Where("engine = ? AND cluster_id = ? AND namespace = ? AND workflow_name = ?",
		model.PipelineEngineNativeArgo, s.Cluster, namespace, name).Limit(2).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return err
	}
	if len(rows) != 1 {
		return &k8s.Permanent{Reason: "ambiguous native Workflow binding"}
	}
	return s.syncNativePipelineAttempt(ctx, &rows[0], false)
}

// ConfirmNativePipelineWorkflow is reserved for the future trusted submitter,
// after durable intent and the external request. No user route exposes it.
// It verifies the live spec digest and binds its UID once. Watch cannot bind.
func (s *Service) ConfirmNativePipelineWorkflow(ctx context.Context, groupID, runID uint64, instanceID string) error {
	a, err := s.Store.PipelineRunAttempt(ctx, groupID, runID, instanceID, false)
	if err != nil {
		return err
	}
	return s.syncNativePipelineAttempt(ctx, a, true)
}

func nativePipelineRef(a *model.PipelineRunAttempt) (k8s.PipelineWorkflowRef, bool) {
	ref := k8s.PipelineWorkflowRef{GroupID: a.GroupID, RunID: a.RunID, InstanceID: a.InstanceID}
	if a.Engine != model.PipelineEngineNativeArgo || a.ClusterID == nil || a.Namespace == nil ||
		a.NamespaceUID == nil || a.WorkflowName == nil || a.PlanHash == nil || !validator.IsSHA256(*a.PlanHash) {
		return ref, false
	}
	ref.Namespace, ref.NamespaceUID, ref.Name, ref.PlanHash = *a.Namespace, *a.NamespaceUID, *a.WorkflowName, *a.PlanHash
	if a.WorkflowUID != nil {
		if !validator.IsCatalogID(*a.WorkflowUID) {
			return ref, false
		}
		ref.UID = *a.WorkflowUID
	}
	stop, err := nativeStopState(a)
	if err != nil {
		return ref, false
	}
	if stop != nil {
		ref.StopToken = stop.Token
	}
	return ref, ref.NamespaceUID != "" && ref.Name == "loom-"+a.InstanceID && validator.IsDNS1123Label(ref.Namespace)
}

func nativePipelineJournalMatches(j *model.PipelineSubmission, a *model.PipelineRunAttempt, ref k8s.PipelineWorkflowRef) bool {
	return j.Engine == model.PipelineEngineNativeArgo && j.Attempted && j.InstanceID == a.InstanceID &&
		j.RunName == ref.Name && j.ClusterID != nil && a.ClusterID != nil && *j.ClusterID == *a.ClusterID &&
		j.Namespace != nil && *j.Namespace == ref.Namespace && j.NamespaceUID != nil && *j.NamespaceUID == ref.NamespaceUID &&
		validator.IsSHA256(j.RequestHash) && ((j.WorkflowUID == nil && ref.UID == "") || (j.WorkflowUID != nil && *j.WorkflowUID == ref.UID))
}

func (s *Service) syncNativePipelineAttempt(ctx context.Context, hint *model.PipelineRunAttempt, bind bool) error {
	if s.NativePipelineClient == nil || s.PipelineVerifier == nil {
		return apperr.New(apperr.CodeUnavailable, "原生 Workflow 观测依赖尚未配置")
	}
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(hint.RunID))
	if err != nil {
		return err
	}
	defer lock.Close()
	a, err := s.Store.PipelineRunAttempt(ctx, hint.GroupID, hint.RunID, hint.InstanceID, false)
	if err != nil {
		return err
	}
	ref, valid := nativePipelineRef(a)
	if !valid || *a.ClusterID != s.Cluster {
		return &k8s.Permanent{Reason: "invalid native execution intent"}
	}
	if !bind && ref.UID == "" {
		return nil
	} // Only the submitter may establish identity.
	run, err := s.Store.PipelineRun(ctx, a.GroupID, a.RunID, false)
	if err != nil {
		return err
	}
	if run.Engine != model.PipelineEngineNativeArgo || run.Cluster != s.Cluster || run.Namespace != ref.Namespace {
		return &k8s.Permanent{Reason: "native Run identity mismatch"}
	}
	g, err := s.Store.Group(ctx, a.GroupID, false)
	if err != nil {
		return err
	}
	if g.DeletedAt != nil || g.Cluster != s.Cluster || g.Name != ref.Namespace || g.NamespaceUID == nil ||
		*g.NamespaceUID != ref.NamespaceUID || g.ProfileUID == nil || *g.ProfileUID == "" {
		return &k8s.Permanent{Reason: "native execution space identity changed"}
	}
	j := &model.PipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", a.InstanceID).First(j).Error; err != nil {
		return err
	}
	if !nativePipelineJournalMatches(j, a, ref) {
		return &k8s.Permanent{Reason: "native submission identity mismatch"}
	}
	claim := &MLExecutionClaim{Command: MLCommand{GroupID: a.GroupID, Namespace: ref.Namespace},
		MLExecutionView: &MLExecutionView{Space: &MLExecutionSpace{InstanceID: g.InstanceID, NamespaceUID: ref.NamespaceUID, ProfileUID: *g.ProfileUID}}}
	if err := s.PipelineVerifier.VerifyPipelineSpace(ctx, claim); err != nil {
		return err
	}
	obj, readErr := s.NativePipelineClient.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	reason := "WORKFLOW_READ_FAILED"
	if apierrors.IsNotFound(readErr) {
		reason = "RESOURCE_NOT_FOUND"
	}
	if readErr == nil && !k8s.PipelineWorkflowMatches(obj, ref) {
		readErr, reason = &k8s.Permanent{Reason: "native Workflow identity mismatch"}, "IDENTITY_CONFLICT"
	}
	var obs *k8s.PipelineWorkflowObservation
	if readErr == nil {
		obs, readErr = k8s.ObserveNativePipelineWorkflow(obj, ref)
		reason = "WORKFLOW_STATUS_INVALID"
	}
	if readErr == nil && bind && ref.UID == "" && obs.SpecHash != j.RequestHash {
		readErr, reason = &k8s.Permanent{Reason: "native Workflow spec digest mismatch"}, "IDENTITY_CONFLICT"
	}
	var nodeObservation *nativeStaticNodeObservation
	if readErr == nil {
		nodeObservation, readErr = restoreNativeStaticNodes(a, j)
		reason = "FROZEN_NODE_PLAN_INVALID"
		if readErr == nil && (nodeObservation.Input.Space.InstanceID != g.InstanceID ||
			nodeObservation.Input.Space.NamespaceUID != ref.NamespaceUID || nodeObservation.Input.Space.ProfileUID != *g.ProfileUID) {
			readErr, reason = &k8s.Permanent{Reason: "native frozen execution space identity changed"}, "SPACE_IDENTITY_CONFLICT"
		}
	}
	if readErr == nil {
		observedRef := ref
		observedRef.UID = obs.UID
		readErr = k8s.ValidateNativePipelineObservedSpec(obj, observedRef, j.RequestHash)
		reason = "WORKFLOW_SPEC_CONFLICT"
		if readErr == nil && obs.NodesComplete {
			nodeObservation.Nodes, readErr = k8s.MatchNativeStaticWorkflowNodes(observedRef, nodeObservation.Plan, obs)
			reason = "NODE_SOURCE_CONFLICT"
			if readErr == nil {
				nodeObservation.Pods, readErr = s.NativePipelineClient.ReadNativeStaticWorkflowPods(ctx, observedRef, nodeObservation.Nodes)
				reason = "POD_BINDING_READ_FAILED"
			}
		}
	}
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return err
	}
	if readErr != nil {
		// Keep known execution facts on GET/parse failure. A deletion event is
		// not proof of successful completion or an acknowledged stop.
		return s.recordNativePipelineSyncFailure(ctx, a, reason, readErr)
	}
	txReason := "NODE_BINDING_CONFLICT"
	txErr := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		space, err := st.Group(ctx, a.GroupID, true)
		if err != nil {
			return err
		}
		current, err := st.PipelineRun(ctx, a.GroupID, a.RunID, true)
		if err != nil {
			return err
		}
		fresh, err := st.PipelineRunAttempt(ctx, a.GroupID, a.RunID, a.InstanceID, true)
		if err != nil {
			return err
		}
		currentRef, valid := nativePipelineRef(fresh)
		if !valid || currentRef != ref || fresh.ObservationVersion != a.ObservationVersion ||
			current.Engine != model.PipelineEngineNativeArgo || current.Cluster != s.Cluster || current.Namespace != ref.Namespace ||
			space.InstanceID != g.InstanceID || space.DeletedAt != nil || space.NamespaceUID == nil || *space.NamespaceUID != ref.NamespaceUID ||
			space.ProfileUID == nil || *space.ProfileUID != *g.ProfileUID || space.Cluster != s.Cluster || space.Name != ref.Namespace {
			return mlExecutionConflict()
		}
		journal := &model.PipelineSubmission{}
		if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", a.InstanceID).First(journal).Error; err != nil {
			return err
		}
		if !nativePipelineJournalMatches(journal, fresh, ref) || journal.RequestHash != j.RequestHash {
			return mlExecutionConflict()
		}
		if err := nativeStagingIntentUnchanged(fresh, journal, nodeObservation.SnapshotHash, j.RequestHash, ref, nodeObservation.Input); err != nil {
			return err
		}
		if ref.UID == "" {
			if !bind {
				return mlExecutionConflict()
			}
			r := st.DB.WithContext(ctx).Model(journal).Where("instance_id = ? AND workflow_uid IS NULL", a.InstanceID).Update("workflow_uid", obs.UID)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return mlExecutionConflict()
			}
		}
		summary, _, err := nativeStartSummary(fresh)
		if err != nil {
			return err
		}
		syncReason := obs.NodesReason
		stats := nativeTaskSyncStats{}
		if obs.NodesComplete {
			stats, err = syncNativePipelineTasks(ctx, st, fresh, nodeObservation)
			if err != nil {
				return err
			}
			if stats.Missing > 0 {
				syncReason = "NODE_BINDINGS_INCOMPLETE"
			}
			if obs.Phase == "SUCCEEDED" && stats.NonSucceeded > 0 {
				syncReason = "NODE_PHASES_INCOMPLETE"
			}
			if stats.PendingPods > 0 {
				syncReason = "POD_BINDINGS_INCOMPLETE"
			}
			if stats.PendingOutputs > 0 {
				syncReason = "NODE_OUTPUTS_PENDING"
			}
		}
		observedRef := ref
		observedRef.UID = obs.UID
		startReason, err := observeNativeStartCheckpoint(fresh, nodeObservation, observedRef, journal.RequestHash, obj, summary)
		if err != nil {
			return err
		}
		if startReason != "" {
			syncReason = startReason
		}
		// Once archived, even an incomplete/newly suspended live observation
		// cannot mutate terminal task facts behind the persisted proof.
		txReason = "FINALIZATION_CONFLICT"
		if nativeHasTerminalArchive(fresh) {
			archive, archivedNodes, err := readNativeTerminalArchive(fresh, journal, nodeObservation)
			if err != nil {
				return err
			}
			taskHash, err := nativeTerminalTaskState(ctx, st, fresh, nodeObservation, archivedNodes)
			if err != nil {
				return err
			}
			if taskHash != archive.TaskStateHash {
				return nativeFinalConflict()
			}
			if (obs.StartedAt != nil && !sameNativeTime(nativeSQLTime(fresh.StartedAt), nativeSQLTime(obs.StartedAt))) ||
				(obs.FinishedAt != nil && !sameNativeTime(nativeSQLTime(fresh.FinishedAt), nativeSQLTime(obs.FinishedAt))) {
				return nativeFinalConflict()
			}
			if _, _, err := readNativeFinalReceipt(summary, nodeObservation.OperationID, *fresh.WorkflowSnapshotHash, archive.Phase); err != nil {
				return err
			}
		} else if summary["finalizationState"] != nil || summary["finalizationReceipt"] != nil {
			return nativeFinalConflict()
		}
		var finalSnapshot json.RawMessage
		var finalHash string
		if nativeFinalPhase(obs.Phase) && syncReason == "" {
			if !nativeTerminalFactsComplete(obs, nodeObservation) {
				syncReason = "TERMINAL_FACTS_INCOMPLETE"
			} else {
				finalSnapshot, finalHash, err = prepareNativeTerminalArchive(ctx, st, fresh, journal, nodeObservation, observedRef, obj, summary)
				if err != nil {
					return err
				}
				summary["finalizationProtocol"], summary["finalArchiveHash"] = nativeFinalProtocol, finalHash
				if summary["finalizationState"] != "COMPLETED" {
					summary["finalizationState"] = "SNAPSHOT_STORED"
					syncReason = "FINAL_RECEIPT_PENDING"
				}
			}
		}
		summary["schemaVersion"], summary["observedResourceVersion"], summary["argoPhase"] = 1, obs.ResourceVersion, obs.RawPhase
		summary["nodeStatusComplete"], summary["observedNodeCount"], summary["matchedNodeCount"] = obs.NodesComplete, len(obs.Nodes), stats.Matched
		summary["missingRegisteredNodeCount"], summary["pendingOutputNodeCount"] = stats.Missing, stats.PendingOutputs
		summary["pendingPodBindingCount"], summary["nonSucceededNodeCount"] = stats.PendingPods, stats.NonSucceeded
		summary["nodeBindingProtocol"] = "loom-native-static-binding-v1"
		// This transaction records evidence only, never operation completion or GC.
		summary["observationOnly"] = true
		data, err := json.Marshal(summary)
		if err != nil || !validator.IsBoundedJSONObject(data, 64<<10, 16, 4096) {
			if err == nil {
				return nativeFinalConflict()
			}
			return err
		}
		fields := map[string]any{"workflow_uid": obs.UID, "phase": obs.Phase, "runtime_summary": string(data),
			"observed_at": gorm.Expr("UTC_TIMESTAMP(3)"), "sync_attempted_at": gorm.Expr("UTC_TIMESTAMP(3)"), "sync_reason": nil,
			"observation_version": gorm.Expr("observation_version + 1")}
		if obs.StartedAt != nil {
			fields["started_at"] = nativeSQLTime(obs.StartedAt)
		}
		if obs.FinishedAt != nil {
			fields["finished_at"] = nativeSQLTime(obs.FinishedAt)
		}
		if len(finalSnapshot) != 0 {
			fields["workflow_snapshot"], fields["workflow_snapshot_hash"] = string(finalSnapshot), finalHash
			fields["workflow_archived_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
		}
		if syncReason != "" {
			fields["sync_reason"] = syncReason
		}
		if terminalNativePhase(fresh.Phase) && obs.Phase != *fresh.Phase {
			return &k8s.Permanent{Reason: "native terminal state cannot be replaced"}
		}
		if fresh.ObservationVersion == ^uint64(0) {
			return mlExecutionConflict()
		}
		r := st.DB.WithContext(ctx).Model(fresh).Where("instance_id = ? AND observation_version = ?", a.InstanceID, a.ObservationVersion).Updates(fields)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		if current.InstanceID != a.InstanceID || current.ExecutionCleaned || current.DeletedAt != nil {
			return nil
		} // Historical attempt only.
		state := obs.Phase
		projection := map[string]any{"external_id": obs.UID, "observed_state": state, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}
		if current.DesiredState == "RUNNING" && current.DeletedAt == nil && !current.ExecutionCleaned {
			projection["status"], projection["status_reason"] = state, nil
			if state == "FAILED" && summary["finalizationState"] == "COMPLETED" {
				projection["status_reason"] = "ARGO_WORKFLOW_FAILED"
			}
			if syncReason != "" {
				projection["status_reason"] = syncReason
				if nativeFinalPhase(state) && summary["finalizationState"] != "COMPLETED" {
					// Both terminal outcomes require the final durable receipt.
					projection["status"] = "RUNNING"
				}
			}
		}
		return st.DB.WithContext(ctx).Model(current).Where("id = ? AND instance_id = ? AND revision = ?", current.ID, a.InstanceID, current.Revision).Updates(projection).Error
	})
	var permanent *k8s.Permanent
	if errors.As(txErr, &permanent) {
		// Rollback preserved every prior binding/fact. Record only a bounded
		// diagnostic separately, fenced against a newer successful observation.
		return s.recordNativePipelineSyncFailure(ctx, a, txReason, txErr)
	}
	return txErr
}

func (s *Service) recordNativePipelineSyncFailure(ctx context.Context, a *model.PipelineRunAttempt, reason string, cause error) error {
	r := s.DB.WithContext(ctx).Model(&model.PipelineRunAttempt{}).
		Where("group_id = ? AND run_id = ? AND instance_id = ? AND observation_version = ?", a.GroupID, a.RunID, a.InstanceID, a.ObservationVersion).
		Updates(map[string]any{"sync_attempted_at": gorm.Expr("UTC_TIMESTAMP(3)"), "sync_reason": reason})
	if r.Error != nil {
		return r.Error
	}
	return cause
}

func terminalNativePhase(phase *string) bool {
	return phase != nil && (*phase == "SUCCEEDED" || *phase == "FAILED" || *phase == "STOPPED" || *phase == "SKIPPED")
}
