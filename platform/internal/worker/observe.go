package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// RunObserver 只观测已记录的空间，不重建缺失 Profile，不反向写业务角色。
// Informer 通知与低频数据库对账共用队列，事件不直接提供写库证据。
func (r *Runner) RunObserver(ctx context.Context) error {
	if r.Kube == nil {
		return fmt.Errorf("观测器必须配置 Kubernetes 客户端")
	}
	maxOpen, err := inframysql.MaxOpenConnections(r.DB)
	if err != nil {
		return err
	}
	if maxOpen != 0 && maxOpen < 6 {
		return fmt.Errorf("sync Informer 的数据库 max_open 至少为 6")
	}
	var enqueue func(string)
	space := func(obj any) []string {
		n := observationObject(obj)
		if n == nil {
			return nil
		}
		name := n.GetNamespace()
		if name == "" {
			name = n.GetName() // cluster-scoped Profile / Namespace
		}
		return []string{"space/" + name}
	}
	namespace := func(obj any) []string {
		n := observationObject(obj)
		if n == nil {
			return nil
		}
		name := n.GetNamespace()
		if name == "" {
			name = n.GetName()
		}
		return []string{"space/" + name, "notebooks/" + name}
	}
	notebook := func(obj any) []string {
		n := observationObject(obj)
		if n == nil || n.GetNamespace() == "" {
			return nil
		}
		return []string{"notebook-name/" + n.GetNamespace() + "/" + n.GetName()}
	}
	child := func(obj any) []string {
		n := observationObject(obj)
		if n == nil || n.GetNamespace() == "" {
			return nil
		}
		for _, owner := range n.GetOwnerReferences() {
			if owner.Controller == nil || !*owner.Controller {
				continue
			}
			if owner.Kind == "Notebook" || owner.Kind == "StatefulSet" {
				return []string{"notebook-name/" + n.GetNamespace() + "/" + owner.Name}
			}
		}
		return nil
	}
	sources := []observationSource{}
	add := func(gvr schema.GroupVersionResource, mapper func(any) []string) {
		// No label filter: removing a management label must also trigger checks.
		sources = append(sources, observationSource{Informer: r.Kube.NewResourceInformer(gvr, ""), MapKeys: mapper})
	}
	add(k8s.ProfileGVR, space)
	add(k8s.NamespaceGVR, namespace)
	add(k8s.ResourceQuotaGVR, namespace)
	add(k8s.ServiceAccountGVR, space)
	add(k8s.RoleBindingGVR, space)
	add(k8s.AuthzPolicyGVR, space)
	add(k8s.ClusterRoleGVR, func(any) []string { return []string{"spaces"} })
	add(k8s.NotebookGVR, notebook)
	add(k8s.StatefulSetGVR, child)
	add(k8s.PodGVR, child)
	add(k8s.ServiceGVR, notebook)
	add(k8s.PersistentVolumeClaimGVR, func(obj any) []string {
		keys := observationNamespace(obj)
		if len(keys) == 0 {
			return nil
		}
		return []string{"notebooks/" + keys[0]}
	})
	spaceSweep := observationIDSweep(func(ctx context.Context) *gorm.DB {
		return r.DB.WithContext(ctx).Model(&model.Group{}).Where("cluster = ? AND deleted_at IS NULL AND profile_uid IS NOT NULL AND status IN ?", r.Cluster, []string{"ACTIVE", "DEGRADED"})
	}, "space-id/")
	notebookSweep := observationIDSweep(func(ctx context.Context) *gorm.DB {
		return r.DB.WithContext(ctx).Model(&model.Notebook{}).Where("cluster = ? AND deleted_at IS NULL", r.Cluster)
	}, "notebook-id/")
	return r.runResourceObserver(ctx, resourceObservation{
		Name: "space-notebook", Sources: sources, Concurrency: 2, QPS: 5,
		BindEnqueue: func(push func(string)) { enqueue = push }, SweepInterval: time.Minute,
		Sweep: func(ctx context.Context, push func(string)) error {
			spaceCtx, finishSpace := context.WithTimeout(ctx, 10*time.Second)
			spaceErr := spaceSweep(spaceCtx, push)
			finishSpace()
			notebookCtx, finishNotebook := context.WithTimeout(ctx, 10*time.Second)
			notebookErr := notebookSweep(notebookCtx, push)
			finishNotebook()
			return errors.Join(spaceErr, notebookErr)
		},
		Sync: func(ctx context.Context, key string) error {
			kind, value, _ := strings.Cut(key, "/")
			switch kind {
			case "spaces":
				return r.enqueueObservedSpaces(ctx, "", enqueue)
			case "notebooks":
				return r.enqueueObservedNotebooks(ctx, value, enqueue)
			case "space":
				return r.enqueueObservedSpaces(ctx, value, enqueue)
			case "space-id":
				id, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return nil
				}
				return r.observeSpace(ctx, id)
			case "notebook-id", "notebook-name":
				var n model.Notebook
				query := r.DB.WithContext(ctx).Where("cluster = ? AND deleted_at IS NULL", r.Cluster)
				if kind == "notebook-id" {
					id, err := strconv.ParseUint(value, 10, 64)
					if err != nil {
						return nil
					}
					query = query.Where("id = ?", id)
				} else {
					ns, name, ok := strings.Cut(value, "/")
					if !ok {
						return nil
					}
					query = query.Where("namespace = ? AND name = ?", ns, name)
				}
				if err := query.First(&n).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return nil
					}
					return err
				}
				// Merge child resource and periodic events onto the same ID key.
				if kind == "notebook-name" {
					enqueue("notebook-id/" + strconv.FormatUint(n.ID, 10))
					return nil
				}
				return r.observeNotebook(ctx, n.ID, n.GroupID)
			}
			return nil
		},
	})
}

