package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/service"
	"gorm.io/gorm"
)

func (r *Runner) groupLifecycle(ctx context.Context, c *repository.Claim, guard func() error) error {
	var command service.GroupLifecycleCommand
	if err := json.Unmarshal(c.Payload, &command); err != nil || command.GroupID == 0 || command.Revision != c.TargetVersion || fmt.Sprint(command.GroupID) != c.TargetKey {
		return &k8s.Permanent{Reason: "空间生命周期操作契约无效"}
	}
	read := func(lock bool, st *repository.Store) (*model.Group, error) {
		g, err := st.Group(ctx, command.GroupID, lock)
		if err != nil {
			return nil, err
		}
		if g.Revision != command.Revision || g.Cluster != r.Cluster || g.InstanceID != command.InstanceID || g.Name != command.Namespace ||
			!sameLifecycleUID(g.ProfileUID, command.ProfileUID) || !sameLifecycleUID(g.NamespaceUID, command.NamespaceUID) {
			return nil, errSuperseded
		}
		return g, nil
	}
	st := repository.New(r.DB)
	g, err := read(false, st)
	if err != nil {
		return err
	}
	check := func() error {
		if err := guard(); err != nil {
			return err
		}
		_, err := read(false, repository.New(r.DB))
		return err
	}
	status := g.Status
	var observedQuota *k8s.QuotaUsage
	switch c.Action {
	case "group.disable":
		if g.Status != "DISABLING" {
			return errSuperseded
		}
		var pending, failed int64
		if err := st.DB.WithContext(ctx).Model(&model.Notebook{}).Where("group_id = ? AND deleted_at IS NULL", g.ID).
			Where("desired_state <> 'STOPPED' OR observed_state <> 'STOPPED' OR applied_revision <> revision OR status <> 'STOPPED' OR observed_at IS NULL OR observed_at < TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(3))").Count(&pending).Error; err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(&model.Notebook{}).Where("group_id = ? AND deleted_at IS NULL AND status IN ?", g.ID, []string{"FAILED", "ERROR"}).Count(&failed).Error; err != nil {
			return err
		}
		if failed > 0 {
			return &k8s.Permanent{Reason: "空间停用等待的 Notebook 停止失败，请处理实例失败操作后重试"}
		}
		if pending > 0 {
			return &k8s.Pending{Step: "WAIT_NOTEBOOKS_STOPPED", Reason: "空间已冻结，等待 Notebook 停止并确认资源回收"}
		}
		if err := r.waitLifecycleAccess(ctx, g, false); err != nil {
			return err
		}
		status = "DISABLED"
	case "group.restore":
		if g.Status != "RESTORING" {
			return errSuperseded
		}
		input, err := profileInput(g)
		if err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		if _, err := r.Kube.EnsureSpace(ctx, spaceRef(g), input, false); err != nil {
			return err
		}
		if err := r.waitLifecycleAccess(ctx, g, true); err != nil {
			return err
		}
		status = "ACTIVE"
	case "group.quota.update":
		if g.Status != "ACTIVE" && g.Status != "DEGRADED" && g.Status != "DISABLED" {
			return errSuperseded
		}
		input, err := profileInput(g)
		if err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		if err := r.Kube.UpdateSpaceQuota(ctx, spaceRef(g), input, check); err != nil {
			return err
		}
		observedQuota, err = r.Kube.ReadQuota(ctx, g.Name)
		if err != nil {
			return err
		}
	case "group.delete":
		if g.Status != "DELETING" || !command.RequireEmpty {
			return errSuperseded
		}
		deps, err := st.GroupDependencies(ctx, g.ID)
		if err != nil {
			return err
		}
		if !deps.Empty() {
			return &k8s.Permanent{Reason: "空间仍有业务资源或保留数据，拒绝删除"}
		}
		if err := r.waitLifecycleAccess(ctx, g, false); err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		if err := r.Kube.DeleteEmptySpace(ctx, spaceRef(g), check); err != nil {
			return err
		}
		status = "DELETED"
	default:
		return &k8s.Permanent{Reason: "不支持的空间生命周期操作"}
	}
	return repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
		if err := tx.LockAuthorization(ctx); err != nil {
			return err
		}
		if err := tx.Fence(ctx, c); err != nil {
			return err
		}
		if _, err := read(true, tx); err != nil {
			return err
		}
		fields := map[string]any{"status": status, "last_error": nil, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}
		if observedQuota != nil {
			quota, err := json.Marshal(observedQuota)
			if err != nil {
				return err
			}
			fields["quota_observed"] = string(quota)
		}
		if status == "DELETED" {
			fields["deleted_at"], fields["observed_state"] = gorm.Expr("UTC_TIMESTAMP(3)"), "DELETED"
		}
		if err := tx.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ?", g.ID).Updates(fields).Error; err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"groupId": g.ID, "revision": g.Revision, "status": status})
		if err := tx.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", c.ID).Update("result", string(data)).Error; err != nil {
			return err
		}
		return tx.Finish(ctx, c, "SUCCEEDED", "COMPLETED", "", 0, false)
	})
}

