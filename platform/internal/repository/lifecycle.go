package repository

import (
	"context"
	"fmt"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (s *Store) HasGroupOperation(ctx context.Context, groupID uint64) (bool, error) {
	var count int64
	err := s.query(ctx, false).Model(&model.Operation{}).Where("target_type = 'group' AND target_key = ? AND status IN ?", fmt.Sprint(groupID), []string{"PENDING", "RUNNING", "RETRYING"}).Count(&count).Error
	return count > 0, err
}

func (s *Store) ActiveMember(ctx context.Context, groupID, userID uint64) (bool, error) {
	var count int64
	err := s.effective(ctx).Where("b.scope_type = 'group' AND b.scope_id = ? AND b.user_id = ?", groupID, userID).Count(&count).Error
	return count > 0, err
}

// GroupDependencies 包括未纳入目录表的旧 Notebook 数据；外部资源由执行器另行完整检查。
type GroupDependencies struct {
	Notebooks        int64 `json:"notebooks"`
	Workspaces       int64 `json:"workspaces"`
	LegacyWorkspaces int64 `json:"legacyWorkspaces"`
	Reservations     int64 `json:"reservations"`
	Pipelines        int64 `json:"pipelines"`
	PipelineRuns     int64 `json:"pipelineRuns"`
	TrainerJobs      int64 `json:"trainerJobs"`
	KServeServices   int64 `json:"kserveServices"`
}

func (d GroupDependencies) Empty() bool {
	return d.Notebooks+d.Workspaces+d.LegacyWorkspaces+d.Reservations+d.Pipelines+d.PipelineRuns+d.TrainerJobs+d.KServeServices == 0
}
func (s *Store) GroupDependencies(ctx context.Context, groupID uint64) (*GroupDependencies, error) {
	d := &GroupDependencies{}
	queries := []struct {
		sql  string
		dest *int64
	}{
		{"SELECT COUNT(*) FROM tb_notebook WHERE group_id=? AND deleted_at IS NULL", &d.Notebooks},
		{"SELECT COUNT(*) FROM tb_workspace WHERE group_id=? AND deleted_at IS NULL", &d.Workspaces},
		{`SELECT COUNT(*) FROM tb_notebook n WHERE n.group_id=? AND n.workspace_pvc<>''
 AND NOT EXISTS (SELECT 1 FROM tb_workspace w WHERE w.group_id=n.group_id
 AND w.cluster=n.cluster AND w.namespace=n.namespace AND w.pvc_name=n.workspace_pvc
 AND w.pvc_uid=n.workspace_uid AND w.status='DELETED' AND w.deleted_at IS NOT NULL)`, &d.LegacyWorkspaces},
		{"SELECT COUNT(*) FROM tb_quota_reservation WHERE group_id=? AND status<>'RELEASED'", &d.Reservations},
		{"SELECT COUNT(*) FROM tb_pipeline WHERE group_id=? AND deleted_at IS NULL", &d.Pipelines},
		{"SELECT COUNT(*) FROM tb_pipeline_run WHERE group_id=? AND deleted_at IS NULL", &d.PipelineRuns},
		{"SELECT COUNT(*) FROM tb_trainer_job WHERE group_id=? AND deleted_at IS NULL", &d.TrainerJobs},
		{"SELECT COUNT(*) FROM tb_kserve_service WHERE group_id=? AND deleted_at IS NULL", &d.KServeServices},
	}
	for _, q := range queries {
		if err := s.query(ctx, false).Raw(q.sql, groupID).Scan(q.dest).Error; err != nil {
			return nil, err
		}
	}
	return d, nil
}

// LiveMLDependencies 只把已确认终态且没有排队操作的资源视为静默。
// 表名/target 全为代码常量；版本、配置和历史表是保留的业务数据，不当作运行进程。
// 缺表、查询失败或未观测一律报错/阻断，不回退为零资源。
func (s *Store) LiveMLDependencies(ctx context.Context, groupID, userID uint64) (int64, error) {
	var total int64
	for _, spec := range []struct {
		table, target string
		jobs          bool
	}{
		{"tb_pipeline_run", "pipeline_run", true},
		{"tb_trainer_job", "trainer_job", true},
		{"tb_kserve_service", "kserve_service", false},
	} {
		q := s.query(ctx, false).Table(spec.table + " n")
		if groupID != 0 {
			q = q.Where("n.group_id=?", groupID)
		}
		if userID != 0 {
			q = q.Where("n.created_by=?", userID)
		}
		// 未完成/重试中的 stop/delete 也阻断；只写了 STOPPED 意图不算停止。
		quiet := `n.observed_at IS NOT NULL AND n.applied_revision=n.revision AND
 ((n.desired_state='STOPPED' AND n.observed_state='STOPPED' AND n.status='STOPPED'
 AND n.observed_at >= TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(3)))
 OR (n.desired_state='DELETED' AND n.observed_state='DELETED' AND n.status='DELETED' AND n.deleted_at IS NOT NULL)`
		if spec.jobs {
			quiet += ` OR (n.observed_state IN ('SUCCEEDED','FAILED') AND n.status=n.observed_state AND n.desired_state<>'DELETED')`
		}
		if spec.target == "pipeline_run" {
			// 归档只接受已应用的不可重启作业终态。保留其原观测证据，不能因
			// STOPPED 时间超过两分钟又把历史归档记录当作活动任务。
			quiet += ` OR (n.deleted_at IS NOT NULL AND n.observed_state IN ('SUCCEEDED','FAILED','STOPPED')
 AND n.status=n.observed_state AND n.desired_state<>'DELETED')`
		}
		quiet += `)`
		var count int64
		if err := q.Where("NOT ("+quiet+") OR EXISTS (SELECT 1 FROM tb_operation o WHERE o.target_type=? AND o.target_key=CAST(n.id AS CHAR) AND o.status IN ('PENDING','RUNNING','RETRYING'))", spec.target).Count(&count).Error; err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (s *Store) LifecycleNotebooks(ctx context.Context, groupID, userID uint64) ([]model.Notebook, error) {
	// 统一先空间再实例；不能先锁实例再等待空间，与观测器形成锁环。
	groups := []model.Group{}
	groupQuery := s.query(ctx, true).Model(&model.Group{}).
		Where("EXISTS (SELECT 1 FROM tb_notebook n WHERE n.group_id=tb_group.id AND n.deleted_at IS NULL)")
	if groupID != 0 {
		groupQuery = groupQuery.Where("id=?", groupID)
	}
	if userID != 0 {
		groupQuery = groupQuery.Where(`EXISTS (SELECT 1 FROM tb_notebook n WHERE n.group_id=tb_group.id AND n.deleted_at IS NULL AND COALESCE((SELECT owner_user_id FROM tb_notebook_owner WHERE notebook_id=n.id),n.created_by)=?)`, userID)
	}
	if err := groupQuery.Order("id").Find(&groups).Error; err != nil {
		return nil, err
	}
	q := s.query(ctx, true).Model(&model.Notebook{}).Where("deleted_at IS NULL")
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	if userID != 0 {
		q = q.Where(`COALESCE((SELECT owner_user_id FROM tb_notebook_owner WHERE notebook_id=tb_notebook.id),created_by)=?`, userID)
	}
	rows := []model.Notebook{}
	err := q.Order("group_id,id").Find(&rows).Error
	return rows, err
}
