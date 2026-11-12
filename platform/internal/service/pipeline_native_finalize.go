package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// This is a durable receipt, not a cleanup grant. It binds the final archive to
// the exact operation report; its canonical JSON is also the report digest.
type nativeFinalReceipt struct {
	SchemaVersion int    `json:"schemaVersion"`
	Protocol      string `json:"protocol"`
	OperationID   string `json:"operationId"`
	ArchiveHash   string `json:"archiveHash"`
	ReportID      string `json:"reportId"`
	Sequence      uint64 `json:"sequence"`
	Phase         string `json:"phase"`
	Owner         string `json:"owner"`
	Peer          string `json:"peer"`
}

func nativeFinalReportID(operationID, archiveHash string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(nativeFinalProtocol+"\x00"+operationID+"\x00"+archiveHash)).String()
}

func readNativeFinalReceipt(summary map[string]any, operationID, archiveHash, phase string) (*nativeFinalReceipt, string, error) {
	value, exists := summary["finalizationReceipt"]
	if !exists {
		if summary["finalizationState"] == "COMPLETED" {
			return nil, "", nativeFinalConflict()
		}
		return nil, "", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	var receipt nativeFinalReceipt
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&receipt) != nil || receipt.SchemaVersion != 1 || receipt.Protocol != nativeFinalProtocol ||
		receipt.OperationID != operationID || receipt.ArchiveHash != archiveHash || receipt.Phase != phase ||
		!validator.IsSHA256(archiveHash) || !nativeFinalPhase(phase) || receipt.Sequence == 0 ||
		receipt.ReportID != nativeFinalReportID(operationID, archiveHash) || !validator.IsCanonicalUUID(receipt.Owner) ||
		!validator.IsSHA256(receipt.Peer) || summary["finalizationState"] != "COMPLETED" ||
		summary["finalizationProtocol"] != nativeFinalProtocol || summary["finalArchiveHash"] != archiveHash {
		return nil, "", nativeFinalConflict()
	}
	hash, err := nativeJSONHash(raw)
	return &receipt, hash, err
}

func nativeFinalReplayMatches(o *model.Operation, e *mlExecution, r *nativeFinalReceipt, digest string,
	claim *MLExecutionClaim, peer, uid string) bool {
	return o.Status == r.Phase && o.Step == "COMPLETED" && o.FinishedAt != nil && o.LeaseOwner == nil && o.LeaseUntil == nil &&
		r.Owner == claim.LeaseOwner && r.Peer == peer && e.ObservationSequence == r.Sequence &&
		e.ExternalID != nil && *e.ExternalID == uid && e.LastObservedState != nil && *e.LastObservedState == r.Phase &&
		e.LastReportID != nil && *e.LastReportID == r.ReportID && e.LastReportSHA256 != nil && *e.LastReportSHA256 == digest &&
		e.LastReportOwner != nil && *e.LastReportOwner == r.Owner && e.LastReportPeerSHA256 != nil && *e.LastReportPeerSHA256 == r.Peer &&
		e.LastReportAt != nil
}

