package model

import "time"

// LocalCredential 与业务身份分开；密码散列绝不作为 API 响应或审计快照。
type LocalCredential struct {
	UserID                 uint64    `gorm:"primaryKey;autoIncrement:false" json:"-"`
	PasswordHash           string    `json:"-"`
	PasswordChangeRequired bool      `json:"-"`
	UpdatedAt              time.Time `gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)" json:"-"`
}

func (LocalCredential) TableName() string { return "tb_local_credential" }

// AuthToken 只保存 Access JWT / Refresh 的 SHA-256 摘要，不保存可重放的原文。
// FamilyID 只是同一设备的轮换关联 ID，不是第三种凭据。
type AuthToken struct {
	TokenID         string     `gorm:"primaryKey" json:"-"`
	FamilyID        string     `json:"-"`
	AuthDomain      string     `json:"-"`
	UserID          uint64     `json:"-"`
	SessionVersion  uint64     `json:"-"`
	TokenType       string     `json:"-"`
	TokenHash       string     `json:"-"`
	ExpiresAt       time.Time  `json:"-"`
	FamilyExpiresAt time.Time  `json:"-"`
	ConsumedAt      *time.Time `json:"-"`
	RevokedAt       *time.Time `json:"-"`
	ReplacedBy      *string    `json:"-"`
	CreatedAt       time.Time  `json:"-"`
}

func (AuthToken) TableName() string { return "tb_auth_token" }

// AuthSession 是设备 family 的 SQL 状态和展示元数据，不是身份凭据。
// 一次登录创建一个 family，刷新不延长其绝对期限。
type AuthSession struct {
	FamilyID        string     `gorm:"primaryKey" json:"-"`
	AuthDomain      string     `json:"-"`
	UserID          uint64     `json:"-"`
	SessionVersion  uint64     `json:"-"`
	CreatedAt       time.Time  `json:"-"`
	LastRefreshedAt time.Time  `json:"-"`
	ExpiresAt       time.Time  `json:"-"`
	RevokedAt       *time.Time `json:"-"`
}

func (AuthSession) TableName() string { return "tb_auth_session" }

// AuthRateBucket 是跨副本共享的限流计数，不是登录凭据。
type AuthRateBucket struct {
	BucketKey string `gorm:"primaryKey"`
	Attempts  int
	ExpiresAt time.Time
}

func (AuthRateBucket) TableName() string { return "tb_auth_rate_bucket" }
