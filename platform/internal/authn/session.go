package authn

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Refresh 使用独立 ID + 32 字节随机 secret；公开的 JWT sid 不是刷新凭据。
func splitSecret(raw string) (string, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(raw) > 128 {
		return "", false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil || id.String() != parts[0] {
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	return parts[0], err == nil && len(b) == 32
}

func sameHash(stored, raw string) bool {
	return subtle.ConstantTimeCompare([]byte(stored), []byte(digest(raw))) == 1
}

func tokenReadError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return denied()
	}
	return unavailable(err)
}

func (m *Manager) Access(ctx context.Context, raw string) (*Principal, error) {
	if len(raw) < 1 || len(raw) > 4096 {
		return nil, denied()
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) { return m.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(m.Config.Issuer), jwt.WithAudience(m.Config.Audience),
		jwt.WithExpirationRequired(), jwt.WithNotBeforeRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid || claims.Kind != "access" || claims.IssuedAt == nil || claims.ExpiresAt == nil ||
		!claims.ExpiresAt.After(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > m.Config.AccessTTL.Std() {
		return nil, denied()
	}
	if id, err := uuid.Parse(claims.ID); err != nil || id.String() != claims.ID {
		return nil, denied()
	}
	row, err := m.accessRecord(ctx, claims.ID, digest(raw))
	if err != nil {
		return nil, tokenReadError(err)
	}
	now := time.Now().UTC()
	if row.TokenType != "ACCESS" || row.AuthDomain != m.domain() || row.FamilyID != claims.SessionID || number(row.UserID) != claims.Subject ||
		number(row.SessionVersion) != claims.Version || row.RevokedAt != nil || row.ConsumedAt != nil ||
		!row.ExpiresAt.After(now) || !row.FamilyExpiresAt.After(now) || !sameHash(row.TokenHash, raw) {
		return nil, denied()
	}
	u, err := m.currentUser(ctx, claims.Subject, claims.Version)
	if err != nil {
		return nil, err
	}
	if _, err := m.activeSession(ctx, repository.New(m.DB), row, false, now); err != nil {
		return nil, err
	}
	credential, err := repository.New(m.DB).Credential(ctx, u.ID, false)
	if err != nil {
		return nil, tokenReadError(err)
	}
	// JWT 和 SQL 均有效才返回；Cookie 不能晚于任何一方的到期时间。
	expires := claims.ExpiresAt.Time
	if row.ExpiresAt.Before(expires) {
		expires = row.ExpiresAt
	}
	if row.FamilyExpiresAt.Before(expires) {
		expires = row.FamilyExpiresAt
	}
	return &Principal{User: u, SessionID: row.FamilyID, AccessExpiresAt: expires,
		PasswordChangeRequired: credential.PasswordChangeRequired}, nil
}

// Notebook 的 HttpOnly Cookie 复用 Access JWT，没有第三种浏览器身份凭据。
func (m *Manager) Browser(ctx context.Context, raw string) (*Principal, error) {
	p, err := m.Access(ctx, raw)
	if err == nil && p.PasswordChangeRequired {
		return nil, PasswordChangeRequiredError()
	}
	return p, err
}

// 保留通用 FORBIDDEN/403 契约，不增加第三类 token 或绕过 SQL 的 JWT 标记。
func PasswordChangeRequiredError() error {
	return apperr.New(apperr.CodeForbidden, "必须先修改初始或重置密码")
}

func (m *Manager) activeSession(ctx context.Context, st *repository.Store, token *model.AuthToken, lock bool, now time.Time) (*model.AuthSession, error) {
	session, err := st.AuthSession(ctx, token.FamilyID, m.domain(), token.UserID, lock)
	if err != nil {
		return nil, tokenReadError(err)
	}
	if session.SessionVersion != token.SessionVersion || session.RevokedAt != nil || !session.ExpiresAt.After(now) ||
		!session.ExpiresAt.Equal(token.FamilyExpiresAt) {
		return nil, denied()
	}
	return session, nil
}

func (m *Manager) Refresh(ctx context.Context, raw string) (*Tokens, error) {
	id, ok := splitSecret(raw)
	if !ok {
		return nil, denied()
	}
	// 先验证凭据再占限流桶，避免随机 ID 污染数据库、猜 ID 耗尽他人额度。
	preview, err := repository.New(m.DB).AuthToken(ctx, id, false)
	if err != nil {
		return nil, tokenReadError(err)
	}
	if preview.TokenType != "REFRESH" || preview.AuthDomain != m.domain() || !sameHash(preview.TokenHash, raw) ||
		preview.RevokedAt != nil || !preview.FamilyExpiresAt.After(time.Now()) {
		return nil, denied()
	}
	if err := m.limit(ctx, "refresh:"+preview.FamilyID, time.Minute, 20); err != nil {
		return nil, err
	}
	var tokens *Tokens
	var outcome error
	err = repository.InTx(ctx, m.DB, func(st *repository.Store) error {
		// 与密码、禁用和登出串行；Redis 无论健康与否都只有这一条轮换路径。
		if err := st.LockAuthorization(ctx); err != nil {
			return unavailable(err)
		}
		old, err := st.AuthToken(ctx, id, true)
		if err != nil {
			return tokenReadError(err)
		}
		// 先验证 secret，不能凭猜到的 token ID 撤销别人的设备。
		if old.TokenType != "REFRESH" || old.AuthDomain != m.domain() || !sameHash(old.TokenHash, raw) {
			return denied()
		}
		now := time.Now().UTC()
		if old.RevokedAt != nil || !old.ExpiresAt.After(now) || !old.FamilyExpiresAt.After(now) {
			return denied()
		}
		u, err := st.User(ctx, old.UserID, true)
		if err != nil {
			return tokenReadError(err)
		}
		if u.Status != "ACTIVE" || u.DeletedAt != nil || u.Issuer != m.Config.Issuer || u.Subject == nil || u.SessionVersion != old.SessionVersion {
			return denied()
		}
		if _, err := m.activeSession(ctx, st, old, true, now); err != nil {
			return err
		}
		credential, err := st.Credential(ctx, u.ID, false)
		if err != nil {
			return tokenReadError(err)
		}
		if old.ConsumedAt != nil {
			if now.Sub(*old.ConsumedAt) <= 10*time.Second {
				return apperr.New(apperr.CodeConflict, "刷新请求发生并发，请等待正在进行的刷新完成，不要并发重试")
			}
			if err := st.RevokeTokenFamily(ctx, old.FamilyID, old.UserID, now); err != nil {
				return unavailable(err)
			}
			if err := authAudit(ctx, st, u.ID, "auth.refresh_reuse", "REVOKED"); err != nil {
				return unavailable(err)
			}
			// 必须提交撤销，再向外返回 401；不能因返回错误而回滚撤销。
			outcome = denied()
			return nil
		}
		count, err := st.RefreshCount(ctx, old.FamilyID)
		if err != nil {
			return unavailable(err)
		}
		if count >= 4097 {
			if err := st.RevokeTokenFamily(ctx, old.FamilyID, old.UserID, now); err != nil {
				return unavailable(err)
			}
			if err := authAudit(ctx, st, u.ID, "auth.refresh_limit", "REVOKED"); err != nil {
				return unavailable(err)
			}
			outcome = denied()
			return nil
		}
		tokens, err = m.issue(u, old.FamilyID, old.FamilyExpiresAt)
		if err != nil {
			return err
		}
		tokens.PasswordChangeRequired = credential.PasswordChangeRequired
		if err := st.ConsumeRefresh(ctx, old.TokenID, tokens.refreshID, now); err != nil {
			return unavailable(err)
		}
		if err := st.InsertAuthTokens(ctx, tokens.records(u.SessionVersion)); err != nil {
			return unavailable(err)
		}
		if err := st.TouchAuthSession(ctx, old.FamilyID, m.domain(), u.ID, now); err != nil {
			return unavailable(err)
		}
		return nil
	})
	if err != nil {
		return nil, authFailure(err)
	}
	if outcome != nil {
		return nil, outcome
	}
	// 不保存可恢复的新凭据原文；响应丢失时必要情况下重新登录。
	m.cacheAccess(ctx, tokens)
	return tokens, nil
}

func (m *Manager) Logout(ctx context.Context, principal *Principal) error {
	return m.LogoutDevice(ctx, principal, "")
}

// LogoutDevice 允许有效 Refresh 独立撤销当前 family，不旋转或发放新凭据。
// 若同时提交 Access 和 Refresh，必须属于同一个用户、版本和 family。
// 任何失败均不能由 handler 清 Cookie，尤其不能把仍有活 Refresh 的设备遗留在 SQL 中。
func (m *Manager) LogoutDevice(ctx context.Context, principal *Principal, rawRefresh string) error {
	if principal == nil && rawRefresh == "" {
		return denied()
	}
	refreshID := ""
	if rawRefresh != "" {
		var ok bool
		refreshID, ok = splitSecret(rawRefresh)
		if !ok {
			return denied()
		}
		// 随机 ID / 错误 secret 不得占用全局授权写锁，事务内仍会再次完整校验。
		preview, err := repository.New(m.DB).AuthToken(ctx, refreshID, false)
		if err != nil {
			return tokenReadError(err)
		}
		if preview.TokenType != "REFRESH" || preview.AuthDomain != m.domain() || !sameHash(preview.TokenHash, rawRefresh) {
			return denied()
		}
	}
	err := repository.InTx(ctx, m.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return unavailable(err)
		}
		now := time.Now().UTC()
		var token *model.AuthToken
		if refreshID != "" {
			var err error
			token, err = st.AuthToken(ctx, refreshID, true)
			if err != nil {
				return tokenReadError(err)
			}
			if token.TokenType != "REFRESH" || token.AuthDomain != m.domain() || !sameHash(token.TokenHash, rawRefresh) ||
				token.RevokedAt != nil || (token.ConsumedAt != nil && principal == nil) || !token.ExpiresAt.After(now) || !token.FamilyExpiresAt.After(now) {
				return denied()
			}
			if principal != nil && (principal.User == nil || principal.User.ID != token.UserID ||
				principal.SessionID != token.FamilyID || principal.User.SessionVersion != token.SessionVersion) {
				return apperr.New(apperr.CodeConflict, "Access 与 Refresh 不属于同一设备会话")
			}
		} else {
			if principal.User == nil {
				return denied()
			}
			token = &model.AuthToken{FamilyID: principal.SessionID, UserID: principal.User.ID, SessionVersion: principal.User.SessionVersion}
			// 下面直接校验 family；不把 Principal 当作可绕过 SQL 的授权快照。
			session, err := st.AuthSession(ctx, token.FamilyID, m.domain(), token.UserID, true)
			if err != nil {
				return tokenReadError(err)
			}
			token.FamilyExpiresAt = session.ExpiresAt
		}
		u, err := st.User(ctx, token.UserID, true)
		if err != nil {
			return tokenReadError(err)
		}
		if u.Status != "ACTIVE" || u.DeletedAt != nil || u.Issuer != m.Config.Issuer || u.Subject == nil || u.SessionVersion != token.SessionVersion {
			return denied()
		}
		if _, err := m.activeSession(ctx, st, token, true, now); err != nil {
			return err
		}
		if err := st.RevokeTokenFamily(ctx, token.FamilyID, u.ID, now); err != nil {
			return unavailable(err)
		}
		return sessionAudit(ctx, st, u, u.ID, token.FamilyID, "auth.logout")
	})
	return authFailure(err)
}

