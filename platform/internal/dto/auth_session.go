package dto

import (
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/google/uuid"
)

// FamilyID 是设备会话的标识，不包含 token ID、摘要或可重放凭据。
type AuthSession struct {
	FamilyID        string    `json:"familyId"`
	Current         bool      `json:"current"`
	CreatedAt       time.Time `json:"createdAt"`
	LastRefreshedAt time.Time `json:"lastRefreshedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type AuthSessionPage struct {
	Items     []AuthSession `json:"items"`
	NextAfter string        `json:"nextAfter,omitempty"`
}

type RevokeAuthSessionResult struct {
	Revoked       bool `json:"revoked"`
	LoginRequired bool `json:"loginRequired"`
}

func ValidateAuthFamily(value string) error {
	id, err := uuid.Parse(value)
	if err != nil || id.String() != value {
		return apperr.New(apperr.CodeInvalid, "设备会话 ID 必须为规范 UUID")
	}
	return nil
}
