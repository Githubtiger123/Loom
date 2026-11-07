package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

func savePipelineVersionRevision(ctx context.Context, st *repository.Store, v *model.PipelineVersion, actor uint64) error {
	return st.DB.WithContext(ctx).Create(&model.PipelineVersionRevision{VersionID: v.ID, Revision: v.Revision,
		GroupID: v.GroupID, Spec: v.Spec, SpecHash: v.SpecHash, CreatedBy: &actor}).Error
}

func (s *Service) UpdatePipelineVersion(ctx context.Context, actor, groupID, pipelineID, versionID uint64, key string, in dto.UpdatePipelineVersionReq) (*model.Operation, error) {
	if in.Revision == 0 || in.ParentRevision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供版本 revision 和定义 parentRevision")
	}
	pack := dto.CreatePipelineVersionReq{Revision: 1, Name: "version", PackageYAML: in.PackageYAML, PipelineVersionSpec: in.PipelineVersionSpec}
	if err := normalizePipelinePackage(&pack); err != nil {
		return nil, err
	}
	if err := pack.Validate(); err != nil {
		return nil, err
	}
	if s.PipelineClient != nil && len(pack.PipelineSpec) == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "内置 KFP 模式编辑版本需提供编译后的 IR")
	}
	in.PackageYAML, in.PipelineVersionSpec = "", pack.PipelineVersionSpec
	return s.mutate(ctx, actor, groupID, key, "pipeline.version.update", "pipeline.version.update", "pipeline",
		fmt.Sprintf("%d/%d/%d", groupID, pipelineID, versionID), in, func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			p, err := st.Pipeline(ctx, groupID, pipelineID, true)
			if err != nil {
				return err
			}
			if p.DeletedAt != nil || p.Revision != in.ParentRevision || p.Revision == ^uint64(0) {
				return apperr.New(apperr.CodeConflict, "Pipeline 定义已变化或归档")
			}
			v := &model.PipelineVersion{}
			if err := st.DB.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND pipeline_id = ? AND group_id = ?", versionID, pipelineID, groupID).First(v).Error; err != nil {
				return err
			}
			if v.Revision != in.Revision || v.Revision == ^uint64(0) {
				return apperr.New(apperr.CodeConflict, "版本 revision 已变化")
			}
			data, err := json.Marshal(in.PipelineVersionSpec)
			if err != nil {
				return err
			}
			r := st.DB.WithContext(ctx).Model(v).Where("revision = ?", in.Revision).
				Updates(map[string]any{"revision": v.Revision + 1, "spec": string(data), "spec_hash": mlHash(data)})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return mlExecutionConflict()
			}
			v.Revision, v.Spec, v.SpecHash = in.Revision+1, data, mlHash(data)
			if err := savePipelineVersionRevision(ctx, st, v, actor); err != nil {
				return err
			}
			if err := st.UpdateML(ctx, p, groupID, pipelineID, in.ParentRevision, map[string]any{"revision": in.ParentRevision + 1}); err != nil {
				return err
			}
			if err := st.DB.WithContext(ctx).Where("id = ?", versionID).First(v).Error; err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion, o.Payload = g.Cluster, strconv.FormatUint(pipelineID, 10), in.ParentRevision+1, json.RawMessage(`{}`)
			return result(o, map[string]any{"pipelineId": pipelineID, "revision": in.ParentRevision + 1, "version": v})
		}, pipelineGuard(ctx, actor, groupID, pipelineID, "pipeline.version.update"))
}

