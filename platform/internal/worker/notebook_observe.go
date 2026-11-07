package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

// GET 轮询不创建/修复资源；目标锁防止较早观测跨过执行结果回填。
func (r *Runner) runNotebookObserver(ctx context.Context) {
	var after uint64
	for ctx.Err() == nil {
		var rows []model.Notebook
		err := r.DB.WithContext(ctx).Where("cluster = ? AND id > ? AND deleted_at IS NULL", r.Cluster, after).Order("id").Limit(50).Find(&rows).Error
		if err != nil {
			r.Log.Warn("读取 Notebook 观测目标失败", "err", err)
			if !pause(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if len(rows) == 0 {
			after = 0
			if !pause(ctx, 20*time.Second) {
				return
			}
			continue
		}
		for _, n := range rows {
			after = n.ID
			workCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := r.observeNotebook(workCtx, n.ID, n.GroupID)
			cancel()
			if err != nil && !errors.Is(err, repository.ErrLeaseLost) && ctx.Err() == nil {
				r.Log.Warn("Notebook 观测失败", "notebook", n.ID, "err", err)
			}
		}
	}
}

func (r *Runner) observeNotebook(ctx context.Context, id, groupID uint64) error {
	st := repository.New(r.DB)
	n, err := st.Notebook(ctx, groupID, id, false, false)
	if err != nil {
		return err
	}
	lock, err := repository.TargetLock(ctx, r.DB, n.Cluster, "notebook", notebookID(id))
	if err != nil {
		return err
	}
	defer lock.Close()
	n, err = st.Notebook(ctx, groupID, id, false, false)
	if err != nil {
		return err
	}
	g, err := st.Group(ctx, groupID, false)
	if err != nil {
		return err
	}
	var obs *k8s.NotebookObservation
	exists, observeErr := r.Kube.AccessNamespace(ctx, spaceRef(g))
	if observeErr == nil {
		if !exists {
			obs = &k8s.NotebookObservation{UID: notebookString(n.UID), State: "MISSING", Reason: "NOTEBOOK_NAMESPACE_MISSING: namespace 已消失"}
		} else {
			obs, observeErr = r.Kube.ObserveNotebook(ctx, notebookRef(n))
		}
	}
	var identityConflict *k8s.Permanent
	if errors.As(observeErr, &identityConflict) {
		obs, observeErr = &k8s.NotebookObservation{UID: notebookString(n.UID), State: "CONFLICT", Reason: truncate(identityConflict.Reason, 1000)}, nil
	}
	if err := repository.CheckTargetLock(ctx, lock); err != nil {
		return err
	}
	return repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
		if err := tx.LockAuthorization(ctx); err != nil {
			return err
		}
		if _, err := tx.Group(ctx, groupID, true); err != nil {
			return err
		}
		fresh, err := tx.Notebook(ctx, groupID, id, true, true)
		if err != nil {
			return err
		}
		if fresh.DeletedAt != nil || fresh.ServerType != n.ServerType || fresh.Revision != n.Revision || fresh.InstanceID != n.InstanceID || notebookString(fresh.UID) != notebookString(n.UID) || notebookString(fresh.LastOperationID) != notebookString(n.LastOperationID) ||
			fresh.WorkspaceID != n.WorkspaceID || fresh.WorkspaceInstanceID != n.WorkspaceInstanceID || fresh.WorkspacePVC != n.WorkspacePVC || notebookString(fresh.WorkspaceUID) != notebookString(n.WorkspaceUID) {
			return nil
		}
		fields := map[string]any{}
		busy, err := tx.HasNotebookOperation(ctx, id)
		if err != nil {
			return err
		}
		lastSucceeded := false
		if n.LastOperationID != nil {
			o, err := tx.Operation(ctx, *n.LastOperationID)
			if err != nil {
				return err
			}
			lastSucceeded = o.Status == "SUCCEEDED"
		}
		if observeErr != nil {
			// API 失败不推进 observed_at；超过时效明确标陈旧，不捏造对象丢失。
			if n.ObservedAt == nil || time.Since(*n.ObservedAt) > 2*time.Minute {
				fields["observed_state"] = "STALE"
				if !busy && lastSucceeded {
					fields["status"] = "UNKNOWN"
				}
				fields["status_reason"] = "OBSERVATION_STALE: 无法取得近期可信观测"
			}
		} else if obs != nil {
			data, err := json.Marshal(obs)
			if err != nil {
				return err
			}
			fields["observed_at"], fields["observed_state"], fields["observed_spec"] = gorm.Expr("UTC_TIMESTAMP(3)"), obs.State, string(data)
			if obs.UID != "" && obs.State != "MISSING" && obs.State != "CONFLICT" {
				fields["uid"] = obs.UID
			}
			if !busy && lastSucceeded {
				fields["status_reason"] = nil
				if obs.State == "MISSING" || obs.State == "CONFLICT" {
					fields["status"], fields["status_reason"] = "ERROR", obs.Reason
				} else if obs.State != n.DesiredState {
					fields["status"], fields["status_reason"] = "ERROR", "RUNTIME_DRIFT: 真实运行状态与业务期望不一致；请显式重试或启停"
				} else {
					fields["status"] = obs.State
				}
			}
			if !busy && obs.State == "RUNNING" && obs.Revision == n.Revision && obs.BudgetAccounted && n.LastOperationID != nil {
				if err := tx.AccountNotebookBudget(ctx, *n.LastOperationID); err != nil {
					return err
				}
			}
		}
		if len(fields) == 0 {
			return nil
		}
		return tx.UpdateNotebook(ctx, groupID, id, fields)
	})
}
