package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PipelineSpaceVerifier 在外部写入之前核实真实 Profile/namespace 身份，不在 SQL 锁内调用。
type PipelineSpaceVerifier interface {
	VerifyPipelineSpace(context.Context, *MLExecutionClaim) error
}

type pipelineSubmission struct {
	InstanceID  string `gorm:"primaryKey"`
	RequestHash string
	RunName     string
	Attempted   bool
	ExternalID  *string
}

func (pipelineSubmission) TableName() string { return "tb_pipeline_submission" }

func (s *Service) PipelinePeer() string {
	return mlHash([]byte("loom-internal-kfp-v1\x00" + s.Cluster + "\x00" + s.PipelineConfig.Endpoint))
}

func pipelineLease(c *MLExecutionClaim) dto.MLExecutionLeaseReq {
	return dto.MLExecutionLeaseReq{LeaseOwner: c.LeaseOwner, InstanceID: c.InstanceID, Revision: c.Revision, TargetFence: c.TargetFence}
}

type pipelineFailure struct {
	reason    string
	permanent bool
}

func (e *pipelineFailure) Error() string { return e.reason }

func pipelineProblem(reason string, permanent bool) error {
	return &pipelineFailure{reason: reason, permanent: permanent}
}

// ExecutePipelineClaim 是内置 KFP 适配器的一次有界处理；外部 I/O 不进入业务事务。
func (s *Service) ExecutePipelineClaim(ctx context.Context, claim *MLExecutionClaim) error {
	if s.PipelineClient == nil || s.PipelineVerifier == nil {
		return fmt.Errorf("KFP 执行依赖未配置")
	}
	err := s.executePipelineClaim(ctx, claim)
	if err == nil || ctx.Err() != nil {
		return err
	}
	var problem *pipelineFailure
	reason, outcome := "EXTERNAL_UNAVAILABLE", "RETRY"
	if errors.As(err, &problem) {
		reason = problem.reason
		if problem.permanent {
			outcome = "FAILED"
		}
	}
	var identity *kfp.IdentityError
	var response *kfp.Error
	if errors.As(err, &identity) {
		reason, outcome = "IDENTITY_CONFLICT", "FAILED"
	} else if errors.As(err, &response) && response.Status == 404 {
		reason = "RESOURCE_NOT_FOUND"
	}
	// 失去授权/租约时 Report 自身会拒绝；不得绕过 fence 写运行状态。
	externalID := ""
	if claim.ExternalID != nil {
		externalID = *claim.ExternalID
	}
	_, reportErr := s.ReportMLExecution(ctx, s.PipelinePeer(), claim.OperationID, dto.ReportMLExecutionReq{
		MLExecutionLeaseReq: pipelineLease(claim), ReportID: uuid.NewString(), Sequence: claim.Sequence + 1,
		Outcome: outcome, Reason: reason, ExternalID: externalID,
	})
	return reportErr
}

