package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Auxiliary execution receipts do not merge the three domain resource tables.
// The observation sequence and immutable per-revision external ID survive lease
// takeovers and operation retries. Only this bridge writes these receipts.
type mlExecution struct {
	OperationID          string `gorm:"primaryKey"`
	TargetType           string
	InstanceID           string
	Revision             uint64
	ExternalID           *string
	ObservationSequence  uint64
	LeasePeerSHA256      string `gorm:"column:lease_peer_sha256"`
	LastReportID         *string
	LastReportSHA256     *string `gorm:"column:last_report_sha256"`
	LastReportOwner      *string
	LastReportPeerSHA256 *string `gorm:"column:last_report_peer_sha256"`
	LastObservedState    *string
	LastReportAt         *time.Time
	GroupInstanceID      *string
	NamespaceUID         *string
	ProfileUID           *string
	WatchSequence        uint64
	WatchReportID        *string
	WatchReportSHA256    *string `gorm:"column:watch_report_sha256"`
	WatchPeerSHA256      *string `gorm:"column:watch_peer_sha256"`
	WatchObservedState   *string
	WatchObservedAt      *time.Time
}

func (mlExecution) TableName() string { return "tb_ml_execution" }

// MLExecutionView is a sanitized operation receipt, not the generic Operation
// (which also contains actor metadata and potentially unrelated result data).
type MLExecutionView struct {
	OperationID    string                  `json:"operationId"`
	Status         string                  `json:"status"`
	Step           string                  `json:"step"`
	InstanceID     string                  `json:"instanceId"`
	Revision       uint64                  `json:"revision"`
	TargetFence    string                  `json:"targetFence"`
	IdempotencyKey string                  `json:"idempotencyKey"`
	Sequence       uint64                  `json:"sequence"`
	ExternalID     *string                 `json:"externalId"`
	ObservedState  *string                 `json:"observedState"`
	ReportID       *string                 `json:"reportId"`
	ReportedAt     *time.Time              `json:"reportedAt"`
	LeaseUntil     *time.Time              `json:"leaseUntil"`
	Space          *MLExecutionSpace       `json:"space,omitempty"`
	Observation    *MLExecutionObservation `json:"observation,omitempty"`
}

type MLExecutionSpace struct {
	InstanceID   string `json:"instanceId"`
	NamespaceUID string `json:"namespaceUid"`
	ProfileUID   string `json:"profileUid"`
}

// Runtime readiness is separate from the immutable completion receipt. It can
// deteriorate/recover without reopening a succeeded deploy operation.
type MLExecutionObservation struct {
	Sequence      uint64     `json:"sequence"`
	ReportID      *string    `json:"reportId"`
	ObservedState *string    `json:"observedState"`
	ObservedAt    *time.Time `json:"observedAt"`
}

type MLExecutionClaim struct {
	*MLExecutionView
	LeaseOwner string    `json:"leaseOwner"`
	Command    MLCommand `json:"command"`
}

func mlExecutionConflict() error {
	return apperr.New(apperr.CodeConflict, "执行租约、目标版本或观察已失效")
}

func mlExecutionInvalid() error {
	return apperr.New(apperr.CodeInvalid, "执行请求字段或规范原因码无效")
}

func mlExecutionAction(action string) bool {
	for _, allowed := range MLExecutorActions() {
		if action == allowed {
			return true
		}
	}
	return false
}

func mlExecutionCommand(o *model.Operation) (*MLCommand, error) {
	c := &MLCommand{}
	if !mlExecutionAction(o.Action) || json.Unmarshal(o.Payload, c) != nil || c.SchemaVersion != 1 ||
		string(c.Action) != o.Action || c.RequestedBy != o.ActorID || c.Revision == 0 || c.Revision != o.TargetVersion ||
		c.Cluster != o.Cluster || c.GroupID == 0 || c.ResourceID == 0 ||
		strconv.FormatUint(c.ResourceID, 10) != o.TargetKey {
		return nil, mlExecutionConflict()
	}
	if id, err := uuid.Parse(c.InstanceID); err != nil || id.String() != c.InstanceID {
		return nil, mlExecutionConflict()
	}
	target, desired := "", ""
	switch c.Action {
	case MLPipelineSubmit:
		target, desired = "pipeline_run", "RUNNING"
	case MLPipelineStop, MLPipelineEdit:
		target, desired = "pipeline_run", "STOPPED"
	case MLPipelineDelete:
		target, desired = "pipeline_run", "DELETED"
	case MLTrainerCreate:
		target, desired = "trainer_job", "RUNNING"
	case MLTrainerStop:
		target, desired = "trainer_job", "STOPPED"
	case MLTrainerDelete:
		target, desired = "trainer_job", "DELETED"
	case MLKServeCreate, MLKServeDeploy:
		target, desired = "kserve_service", "SERVING"
	case MLKServeUpdate:
		// Updating a stopped service must not implicitly deploy it.
		target = "kserve_service"
		if c.DesiredState == "SERVING" || c.DesiredState == "STOPPED" {
			desired = c.DesiredState
		}
	case MLKServeStop:
		target, desired = "kserve_service", "STOPPED"
	case MLKServeDelete:
		target, desired = "kserve_service", "DELETED"
	}
	if o.TargetType != target || c.DesiredState != desired ||
		(c.Action == MLPipelineEdit) != (c.PendingPipelineRun != nil) ||
		(target == "pipeline_run") != (c.PipelineRun != nil) ||
		(target == "trainer_job") != (c.TrainerJob != nil) ||
		(target == "kserve_service") != (c.KServe != nil) {
		return nil, mlExecutionConflict()
	}
	return c, nil
}

