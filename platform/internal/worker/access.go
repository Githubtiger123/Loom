package worker

import (
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

func (r *Runner) access(ctx context.Context, c *repository.Claim, guard func() error) error {
	groupID, userID, err := accessIDs(c.TargetKey)
	if err != nil {
		return err
	}
	if err := r.Service.RefreshAccessTarget(ctx, groupID, userID); err != nil {
		return err
	}
	st := repository.New(r.DB)
	a, err := st.Access(ctx, groupID, userID, false)
	if err != nil {
		return err
	}
	if a.DesiredVersion != c.TargetVersion {
		return errSuperseded
	}
	g, err := st.Group(ctx, groupID, false)
	if err != nil {
		return err
	}
	u, err := st.User(ctx, userID, false)
	if err != nil {
		return err
	}
	if g.Cluster != r.Cluster {
		return &k8s.Permanent{Reason: "访问任务的集群配置不匹配"}
	}
	ref := spaceRef(g)
	exists, err := r.Kube.AccessNamespace(ctx, ref)
	if err != nil {
		return err
	}
	if !exists && a.DesiredLevel != "none" {
		return &k8s.Pending{Step: "WAIT_NAMESPACE", Reason: "等待空间 namespace 创建"}
	}
	var previous []k8s.ObjectRef
	if err := json.Unmarshal(a.Objects, &previous); err != nil {
		return &k8s.Permanent{Reason: "历史成员对象观测格式无效，不能安全接管"}
	}
	current := func() error {
		if err := guard(); err != nil {
			return err
		}
		fresh, err := st.Access(ctx, groupID, userID, false)
		if err != nil {
			return err
		}
		level, err := st.AccessLevel(ctx, groupID, userID)
		if err != nil {
			return err
		}
		space, err := st.Group(ctx, groupID, false)
		if err != nil {
			return err
		}
		if space.Status == "DISABLING" || space.Status == "DISABLED" || space.Status == "DELETING" || space.Status == "DELETED" {
			level = "none"
		}
		if fresh.DesiredVersion != a.DesiredVersion || fresh.DesiredLevel != a.DesiredLevel || level != a.DesiredLevel {
			return errSuperseded
		}
		return nil
	}
	if err := current(); err != nil {
		// 到期发生在本轮内，持久化最新目标并安排清理，不能只淘汰旧任务。
		if refreshErr := r.Service.RefreshAccessTarget(ctx, groupID, userID); refreshErr != nil {
			return refreshErr
		}
		return err
	}
	if err := repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
		if err := tx.Fence(ctx, c); err != nil {
			return err
		}
		status := "APPLYING"
		if a.DesiredLevel == "none" {
			status = "REVOKING"
		}
		return tx.DB.WithContext(ctx).Model(&model.GroupAccess{}).Where("group_id = ? AND user_id = ? AND desired_version = ?", groupID, userID, a.DesiredVersion).
			Updates(map[string]any{"status": status, "last_error": nil}).Error
	}); err != nil {
		return err
	}
	obs := &k8s.AccessObservation{Level: "none", Objects: []k8s.ObjectRef{}}
	if exists {
		if a.DesiredLevel != "none" {
			// 授权前确认系统基础资源；撤权不以额度或系统策略就绪作为前提。
			input, err := profileInput(g)
			if err != nil {
				return err
			}
			if _, err := r.Kube.EnsureSpace(ctx, ref, input, false); err != nil {
				return err
			}
		}
		obs, err = r.Kube.ReconcileAccess(ctx, k8s.AccessTarget{Space: ref, UserID: userID, Username: u.Username, Level: a.DesiredLevel, Version: a.DesiredVersion, Previous: previous}, current, false)
		if err != nil {
			if refreshErr := r.Service.RefreshAccessTarget(ctx, groupID, userID); refreshErr != nil {
				return refreshErr
			}
			return err
		}
	}
	return repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
		if err := tx.LockAuthorization(ctx); err != nil {
			return err
		}
		if err := tx.Fence(ctx, c); err != nil {
			return err
		}
		fresh, err := tx.Access(ctx, groupID, userID, true)
		if err != nil {
			return err
		}
		level, err := tx.AccessLevel(ctx, groupID, userID)
		if err != nil {
			return err
		}
		space, err := tx.Group(ctx, groupID, false)
		if err != nil {
			return err
		}
		if space.Status == "DISABLING" || space.Status == "DISABLED" || space.Status == "DELETING" || space.Status == "DELETED" {
			level = "none"
		}
		if fresh.DesiredVersion != a.DesiredVersion || fresh.DesiredLevel != a.DesiredLevel || level != a.DesiredLevel {
			return errSuperseded
		}
		objects, _ := json.Marshal(obs.Objects)
		status := "READY"
		if obs.Warning != "" {
			status = "DRIFTED"
		}
		var lastError any
		if obs.Warning != "" {
			lastError = obs.Warning
		}
		if err := tx.DB.WithContext(ctx).Model(&model.GroupAccess{}).Where("group_id = ? AND user_id = ?", groupID, userID).
			Updates(map[string]any{"observed_level": obs.Level, "applied_version": a.DesiredVersion, "status": status,
				"observed_objects": string(objects), "observed_at": gorm.Expr("UTC_TIMESTAMP(3)"), "last_error": lastError}).Error; err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"groupId": groupID, "userId": userID, "desiredLevel": a.DesiredLevel, "observedLevel": obs.Level, "accessStatus": status, "warning": obs.Warning,
			"warningCode": obs.WarningCode, "infrastructureOwnerOnly": obs.InfrastructureOwnerOnly})
		if err := tx.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", c.ID).Update("result", string(data)).Error; err != nil {
			return err
		}
		if obs.Warning != "" {
			r.Log.Warn("成员存在额外原生访问，未报告完全撤权", "group", groupID, "user", userID, "warning", obs.Warning)
			return tx.Finish(ctx, c, "FAILED", "EXTERNAL_ACCESS_REMAINS", obs.Warning, 0, false)
		}
		return tx.Finish(ctx, c, "SUCCEEDED", "COMPLETED", "", 0, false)
	})
}
