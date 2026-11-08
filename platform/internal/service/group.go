package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
)

func (s *Service) CreateGroup(ctx context.Context, actor uint64, key string, in dto.CreateGroupReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	if !validator.MaxTextLength(in.DisplayName, 128) {
		return nil, apperr.New(apperr.CodeInvalid, "展示名称过长")
	}
	return s.mutate(ctx, actor, 0, key, "group.create", "group.create", "group", in.Name, in, func(st *repository.Store, o *model.Operation) error {
		quota, err := json.Marshal(in.Quota)
		if err != nil {
			return err
		}
		g := &model.Group{InstanceID: uuid.NewString(), Cluster: s.Cluster, Name: in.Name, DisplayName: in.DisplayName,
			CreatedBy: actor, InfrastructureOwner: s.InfrastructureOwner, QuotaRequested: quota,
			Status: "CREATING", ObservedState: "UNKNOWN", Revision: 1}
		if err := st.DB.WithContext(ctx).Create(g).Error; err != nil {
			return err
		}
		groupID := g.ID
		role, err := st.RoleID(ctx, "group_owner")
		if err != nil {
			return err
		}
		if err := st.GrantBinding(ctx, &model.Binding{UserID: actor, RoleID: role, Scope: "group", ScopeID: groupID, GrantedBy: actor}, false); err != nil {
			return err
		}
		if err := st.BumpUserVersion(ctx, actor); err != nil {
			return err
		}
		if err := refreshAccess(ctx, st, groupID, actor); err != nil {
			return err
		}
		o.TargetKey = fmt.Sprint(groupID)
		o.Status = "PENDING"
		o.Step = "ACCEPTED"
		return result(o, map[string]any{"groupId": groupID, "status": "CREATING"})
	})
}

type GroupMetadata struct {
	DisplayName *string                `json:"displayName,omitempty"`
	Description *string                `json:"description,omitempty"`
	Expected    *GroupMetadataSnapshot `json:"expected,omitempty"`
}

// 元数据使用旧值比较，避免为展示字段变更递增集群执行器的 revision。
// Expected 可选以兼容既有客户端；新客户端应提交读取时的完整元数据快照。
type GroupMetadataSnapshot struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

func (s *Service) UpdateGroup(ctx context.Context, actor, groupID uint64, key string, in GroupMetadata) (*model.Operation, error) {
	if (in.DisplayName != nil && !validator.MaxTextLength(*in.DisplayName, 128)) || (in.Description != nil && !validator.MaxTextLength(*in.Description, 1000)) {
		return nil, apperr.New(apperr.CodeInvalid, "名称或描述过长")
	}
	if in.DisplayName == nil && in.Description == nil {
		return nil, apperr.New(apperr.CodeInvalid, "至少修改一个元数据字段")
	}
	return s.mutate(ctx, actor, groupID, key, "group.update", "group.metadata.update", "group", fmt.Sprint(groupID), in, func(st *repository.Store, o *model.Operation) error {
		g, err := st.Group(ctx, groupID, true)
		if err != nil {
			return err
		}
		if g.Status == "DELETING" || g.Status == "DELETED" {
			return apperr.New(apperr.CodeConflict, "删除中的空间不可修改")
		}
		if in.Expected != nil && (in.Expected.DisplayName != g.DisplayName || in.Expected.Description != g.Description) {
			return apperr.New(apperr.CodeConflict, "空间名称或描述已变化，请重新读取后编辑")
		}
		updates := map[string]any{}
		if in.DisplayName != nil {
			updates["display_name"] = *in.DisplayName
		}
		if in.Description != nil {
			updates["description"] = *in.Description
		}
		if err := st.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ?", groupID).
			Updates(updates).Error; err != nil {
			return err
		}
		return result(o, in)
	})
}

type TransferOwner struct {
	UserID      uint64 `json:"userId"`
	KeepCurrent bool   `json:"keepCurrent"`
}

func (s *Service) TransferOwner(ctx context.Context, actor, groupID uint64, key string, in TransferOwner) (*model.Operation, error) {
	if in.UserID == 0 || in.UserID == actor {
		return nil, apperr.New(apperr.CodeInvalid, "请选择其他用户作为新负责人")
	}
	return s.mutate(ctx, actor, groupID, key, "group.owner.transfer", "group.owner.transfer", "group", fmt.Sprint(groupID), in, func(st *repository.Store, o *model.Operation) error {
		g, err := st.Group(ctx, groupID, true)
		if err != nil {
			return err
		}
		if g.Status != "ACTIVE" && g.Status != "DEGRADED" && g.Status != "CREATING" {
			return apperr.New(apperr.CodeConflict, "空间当前不可交接")
		}
		u, err := st.User(ctx, in.UserID, true)
		if err != nil {
			return err
		}
		if u.Status != "ACTIVE" {
			return apperr.New(apperr.CodeConflict, "目标用户未启用")
		}
		roleID, err := st.RoleID(ctx, "group_owner")
		if err != nil {
			return err
		}
		if err := st.GrantBinding(ctx, &model.Binding{UserID: in.UserID, RoleID: roleID, Scope: "group", ScopeID: groupID, GrantedBy: actor}, false); err != nil {
			return err
		}
		if !in.KeepCurrent {
			if err := st.RevokeBindings(ctx, actor, actor, groupID, "group", &roleID); err != nil {
				return err
			}
		}
		if err := ensureOwner(ctx, st, groupID); err != nil {
			return err
		}
		for _, id := range []uint64{actor, in.UserID} {
			if err := st.BumpUserVersion(ctx, id); err != nil {
				return err
			}
			if err := refreshAccess(ctx, st, groupID, id); err != nil {
				return err
			}
		}
		return result(o, map[string]any{"groupId": groupID, "ownerId": in.UserID, "infrastructureOwnerChanged": false, "accessPending": true})
	})
}