func mlExecutionKeys(o *model.Operation, c *MLCommand) (string, string) {
	// No lease owner, operation attempt, or external ID enters the idempotency
	// key: one instance+revision always denotes one external intent.
	b, _ := json.Marshal([]any{"loom-ml-v1", c.Cluster, o.TargetType, c.InstanceID, c.Revision})
	key := mlHash(b)
	b, _ = json.Marshal([]any{key, o.ID, c.ResourceID, c.GroupID, c.Namespace, c.Name, c.DesiredState, c.Action})
	return key, mlHash(b)
}

func mlExecutionMatches(n *mlObject, o *model.Operation, c *MLCommand) bool {
	return n.identity.ID == c.ResourceID && n.identity.GroupID == c.GroupID && n.identity.Name == c.Name &&
		n.identity.DeletedAt == nil && n.state.InstanceID == c.InstanceID && n.state.Revision == c.Revision &&
		n.state.Cluster == c.Cluster && n.state.Namespace == c.Namespace && n.state.DesiredState == c.DesiredState &&
		(n.configID == nil || *n.configID == c.ConfigRevisionID) &&
		n.state.LastOperationID != nil && *n.state.LastOperationID == o.ID &&
		(c.ExternalID == nil || (n.state.ExternalID != nil && *c.ExternalID == *n.state.ExternalID))
}

func mlExecutionView(o *model.Operation, c *MLCommand, e *mlExecution) *MLExecutionView {
	key, fence := mlExecutionKeys(o, c)
	v := &MLExecutionView{OperationID: o.ID, Status: o.Status, Step: o.Step,
		InstanceID: c.InstanceID, Revision: c.Revision, TargetFence: fence,
		IdempotencyKey: key, ExternalID: c.ExternalID, LeaseUntil: o.LeaseUntil}
	if e != nil {
		v.Sequence, v.ExternalID, v.ObservedState = e.ObservationSequence, e.ExternalID, e.LastObservedState
		v.ReportID, v.ReportedAt = e.LastReportID, e.LastReportAt
		if e.GroupInstanceID != nil && e.NamespaceUID != nil && e.ProfileUID != nil {
			v.Space = &MLExecutionSpace{InstanceID: *e.GroupInstanceID, NamespaceUID: *e.NamespaceUID, ProfileUID: *e.ProfileUID}
		}
		v.Observation = &MLExecutionObservation{Sequence: e.WatchSequence, ReportID: e.WatchReportID,
			ObservedState: e.WatchObservedState, ObservedAt: e.WatchObservedAt}
	}
	return v
}

// All authorization mutations use Store.LockAuthorization's exclusive lock on
// this same row. Shared locks permit parallel claims but serialize grants with
// revocation, role edits, account disable and group lifecycle mutations.
func mlExecutionAuthorizationFence(ctx context.Context, st *repository.Store) error {
	var role model.Role
	return st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Select("id").
		Where("code = ?", "platform_admin").First(&role).Error
}

func mlExecutionGroup(ctx context.Context, st *repository.Store, c *MLCommand) (*model.Group, error) {
	g := &model.Group{}
	err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", c.GroupID).First(g).Error
	return g, err
}

func mlExecutionSpaceMatches(e *mlExecution, g *model.Group) bool {
	return e.GroupInstanceID != nil && *e.GroupInstanceID == g.InstanceID &&
		e.NamespaceUID != nil && g.NamespaceUID != nil && *e.NamespaceUID == *g.NamespaceUID &&
		e.ProfileUID != nil && g.ProfileUID != nil && *e.ProfileUID == *g.ProfileUID
}

