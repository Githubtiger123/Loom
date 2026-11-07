// Package authn 实现本地凭据与 SQL 权威的 Access / Refresh，不产生业务角色。
package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/password"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const BrowserCookie = "__Host-loom-access"
const RefreshCookie = "__Secure-loom-refresh"
const LegacyBrowserCookie = "__Host-loom-session"

type Manager struct {
	Config config.Auth
	DB     *gorm.DB
	Redis  *redis.Client
	key    []byte
	dummy  string
	slots  chan struct{}
}

func New(db *gorm.DB, rdb *redis.Client, cfg config.Auth) (*Manager, error) {
	key, err := base64.StdEncoding.DecodeString(cfg.SigningKey)
	if err != nil || len(key) < 32 || db == nil || !cfg.Enabled {
		return nil, fmt.Errorf("认证依赖或签名密钥未配置")
	}
	dummy, err := password.Hash(uuid.NewString())
	if err != nil {
		return nil, err
	}
	return &Manager{Config: cfg, DB: db, Redis: rdb, key: key, dummy: dummy, slots: make(chan struct{}, 4)}, nil
}

type Claims struct {
	SessionID string `json:"sid"`
	Version   string `json:"ver"`
	Kind      string `json:"type"`
	jwt.RegisteredClaims
}

type Principal struct {
	User                   *model.User
	SessionID              string
	AccessExpiresAt        time.Time
	PasswordChangeRequired bool
}

// Refresh 仅由 handler 写入 HttpOnly Cookie；Token 原文均不得写入审计。
type Tokens struct {
	AccessToken            string    `json:"accessToken"`
	TokenType              string    `json:"tokenType"`
	ExpiresIn              int64     `json:"expiresIn"`
	UserID                 uint64    `json:"userId"`
	PasswordChangeRequired bool      `json:"passwordChangeRequired"`
	RefreshToken           string    `json:"-"`
	AccessExpires          time.Time `json:"-"`
	SessionExpires         time.Time `json:"-"`
	accessID               string
	refreshID              string
	familyID               string
	authDomain             string
}

func denied() error {
	return apperr.New(apperr.CodeUnauthorized, "凭据无效、已过期或会话已撤销")
}
func unavailable(err error) error {
	return apperr.Wrap(apperr.CodeUnavailable, "认证依赖暂不可用，请稍后重试", err)
}

// 保留业务错误；数据库事务开始/提交失败也按依赖故障返回，不能误清理 Cookie。
func authFailure(err error) error {
	if err == nil {
		return nil
	}
	var known *apperr.Error
	if errors.As(err, &known) {
		return err
	}
	return unavailable(err)
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func number(v uint64) string { return strconv.FormatUint(v, 10) }

func secret(id string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return id + "." + base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *Manager) issue(u *model.User, sid string, expires time.Time) (*Tokens, error) {
	now := time.Now().UTC()
	until := now.Add(m.Config.AccessTTL.Std())
	if until.After(expires) {
		until = expires
	}
	// 与 JWT 的秒级 NumericDate 保持一致，避免 Cookie 比 JWT 晚过期。
	until = until.Truncate(time.Second)
	if !until.After(now) {
		return nil, denied()
	}
	claims := Claims{SessionID: sid, Version: number(u.SessionVersion), Kind: "access", RegisteredClaims: jwt.RegisteredClaims{
		Issuer: m.Config.Issuer, Subject: number(u.ID), Audience: jwt.ClaimStrings{m.Config.Audience},
		ExpiresAt: jwt.NewNumericDate(until), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ID: uuid.NewString(),
	}}
	encoded, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.key)
	if err != nil {
		return nil, err
	}
	refreshID := uuid.NewString()
	refresh, err := secret(refreshID)
	if err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: encoded, TokenType: "Bearer", ExpiresIn: int64(until.Sub(now).Seconds()),
		UserID: u.ID, RefreshToken: refresh, AccessExpires: until, SessionExpires: expires,
		accessID: claims.ID, refreshID: refreshID, familyID: sid, authDomain: m.domain()}, nil
}

func (t *Tokens) records(version uint64) []model.AuthToken {
	now := time.Now().UTC()
	base := model.AuthToken{FamilyID: t.familyID, AuthDomain: t.authDomain, UserID: t.UserID, SessionVersion: version,
		FamilyExpiresAt: t.SessionExpires, CreatedAt: now}
	access, refresh := base, base
	access.TokenID, access.TokenType, access.TokenHash, access.ExpiresAt = t.accessID, "ACCESS", digest(t.AccessToken), t.AccessExpires
	refresh.TokenID, refresh.TokenType, refresh.TokenHash, refresh.ExpiresAt = t.refreshID, "REFRESH", digest(t.RefreshToken), t.SessionExpires
	return []model.AuthToken{access, refresh}
}

func (m *Manager) withPasswordSlot(fn func() error) error {
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
		return fn()
	default:
		return apperr.New(apperr.CodeRateLimited, "认证请求过多，请稍后重试")
	}
}

func (m *Manager) currentUser(ctx context.Context, uid, version string) (*model.User, error) {
	id, err := strconv.ParseUint(uid, 10, 64)
	if err != nil || id == 0 {
		return nil, denied()
	}
	u, err := repository.New(m.DB).User(ctx, id, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, denied()
		}
		return nil, unavailable(err)
	}
	if u.DeletedAt != nil || u.Status != "ACTIVE" || u.Issuer != m.Config.Issuer || u.Subject == nil || number(u.SessionVersion) != version {
		return nil, denied()
	}
	return u, nil
}
