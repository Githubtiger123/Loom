package service

import (
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/model"
)

// Shared receipt verification for runtime, DAG, node details, logs and terminal.
func (s *Service) readPipelineObservation(ctx context.Context, actor, groupID, id uint64) (*model.PipelineRun, *kfp.Run, *kfp.RunDetails, error) {
	n, err := s.PipelineRun(ctx, actor, groupID, id)
	if err != nil {
		return nil, nil, nil, err
	}
	if s.PipelineClient == nil {
		return nil, nil, nil, apperr.New(apperr.CodeUnavailable, "官方 KFP 后端尚未配置")
	}
	if n.Cluster != s.Cluster || n.ExecutionCleaned || n.ExternalID == nil {
		return nil, nil, nil, apperr.New(apperr.CodeConflict, "当前执行尚未取得 KFP Run ID 或已经清理，请查询操作进度/历史记录")
	}
	journal := &pipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", n.InstanceID).First(journal).Error; err != nil {
		return nil, nil, nil, domainError(err)
	}
	run, err := s.PipelineClient.GetRun(ctx, *n.ExternalID)
	if err != nil {
		return nil, nil, nil, apperr.New(apperr.CodeUnavailable, "无法读取 KFP 运行详情")
	}
	if !journal.Attempted || journal.ExternalID == nil || *journal.ExternalID != *n.ExternalID || run.ID != n.InstanceID || run.ID != *n.ExternalID || run.Namespace != n.Namespace || run.Name != journal.RunName || run.Description != "loom:v1:"+n.InstanceID+":"+journal.RequestHash {
		return nil, nil, nil, apperr.New(apperr.CodeConflict, "KFP 运行身份不匹配")
	}
	details := &kfp.RunDetails{Tasks: []kfp.TaskDetail{}}
	if len(run.Details) > 0 && json.Unmarshal(run.Details, details) != nil {
		return nil, nil, nil, apperr.New(apperr.CodeUnavailable, "KFP 节点详情格式无效")
	}
	if len(details.Tasks) > 4000 {
		return nil, nil, nil, apperr.New(apperr.CodeUnavailable, "KFP 节点数超过读取上限")
	}
	seen := map[string]bool{}
	for _, task := range details.Tasks {
		if task.ID == "" || seen[task.ID] || (task.RunID != "" && task.RunID != run.ID) {
			return nil, nil, nil, apperr.New(apperr.CodeConflict, "KFP 节点身份不匹配或重复")
		}
		seen[task.ID] = true
	}
	return n, run, details, nil
}

func (s *Service) checkPipelineObservation(ctx context.Context, actor uint64, original *model.PipelineRun) error {
	fresh, err := s.PipelineRun(ctx, actor, original.GroupID, original.ID)
	if err != nil {
		return err
	}
	if fresh.InstanceID != original.InstanceID || fresh.Revision != original.Revision || fresh.ExecutionCleaned || fresh.ExternalID == nil || original.ExternalID == nil || *fresh.ExternalID != *original.ExternalID {
		return apperr.New(apperr.CodeConflict, "运行已变化，请刷新")
	}
	return nil
}
