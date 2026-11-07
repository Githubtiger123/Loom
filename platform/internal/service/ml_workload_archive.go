package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

// DeletePipeline archives the definition, never its immutable versions or KFP
// resources. Submit and archive both lock the group then the parent definition.
func (s *Service) DeletePipeline(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	if in.Revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision")
	}
	return s.mutate(ctx, actor, groupID, key, "pipeline.delete", "pipeline.delete", "pipeline", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, true)
			if err != nil {
				return err
			}
			p, err := st.Pipeline(ctx, groupID, id, true)
			if err != nil {
				return err
			}
			if p.DeletedAt != nil || p.Revision != in.Revision || p.Revision == ^uint64(0) {
				return apperr.New(apperr.CodeConflict, "Pipeline 已归档或 revision 已变化")
			}
			var count int64
			if err := st.DB.WithContext(ctx).Model(&model.PipelineRun{}).
				Where("group_id = ? AND pipeline_id = ? AND deleted_at IS NULL", groupID, id).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return apperr.New(apperr.CodeConflict, "请先停止并归档该定义下所有运行")
			}
			now, nextRevision := time.Now().UTC(), p.Revision+1
			if err := st.UpdateML(ctx, &model.Pipeline{}, groupID, id, p.Revision,
				map[string]any{"deleted_at": now, "revision": nextRevision}); err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion, o.Payload = g.Cluster, strconv.FormatUint(id, 10), nextRevision, json.RawMessage(`{}`)
			return result(o, map[string]any{"pipelineId": id, "groupId": groupID, "revision": nextRevision,
				"archived": true, "deletedAt": now, "versionsRetained": true, "externalResourcesDeleted": false})
		}, pipelineGuard(ctx, actor, groupID, id, "pipeline.delete"))
}

// DeletePipelineRun is local archival, NOT a KFP delete/terminate command. Keep
// runtime revision, last operation and all observed evidence unchanged: archiving
// metadata must not fabricate a new externally applied revision or DELETED state.
func (s *Service) DeletePipelineRun(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	if in.Revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision")
	}
	return s.mutate(ctx, actor, groupID, key, "pipeline.delete", "pipeline.run.delete", "pipeline_run", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, true)
			if err != nil {
				return err
			}
			n, err := loadML(ctx, st, "pipeline_run", groupID, id, true)
			if err != nil {
				return err
			}
			if n.identity.DeletedAt != nil || n.state.Revision != in.Revision {
				return apperr.New(apperr.CodeConflict, "运行已归档或 revision 已变化")
			}
			terminal := n.state.ObservedState == "SUCCEEDED" || n.state.ObservedState == "FAILED" || n.state.ObservedState == "STOPPED"
			if !terminal || n.state.ObservedAt == nil || n.state.Status != n.state.ObservedState || n.state.AppliedRevision != n.state.Revision {
				return apperr.New(apperr.CodeConflict, "只有执行器确认的已应用终态运行可以归档；请先停止并等待生效")
			}
			busy, err := st.HasMLOperation(ctx, "pipeline_run", id)
			if err != nil {
				return err
			}
			if busy {
				return apperr.New(apperr.CodeConflict, "运行仍有未完成操作，不可归档")
			}
			now := time.Now().UTC()
			if err := st.UpdateML(ctx, &model.PipelineRun{}, groupID, id, in.Revision, map[string]any{"deleted_at": now}); err != nil {
				return err
			}
			n.identity.DeletedAt = &now
			o.Cluster, o.TargetKey, o.TargetVersion, o.Payload = g.Cluster, strconv.FormatUint(id, 10), n.state.Revision, json.RawMessage(`{}`)
			if err := mlRecord(ctx, st, o, n, "ARCHIVE"); err != nil {
				return err
			}
			return result(o, map[string]any{"pipelineRunId": id, "groupId": groupID, "revision": n.state.Revision,
				"archived": true, "deletedAt": now, "status": n.state.Status, "observedState": n.state.ObservedState,
				"historyRetained": true, "externalResourcesDeleted": false})
		}, mlGuard(ctx, actor, groupID, id, "pipeline_run", "pipeline.delete"))
}
