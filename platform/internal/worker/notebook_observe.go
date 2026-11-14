package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

// Low-frequency sweep only enqueues durable business IDs, including resources
// deleted while the observer was down. It does not read Kubernetes objects.
func (r *Runner) enqueueObservedNotebooks(ctx context.Context, namespace string, enqueue func(string)) error {
	var after uint64
	for {
		var ids []uint64
		query := r.DB.WithContext(ctx).Model(&model.Notebook{}).Where("cluster = ? AND id > ? AND deleted_at IS NULL", r.Cluster, after)
		if namespace != "" {
			query = query.Where("namespace = ?", namespace)
		}
		if err := query.Order("id").Limit(100).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			enqueue("notebook-id/" + strconv.FormatUint(id, 10))
			after = id
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
	writeErr := repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
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
	if writeErr != nil {
		return writeErr
	}
	return observeErr
}
