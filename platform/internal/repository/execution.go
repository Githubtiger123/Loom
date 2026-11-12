package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrLeaseLost = errors.New("任务租约已失效")

func (s *Store) LockAuthorization(ctx context.Context) error {
	var role model.Role
	return s.query(ctx, true).Select("id").Where("code = ?", "platform_admin").First(&role).Error
}

type Claim struct {
	*model.Operation
	Token    string
	Deadline time.Time
}

func ClaimOperation(ctx context.Context, db *gorm.DB, cluster, token string) (*Claim, error) {
	var claim *Claim
	err := InTx(ctx, db, func(st *Store) error {
		o := &model.Operation{}
		err := st.query(ctx, false).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("cluster = ? AND action IN ?", cluster, []string{"group.create", "group.disable", "group.restore", "group.delete", "group.quota.update", "workspace.delete", "user.DISABLED", "member.roles.replace", "access.reconcile", "notebook.create", "notebook.start", "notebook.stop", "notebook.update", "notebook.delete", "model.pipeline.import"}).
			Where("(status IN ? AND next_attempt_at <= UTC_TIMESTAMP(3)) OR (status = ? AND lease_until < UTC_TIMESTAMP(3))",
				[]string{"PENDING", "RETRYING"}, "RUNNING").Order("next_attempt_at").Order("id").First(o).Error
		if err != nil {
			return err
		}
		if err := st.query(ctx, false).Model(&model.Operation{}).Where("operation_id = ?", o.ID).
			Updates(map[string]any{"status": "RUNNING", "lease_owner": token,
				"lease_until": gorm.Expr("TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(3))"),
				"deadline_at": gorm.Expr("COALESCE(deadline_at,TIMESTAMPADD(MINUTE,30,UTC_TIMESTAMP(3)))")}).Error; err != nil {
			return err
		}
		o, err = st.Operation(ctx, o.ID)
		if err != nil {
			return err
		}
		if o.DeadlineAt == nil {
			return fmt.Errorf("任务 deadline_at 未设置")
		}
		claim = &Claim{Operation: o, Token: token, Deadline: *o.DeadlineAt}
		return nil
	})
	return claim, err
}
func (s *Store) liveClaim(ctx context.Context, c *Claim) *gorm.DB {
	return s.query(ctx, false).Model(&model.Operation{}).
		Where("operation_id = ? AND lease_owner = ? AND status = ? AND lease_until > UTC_TIMESTAMP(3)", c.ID, c.Token, "RUNNING")
}
func (s *Store) Renew(ctx context.Context, c *Claim) error {
	r := s.liveClaim(ctx, c).Update("lease_until",
		gorm.Expr("GREATEST(TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(3)),TIMESTAMPADD(MICROSECOND,1000,lease_until))"))
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Fence 在短事务中检查领取令牌，领域状态与操作结果必须同事务提交。
func (s *Store) Fence(ctx context.Context, c *Claim) error {
	var o model.Operation
	err := s.liveClaim(ctx, c).Clauses(clause.Locking{Strength: "UPDATE"}).Select("operation_id").First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrLeaseLost
	}
	return err
}
func (s *Store) Finish(ctx context.Context, c *Claim, status, step, message string, delay time.Duration, failedAttempt bool) error {
	var lastError any
	if message != "" {
		lastError = message
	}
	var finished any
	terminal := status == "SUCCEEDED" || status == "FAILED" || status == "SUPERSEDED"
	if terminal {
		finished = gorm.Expr("UTC_TIMESTAMP(3)")
	}
	attempt := 0
	if failedAttempt {
		attempt = 1
	}
	r := s.liveClaim(ctx, c).Updates(map[string]any{
		"status": status, "step": step, "last_error": lastError, "attempt": gorm.Expr("attempt + ?", attempt),
		"next_attempt_at": gorm.Expr("TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(3))", delay.Microseconds()),
		"lease_owner":     nil, "lease_until": nil, "finished_at": finished,
	})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrLeaseLost
	}
	if terminal {
		actor := uint64(0)
		a := &model.Audit{OperationID: &c.ID, ActorID: &actor, ActorName: "worker", Action: c.Action,
			TargetType: c.TargetType, TargetID: c.TargetKey, Outcome: status}
		if message != "" {
			a.Reason = &message
		}
		return s.query(ctx, false).Create(a).Error
	}
	return nil
}
func TargetLock(ctx context.Context, db *gorm.DB, cluster, targetType, target string) (*inframysql.AdvisoryLock, error) {
	name := fmt.Sprintf("%x", sha256.Sum256([]byte(cluster+"/"+targetType+"/"+target)))
	return inframysql.AcquireLock(ctx, db, name, 0)
}
func CheckTargetLock(ctx context.Context, lock *inframysql.AdvisoryLock) error {
	if err := lock.Check(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrLeaseLost, err)
	}
	return nil
}
