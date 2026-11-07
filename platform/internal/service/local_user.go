package service

import (
	"context"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
)

// CreateLocalUser 仅由本地认证开启后的管理员入口调用；issuer 来自服务端配置。
// ACTIVE 仅表示业务账号启用，没有本地凭据仍不能登录，也不自动获得空间角色。
func (s *Service) CreateLocalUser(ctx context.Context, actor uint64, key, issuer string, in dto.CreateLocalUserReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if issuer == "" || len(issuer) > 255 {
		return nil, apperr.New(apperr.CodeInvalid, "本地 issuer 未配置")
	}
	return s.mutate(ctx, actor, 0, key, "user.manage", "user.local.create", "user", in.Username, in,
		func(st *repository.Store, o *model.Operation) error {
			subject := uuid.NewString()
			u := &model.User{Issuer: issuer, Subject: &subject, Username: in.Username, DisplayName: in.DisplayName,
				Email: in.Email, Status: "ACTIVE", SessionVersion: 1}
			if err := st.InsertUser(ctx, u); err != nil {
				return err
			}
			if err := grantDefault(ctx, st, u.ID, actor); err != nil {
				return err
			}
			o.TargetKey = strconv.FormatUint(u.ID, 10)
			return result(o, map[string]any{"id": u.ID, "status": u.Status, "passwordSetupRequired": true})
		})
}