func (s *Service) UpdatePipelineRun(ctx context.Context, actor, groupID, id uint64, key string, in dto.UpdatePipelineRunReq) (*model.Operation, error) {
	if s.PipelineClient == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "运行编辑需启用内置 KFP 清理执行器")
	}
	if in.Revision == 0 || in.VersionID == 0 || in.VersionRevision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision、versionId 和 versionRevision")
	}
	// 提前校验公开值；类型绑定使用事务中读取的版本/IR schema。
	if err := (dto.SubmitPipelineRunReq{Name: "edit", VersionID: in.VersionID, Parameters: in.Parameters}).Validate(); err != nil {
		return nil, err
	}
	var inline *dto.PipelineVersionSpec
	if in.PackageYAML != "" || len(in.PipelineSpec) != 0 {
		pack := dto.CreatePipelineVersionReq{Revision: 1, Name: "edit", PackageYAML: in.PackageYAML,
			PipelineVersionSpec: dto.PipelineVersionSpec{PipelineSpec: in.PipelineSpec}}
		if err := normalizePipelinePackage(&pack); err != nil {
			return nil, err
		}
		if err := pack.Validate(); err != nil {
			return nil, err
		}
		inline = &pack.PipelineVersionSpec
		in.PackageYAML, in.PipelineSpec = "", pack.PipelineSpec
	}
	guard := func(st *repository.Store) error {
		for _, permission := range []string{"pipeline.update", "pipeline.stop", "pipeline.delete"} {
			if err := mlGuard(ctx, actor, groupID, id, "pipeline_run", permission)(st); err != nil {
				return err
			}
		}
		return nil
	}
	return s.mutate(ctx, actor, groupID, key, "pipeline.update", string(MLPipelineEdit), "pipeline_run", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			if _, err := mlGroup(ctx, st, groupID, true); err != nil {
				return err
			}
			// 定义锁与提交/版本编辑一致，版本读取不能混入并发编辑的一半。
			current, err := st.PipelineRun(ctx, groupID, id, false)
			if err != nil {
				return err
			}
			p, err := st.Pipeline(ctx, groupID, current.PipelineID, true)
			if err != nil {
				return err
			}
			if p.DeletedAt != nil {
				return apperr.New(apperr.CodeConflict, "Pipeline 已归档")
			}
			if err := mlAllowed(ctx, st, actor, &p.MLIdentity, "pipeline", "pipeline.read"); err != nil {
				return err
			}
			v, err := st.PipelineVersion(ctx, groupID, p.ID, in.VersionID)
			if err != nil {
				return err
			}
			if v.Revision != in.VersionRevision {
				return apperr.New(apperr.CodeConflict, "所选版本已变化，请刷新")
			}
			var spec dto.PipelineVersionSpec
			if err := json.Unmarshal(v.Spec, &spec); err != nil {
				return err
			}
			if inline != nil {
				spec = *inline
			}
			if len(spec.PipelineSpec) == 0 {
				return apperr.New(apperr.CodeInvalid, "编辑运行需要已编译的 KFP IR")
			}
			values, err := dto.BindPipelineParameters(spec.Parameters, in.Parameters)
			if err != nil {
				return err
			}
			pending, err := json.Marshal(dto.PipelineRunSpec{PipelineID: p.ID, VersionID: v.ID, VersionRevision: v.Revision, PipelineVersionSpec: spec, Values: values})
			if err != nil {
				return err
			}
			n, err := writableML(ctx, st, groupID, id, in.Revision, "pipeline_run", true)
			if err != nil {
				return err
			}
			// 新配置先暂存，执行命令继续携带旧配置来核对并清理旧 KFP Run。
			run := n.row.(*model.PipelineRun)
			run.PendingSpec = pending
			if err := st.DB.WithContext(ctx).Model(run).Where("id = ? AND revision = ?", id, in.Revision).Update("pending_spec", string(pending)).Error; err != nil {
				return err
			}
			n.state.DesiredState = "STOPPED"
			return saveMLIntent(ctx, st, o, n)
		}, guard)
}

func (s *Service) StartPipelineRun(ctx context.Context, actor, groupID, id uint64, key string, in dto.MLActionReq) (*model.Operation, error) {
	if in.Revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision")
	}
	if s.PipelineClient == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "启动需启用内置 KFP 执行器")
	}
	return s.mutate(ctx, actor, groupID, key, "pipeline.run", string(MLPipelineSubmit), "pipeline_run", fmt.Sprintf("%d/%d/start", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			if _, err := mlGroup(ctx, st, groupID, false); err != nil {
				return err
			}
			n, err := writableML(ctx, st, groupID, id, in.Revision, "pipeline_run", false)
			if err != nil {
				return err
			}
			run := n.row.(*model.PipelineRun)
			if !run.ExecutionCleaned || len(run.PendingSpec) != 0 || run.DesiredState != "STOPPED" || run.Status != "STOPPED" || run.ObservedState != "STOPPED" {
				return apperr.New(apperr.CodeConflict, "需先完成编辑与旧实例清理，再手动启动")
			}
			p, err := st.Pipeline(ctx, groupID, run.PipelineID, false)
			if err != nil {
				return err
			}
			if p.DeletedAt != nil {
				return apperr.New(apperr.CodeConflict, "Pipeline 已归档")
			}
			if err := mlAllowed(ctx, st, actor, &p.MLIdentity, "pipeline", "pipeline.run"); err != nil {
				return err
			}
			// 使用编辑保存的快照；不能偷偷换成后来再次修改过的版本。
			run.InstanceID, run.ExternalID, run.AppliedRevision = uuid.NewString(), nil, 0
			run.DesiredState, run.ObservedState, run.ObservedAt, run.ExecutionCleaned = "RUNNING", "UNKNOWN", nil, false
			if err := st.DB.WithContext(ctx).Model(run).Where("id = ? AND revision = ?", id, in.Revision).Updates(map[string]any{
				"instance_id": run.InstanceID, "external_id": nil, "applied_revision": 0, "observed_state": "UNKNOWN", "observed_at": nil,
				"execution_cleaned": false, "sync_enabled": false, "sync_lease_owner": nil, "sync_lease_until": nil, "sync_failures": 0,
				"kfp_state": nil, "kfp_synced_at": nil, "kfp_sync_attempted_at": nil, "kfp_sync_reason": nil,
			}).Error; err != nil {
				return err
			}
			return saveMLIntent(ctx, st, o, n)
		}, mlGuard(ctx, actor, groupID, id, "pipeline_run", "pipeline.run"))
}

