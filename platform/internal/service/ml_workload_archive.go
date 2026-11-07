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

// DeletePipeline archives the definition, never its versions/revision history or KFP
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
				return apperr.New(apperr.CodeConflict, "请先删除该定义下所有运行并等待外部资源清理完成")
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

func (s *Service) DeletePipelineRun(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	return s.changeMLState(ctx, actor, groupID, id, key, in, "pipeline_run", MLPipelineDelete, "pipeline.delete", "DELETED")
}
