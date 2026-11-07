package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type KServeSpaceVerifier interface {
	VerifyKServeSpace(context.Context, *MLExecutionClaim) error
}

func (s *Service) KServePeer() string {
	return mlHash([]byte("loom-internal-kserve-v1\x00" + s.Cluster + "\x00" + s.KServeConfig.ServiceAccount))
}

type kserveFailure struct {
	reason    string
	permanent bool
}

func (e *kserveFailure) Error() string { return e.reason }

func kserveProblem(reason string, permanent bool) error {
	return &kserveFailure{reason: reason, permanent: permanent}
}

func (s *Service) ExecuteKServeClaim(ctx context.Context, claim *MLExecutionClaim) error {
	err := s.executeKServeClaim(ctx, claim)
	if err == nil || ctx.Err() != nil {
		return err
	}
	reason, outcome := "EXTERNAL_UNAVAILABLE", "RETRY"
	var problem *kserveFailure
	if errors.As(err, &problem) {
		reason = problem.reason
		if problem.permanent {
			outcome = "FAILED"
		}
	}
	var permanent *k8s.Permanent
	if errors.As(err, &permanent) {
		outcome = "FAILED"
		reason = "IDENTITY_CONFLICT"
		if strings.Contains(permanent.Reason, "MISSING") {
			reason = "RESOURCE_NOT_FOUND"
		}
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		outcome, reason = "FAILED", "EXTERNAL_REJECTED"
	}
	externalID := ""
	if claim.ExternalID != nil {
		externalID = *claim.ExternalID
	}
	_, reportErr := s.ReportMLExecution(ctx, s.KServePeer(), claim.OperationID, dto.ReportMLExecutionReq{
		MLExecutionLeaseReq: pipelineLease(claim), ReportID: uuid.NewString(), Sequence: claim.Sequence + 1,
		Outcome: outcome, Reason: reason, ExternalID: externalID,
	})
	return reportErr
}

func (s *Service) executeKServeClaim(ctx context.Context, claim *MLExecutionClaim) error {
	if s.KServeClient == nil || s.KServeVerifier == nil || !s.KServeConfig.Enabled {
		return fmt.Errorf("KServe 执行依赖未配置")
	}
	c := &claim.Command
	if c.KServe == nil || (c.Action != MLKServeCreate && c.Action != MLKServeUpdate && c.Action != MLKServeDeploy && c.Action != MLKServeStop && c.Action != MLKServeDelete) {
		return kserveProblem("REFERENCE_INVALID", true)
	}
	if err := c.KServe.Validate(); err != nil {
		return kserveProblem("REFERENCE_INVALID", true)
	}
	lock, err := repository.TargetLock(ctx, s.DB, c.Cluster, "kserve_service", fmt.Sprint(c.ResourceID))
	if err != nil {
		return err
	}
	defer lock.Close()
	guard := func() error {
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return err
		}
		_, err := s.RenewMLExecution(ctx, s.KServePeer(), claim.OperationID, pipelineLease(claim))
		return err
	}
	if err := guard(); err != nil {
		return err
	}
	if err := s.KServeVerifier.VerifyKServeSpace(ctx, claim); err != nil {
		if apperr.From(err).Code == apperr.CodeConflict {
			return kserveProblem("IDENTITY_CONFLICT", true)
		}
		return err
	}
	uid := ""
	if claim.ExternalID != nil {
		uid = *claim.ExternalID
	}
	ref := k8s.KServeRef{ID: c.ResourceID, GroupID: c.GroupID, Revision: c.Revision, InstanceID: c.InstanceID,
		Namespace: c.Namespace, Name: c.Name, UID: uid}

	var observation *k8s.KServeObservation
	if c.DesiredState == "SERVING" {
		body, _ := json.Marshal(c.KServe)
		observation, err = s.KServeClient.ReconcileKServe(ctx, ref, *c.KServe, s.KServeConfig.ServiceAccount, mlHash(body), guard)
	} else {
		observation, err = s.KServeClient.DeleteKServe(ctx, ref, guard)
	}
	if err != nil {
		return err
	}
	if uid != "" && observation.UID != "" && observation.UID != uid {
		return kserveProblem("IDENTITY_CONFLICT", true)
	}
	state := observation.State
	if observation.Absent {
		if c.DesiredState == "DELETED" {
			state = "DELETED"
		} else {
			state = "STOPPED"
		}
	}
	return s.reportKServe(ctx, claim, observation, state)
}

func (s *Service) reportKServe(ctx context.Context, claim *MLExecutionClaim, observation *k8s.KServeObservation, state string) error {
	reason := ""
	if state == "FAILED" {
		reason = "RESOURCE_FAILED"
	}
	out, err := s.ReportMLExecution(ctx, s.KServePeer(), claim.OperationID, dto.ReportMLExecutionReq{
		MLExecutionLeaseReq: pipelineLease(claim), Sequence: claim.Sequence + 1, ReportID: uuid.NewString(), Outcome: "OBSERVED",
		ObservedState: state, ExternalID: observation.UID, ResourceAbsent: observation.Absent, Reason: reason,
	})
	if err != nil {
		return err
	}
	claim.MLExecutionView = out
	if err := s.saveKServeProjection(ctx, claim.Command.ResourceID, claim.Command.InstanceID, claim.Command.Revision, observation); err != nil {
		return err
	}
	if out.Status != "RUNNING" {
		return nil
	}
	return s.yieldKServeExecution(ctx, claim)
}

func (s *Service) yieldKServeExecution(ctx context.Context, claim *MLExecutionClaim) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, s.KServePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		r := mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Updates(map[string]any{
			"status": "RETRYING", "step": "WAIT_KSERVE_STATE", "lease_owner": nil, "lease_until": nil,
			"next_attempt_at": gorm.Expr("TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(3))", int(s.KServeConfig.PollInterval.Std()/time.Second)),
		})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}

func (s *Service) saveKServeProjection(ctx context.Context, id uint64, instanceID string, revision uint64, observation *k8s.KServeObservation) error {
	fields := map[string]any{
		"kserve_generation": observation.Generation, "kserve_observed_generation": observation.ObservedGeneration,
		"kserve_resource_version": nullableString(observation.ResourceVersion), "kserve_ready": observation.Ready,
		"kserve_url": nullableString(observation.URL), "kserve_address": nullableString(observation.Address),
		"kserve_reason": nullableString(observation.Reason), "kserve_sync_attempted_at": gorm.Expr("UTC_TIMESTAMP(3)"),
	}
	if !observation.Absent {
		fields["kserve_synced_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
	}
	return s.DB.WithContext(ctx).Model(&model.KServeService{}).
		Where("id = ? AND instance_id = ? AND revision = ? AND deleted_at IS NULL", id, instanceID, revision).Updates(fields).Error
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