func sameLifecycleUID(a, b *string) bool {
	return a != nil && b != nil && *a != "" && *a == *b
}

func (r *Runner) waitLifecycleAccess(ctx context.Context, g *model.Group, restore bool) error {
	var pending, failed int64
	q := r.DB.WithContext(ctx).Table("tb_group_access a").Joins("JOIN tb_user u ON u.id=a.user_id").Where("a.group_id = ?", g.ID)
	if err := q.Session(&gorm.Session{}).Where("a.status = 'FAILED' OR (a.status='DRIFTED' AND u.username <> ?)", g.InfrastructureOwner).Count(&failed).Error; err != nil {
		return err
	}
	if failed > 0 {
		return &k8s.Permanent{Reason: "成员访问落实失败或发现额外原生授权，请处理访问记录后重试"}
	}
	var ownerDrifts []model.GroupAccess
	if err := r.DB.WithContext(ctx).Model(&model.GroupAccess{}).Where("group_id=? AND status='DRIFTED'", g.ID).Find(&ownerDrifts).Error; err != nil {
		return err
	}
	for _, access := range ownerDrifts {
		var operation model.Operation
		if err := r.DB.WithContext(ctx).Where("action='access.reconcile' AND target_key=? AND target_version=?", fmt.Sprintf("%d/%d", g.ID, access.UserID), access.DesiredVersion).Order("id DESC").First(&operation).Error; err != nil {
			return err
		}
		var receipt struct {
			WarningCode             string `json:"warningCode"`
			InfrastructureOwnerOnly bool   `json:"infrastructureOwnerOnly"`
		}
		if json.Unmarshal(operation.Result, &receipt) != nil || !receipt.InfrastructureOwnerOnly || receipt.WarningCode != "INFRASTRUCTURE_OWNER_ACCESS_RETAINED" || operation.Status != "FAILED" || operation.Step != "EXTERNAL_ACCESS_REMAINS" {
			return &k8s.Permanent{Reason: "无法确认仅保留固定 Owner 系统权限，拒绝忽略授权漂移"}
		}
	}
	if err := q.Session(&gorm.Session{}).Where("a.applied_version <> a.desired_version OR a.status NOT IN ('READY','DRIFTED')").Count(&pending).Error; err != nil {
		return err
	}
	if !restore {
		var grants int64
		if err := q.Session(&gorm.Session{}).Where("a.desired_level <> 'none'").Count(&grants).Error; err != nil {
			return err
		}
		pending += grants
	}
	if pending > 0 {
		return &k8s.Pending{Step: "WAIT_MEMBER_ACCESS", Reason: "等待成员原生访问落实；固定基础设施 Owner 系统权限按约定保留"}
	}
	return nil
}

// 失败保留业务冻结状态，重试同一操作继续收敛，不自动恢复对外访问。
func (r *Runner) markLifecycleFailure(ctx context.Context, st *repository.Store, c *repository.Claim, message string) error {
	if c.TargetType != "group" || c.Action == "group.create" {
		return nil
	}
	id, err := strconv.ParseUint(c.TargetKey, 10, 64)
	if err != nil {
		return err
	}
	return st.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ? AND revision = ?", id, c.TargetVersion).
		Update("last_error", message).Error
}