func (s *Service) executePipelineClaim(ctx context.Context, claim *MLExecutionClaim) error {
	c := &claim.Command
	if c.PipelineRun == nil || (c.Action != MLPipelineSubmit && c.Action != MLPipelineStop) {
		return pipelineProblem("REFERENCE_INVALID", true)
	}
	lock, err := repository.TargetLock(ctx, s.DB, c.Cluster, "pipeline_run", fmt.Sprint(c.ResourceID))
	if err != nil {
		return err
	}
	defer lock.Close()
	guard := func() error {
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return err
		}
		_, err := s.RenewMLExecution(ctx, s.PipelinePeer(), claim.OperationID, pipelineLease(claim))
		return err
	}
	if err := guard(); err != nil {
		return err
	}
	if err := s.PipelineVerifier.VerifyPipelineSpace(ctx, claim); err != nil {
		return err
	}
	spec := c.PipelineRun
	canonical, err := canonicalPipelineIR(spec.PipelineSpec)
	if err != nil || mlHash(canonical) != spec.PackageDigest {
		return pipelineProblem("REFERENCE_INVALID", true)
	}
	spec.PipelineSpec = canonical
	parameters, err := pipelineRuntimeParameters(*spec)
	if err != nil {
		return pipelineProblem("REFERENCE_INVALID", true)
	}
	name := "loom-" + c.InstanceID
	input := kfp.CreateRun{Name: name, Namespace: c.Namespace, ServiceAccount: s.PipelineConfig.ServiceAccount,
		PipelineSpec: spec.PipelineSpec, RuntimeConfig: kfp.RuntimeConfig{Parameters: parameters,
			PipelineRoot: strings.TrimRight(s.PipelineConfig.PipelineRoot, "/") + "/" + c.Namespace + "/" + c.InstanceID}}
	data, _ := json.Marshal([]any{s.PipelineConfig.Endpoint, input})
	hash := mlHash(data)
	input.Description = "loom:v1:" + c.InstanceID + ":" + hash
	journal := &pipelineSubmission{}
	err = s.DB.WithContext(ctx).Where("instance_id = ?", c.InstanceID).First(journal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if c.Action == MLPipelineStop {
			if claim.ExternalID != nil {
				return pipelineProblem("IDENTITY_CONFLICT", true)
			}
			// 与提交使用同一个目标锁；从未持久化提交意图时可以确认没有发起提交。
			return s.reportPipelineRun(ctx, claim, nil, "STOPPED", true)
		}
		journal = &pipelineSubmission{InstanceID: c.InstanceID, RequestHash: hash, RunName: name}
		if err := s.DB.WithContext(ctx).Create(journal).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if journal.RequestHash != hash || journal.RunName != name {
		return pipelineProblem("POLICY_DENIED", true)
	}
	id := journal.ExternalID
	if claim.ExternalID != nil {
		if id != nil && *id != *claim.ExternalID {
			return pipelineProblem("IDENTITY_CONFLICT", true)
		}
		id = claim.ExternalID
	}
	var run *kfp.Run
	if id != nil {
		run, err = s.PipelineClient.GetRun(ctx, *id)
		if err != nil {
			return err
		} // 404 不能冒充成功或再次创建
	} else if journal.Attempted {
		run, err = s.PipelineClient.FindRun(ctx, c.Namespace, name)
		if err != nil {
			return err
		}
		if run == nil {
			return pipelineProblem("SUBMISSION_UNCERTAIN", false)
		}
	} else if c.Action == MLPipelineStop {
		if claim.ExternalID != nil {
			return pipelineProblem("IDENTITY_CONFLICT", true)
		}
		return s.reportPipelineRun(ctx, claim, nil, "STOPPED", true)
	} else {
		// 先落盘再 POST。即使进程在 POST 前后崩溃，也绝不盲目二次创建。
		if err := guard(); err != nil {
			return err
		}
		r := s.DB.WithContext(ctx).Model(journal).Where("instance_id = ? AND attempted = ?", c.InstanceID, false).Update("attempted", true)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return pipelineProblem("SUBMISSION_UNCERTAIN", false)
		}
		if err := guard(); err != nil {
			return err
		}
		run, err = s.PipelineClient.CreateRun(ctx, input)
		if err != nil {
			var response *kfp.Error
			if errors.As(err, &response) && (response.Status == 400 || response.Status == 401 || response.Status == 403) {
				return pipelineProblem("EXTERNAL_REJECTED", true)
			}
			return pipelineProblem("SUBMISSION_UNCERTAIN", false)
		}
	}
	if !validator.IsCatalogID(run.ID) || run.Name != name || run.Namespace != c.Namespace || run.Description != input.Description {
		return pipelineProblem("IDENTITY_CONFLICT", true)
	}
	if id != nil && run.ID != *id {
		return pipelineProblem("IDENTITY_CONFLICT", true)
	}
	// 独立保存已发生的外部副作用，即使提交任务刚被停止意图替代也保留，供停止任务收敛。
	bound := s.DB.WithContext(ctx).Model(journal).Where("instance_id = ? AND request_hash = ? AND (external_id IS NULL OR external_id = ?)", c.InstanceID, hash, run.ID).Update("external_id", run.ID)
	if bound.Error != nil {
		return bound.Error
	}
	if bound.RowsAffected == 0 {
		// MySQL 相同值 UPDATE 可能为 0；读回区分幂等命中和条件不匹配。
		fresh := &pipelineSubmission{}
		if err := s.DB.WithContext(ctx).Where("instance_id = ?", c.InstanceID).First(fresh).Error; err != nil {
			return err
		}
		if fresh.RequestHash != hash || fresh.ExternalID == nil || *fresh.ExternalID != run.ID {
			return pipelineProblem("IDENTITY_CONFLICT", true)
		}
	}
	state := pipelineRunState(run.State)
	if claim.ObservedState != nil && *claim.ObservedState == "RUNNING" && (state == "UNKNOWN" || state == "PENDING") {
		// KFP 暂时回退/未知不能覆盖已确认的运行状态，也不能伪造一个新的运行证据。
		return pipelineProblem("EXTERNAL_UNAVAILABLE", false)
	}
	if c.Action == MLPipelineStop && run.State != "CANCELING" && state != "SUCCEEDED" && state != "FAILED" && state != "STOPPED" {
		if err := guard(); err != nil {
			return err
		}
		if err := s.PipelineClient.TerminateRun(ctx, run.ID); err != nil {
			return err
		}
		// terminate 成功只是请求受理，下一轮 GET 真实 CANCELED 后才完成。
	}
	return s.reportPipelineRun(ctx, claim, run, state, false)
}

func pipelineRunState(state string) string {
	switch state {
	case "PENDING":
		return "PENDING"
	case "RUNNING", "CANCELING", "PAUSED":
		return "RUNNING"
	case "SUCCEEDED", "SKIPPED":
		return "SUCCEEDED"
	case "FAILED":
		return "FAILED"
	case "CANCELED":
		return "STOPPED"
	default:
		return "UNKNOWN"
	}
}