// Only activation intents require a still-enabled, still-authorized requester.
// Already authorized stop/delete work remains executable in frozen groups and
// after requester revocation; cleanup never grants authority to run/deploy.
func mlExecutionGrant(ctx context.Context, st *repository.Store, o *model.Operation, c *MLCommand, n *mlObject) (*model.Group, error) {
	g, err := mlExecutionGroup(ctx, st, c)
	if err != nil {
		return nil, err
	}
	if g.DeletedAt != nil || g.Status == "DELETED" || g.Cluster != c.Cluster || g.Name != c.Namespace ||
		g.InstanceID == "" || g.NamespaceUID == nil || *g.NamespaceUID == "" || g.ProfileUID == nil || *g.ProfileUID == "" {
		return nil, mlExecutionConflict()
	}
	switch c.Action {
	case MLPipelineStop, MLPipelineDelete, MLPipelineEdit, MLTrainerStop, MLTrainerDelete, MLKServeStop, MLKServeDelete:
		return g, nil
	}
	if g.Status != "ACTIVE" || c.RequestedBy == 0 || c.RequestedBy != o.ActorID {
		return nil, apperr.New(apperr.CodeForbidden, "空间或执行请求人当前不允许启动资源")
	}
	u, err := st.User(ctx, c.RequestedBy, false)
	if err != nil {
		return nil, err
	}
	if u.Status != "ACTIVE" || u.DeletedAt != nil {
		return nil, apperr.New(apperr.CodeForbidden, "执行请求人已失效")
	}
	permission := mlActionPermission(o.Action)
	if permission == "" {
		return nil, mlExecutionConflict()
	}
	if err := mlAllowed(ctx, st, c.RequestedBy, n.identity, mlModule(o.TargetType), permission); err != nil {
		return nil, err
	}
	if c.PipelineRun != nil {
		// The original submit was authorized against the definition, not just
		// the newly-created run owned by its submitter. Recheck both objects.
		p, err := st.Pipeline(ctx, c.GroupID, c.PipelineRun.PipelineID, false)
		if err != nil {
			return nil, err
		}
		if p.DeletedAt != nil {
			return nil, mlExecutionConflict()
		}
		if err := mlAllowed(ctx, st, c.RequestedBy, &p.MLIdentity, "pipeline", permission); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// ClaimMLExecution claims one durable command, never doing external I/O. A nil
// result means no eligible command. Lease time uses the database clock only.
func (s *Service) ClaimMLExecution(ctx context.Context, peer string, in dto.ClaimMLExecutionReq) (*MLExecutionClaim, error) {
	if !validator.IsSHA256(peer) || !validator.IsCatalogID(in.Cluster) || len(in.Actions) == 0 || len(in.Actions) > len(MLExecutorActions()) {
		return nil, mlExecutionInvalid()
	}
	seen := map[string]bool{}
	for _, action := range in.Actions {
		if s.PipelineClient != nil && (action == string(MLPipelineSubmit) || action == string(MLPipelineStop) || action == string(MLPipelineDelete) || action == string(MLPipelineEdit)) && peer != s.PipelinePeer() {
			return nil, apperr.New(apperr.CodeConflict, "Pipeline 已由内置 KFP worker 管理，不允许外部执行器同时领取")
		}
		if s.KServeConfig.Enabled && (action == string(MLKServeCreate) || action == string(MLKServeUpdate) || action == string(MLKServeDeploy) || action == string(MLKServeStop) || action == string(MLKServeDelete)) && peer != s.KServePeer() {
			return nil, apperr.New(apperr.CodeConflict, "KServe 已由内置 worker 管理，不允许外部执行器同时领取")
		}
		if !mlExecutionAction(action) || seen[action] {
			return nil, mlExecutionInvalid()
		}
		seen[action] = true
	}
	var out *MLExecutionClaim
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		if err := mlExecutionAuthorizationFence(ctx, st); err != nil {
			return err
		}
		o := &model.Operation{}
		err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("cluster = ? AND action IN ? AND target_type IN ?", in.Cluster, in.Actions, []string{"pipeline_run", "trainer_job", "kserve_service"}).
			Where("(status IN ? AND next_attempt_at <= UTC_TIMESTAMP(3)) OR (status = ? AND (lease_until IS NULL OR lease_until <= UTC_TIMESTAMP(3)))",
				[]string{"PENDING", "RETRYING"}, "RUNNING").Order("next_attempt_at").Order("id").First(o).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		c, err := mlExecutionCommand(o)
		if err != nil {
			// Quarantine malformed persisted commands without reflecting payloads.
			return st.DB.WithContext(ctx).Model(o).Updates(map[string]any{"status": "FAILED", "step": "INVALID_COMMAND",
				"last_error": "INVALID_COMMAND", "lease_owner": nil, "lease_until": nil, "finished_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error
		}
		// No domain lock here: ordinary mutations lock domain then operation.
		// Read-committed intent validation avoids an inverse lock order; report
		// and renew recheck under domain+operation locks before any state write.
		n, err := loadML(ctx, st, o.TargetType, c.GroupID, c.ResourceID, false)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err != nil || !mlExecutionMatches(n, o, c) {
			return st.DB.WithContext(ctx).Model(o).Updates(map[string]any{"status": "SUPERSEDED", "step": "SUPERSEDED",
				"lease_owner": nil, "lease_until": nil, "finished_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error
		}
		g, err := mlExecutionGrant(ctx, st, o, c, n)
		if err != nil {
			code := apperr.From(err).Code
			if code != apperr.CodeForbidden && code != apperr.CodeConflict && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// Do not claim unauthorized activation, or pretend it was stopped.
			// Only the operation is ended; cleanup requires a separate intent.
			return st.DB.WithContext(ctx).Model(o).Updates(map[string]any{"status": "FAILED", "step": "AUTHORIZATION_REVOKED",
				"last_error": "POLICY_DENIED", "lease_owner": nil, "lease_until": nil, "finished_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error
		}
		e := &mlExecution{}
		err = st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id = ?", o.ID).First(e).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			e = &mlExecution{OperationID: o.ID, TargetType: o.TargetType, InstanceID: c.InstanceID,
				Revision: c.Revision, ExternalID: n.state.ExternalID, LeasePeerSHA256: peer,
				GroupInstanceID: &g.InstanceID, NamespaceUID: g.NamespaceUID, ProfileUID: g.ProfileUID}
			if err := st.DB.WithContext(ctx).Create(e).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if e.GroupInstanceID == nil && e.NamespaceUID == nil && e.ProfileUID == nil {
			// Upgrade of an existing receipt: bind at reclaim only, never in
			// renew/report. A currently held pre-upgrade lease fails closed.
			if err := st.DB.WithContext(ctx).Model(e).Updates(map[string]any{"group_instance_id": g.InstanceID,
				"namespace_uid": g.NamespaceUID, "profile_uid": g.ProfileUID}).Error; err != nil {
				return err
			}
			e.GroupInstanceID, e.NamespaceUID, e.ProfileUID = &g.InstanceID, g.NamespaceUID, g.ProfileUID
		}
		if !mlExecutionSpaceMatches(e, g) {
			return st.DB.WithContext(ctx).Model(o).Updates(map[string]any{"status": "SUPERSEDED", "step": "SPACE_IDENTITY_CHANGED",
				"lease_owner": nil, "lease_until": nil, "finished_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error
		}
		if e.InstanceID != c.InstanceID || e.Revision != c.Revision || e.TargetType != o.TargetType ||
			(e.ExternalID != nil && (n.state.ExternalID == nil || *e.ExternalID != *n.state.ExternalID)) {
			return mlExecutionConflict()
		}
		owner := uuid.NewString()
		if err := st.DB.WithContext(ctx).Model(o).Updates(map[string]any{"status": "RUNNING", "step": "EXECUTING",
			"lease_owner": owner, "lease_until": gorm.Expr("TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(3))"), "finished_at": nil}).Error; err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(e).Update("lease_peer_sha256", peer).Error; err != nil {
			return err
		}
		o, err = st.Operation(ctx, o.ID)
		if err != nil {
			return err
		}
		c.ExternalID = e.ExternalID
		out = &MLExecutionClaim{MLExecutionView: mlExecutionView(o, c, e), LeaseOwner: owner, Command: *c}
		return nil
	})
	return out, err
}

func mlExecutionLeaseValid(peer string, in dto.MLExecutionLeaseReq) bool {
	owner, err := uuid.Parse(in.LeaseOwner)
	return validator.IsSHA256(peer) && err == nil && owner.String() == in.LeaseOwner &&
		in.Revision > 0 && validator.IsSHA256(in.TargetFence)
}

// mlLockExecution follows the same domain -> operation order as ML mutations.
// It deliberately does not demand a live lease yet, so an exact already-committed
// report can be acknowledged after a lost response without performing a write.
func mlLockExecution(ctx context.Context, st *repository.Store, id string) (*model.Operation, *MLCommand, *mlObject, *mlExecution, error) {
	if err := mlExecutionAuthorizationFence(ctx, st); err != nil {
		return nil, nil, nil, nil, err
	}
	o, err := st.Operation(ctx, id)
	if err != nil {
		return nil, nil, nil, nil, domainError(err)
	}
	c, err := mlExecutionCommand(o)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if _, err := mlExecutionGroup(ctx, st, c); err != nil {
		return nil, nil, nil, nil, err
	}
	n, err := loadML(ctx, st, o.TargetType, c.GroupID, c.ResourceID, true)
	if err != nil {
		return nil, nil, nil, nil, domainError(err)
	}
	if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id = ?", id).First(o).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	e := &mlExecution{}
	if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id = ?", id).First(e).Error; err != nil {
		return nil, nil, nil, nil, domainError(err)
	}
	return o, c, n, e, nil
}

func mlLiveExecution(ctx context.Context, st *repository.Store, o *model.Operation, c *MLCommand, n *mlObject, e *mlExecution,
	peer string, in dto.MLExecutionLeaseReq) error {
	_, fence := mlExecutionKeys(o, c)
	if !mlExecutionMatches(n, o, c) || e.TargetType != o.TargetType || e.InstanceID != c.InstanceID || e.Revision != c.Revision ||
		in.InstanceID != c.InstanceID || in.Revision != c.Revision || in.TargetFence != fence || e.LeasePeerSHA256 != peer ||
		(e.ExternalID != nil && (n.state.ExternalID == nil || *e.ExternalID != *n.state.ExternalID)) {
		return mlExecutionConflict()
	}
	var count int64
	err := mlLiveExecutionQuery(ctx, st, o.ID, in.LeaseOwner).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return mlExecutionConflict()
	}
	g, err := mlExecutionGrant(ctx, st, o, c, n)
	if err != nil {
		return err
	}
	if !mlExecutionSpaceMatches(e, g) {
		return mlExecutionConflict()
	}
	return nil
}

func mlLiveExecutionQuery(ctx context.Context, st *repository.Store, id, owner string) *gorm.DB {
	return st.DB.WithContext(ctx).Model(&model.Operation{}).
		Where("operation_id = ? AND status = 'RUNNING' AND lease_owner = ? AND lease_until > UTC_TIMESTAMP(3)", id, owner)
}

func (s *Service) RenewMLExecution(ctx context.Context, peer, id string, in dto.MLExecutionLeaseReq) (*MLExecutionView, error) {
	if !mlExecutionLeaseValid(peer, in) {
		return nil, mlExecutionInvalid()
	}
	var out *MLExecutionView
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, id)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, peer, in); err != nil {
			return err
		}
		r := mlLiveExecutionQuery(ctx, st, id, in.LeaseOwner).Update("lease_until",
			gorm.Expr("GREATEST(TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(3)),TIMESTAMPADD(MICROSECOND,1000,lease_until))"))
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		o, err = st.Operation(ctx, id)
		if err != nil {
			return err
		}
		out = mlExecutionView(o, c, e)
		return nil
	})
	return out, err
}

func mlExecutionReason(reason string) bool {
	switch reason {
	case "", "REFERENCE_INVALID", "POLICY_DENIED", "EXTERNAL_UNAVAILABLE", "EXTERNAL_REJECTED",
		"RESOURCE_FAILED", "RESOURCE_NOT_FOUND", "TIMEOUT", "INTERNAL_ERROR", "SUBMISSION_UNCERTAIN", "IDENTITY_CONFLICT":
		return true
	}
	return false
}

func mlExecutionReportValid(peer string, in dto.ReportMLExecutionReq) bool {
	if !mlExecutionLeaseValid(peer, in.MLExecutionLeaseReq) || in.Sequence == 0 ||
		!validator.IsIdempotencyKey(in.ReportID) || !mlExecutionReason(in.Reason) ||
		(in.ExternalID != "" && !validator.IsCatalogID(in.ExternalID)) {
		return false
	}
	switch in.Outcome {
	case "OBSERVED":
		return in.ObservedState != ""
	case "RETRY", "FAILED":
		return in.Reason != "" && in.ObservedState == "" && !in.ResourceAbsent
	}
	return false
}

// mlObservation accepts actual observed lifecycle, not adapter submission success.
// Long-running train/run operations stay leased until a real terminal observation.
func mlObservation(c *MLCommand, n *mlObject, e *mlExecution, in dto.ReportMLExecutionReq) (string, string, error) {
	state := in.ObservedState
	switch state {
	case "UNKNOWN", "SUBMITTED", "PENDING", "RUNNING", "SERVING", "SUCCEEDED", "FAILED", "STOPPED", "DELETED":
	default:
		return "", "", mlExecutionInvalid()
	}
	if (in.ResourceAbsent && state != "DELETED" && state != "STOPPED") ||
		(in.ResourceAbsent && state == "STOPPED" && c.DesiredState != "STOPPED") ||
		(state == "DELETED" && (c.DesiredState != "DELETED" || !in.ResourceAbsent)) ||
		(state == "STOPPED" && c.DesiredState != "STOPPED" && c.DesiredState != "DELETED" && c.PipelineRun == nil) ||
		(state == "SERVING" && c.KServe == nil) ||
		(state == "RUNNING" && c.KServe != nil) ||
		(state == "SUCCEEDED" && c.KServe != nil) ||
		(state != "UNKNOWN" && state != "DELETED" && !(state == "STOPPED" && in.ResourceAbsent) && in.ExternalID == "") {
		return "", "", mlExecutionInvalid()
	}
	// Terminal resource observations cannot regress even if somebody retries an
	// operation. A subsequent explicit delete may only advance them to DELETED.
	previous := n.state.ObservedState
	if e.LastObservedState != nil {
		previous = *e.LastObservedState
	}
	if previous == "SUCCEEDED" || previous == "FAILED" || previous == "DELETED" || previous == "STOPPED" {
		if state != previous && !(c.DesiredState == "DELETED" && state == "DELETED" && in.ResourceAbsent) &&
			!(c.Action == MLPipelineEdit && state == "STOPPED" && in.ResourceAbsent) &&
			!(c.KServe != nil && c.DesiredState != "DELETED" && n.state.AppliedRevision < c.Revision && e.LastObservedState == nil) {
			return "", "", mlExecutionConflict()
		}
	}
	// Nonterminal observations must also not move backwards within a revision.
	rank := map[string]int{"UNKNOWN": 0, "SUBMITTED": 1, "PENDING": 2, "RUNNING": 3, "SERVING": 4}
	if e.LastObservedState != nil {
		oldRank, oldOK := rank[*e.LastObservedState]
		newRank, newOK := rank[state]
		if oldOK && newOK && newRank < oldRank {
			return "", "", mlExecutionConflict()
		}
	}
	if c.DesiredState == "DELETED" && state != "DELETED" {
		// A finished job, or a failed/serving service, is still present. Its
		// old terminal state is not evidence that deletion has completed.
		switch state {
		case "SUCCEEDED", "FAILED", "STOPPED", "SERVING", "RUNNING":
			return "RUNNING", state, nil
		default:
			return "RUNNING", "PENDING", nil
		}
	}
	if c.Action == MLPipelineEdit && !in.ResourceAbsent {
		// 旧 Run 已 CANCELED/SUCCEEDED/FAILED 仍不算编辑完成，必须确认外部删除。
		if state == "UNKNOWN" || state == "SUBMITTED" {
			return "RUNNING", "PENDING", nil
		}
		return "RUNNING", state, nil
	}
	switch state {
	case "FAILED":
		return "FAILED", "FAILED", nil
	case "DELETED":
		return "SUCCEEDED", "DELETED", nil
	case "STOPPED":
		return "SUCCEEDED", "STOPPED", nil
	case "SUCCEEDED":
		return "SUCCEEDED", "SUCCEEDED", nil
	case "SERVING":
		if c.DesiredState == "SERVING" {
			return "SUCCEEDED", "SERVING", nil
		}
	case "RUNNING":
		return "RUNNING", "RUNNING", nil
	}
	return "RUNNING", "PENDING", nil
}

func (s *Service) ReportMLExecution(ctx context.Context, peer, id string, in dto.ReportMLExecutionReq) (*MLExecutionView, error) {
	if !mlExecutionReportValid(peer, in) {
		return nil, mlExecutionInvalid()
	}
	b, _ := json.Marshal(in)
	digest := mlHash(b)
	var out *MLExecutionView
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, id)
		if err != nil {
			return err
		}
		// Exact replay is a receipt read, not a late observation write. It is
		// safe after completion/expiry/supersession; changed content is rejected.
		if in.Sequence == e.ObservationSequence && e.LastReportSHA256 != nil && *e.LastReportSHA256 == digest &&
			e.LastReportOwner != nil && *e.LastReportOwner == in.LeaseOwner && e.LastReportPeerSHA256 != nil && *e.LastReportPeerSHA256 == peer {
			out = mlExecutionView(o, c, e)
			return nil
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, peer, in.MLExecutionLeaseReq); err != nil {
			return err
		}
		if in.Sequence <= e.ObservationSequence || (e.LastReportID != nil && *e.LastReportID == in.ReportID) {
			return mlExecutionConflict()
		}
		// Binding may be set once, never replaced/cleared within this revision.
		if (e.ExternalID != nil && in.ExternalID != *e.ExternalID) ||
			(n.state.ExternalID != nil && in.ExternalID != *n.state.ExternalID) {
			return mlExecutionConflict()
		}
		opStatus, domainStatus, step := "RUNNING", n.state.Status, "OBSERVING"
		// Pipeline 相同状态/故障只刷新回执，不反复追加包含完整 IR 的历史快照。
		oldReason := ""
		if n.state.StatusReason != nil {
			oldReason = *n.state.StatusReason
		}
		recordObservation := c.PipelineRun == nil || oldReason != in.Reason ||
			(in.Outcome == "OBSERVED" && (e.LastObservedState == nil || *e.LastObservedState != in.ObservedState)) ||
			(in.Outcome == "FAILED" && n.state.Status != "ERROR")
		domain := map[string]any{}
		if in.Outcome == "OBSERVED" {
			opStatus, domainStatus, err = mlObservation(c, n, e, in)
			if err != nil {
				return err
			}
			if c.Action == MLPipelineEdit && in.ObservedState == "STOPPED" && in.ResourceAbsent {
				// 先归档旧执行状态/配置，再把业务对象推进到待启动的新配置。
				if err := applyPipelineEdit(ctx, st, c, n, domain, in.ExternalID); err != nil {
					return err
				}
			}
			observed := in.ObservedState
			if observed == "SUBMITTED" {
				// SUBMITTED is an adapter receipt; domain schemas intentionally
				// keep a submitted-but-not-running workload at PENDING.
				observed = "PENDING"
			}
			domain["observed_state"], domain["observed_at"], domain["status"] = observed, gorm.Expr("UTC_TIMESTAMP(3)"), domainStatus
			n.state.ObservedState, n.state.Status = observed, domainStatus
			e.LastObservedState = &in.ObservedState
			// 停止与自然失败竞态时仍记录已确认的终态 revision，允许后续删除；
			// 这里只接受资源 FAILED 观测，不包括传输/策略的执行失败。
			if (c.DesiredState == "RUNNING" && in.ExternalID != "" && observed != "UNKNOWN") || opStatus == "SUCCEEDED" ||
				(c.PipelineRun != nil && observed == "FAILED" && in.ExternalID != "") {
				domain["applied_revision"] = c.Revision
				n.state.AppliedRevision = c.Revision
			}
			if in.ObservedState == "DELETED" {
				domain["deleted_at"], domain["applied_revision"] = gorm.Expr("UTC_TIMESTAMP(3)"), c.Revision
				n.state.AppliedRevision = c.Revision
			}
			if c.KServe != nil && in.ObservedState == "STOPPED" && in.ResourceAbsent {
				// KServe 停止即删除 CR；配置与旧 UID 保留在修订和执行回执中。
				domain["external_id"], domain["sync_enabled"] = nil, false
				domain["kserve_ready"], domain["kserve_url"], domain["kserve_address"] = false, nil, nil
				n.state.ExternalID = nil
			}
		} else if in.Outcome == "RETRY" {
			opStatus, step = "RETRYING", "WAIT_EXECUTOR"
		} else {
			opStatus, step = "FAILED", "EXECUTION_FAILED"
			// Transport/policy errors must not fabricate a terminal resource state.
			// Preserve a previously observed resource terminal state on retries.
			if n.state.ObservedState != "SUCCEEDED" && n.state.ObservedState != "FAILED" && n.state.ObservedState != "DELETED" && n.state.ObservedState != "STOPPED" {
				domain["status"], n.state.Status = "ERROR", "ERROR"
			}
		}
		if in.ExternalID != "" && e.ExternalID == nil {
			e.ExternalID, n.state.ExternalID = &in.ExternalID, &in.ExternalID
			domain["external_id"] = in.ExternalID
		}
		var reason any
		if in.Reason != "" {
			reason = in.Reason
			n.state.StatusReason = &in.Reason
		} else {
			n.state.StatusReason = nil
		}
		domain["status_reason"] = reason
		// Instance/revision CAS as well as row locks guard every domain write.
		r := st.DB.WithContext(ctx).Model(n.row).Where("id = ? AND group_id = ? AND instance_id = ? AND revision = ? AND last_operation_id = ?",
			c.ResourceID, c.GroupID, c.InstanceID, c.Revision, o.ID).Updates(domain)
		if r.Error != nil {
			return r.Error
		}
		// MySQL may report zero changed rows for an identical observation. The
		// locked target was already matched; existence is checked above.
		if recordObservation {
			if err := mlRecord(ctx, st, o, n, "OBSERVATION"); err != nil {
				return err
			}
		}
		if err := result(o, map[string]any{"resourceId": c.ResourceID, "groupId": c.GroupID, "instanceId": c.InstanceID,
			"revision": c.Revision, "appliedRevision": n.state.AppliedRevision, "status": n.state.Status,
			"desiredState": n.state.DesiredState, "observedState": n.state.ObservedState,
			"externalId": e.ExternalID, "observationSequence": in.Sequence, "reason": in.Reason,
			"executionPending": opStatus == "RUNNING" || opStatus == "RETRYING"}); err != nil {
			return err
		}
		fields := map[string]any{"status": opStatus, "step": step, "result": string(o.Result), "last_error": reason}
		if opStatus == "SUCCEEDED" || opStatus == "FAILED" {
			fields["step"], fields["finished_at"] = "COMPLETED", gorm.Expr("UTC_TIMESTAMP(3)")
		}
		if opStatus != "RUNNING" {
			fields["lease_owner"], fields["lease_until"] = nil, nil
		} else {
			fields["lease_until"] = gorm.Expr("TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(3))")
		}
		if opStatus == "RETRYING" {
			fields["next_attempt_at"], fields["attempt"] = gorm.Expr("TIMESTAMPADD(SECOND,15,UTC_TIMESTAMP(3))"), gorm.Expr("attempt + 1")
		}
		// Recheck live lease in the final operation UPDATE. If the transaction
		// outlived its lease, roll back the domain/history/receipt changes too.
		r = mlLiveExecutionQuery(ctx, st, id, in.LeaseOwner).Updates(fields)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		if err := st.DB.WithContext(ctx).Model(e).Updates(map[string]any{
			"external_id": e.ExternalID, "observation_sequence": in.Sequence, "last_report_id": in.ReportID,
			"last_report_sha256": digest, "last_report_owner": in.LeaseOwner, "last_report_peer_sha256": peer,
			"last_observed_state": e.LastObservedState, "last_report_at": gorm.Expr("UTC_TIMESTAMP(3)"),
		}).Error; err != nil {
			return err
		}
		if opStatus == "SUCCEEDED" || opStatus == "FAILED" {
			actor := uint64(0)
			if err := st.DB.WithContext(ctx).Create(&model.Audit{OperationID: &o.ID, ActorID: &actor, ActorName: "ml-executor",
				GroupID: &c.GroupID, Action: o.Action, TargetType: o.TargetType, TargetID: o.TargetKey, Outcome: opStatus}).Error; err != nil {
				return err
			}
		}
		o, err = st.Operation(ctx, id)
		if err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Where("operation_id = ?", id).First(e).Error; err != nil {
			return err
		}
		out = mlExecutionView(o, c, e)
		return nil
	})
	return out, err
}

