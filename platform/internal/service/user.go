package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
)

// Authenticate 只接受网关已证明的稳定身份，不以邮箱合并身份、不自动注册或解禁。
func (s *Service) Authenticate(ctx context.Context, issuer, subject, username string) (*model.User, error) {
	u, err := s.Store.Identity(ctx, issuer, subject)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.New(apperr.CodeUnauthorized, "该身份尚未由平台管理员开通")
	}
	if err != nil {
		return nil, err
	}
	if u.Username != username {
		return nil, apperr.New(apperr.CodeUnauthorized, "身份主体与登录名不匹配，请联系管理员处理身份迁移")
	}
	if u.Status != "ACTIVE" {
		return nil, apperr.New(apperr.CodeForbidden, "平台账号未启用或已禁用")
	}
	return u, nil
}

type RegisterUser struct {
	Issuer                    string  `json:"issuer"`
	Subject                   *string `json:"subject"`
	Username                  string  `json:"username"`
	DisplayName               string  `json:"displayName"`
	Email                     *string `json:"email"`
	ExternalIdentityConfirmed bool    `json:"externalIdentityConfirmed"`
}

func (s *Service) CreateUser(ctx context.Context, actor uint64, key string, in RegisterUser) (*model.Operation, error) {
	if !validator.RequiredText(in.Issuer, 255) || !validator.RequiredText(in.Username, 255) || !validator.MaxTextLength(in.DisplayName, 128) {
		return nil, apperr.New(apperr.CodeInvalid, "issuer、username 必填且不超过 255 字符，展示名称最长 128 字符")
	}
	if in.Subject != nil && !validator.RequiredText(*in.Subject, 255) {
		return nil, apperr.New(apperr.CodeInvalid, "subject 无效")
	}
	if in.Email != nil && len(*in.Email) > 255 {
		return nil, apperr.New(apperr.CodeInvalid, "邮箱过长")
	}
	if in.ExternalIdentityConfirmed && in.Subject == nil {
		return nil, apperr.New(apperr.CodeInvalid, "确认身份开通必须提供身份提供方的稳定 subject")
	}
	return s.mutate(ctx, actor, 0, key, "user.manage", "user.register", "user", in.Username, in, func(st *repository.Store, o *model.Operation) error {
		status := "PENDING"
		if in.ExternalIdentityConfirmed {
			status = "ACTIVE"
		}
		u := &model.User{Issuer: in.Issuer, Subject: in.Subject, Username: in.Username, DisplayName: in.DisplayName, Email: in.Email, Status: status}
		if err := st.InsertUser(ctx, u); err != nil {
			return err
		}
		if status == "ACTIVE" {
			if err := grantDefault(ctx, st, u.ID, actor); err != nil {
				return err
			}
		}
		o.TargetKey = fmt.Sprint(u.ID)
		return result(o, map[string]any{"id": u.ID, "status": status, "identityProviderChanged": false})
	})
}

func grantDefault(ctx context.Context, st *repository.Store, userID, actor uint64) error {
	roleID, err := st.RoleID(ctx, "platform_user")
	if err != nil {
		return err
	}
	return st.GrantBinding(ctx, &model.Binding{UserID: userID, RoleID: roleID, Scope: "global", ScopeID: 0, GrantedBy: actor}, true)
}

type UserState struct {
	Subject                      string  `json:"subject"`
	ExternalIdentityConfirmed    bool    `json:"externalIdentityConfirmed"`
	ExpectedAuthorizationVersion *uint64 `json:"expectedAuthorizationVersion,omitempty"`
}

func (s *Service) UserStatus(ctx context.Context, actor, userID uint64, key, status string, in UserState, localIssuers ...string) (*model.Operation, error) {
	if status != "ACTIVE" && status != "DISABLED" {
		return nil, apperr.New(apperr.CodeInvalid, "不支持的用户状态")
	}
	return s.mutate(ctx, actor, 0, key, "user.manage", "user."+status, "user", fmt.Sprint(userID), in, func(st *repository.Store, o *model.Operation) error {
		u, err := st.User(ctx, userID, true)
		if err != nil {
			return err
		}
		if in.ExpectedAuthorizationVersion != nil && u.AuthorizationVersion != *in.ExpectedAuthorizationVersion {
			return apperr.New(apperr.CodeConflict, "用户状态或授权已变化，请重新读取后操作")
		}
		if u.Status == "DELETING" || u.Status == "DELETED" {
			return apperr.New(apperr.CodeConflict, "已删除的身份不能直接启用")
		}
		if status == "DISABLED" {
			if err := requireQuietML(ctx, st, 0, userID); err != nil {
				return err
			}
		}
		if status == "ACTIVE" {
			local := len(localIssuers) == 1 && localIssuers[0] != "" && u.Issuer == localIssuers[0]
			if local && u.Subject == nil {
				return apperr.New(apperr.CodeConflict, "本地账号缺少稳定主体，不能通过启用接口认领或迁移")
			}
			if !local && !in.ExternalIdentityConfirmed {
				return apperr.New(apperr.CodeInvalid, "需要管理员确认外部身份已经可用")
			}
			if u.Subject == nil {
				if !validator.RequiredText(in.Subject, 255) {
					return apperr.New(apperr.CodeInvalid, "首次启用必须绑定经确认的稳定 subject")
				}
				if err := st.DB.WithContext(ctx).Model(&model.User{}).Where("id = ? AND subject IS NULL", userID).Update("subject", in.Subject).Error; err != nil {
					return err
				}
			} else if in.Subject != "" && in.Subject != *u.Subject {
				return apperr.New(apperr.CodeConflict, "不能通过启用接口更换身份主体")
			}
		}
		if err := st.SetUserStatus(ctx, userID, status); err != nil {
			return err
		}
		if u.Status == "PENDING" && status == "ACTIVE" {
			if err := grantDefault(ctx, st, userID, actor); err != nil {
				return err
			}
		}
		if err := ensureAdmin(ctx, st); err != nil {
			return err
		}
		groups, err := st.UserGroups(ctx, userID)
		if err != nil {
			return err
		}
		for _, groupID := range groups {
			if err := ensureOwner(ctx, st, groupID); err != nil {
				return err
			}
			if err := refreshAccess(ctx, st, groupID, userID); err != nil {
				return err
			}
		}
		if status == "DISABLED" {
			ids, err := stopLifecycleNotebooks(ctx, st, actor, 0, userID)
			if err != nil {
				return err
			}
			if err := s.queueResourcePolicy(ctx, st, o, userID, 0, ids, s.Cluster); err != nil {
				return err
			}
			return result(o, map[string]any{"id": userID, "status": status, "resourcePolicy": "PENDING", "dataPolicy": "RETAIN", "notebookStopOperations": ids, "identityProviderChanged": false})
		}
		return result(o, map[string]any{"id": userID, "status": status, "accessCleanupPending": len(groups) > 0, "identityProviderChanged": false})
	})
}

func ensureAdmin(ctx context.Context, st *repository.Store) error {
	count, err := st.AdminCount(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return apperr.New(apperr.CodeConflict, "必须保留至少一个有效的平台管理员")
	}
	return nil
}

func ensureOwner(ctx context.Context, st *repository.Store, groupID uint64) error {
	count, err := st.OwnerCount(ctx, groupID)
	if err != nil {
		return err
	}
	if count == 0 {
		return apperr.New(apperr.CodeConflict, "必须先交接空间负责人，不能移除最后一个有效 Owner")
	}
	return nil
}
