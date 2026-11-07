package repository

import (
	"context"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"time"
)

// ACCOUNTED 发生在快照开始之后的占用仍须加上，避免锁外快照与执行器确认间的窗口。
func (s *Store) UnreflectedBudget(ctx context.Context, groupID uint64, snapshotStart time.Time) (dto.ResourceBudget, error) {
	var total dto.ResourceBudget
	err := s.query(ctx, false).Model(&model.QuotaReservation{}).
		Select("COALESCE(SUM(cpu_milli),0) AS cpu_milli, COALESCE(SUM(memory_bytes),0) AS memory_bytes, COALESCE(SUM(gpu),0) AS gpu").
		Where("group_id=? AND (status='HELD' OR (status='ACCOUNTED' AND (accounted_at IS NULL OR accounted_at>=?)))", groupID, snapshotStart.Add(-time.Second)).
		Scan(&total).Error
	return total, err
}

// 以下执行器 hook 必须在空间锁内调用，并已重新验证 Pod UID 和实际 ResourceQuota 用量。
// 不能仅凭 Notebook CR 创建成功就标 ACCOUNTED，否则 controller 尚未建 Pod 的窗口会超售。
func (s *Store) AccountNotebookBudget(ctx context.Context, operationID string) error {
	return s.query(ctx, false).Model(&model.QuotaReservation{}).Where("operation_id=? AND status='HELD'", operationID).
		Updates(map[string]any{"status": "ACCOUNTED", "accounted_at": time.Now().UTC()}).Error
}

// 仅在确认相应版本的工作负载及 Pod 已不存在/停止后调用；不得释放较新启动的占用。
func (s *Store) ReleaseNotebookBudget(ctx context.Context, notebookID, throughRevision uint64) error {
	return s.query(ctx, false).Model(&model.QuotaReservation{}).Where("notebook_id=? AND notebook_revision<=? AND status<>'RELEASED'", notebookID, throughRevision).
		Update("status", "RELEASED").Error
}
