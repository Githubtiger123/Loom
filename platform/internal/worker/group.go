package worker

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
)

func spaceRef(g *model.Group) k8s.SpaceRef {
	ref := k8s.SpaceRef{ID: g.ID, InstanceID: g.InstanceID, Name: g.Name, Owner: g.InfrastructureOwner}
	if g.ProfileUID != nil {
		ref.ProfileUID = *g.ProfileUID
	}
	if g.NamespaceUID != nil {
		ref.NamespaceUID = *g.NamespaceUID
	}
	return ref
}
func profileInput(g *model.Group) (k8s.ProfileInput, error) {
	var quota dto.Quota
	if err := json.Unmarshal(g.QuotaRequested, &quota); err != nil {
		return k8s.ProfileInput{}, &k8s.Permanent{Reason: "空间额度请求格式无效"}
	}
	for _, value := range []string{quota.CPU, quota.Memory} {
		if !validator.IsNonNegativeQuantity(value) {
			return k8s.ProfileInput{}, &k8s.Permanent{Reason: "空间 CPU/内存额度无效"}
		}
	}
	if quota.GPU < 0 {
		return k8s.ProfileInput{}, &k8s.Permanent{Reason: "空间 GPU 额度无效"}
	}
	return k8s.ProfileInput{CPU: quota.CPU, Memory: quota.Memory, GPU: quota.GPU, DisplayName: g.DisplayName}, nil
}

func (r *Runner) createGroup(ctx context.Context, c *repository.Claim, guard func() error) error {
	id, err := strconv.ParseUint(c.TargetKey, 10, 64)
	if err != nil || id == 0 {
		return &k8s.Permanent{Reason: "空间任务目标无效"}
	}
	g, err := repository.New(r.DB).Group(ctx, id, false)
	if err != nil {
		return err
	}
	if g.Cluster != r.Cluster {
		return &k8s.Permanent{Reason: "空间与执行器的集群配置不匹配"}
	}
	if g.Status == "DELETING" || g.Status == "DELETED" {
		return errSuperseded
	}
	if c.TargetVersion != g.Revision {
		return errSuperseded
	}
	input, err := profileInput(g)
	if err != nil {
		return err
	}
	if err := guard(); err != nil {
		return err
	}
	obs, observeErr := r.Kube.EnsureSpace(ctx, spaceRef(g), input, true)
	if obs != nil && obs.ProfileUID != "" {
		err = repository.InTx(ctx, r.DB, func(st *repository.Store) error {
			if err := st.Fence(ctx, c); err != nil {
				return err
			}
			if err := st.RecordSpace(ctx, g.ID, obs.ProfileUID, obs.NamespaceUID, obs.Quota); err != nil {
				return err
			}
			if observeErr == nil {
				return st.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ? AND status NOT IN ?", g.ID, []string{"DELETING", "DELETED"}).
					Updates(map[string]any{"observed_state": "READY", "last_error": nil}).Error
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if observeErr != nil {
		return observeErr
	}
	// 不阻塞等待访问任务。先提交基础设施观测并释放本轮，再由调度器领取授权任务。
	owners, err := repository.New(r.DB).OwnerIDs(ctx, g.ID)
	if err != nil {
		return err
	}
	if len(owners) == 0 {
		return &k8s.Permanent{Reason: "空间没有有效业务 Owner，需要平台管理员交接"}
	}
	for _, user := range owners {
		if err := r.Service.RefreshAccessTarget(ctx, g.ID, user); err != nil {
			return err
		}
	}
	err = repository.InTx(ctx, r.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		if err := st.Fence(ctx, c); err != nil {
			return err
		}
		fresh, err := st.Group(ctx, g.ID, true)
		if err != nil {
			return err
		}
		if fresh.Status == "DELETING" || fresh.Status == "DELETED" || fresh.Revision != g.Revision {
			return errSuperseded
		}
		// 授权在等待期间可能变化，事务内重新查询所有当前有效 Owner。
		ownerState, err := st.OwnerAccessState(ctx, g.ID, g.InfrastructureOwner)
		if err != nil {
			return err
		}
		count, missing, failed := ownerState.Count, ownerState.Missing, ownerState.Failed
		if count == 0 {
			return &k8s.Permanent{Reason: "空间已无有效业务 Owner"}
		}
		if failed > 0 {
			return &k8s.Permanent{Reason: "Owner 原生访问开通失败，请查看成员访问操作并重试"}
		}
		if missing > 0 {
			return &k8s.Pending{Step: "APPLY_OWNER_ACCESS", Reason: "基础设施已就绪，等待当前业务 Owner 访问开通"}
		}
		if err := st.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ?", g.ID).Updates(map[string]any{
			"status": "ACTIVE", "observed_state": "READY", "last_error": nil, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error; err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"groupId": g.ID, "status": "ACTIVE", "profileUid": obs.ProfileUID, "namespaceUid": obs.NamespaceUID})
		if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", c.ID).Update("result", string(data)).Error; err != nil {
			return err
		}
		if err := st.Finish(ctx, c, "SUCCEEDED", "COMPLETED", "", 0, false); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		r.Log.Info("空间创建完成", "group", g.ID, "namespace", g.Name, "profile_uid", obs.ProfileUID)
	}
	return err
}
