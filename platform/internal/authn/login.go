package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/password"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (m *Manager) limit(ctx context.Context, key string, ttl time.Duration, maximum int) error {
	allowed, err := repository.New(m.DB).TakeAuthRate(ctx, digest(m.Config.Issuer+"\x00"+key), ttl, maximum)
	if err != nil {
		return unavailable(err)
	}
	if !allowed {
		return apperr.New(apperr.CodeRateLimited, "认证尝试过多，请稍后重试")
	}
	return nil
}

func (m *Manager) Login(ctx context.Context, in dto.LoginReq, peer string) (*Tokens, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if err := m.limit(ctx, "ip:"+digest(peer), time.Minute, 60); err != nil {
		return nil, err
	}
	if err := m.limit(ctx, "user:"+digest(m.Config.Issuer+"\x00"+in.Username), 15*time.Minute, 10); err != nil {
		return nil, err
	}
	st := repository.New(m.DB)
	u, err := st.LoginUser(ctx, m.Config.Issuer, in.Username)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, unavailable(err)
	}
	encoded := m.dummy
	var credential *model.LocalCredential
	if err == nil {
		credential, err = st.Credential(ctx, u.ID, false)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, unavailable(err)
		}
		if err == nil {
			encoded = credential.PasswordHash
		} else {
			credential = nil
		}
	}
	valid := false
	if err := m.withPasswordSlot(func() error { valid = password.Verify(encoded, in.Password); return nil }); err != nil {
		return nil, err
	}
	if !valid || credential == nil || u.Status != "ACTIVE" || u.Subject == nil || u.DeletedAt != nil {
		// 未知账号同样经过 Argon2id，审计不记录输入用户名或密码。
		if err := m.audit(ctx, 0, "auth.login", "DENIED"); err != nil {
			return nil, err
		}
		return nil, denied()
	}
	now := time.Now().UTC()
	expires := now.Add(m.Config.SessionTTL.Std())
	sid := uuid.NewString()
	tokens, err := m.issue(u, sid, expires)
	if err != nil {
		return nil, err
	}
	// Token 双记录、登录时间和审计一起提交；缓存写入不参与发放事务。
	err = repository.InTx(ctx, m.DB, func(tx *repository.Store) error {
		if err := tx.LockAuthorization(ctx); err != nil {
			return unavailable(err)
		}
		current, err := tx.User(ctx, u.ID, true)
		if err != nil {
			return tokenReadError(err)
		}
		latest, err := tx.Credential(ctx, u.ID, true)
		if err != nil {
			return tokenReadError(err)
		}
		if current.Status != "ACTIVE" || current.DeletedAt != nil || current.Issuer != m.Config.Issuer || current.Subject == nil ||
			current.SessionVersion != u.SessionVersion || latest.PasswordHash != encoded {
			return denied()
		}
		tokens.PasswordChangeRequired = latest.PasswordChangeRequired
		if err := tx.InsertAuthSession(ctx, &model.AuthSession{FamilyID: sid, AuthDomain: m.domain(), UserID: current.ID,
			SessionVersion: current.SessionVersion, CreatedAt: now, LastRefreshedAt: now, ExpiresAt: expires}); err != nil {
			return unavailable(err)
		}
		if err := tx.RecordLogin(ctx, u.ID); err != nil {
			return unavailable(err)
		}
		if err := tx.InsertAuthTokens(ctx, tokens.records(current.SessionVersion)); err != nil {
			return unavailable(err)
		}
		return authAudit(ctx, tx, u.ID, "auth.login", "SUCCEEDED")
	})
	if err != nil {
		return nil, authFailure(err)
	}
	m.cacheAccess(ctx, tokens)
	return tokens, nil
}

func authAudit(ctx context.Context, st *repository.Store, userID uint64, action, outcome string) error {
	name := "anonymous"
	var actor *uint64
	if userID > 0 {
		u, err := st.User(ctx, userID, false)
		if err != nil {
			return err
		}
		name, actor = u.Username, &userID
	}
	return st.DB.WithContext(ctx).Create(&model.Audit{ActorID: actor, ActorName: name, Action: action,
		TargetType: "authentication", TargetID: fmt.Sprint(userID), Outcome: outcome}).Error
}

func (m *Manager) audit(ctx context.Context, userID uint64, action, outcome string) error {
	return authFailure(authAudit(ctx, repository.New(m.DB), userID, action, outcome))
}
