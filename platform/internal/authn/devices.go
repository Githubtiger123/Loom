package authn

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

func (m *Manager) Sessions(ctx context.Context, principal *Principal, after string, limit int) (*dto.AuthSessionPage, error) {
	if principal == nil || principal.User == nil {
		return nil, denied()
	}
	if after != "" {
		if err := dto.ValidateAuthFamily(after); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 200 {
		return nil, apperr.New(apperr.CodeInvalid, "limit 必须在 1–200 之间")
	}
	var page *dto.AuthSessionPage
	err := repository.InTx(ctx, m.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		u, err := m.sessionActor(ctx, st, principal.User, true)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		current, err := st.AuthSession(ctx, principal.SessionID, m.domain(), u.ID, true)
		if err != nil {
			return tokenReadError(err)
		}
		if current.SessionVersion != u.SessionVersion || current.RevokedAt != nil || !current.ExpiresAt.After(now) {
			return denied()
		}
		rows, err := st.AuthSessions(ctx, m.domain(), u.ID, u.SessionVersion, after, limit+1, now)
		if err != nil {
			return err
		}
		page = &dto.AuthSessionPage{Items: []dto.AuthSession{}}
		if len(rows) > limit {
			rows = rows[:limit]
			page.NextAfter = rows[len(rows)-1].FamilyID
		}
		for _, row := range rows {
			page.Items = append(page.Items, dto.AuthSession{FamilyID: row.FamilyID, Current: row.FamilyID == principal.SessionID,
				CreatedAt: row.CreatedAt, LastRefreshedAt: row.LastRefreshedAt, ExpiresAt: row.ExpiresAt})
		}
		return nil
	})
	return page, authFailure(err)
}

// AdminSessions 仅返回同认证域内的有效设备元数据，不返回任何 Token/摘要。
func (m *Manager) AdminSessions(ctx context.Context, principal *Principal, target uint64, after string, limit int) (*dto.AuthSessionPage, error) {
	if principal == nil || principal.User == nil {
		return nil, denied()
	}
	if after != "" {
		if err := dto.ValidateAuthFamily(after); err != nil {
			return nil, err
		}
	}
	if target == 0 || limit < 1 || limit > 200 {
		return nil, apperr.New(apperr.CodeInvalid, "用户 ID 或分页参数无效")
	}
	var page *dto.AuthSessionPage
	err := repository.InTx(ctx, m.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		actor, err := m.sessionActor(ctx, st, principal.User, true)
		if err != nil {
			return err
		}
		if err := (rbac.Checker{Store: st}).Require(ctx, actor.ID, 0, "user.manage"); err != nil {
			return err
		}
		now := time.Now().UTC()
		current, err := st.AuthSession(ctx, principal.SessionID, m.domain(), actor.ID, true)
		if err != nil {
			return tokenReadError(err)
		}
		if current.SessionVersion != actor.SessionVersion || current.RevokedAt != nil || !current.ExpiresAt.After(now) {
			return denied()
		}
		u, err := st.User(ctx, target, true)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.New(apperr.CodeNotFound, "用户不存在")
			}
			return err
		}
		if u.Issuer != m.Config.Issuer || u.Subject == nil {
			return apperr.New(apperr.CodeInvalid, "目标不是当前认证域内的本地用户")
		}
		rows, err := st.AuthSessions(ctx, m.domain(), target, u.SessionVersion, after, limit+1, now)
		if err != nil {
			return err
		}
		page = &dto.AuthSessionPage{Items: []dto.AuthSession{}}
		if len(rows) > limit {
			rows = rows[:limit]
			page.NextAfter = rows[len(rows)-1].FamilyID
		}
		for _, row := range rows {
			page.Items = append(page.Items, dto.AuthSession{FamilyID: row.FamilyID, Current: target == actor.ID && row.FamilyID == principal.SessionID,
				CreatedAt: row.CreatedAt, LastRefreshedAt: row.LastRefreshedAt, ExpiresAt: row.ExpiresAt})
		}
		return nil
	})
	return page, authFailure(err)
}

// sessionActor 在持有授权锁的事务内重新校验用户版本及强制改密状态。
func (m *Manager) sessionActor(ctx context.Context, st *repository.Store, actor *model.User, requireChanged bool) (*model.User, error) {
	if actor == nil {
		return nil, denied()
	}
	u, err := st.User(ctx, actor.ID, true)
	if err != nil {
		return nil, tokenReadError(err)
	}
	if u.Status != "ACTIVE" || u.DeletedAt != nil || u.Issuer != m.Config.Issuer || u.Subject == nil || u.SessionVersion != actor.SessionVersion {
		return nil, denied()
	}
	if requireChanged {
		credential, err := st.Credential(ctx, u.ID, false)
		if err != nil {
			return nil, tokenReadError(err)
		}
		if credential.PasswordChangeRequired {
			return nil, PasswordChangeRequiredError()
		}
	}
	return u, nil
}

func (m *Manager) RevokeSession(ctx context.Context, actor *model.User, family string) error {
	if actor == nil {
		return denied()
	}
	return m.revokeSession(ctx, actor, actor.ID, family, false)
}

// 管理员沿用全局 user.manage 权限，不增加或修改 RBAC 权限字典。
func (m *Manager) AdminRevokeSession(ctx context.Context, actor *model.User, target uint64, family string) error {
	return m.revokeSession(ctx, actor, target, family, true)
}

func (m *Manager) revokeSession(ctx context.Context, actor *model.User, target uint64, family string, admin bool) error {
	if err := dto.ValidateAuthFamily(family); err != nil {
		return err
	}
	if target == 0 {
		return apperr.New(apperr.CodeInvalid, "用户 ID 无效")
	}
	err := repository.InTx(ctx, m.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		u, err := m.sessionActor(ctx, st, actor, true)
		if err != nil {
			return err
		}
		if admin {
			if err := (rbac.Checker{Store: st}).Require(ctx, u.ID, 0, "user.manage"); err != nil {
				return err
			}
		}
		// 先以 domain + 用户 + family 定位，不能猜 family 跨用户或认证域撤销。
		if _, err := st.AuthSession(ctx, family, m.domain(), target, true); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.New(apperr.CodeNotFound, "设备会话不存在")
			}
			return err
		}
		if err := st.RevokeTokenFamily(ctx, family, target, time.Now().UTC()); err != nil {
			return err
		}
		action := "auth.session.revoke"
		if admin {
			action = "auth.session.admin_revoke"
		}
		return sessionAudit(ctx, st, u, target, family, action)
	})
	return authFailure(err)
}

func sessionAudit(ctx context.Context, st *repository.Store, actor *model.User, target uint64, family, action string) error {
	return st.DB.WithContext(ctx).Create(&model.Audit{ActorID: &actor.ID, ActorName: actor.Username, Action: action,
		TargetType: "auth_session", TargetID: strconv.FormatUint(target, 10) + "/" + family, Outcome: "SUCCEEDED"}).Error
}
