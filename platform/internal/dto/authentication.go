package dto

import (
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r *LoginReq) Validate() error {
	r.Username = strings.TrimSpace(r.Username)
	if !validator.RequiredText(r.Username, 255) || len(r.Password) < 1 || len(r.Password) > 1024 {
		return apperr.New(apperr.CodeInvalid, "登录名或密码格式无效")
	}
	return nil
}

type PasswordReq struct {
	Password string `json:"password"`
}
type ChangePasswordReq struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (r ChangePasswordReq) Validate() error {
	if len(r.CurrentPassword) < 1 || len(r.CurrentPassword) > 1024 {
		return apperr.New(apperr.CodeInvalid, "当前密码格式无效")
	}
	return ValidatePassword(r.NewPassword)
}

// CreateLocalUserReq 不接受密码或客户端自选的稳定主体，凭据通过独立重置接口配置。
type CreateLocalUserReq struct {
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	Email       *string `json:"email,omitempty"`
}

func (r *CreateLocalUserReq) Validate() error {
	r.Username = strings.TrimSpace(r.Username)
	if !validator.IsIdentityHeaderValue(r.Username) || !validator.MaxTextLength(r.DisplayName, 128) {
		return apperr.New(apperr.CodeInvalid, "登录名无效或展示名称超过 128 字")
	}
	if r.Email != nil && !validator.IsLoginEmail(*r.Email) {
		return apperr.New(apperr.CodeInvalid, "邮箱格式无效")
	}
	return nil
}

func ValidatePassword(value string) error {
	if len(value) < 12 || len(value) > 1024 || !validator.MaxTextLength(value, 1024) {
		return apperr.New(apperr.CodeInvalid, "密码必须为有效文本，长度为 12–1024 字节")
	}
	return nil
}