// MLExecutionOperation resolves response-loss ambiguity using one read-committed
// transaction. The operation row lock makes its receipt and sequence consistent.
// This machine-only query does not provide or renew lease-owner credentials.
func (s *Service) MLExecutionOperation(ctx context.Context, peer, id string) (*MLExecutionView, error) {
	if !validator.IsSHA256(peer) {
		return nil, mlExecutionInvalid()
	}
	var out *MLExecutionView
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o := &model.Operation{}
		if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("operation_id = ?", id).First(o).Error; err != nil {
			return domainError(err)
		}
		c, err := mlExecutionCommand(o)
		if err != nil {
			return err
		}
		e := &mlExecution{}
		err = st.DB.WithContext(ctx).Where("operation_id = ?", id).First(e).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			e = nil
		} else if err != nil {
			return err
		}
		out = mlExecutionView(o, c, e)
		return nil
	})
	return out, err
}

// ObserveMLExecution records KServe health after deploy has completed. This is
// evidence-only: no lease is minted, no external ID is bound, no revision/intent
// is advanced, and no terminal operation is reopened. Any allowed mTLS adapter
// may observe, including a replacement after a certificate rotation.
func (s *Service) ObserveMLExecution(ctx context.Context, peer, id string, in dto.ObserveMLExecutionReq) (*MLExecutionView, error) {
	if s.KServeConfig.Enabled && peer != s.KServePeer() {
		return nil, apperr.New(apperr.CodeConflict, "KServe 已由内置 worker 观测，不接受外部观察者同时写入")
	}
	instance, err := uuid.Parse(in.InstanceID)
	if !validator.IsSHA256(peer) || err != nil || instance.String() != in.InstanceID || in.Revision == 0 ||
		!validator.IsSHA256(in.TargetFence) || !validator.IsCatalogID(in.ExternalID) || in.Sequence == 0 ||
		!validator.IsIdempotencyKey(in.ReportID) || !mlExecutionReason(in.Reason) {
		return nil, mlExecutionInvalid()
	}
	switch in.ObservedState {
	case "SERVING", "PENDING", "UNKNOWN":
	case "ERROR", "FAILED":
		if in.Reason == "" {
			return nil, mlExecutionInvalid()
		}
	default:
		return nil, mlExecutionInvalid()
	}
	b, _ := json.Marshal(in)
	digest := mlHash(b)
	var out *MLExecutionView
	err = repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, id)
		if err != nil {
			return err
		}
		_, fence := mlExecutionKeys(o, c)
		if c.KServe == nil || o.Status != "SUCCEEDED" || c.DesiredState != "SERVING" || !mlExecutionMatches(n, o, c) ||
			n.state.AppliedRevision != c.Revision || in.InstanceID != c.InstanceID || in.Revision != c.Revision ||
			in.TargetFence != fence || e.TargetType != o.TargetType || e.InstanceID != in.InstanceID || e.Revision != in.Revision ||
			e.ExternalID == nil || *e.ExternalID != in.ExternalID || n.state.ExternalID == nil || *n.state.ExternalID != in.ExternalID {
			return mlExecutionConflict()
		}
		g, err := mlExecutionGroup(ctx, st, c)
		if err != nil {
			return err
		}
		if g.DeletedAt != nil || g.Status == "DELETED" || g.Cluster != c.Cluster || g.Name != c.Namespace || !mlExecutionSpaceMatches(e, g) {
			return mlExecutionConflict()
		}
		// No RequestedBy permission check: observing existing reality must keep
		// working after user revocation/freeze. This cannot authorize execution.
		if in.Sequence == e.WatchSequence && e.WatchReportSHA256 != nil && *e.WatchReportSHA256 == digest &&
			e.WatchPeerSHA256 != nil && *e.WatchPeerSHA256 == peer {
			out = mlExecutionView(o, c, e)
			return nil
		}
		if in.Sequence <= e.WatchSequence || (e.WatchReportID != nil && *e.WatchReportID == in.ReportID) {
			return mlExecutionConflict()
		}
		if n.state.ObservedState == "DELETED" || n.state.ObservedState == "STOPPED" ||
			(n.state.ObservedState == "FAILED" && in.ObservedState != "FAILED") {
			return mlExecutionConflict()
		}
		observed, status := in.ObservedState, in.ObservedState
		if in.ObservedState == "ERROR" {
			// Transport/watch failure is not a confirmed terminal CR failure.
			observed = "UNKNOWN"
		}
		var reason any
		if in.Reason != "" {
			reason = in.Reason
		}
		r := st.DB.WithContext(ctx).Model(n.row).
			Where("id = ? AND group_id = ? AND instance_id = ? AND revision = ? AND external_id = ? AND applied_revision = ? AND last_operation_id = ? AND desired_state = ? AND deleted_at IS NULL",
				c.ResourceID, c.GroupID, in.InstanceID, in.Revision, in.ExternalID, in.Revision, o.ID, "SERVING").
			Updates(map[string]any{"observed_state": observed, "status": status, "status_reason": reason, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")})
		if r.Error != nil {
			return r.Error
		}
		// The target is row-locked and matched above, so zero changed rows is
		// valid for identical health within the same DB millisecond.
		n.state.ObservedState, n.state.Status = observed, status
		if err := mlRecord(ctx, st, o, n, "RUNTIME_OBSERVATION"); err != nil {
			return err
		}
		r = st.DB.WithContext(ctx).Model(e).Where("watch_sequence = ? AND instance_id = ? AND revision = ? AND external_id = ?",
			e.WatchSequence, in.InstanceID, in.Revision, in.ExternalID).Updates(map[string]any{
			"watch_sequence": in.Sequence, "watch_report_id": in.ReportID, "watch_report_sha256": digest,
			"watch_peer_sha256": peer, "watch_observed_state": in.ObservedState, "watch_observed_at": gorm.Expr("UTC_TIMESTAMP(3)"),
		})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		if err := st.DB.WithContext(ctx).Where("operation_id = ?", id).First(e).Error; err != nil {
			return err
		}
		out = mlExecutionView(o, c, e)
		return nil
	})
	return out, err
}