func (r *Runner) enqueueObservedSpaces(ctx context.Context, name string, enqueue func(string)) error {
	var after uint64
	for {
		var ids []uint64
		query := r.DB.WithContext(ctx).Model(&model.Group{}).
			Where("cluster = ? AND id > ? AND deleted_at IS NULL AND profile_uid IS NOT NULL AND status IN ?", r.Cluster, after, []string{"ACTIVE", "DEGRADED"})
		if name != "" {
			query = query.Where("name = ?", name)
		}
		if err := query.Order("id").Limit(100).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			enqueue("space-id/" + strconv.FormatUint(id, 10))
			after = id
		}
	}
}

func (r *Runner) observeSpace(ctx context.Context, id uint64) error {
	lock, err := repository.TargetLock(ctx, r.DB, r.Cluster, "group", strconv.FormatUint(id, 10))
	if err != nil {
		return err
	}
	defer lock.Close()
	st := repository.New(r.DB)
	g, err := st.Group(ctx, id, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if g.Cluster != r.Cluster || g.DeletedAt != nil || g.ProfileUID == nil || (g.Status != "ACTIVE" && g.Status != "DEGRADED") {
		return nil
	}
	input, err := profileInput(g)
	if err != nil {
		return err
	}
	obs, observeErr := r.Kube.EnsureSpace(ctx, spaceRef(g), input, false)
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return err
	}
	writeErr := repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
		fresh, err := tx.Group(ctx, id, true)
		if err != nil {
			return err
		}
		if fresh.Revision != g.Revision || fresh.InstanceID != g.InstanceID || fresh.Cluster != g.Cluster || fresh.DeletedAt != nil || (fresh.Status != "ACTIVE" && fresh.Status != "DEGRADED") {
			return nil
		}
		if obs != nil && obs.ProfileUID != "" {
			if err := tx.RecordSpace(ctx, id, obs.ProfileUID, obs.NamespaceUID, obs.Quota); err != nil {
				return err
			}
		}
		if observeErr != nil {
			return tx.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ?", id).
				Updates(map[string]any{"status": "DEGRADED", "observed_state": "ERROR", "last_error": truncate(observeErr.Error(), 1000), "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error
		}
		// 基础设施恢复不自动掩盖成员撤权失败。
		var issues, completed int64
		receipt := tx.FixedOwnerAccessReceipt(ctx)
		if err := tx.DB.WithContext(ctx).Table("tb_group_access a").Joins("JOIN tb_user u ON u.id = a.user_id").
			Where("a.group_id = ?", id).
			Where("a.status = ? OR (a.status = ? AND NOT COALESCE(u.username = ? AND a.desired_level = ? AND a.applied_version = a.desired_version AND EXISTS (?),0))",
				"FAILED", "DRIFTED", fresh.InfrastructureOwner, "edit", receipt).Count(&issues).Error; err != nil {
			return err
		}
		if err := tx.DB.WithContext(ctx).Model(&model.Operation{}).
			Where("action = ? AND target_key = ? AND status = ?", "group.create", fmt.Sprint(id), "SUCCEEDED").Count(&completed).Error; err != nil {
			return err
		}
		unresolved := issues > 0 || completed == 0
		status := "ACTIVE"
		var reason any
		if unresolved {
			status = "DEGRADED"
			reason = "创建操作尚未完成或成员访问存在异常，请查看操作与访问状态"
		}
		return tx.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ?", id).Updates(map[string]any{
			"observed_state": "READY", "observed_at": gorm.Expr("UTC_TIMESTAMP(3)"), "status": status, "last_error": reason}).Error
	})
	if writeErr != nil {
		return writeErr
	}
	return observeErr
}