func (s *Service) reportPipelineRun(ctx context.Context, claim *MLExecutionClaim, run *kfp.Run, state string, absent bool) error {
	id := ""
	if run != nil {
		id = run.ID
	}
	reason := ""
	if state == "FAILED" {
		reason = "RESOURCE_FAILED"
	}
	out, err := s.ReportMLExecution(ctx, s.PipelinePeer(), claim.OperationID, dto.ReportMLExecutionReq{
		MLExecutionLeaseReq: pipelineLease(claim), Sequence: claim.Sequence + 1, ReportID: uuid.NewString(), Outcome: "OBSERVED",
		ObservedState: state, ExternalID: id, ResourceAbsent: absent, Reason: reason,
	})
	if err != nil {
		return err
	}
	claim.MLExecutionView = out
	if out.Status != "RUNNING" {
		return nil
	}
	return s.yieldPipelineExecution(ctx, claim)
}

// 持久化下次轮询时间，长任务不占着线程；不把正常等待记成外部故障。
func (s *Service) yieldPipelineExecution(ctx context.Context, claim *MLExecutionClaim) error {
	return repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		r := mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Updates(map[string]any{
			"status": "RETRYING", "step": "WAIT_KFP_STATE", "lease_owner": nil, "lease_until": nil,
			"next_attempt_at": gorm.Expr("TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(3))", int(s.PipelineConfig.PollInterval.Std()/time.Second)),
		})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
}

// 下游 Runtime 接口只返回经筛选的任务信息，不代理任意 KFP URL/PodSpec/凭据。
type PipelineRuntime struct {
	ExternalID string                `json:"externalId"`
	State      string                `json:"state"`
	CreatedAt  string                `json:"createdAt"`
	FinishedAt string                `json:"finishedAt"`
	Tasks      []PipelineRuntimeTask `json:"tasks"`
}

type PipelineRuntimeTask struct {
	ID        string `json:"taskId"`
	Name      string `json:"name"`
	State     string `json:"state"`
	ParentID  string `json:"parentTaskId"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

func (s *Service) PipelineRunRuntime(ctx context.Context, actor, groupID, id uint64) (*PipelineRuntime, error) {
	n, err := s.PipelineRun(ctx, actor, groupID, id)
	if err != nil {
		return nil, err
	}
	if s.PipelineClient == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "官方 KFP 后端尚未配置")
	}
	if n.ExternalID == nil {
		return nil, apperr.New(apperr.CodeConflict, "运行尚未取得 KFP Run ID，请先查询操作进度")
	}
	journal := &pipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", n.InstanceID).First(journal).Error; err != nil {
		return nil, domainError(err)
	}
	run, err := s.PipelineClient.GetRun(ctx, *n.ExternalID)
	if err != nil {
		return nil, apperr.New(apperr.CodeUnavailable, "无法读取 KFP 运行详情")
	}
	if journal.ExternalID == nil || *journal.ExternalID != *n.ExternalID || run.ID != *n.ExternalID || run.Namespace != n.Namespace || run.Name != journal.RunName || run.Description != "loom:v1:"+n.InstanceID+":"+journal.RequestHash {
		return nil, apperr.New(apperr.CodeConflict, "KFP 运行身份不匹配")
	}
	var details struct {
		Tasks []struct {
			ID        string `json:"task_id"`
			Name      string `json:"display_name"`
			State     string `json:"state"`
			ParentID  string `json:"parent_task_id"`
			StartTime string `json:"start_time"`
			EndTime   string `json:"end_time"`
		} `json:"task_details"`
	}
	if len(run.Details) != 0 && json.Unmarshal(run.Details, &details) != nil {
		return nil, apperr.New(apperr.CodeUnavailable, "KFP 节点详情格式无效")
	}
	out := &PipelineRuntime{ExternalID: run.ID, State: run.State, CreatedAt: run.CreatedAt, FinishedAt: run.FinishedAt, Tasks: []PipelineRuntimeTask{}}
	for _, task := range details.Tasks {
		out.Tasks = append(out.Tasks, PipelineRuntimeTask{ID: task.ID, Name: task.Name, State: task.State, ParentID: task.ParentID, StartTime: task.StartTime, EndTime: task.EndTime})
	}
	// 外部读取期间可能撤权或变更实例；返回前重新授权并核对身份。
	fresh, err := s.PipelineRun(ctx, actor, groupID, id)
	if err != nil {
		return nil, err
	}
	if fresh.InstanceID != n.InstanceID || fresh.Revision != n.Revision || fresh.ExternalID == nil || *fresh.ExternalID != *n.ExternalID {
		return nil, apperr.New(apperr.CodeConflict, "运行已变化，请刷新")
	}
	return out, nil
}
