package authn

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/password"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) ChangePassword(ctx context.Context, actor *model.User, in dto.ChangePasswordReq) error {
	if actor == nil {
		return denied()
	}
	if err := in.Validate(); err != nil {
		return err
	}
	credential, err := repository.New(m.DB).Credential(ctx, actor.ID, false)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return denied()
	}
	if err != nil {
		return unavailable(err)
	}
	if err := m.limit(ctx, "password:"+number(actor.ID), 15*time.Minute, 10); err != nil {
		return err
	}
	return m.storePassword(ctx, actor.ID, actor.ID, actor.SessionVersion, in.NewPassword, false, false, func() error {
		if !password.Verify(credential.PasswordHash, in.CurrentPassword) {
			return denied()
		}
		if password.Verify(credential.PasswordHash, in.NewPassword) {
			return apperr.New(apperr.CodeInvalid, "新密码必须与当前密码不同")
		}
		return nil
	})
}

// RequirePasswordChanged 供业务中间件和网关共用。每次读取 SQL，不能用 JWT 声明或缓存的标记放行。
// 改密、会话状态、退出接口可接受 Access 返回的受限 Principal，业务接口必须调用本门禁。
func (m *Manager) RequirePasswordChanged(ctx context.Context, actor *model.User) error {
	if actor == nil {
		return denied()
	}
	if _, err := m.currentUser(ctx, number(actor.ID), number(actor.SessionVersion)); err != nil {
		return err
	}
	credential, err := repository.New(m.DB).Credential(ctx, actor.ID, false)
	if err != nil {
		return tokenReadError(err)
	}
	if credential.PasswordChangeRequired {
		return PasswordChangeRequiredError()
	}
	return nil
}

func (m *Manager) ResetPassword(ctx context.Context, actor *model.User, target uint64, in dto.PasswordReq) error {
	if actor == nil {
		return denied()
	}
	if err := (rbac.Checker{Store: repository.New(m.DB)}).Require(ctx, actor.ID, 0, "user.manage"); err != nil {
		return err
	}
	return m.storePassword(ctx, actor.ID, target, actor.SessionVersion, in.Password, true, false, nil)
}

// ProvisionPassword 仅供显式运维命令使用，不注册匿名 HTTP 入口。
func (m *Manager) ProvisionPassword(ctx context.Context, target uint64, value string) error {
	return m.storePassword(ctx, 0, target, 0, value, true, true, nil)
}

// ProvisionLocalPassword 无 Redis 依赖，仅限调用者已明确选择的本地运维流程。
func ProvisionLocalPassword(ctx context.Context, db *gorm.DB, cfg config.Auth, target uint64, value string) error {
	if !cfg.Enabled || cfg.Issuer == "" || target == 0 {
		return apperr.New(apperr.CodeInvalid, "需启用本地认证并指定目标用户 ID")
	}
	m := &Manager{DB: db, Config: cfg, slots: make(chan struct{}, 1)}
	return m.ProvisionPassword(ctx, target, value)
}

func (m *Manager) storePassword(ctx context.Context, actor, target, version uint64, value string, reset, offline bool, verify func() error) error {
	if err := dto.ValidatePassword(value); err != nil {
		return err
	}
	var encoded string
	if err := m.withPasswordSlot(func() error {
		if verify != nil {
			if err := verify(); err != nil {
				return err
			}
		}
		var err error
		encoded, err = password.Hash(value)
		return err
	}); err != nil {
		return err
	}
	err := repository.InTx(ctx, m.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		if !offline {
			u, err := st.User(ctx, actor, false)
			if err != nil {
				return err
			}
			if u.Status != "ACTIVE" || u.DeletedAt != nil || u.Issuer != m.Config.Issuer || u.Subject == nil || u.SessionVersion != version {
				return denied()
			}
			if reset {
				credential, err := st.Credential(ctx, actor, false)
				if err != nil {
					return tokenReadError(err)
				}
				if credential.PasswordChangeRequired {
					return PasswordChangeRequiredError()
				}
				if err := (rbac.Checker{Store: st}).Require(ctx, actor, 0, "user.manage"); err != nil {
					return err
				}
			}
		}
		u, err := st.User(ctx, target, true)
		if err != nil {
			return err
		}
		if u.Status != "ACTIVE" || u.DeletedAt != nil || u.Issuer != m.Config.Issuer || u.Subject == nil {
			return apperr.New(apperr.CodeConflict, "只能为当前本地 issuer 下已启用且有稳定 subject 的用户设置密码")
		}
		if u.SessionVersion == ^uint64(0) {
			return apperr.New(apperr.CodeConflict, "登录版本已达到上限")
		}
		if err := st.DB.WithContext(ctx).Clauses(clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{
			"password_hash": encoded, "password_change_required": reset,
		})}).Create(&model.LocalCredential{UserID: target, PasswordHash: encoded, PasswordChangeRequired: reset}).Error; err != nil {
			return err
		}
		if err := st.RevokeUserAuthSessions(ctx, target, time.Now().UTC()); err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", target).Update("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
			return err
		}
		action := "auth.password.change"
		if reset {
			action = "auth.password.reset"
		}
		if offline {
			action = "auth.password.provision"
		}
		// 仅记录 actor 和 target，不记录散列、明文或原始请求体。
		record := &model.Audit{ActorName: "operator", Action: action, TargetType: "user", TargetID: strconv.FormatUint(target, 10), Outcome: "SUCCEEDED"}
		if actor > 0 {
			a, err := st.User(ctx, actor, false)
			if err != nil {
				return err
			}
			record.ActorID, record.ActorName = &actor, a.Username
		}
		return st.DB.WithContext(ctx).Create(record).Error
	})
	return authFailure(err)
}

func (m *Manager) LogoutAll(ctx context.Context, actor *model.User) error {
	if actor == nil {
		return denied()
	}
	err := repository.InTx(ctx, m.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		u, err := st.User(ctx, actor.ID, true)
		if err != nil {
			return err
		}
		if u.Status != "ACTIVE" || u.DeletedAt != nil || u.Issuer != m.Config.Issuer || u.Subject == nil || u.SessionVersion != actor.SessionVersion {
			return denied()
		}
		if u.SessionVersion == ^uint64(0) {
			return apperr.New(apperr.CodeConflict, "登录版本已达到上限")
		}
		if err := st.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", actor.ID).
			Update("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
			return err
		}
		if err := st.RevokeUserAuthSessions(ctx, actor.ID, time.Now().UTC()); err != nil {
			return err
		}
		return authAudit(ctx, st, actor.ID, "auth.logout_all", "SUCCEEDED")
	})
	return authFailure(err)
}