// 在报告事务中调用：只有外部清理已确认才应用 pending_spec，绝不软删除业务记录。
func applyPipelineEdit(ctx context.Context, st *repository.Store, c *MLCommand, n *mlObject, fields map[string]any, externalID string) error {
	run := n.row.(*model.PipelineRun)
	if c.PendingPipelineRun == nil || len(run.PendingSpec) == 0 {
		return mlExecutionConflict()
	}
	var pending dto.PipelineRunSpec
	if err := json.Unmarshal(run.PendingSpec, &pending); err != nil {
		return err
	}
	// MySQL JSON 会改写空白/键序，比较前统一 IR 表示。
	canonical, err := canonicalPipelineIR(pending.PipelineSpec)
	if err != nil {
		return err
	}
	pending.PipelineSpec = canonical
	expected := *c.PendingPipelineRun
	expected.PipelineSpec, err = canonicalPipelineIR(expected.PipelineSpec)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(pending)
	b, _ := json.Marshal(expected)
	if string(a) != string(b) || pending.PipelineID != run.PipelineID {
		return mlExecutionConflict()
	}
	if !run.ExecutionCleaned {
		// pending 配置不混入旧执行快照。
		old := *run
		old.PendingSpec = nil
		if old.ExternalID == nil && externalID != "" {
			old.ExternalID = &externalID
		}
		snapshot, err := json.Marshal(old)
		if err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.PipelineRunAttempt{
			InstanceID: run.InstanceID, RunID: run.ID, Revision: run.Revision, GroupID: run.GroupID, Snapshot: snapshot}).Error; err != nil {
			return err
		}
	}
	run.RequestedSpec, run.VersionID, run.PendingSpec, run.ExecutionCleaned = a, pending.VersionID, nil, true
	fields["requested_spec"], fields["version_id"], fields["pending_spec"] = string(a), pending.VersionID, nil
	fields["execution_cleaned"], fields["sync_enabled"] = true, false
	fields["sync_lease_owner"], fields["sync_lease_until"] = nil, nil
	fields["kfp_sync_reason"] = nil
	return nil
}

func (s *Service) PipelineRunAttempts(ctx context.Context, actor, groupID, id uint64, q dto.MLListQuery) ([]model.PipelineRunAttempt, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.PipelineRun(ctx, actor, groupID, id); err != nil {
		return nil, err
	}
	items := []model.PipelineRunAttempt{}
	// after 使用清理归档时的业务 revision。
	err := s.DB.WithContext(ctx).Where("group_id = ? AND run_id = ?", groupID, id).
		Where("revision > ?", q.After).Order("revision").Limit(q.Limit).Find(&items).Error
	return items, err
}

func (s *Service) PipelineVersionRevisions(ctx context.Context, actor, groupID, pipelineID, versionID uint64, q dto.MLListQuery) ([]model.PipelineVersionRevision, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.PipelineVersion(ctx, actor, groupID, pipelineID, versionID); err != nil {
		return nil, err
	}
	items := []model.PipelineVersionRevision{}
	err := s.DB.WithContext(ctx).Where("group_id = ? AND version_id = ? AND revision > ?", groupID, versionID, q.After).
		Order("revision").Limit(q.Limit).Find(&items).Error
	return items, err
}