// FinalizeNativePipelineSubmission is machine-only and invoked by the native
// worker dispatch. Read persisted final evidence, not a live
// Workflow or today's editable version. Kubernetes absence after archival does
// not erase that evidence. There is no external I/O inside the SQL transaction.
func (s *Service) FinalizeNativePipelineSubmission(ctx context.Context, claim *MLExecutionClaim) (*MLExecutionView, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	peer := s.PipelinePeer()
	if claim == nil || claim.MLExecutionView == nil || !validator.IsCanonicalUUID(claim.OperationID) ||
		!mlExecutionLeaseValid(peer, pipelineLease(claim)) {
		return nil, mlExecutionInvalid()
	}
	// Only persisted command identity may select the target lock. No supplied
	// Claim.Command/Space/sequence or mutable version is authoritative here.
	op, err := s.Store.Operation(ctx, claim.OperationID)
	if err != nil {
		return nil, err
	}
	command, err := mlExecutionCommand(op)
	if err != nil {
		return nil, err
	}
	if command.Action != MLPipelineSubmit || command.Cluster != s.Cluster {
		return nil, mlExecutionConflict()
	}
	lock, err := repository.TargetLock(ctx, s.DB, s.Cluster, "pipeline_run", fmt.Sprint(command.ResourceID))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	attempt, err := s.Store.PipelineRunAttempt(ctx, command.GroupID, command.ResourceID, command.InstanceID, false)
	if err != nil {
		return nil, err
	}
	journal := &model.PipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", command.InstanceID).First(journal).Error; err != nil {
		return nil, err
	}
	frozen, err := restoreNativeStaticNodes(attempt, journal)
	if err != nil {
		return nil, err
	}
	commandInput := frozen.Input
	commandInput.Command = *command
	ref, valid := nativePipelineRef(attempt)
	if !valid || ref.UID == "" || frozen.OperationID != op.ID ||
		nativeStagingInputHash(commandInput) != nativeStagingInputHash(frozen.Input) {
		return nil, mlExecutionConflict()
	}
	archive, nodes, err := readNativeTerminalArchive(attempt, journal, frozen)
	if err != nil {
		return nil, err
	}
	archiveHash := *attempt.WorkflowSnapshotHash
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return nil, err
	}
	var out *MLExecutionView
	err = repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		_, fence := mlExecutionKeys(o, c)
		currentInput := frozen.Input
		currentInput.Command = *c
		run, ok := n.row.(*model.PipelineRun)
		if !ok || run.Engine != model.PipelineEngineNativeArgo || c.Action != MLPipelineSubmit || c.Cluster != s.Cluster ||
			nativeStagingInputHash(currentInput) != nativeStagingInputHash(frozen.Input) ||
			claim.InstanceID != c.InstanceID || claim.Revision != c.Revision || claim.TargetFence != fence ||
			e.TargetType != o.TargetType || e.InstanceID != c.InstanceID || e.Revision != c.Revision ||
			e.GroupInstanceID == nil || *e.GroupInstanceID != frozen.Input.Space.InstanceID ||
			e.NamespaceUID == nil || *e.NamespaceUID != frozen.Input.Space.NamespaceUID ||
			e.ProfileUID == nil || *e.ProfileUID != frozen.Input.Space.ProfileUID {
			return mlExecutionConflict()
		}
		a, j, err := nativeStagingRows(ctx, st, frozen.Input)
		if err != nil {
			return err
		}
		if err := nativeStagingIntentUnchanged(a, j, frozen.SnapshotHash, journal.RequestHash, ref, frozen.Input); err != nil {
			return err
		}
		// Revalidate the locked archive and current checkpoint; never replace
		// a final snapshot or allow partial/corrupt evidence on replay.
		if a.WorkflowSnapshotHash == nil || *a.WorkflowSnapshotHash != archiveHash {
			return nativeFinalConflict()
		}
		if _, _, err := readNativeTerminalArchive(a, j, frozen); err != nil {
			return err
		}
		taskHash, err := nativeTerminalTaskState(ctx, st, a, frozen, nodes)
		if err != nil {
			return err
		}
		if taskHash != archive.TaskStateHash {
			return nativeFinalConflict()
		}
		summary, _, err := nativeStartSummary(a)
		if err != nil {
			return err
		}
		prior, digest, err := readNativeFinalReceipt(summary, o.ID, archiveHash, archive.Phase)
		if err != nil {
			return err
		}
		if prior != nil {
			// Exact committed replay is a read, including after revocation,
			// lease expiry or a newer intent. It never rewrites the new Run.
			if !nativeFinalReplayMatches(o, e, prior, digest, claim, peer, ref.UID) {
				return mlExecutionConflict()
			}
			out = mlExecutionView(o, c, e)
			return nil
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, peer, pipelineLease(claim)); err != nil {
			return err
		}
		input, err := s.nativeSubmissionInput(c, n, e)
		if err != nil {
			return err
		}
		if nativeStagingInputHash(input) != nativeStagingInputHash(frozen.Input) || a.ObservedAt == nil ||
			n.state.ObservedAt == nil || n.state.ObservedState != archive.Phase ||
			(n.state.ExternalID != nil && *n.state.ExternalID != ref.UID) || (e.ExternalID != nil && *e.ExternalID != ref.UID) ||
			(e.LastObservedState != nil && terminalNativePhase(e.LastObservedState) && *e.LastObservedState != archive.Phase) ||
			e.ObservationSequence == ^uint64(0) || a.ObservationVersion == ^uint64(0) ||
			summary["finalizationState"] != "SNAPSHOT_STORED" || summary["finalizationProtocol"] != nativeFinalProtocol ||
			summary["finalArchiveHash"] != archiveHash {
			return mlExecutionConflict()
		}
		receipt := &nativeFinalReceipt{SchemaVersion: 1, Protocol: nativeFinalProtocol, OperationID: o.ID,
			ArchiveHash: archiveHash, ReportID: nativeFinalReportID(o.ID, archiveHash), Sequence: e.ObservationSequence + 1,
			Phase: archive.Phase, Owner: claim.LeaseOwner, Peer: peer}
		summary["finalizationReceipt"], summary["finalizationState"] = receipt, "COMPLETED"
		_, digest, err = readNativeFinalReceipt(summary, o.ID, archiveHash, archive.Phase)
		if err != nil {
			return err
		}
		data, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		if !validator.IsBoundedJSONObject(data, 64<<10, 16, 4096) {
			return nativeFinalConflict()
		}
		r := st.DB.WithContext(ctx).Model(a).Where("instance_id = ? AND observation_version = ? AND workflow_snapshot_hash = ?",
			a.InstanceID, a.ObservationVersion, archiveHash).
			Updates(map[string]any{"runtime_summary": string(data), "sync_reason": nil, "observation_version": gorm.Expr("observation_version + 1")})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		var reason any
		if archive.Phase == "FAILED" {
			reason = "ARGO_WORKFLOW_FAILED"
		}
		// Preserve observed_at: archive replay is not a fresh Kubernetes GET.
		r = st.DB.WithContext(ctx).Model(run).
			Where("id = ? AND group_id = ? AND instance_id = ? AND revision = ? AND last_operation_id = ?",
				c.ResourceID, c.GroupID, c.InstanceID, c.Revision, o.ID).
			Updates(map[string]any{"status": archive.Phase, "status_reason": reason,
				"observed_state": archive.Phase, "external_id": ref.UID, "applied_revision": c.Revision})
		if r.Error != nil {
			return r.Error
		}
		n.state.Status, n.state.ObservedState, n.state.AppliedRevision = archive.Phase, archive.Phase, c.Revision
		n.state.ExternalID = &ref.UID
		if err := mlRecord(ctx, st, o, n, "OBSERVATION"); err != nil {
			return err
		}
		if err := result(o, map[string]any{"resourceId": c.ResourceID, "groupId": c.GroupID, "instanceId": c.InstanceID,
			"revision": c.Revision, "appliedRevision": c.Revision, "status": archive.Phase, "desiredState": c.DesiredState,
			"observedState": archive.Phase, "externalId": ref.UID, "observationSequence": receipt.Sequence,
			"reason": reason, "executionPending": false}); err != nil {
			return err
		}
		r = st.DB.WithContext(ctx).Model(e).Where("operation_id = ? AND observation_sequence = ?", o.ID, e.ObservationSequence).
			Updates(map[string]any{"external_id": ref.UID, "observation_sequence": receipt.Sequence, "last_report_id": receipt.ReportID,
				"last_report_sha256": digest, "last_report_owner": receipt.Owner, "last_report_peer_sha256": peer,
				"last_observed_state": archive.Phase, "last_report_at": gorm.Expr("UTC_TIMESTAMP(3)")})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		actor := uint64(0)
		if err := st.DB.WithContext(ctx).Create(&model.Audit{OperationID: &o.ID, ActorID: &actor, ActorName: "ml-executor",
			GroupID: &c.GroupID, Action: o.Action, TargetType: o.TargetType, TargetID: o.TargetKey, Outcome: archive.Phase}).Error; err != nil {
			return err
		}
		// Last conditional write uses DB time. If lease expires, everything
		// above (Run, receipt, history, audit and checkpoint) rolls back.
		r = mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).
			Updates(map[string]any{"status": archive.Phase, "step": "COMPLETED", "result": string(o.Result), "last_error": reason,
				"finished_at": gorm.Expr("UTC_TIMESTAMP(3)"), "lease_owner": nil, "lease_until": nil})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		o, err = st.Operation(ctx, o.ID)
		if err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Where("operation_id = ?", o.ID).First(e).Error; err != nil {
			return err
		}
		out = mlExecutionView(o, c, e)
		return nil
	})
	return out, err
}