// 缓存只含不可变摘要。有效/撤销/消费状态和用户登录版本始终以 SQL 主库为准。
// 缓存故障只增加数据库读取，不会把签名通过等同于已经授权。
func (m *Manager) cacheKey(id string) string {
	return "loom:auth:access-hash:" + m.domain() + ":" + id
}

func (m *Manager) domain() string { return digest(m.Config.Issuer + "\x00" + m.Config.Audience) }

func (m *Manager) accessRecord(ctx context.Context, id, expectedHash string) (*model.AuthToken, error) {
	st := repository.New(m.DB)
	if m.Redis != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		hash, err := m.Redis.Get(cacheCtx, m.cacheKey(id)).Result()
		cancel()
		// 缓存摘要损坏或不匹配也回源，不能因为缓存内容错误拒绝有效凭据。
		if err == nil && hash == expectedHash {
			row, err := st.AuthTokenState(ctx, id)
			if err != nil {
				return nil, err
			}
			row.TokenHash = hash
			return row, nil
		}
	}
	row, err := st.AuthToken(ctx, id, false)
	if err == nil && row.TokenType == "ACCESS" && row.RevokedAt == nil && row.ExpiresAt.After(time.Now()) {
		m.cacheHash(ctx, row.TokenID, row.TokenHash, row.ExpiresAt)
	}
	return row, err
}

func (m *Manager) cacheHash(ctx context.Context, id, hash string, expires time.Time) {
	if m.Redis == nil {
		return
	}
	ttl := time.Until(expires)
	if ttl <= 0 {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = m.Redis.Set(cacheCtx, m.cacheKey(id), hash, ttl).Err()
}

func (m *Manager) cacheAccess(ctx context.Context, tokens *Tokens) {
	m.cacheHash(ctx, tokens.accessID, digest(tokens.AccessToken), tokens.AccessExpires)
}
