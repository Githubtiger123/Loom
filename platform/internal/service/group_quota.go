package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// QuotaReader 只能读取已验证空间身份的 quota。ReadStartedAt 必须是发起读取前
// 的时刻；用于将快照读取期间被 ACCOUNTED 的申请仍计作未反映占用。
type QuotaReader interface {
	ReadGroupQuota(context.Context, GroupLifecycleCommand) (*QuotaSnapshot, error)
}
type QuotaSnapshot struct {
	GroupID       uint64             `json:"groupId"`
	GroupRevision uint64             `json:"groupRevision"`
	NamespaceUID  string             `json:"namespaceUID"`
	Hard          dto.ResourceBudget `json:"hard"`
	Used          dto.ResourceBudget `json:"used"`
	ReadStartedAt time.Time          `json:"readStartedAt"`
}
type GroupQuotaView struct {
	Requested json.RawMessage    `json:"requested"`
	Observed  json.RawMessage    `json:"observed"`
	Snapshot  *QuotaSnapshot     `json:"snapshot"`
	Reserved  dto.ResourceBudget `json:"reserved"`
	Remaining dto.ResourceBudget `json:"remaining"`
}

// 配置期注入；禁止请求处理中替换，也不能在数据库事务/锁内读取。
func (s *Service) SetQuotaReader(reader QuotaReader) { s.QuotaReader = reader }

func (s *Service) quotaSnapshot(ctx context.Context, groupID uint64) (*QuotaSnapshot, error) {
	if s.QuotaReader == nil {
		return nil, apperr.New(apperr.CodeConflict, "集群额度读取尚未接入，不能确认运行预算")
	}
	g, err := s.Store.Group(ctx, groupID, false)
	if err != nil {
		return nil, domainError(err)
	}
	started := time.Now().UTC()
	snap, err := s.QuotaReader.ReadGroupQuota(ctx, groupLifecycleCommand(g))
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, apperr.New(apperr.CodeConflict, "空间实际额度尚未确认")
	}
	// 使用本地调用开始时间，不能信任适配器把旧缓存伪装成新快照。
	if snap.ReadStartedAt.Before(started) || snap.ReadStartedAt.After(time.Now().UTC()) {
		return nil, apperr.New(apperr.CodeConflict, "额度快照时间无效或使用了缓存")
	}
	if err := validateQuotaSnapshot(g, snap); err != nil {
		return nil, err
	}
	return snap, nil
}
func validateQuotaSnapshot(g *model.Group, snap *QuotaSnapshot) error {
	if snap == nil || g.NamespaceUID == nil || snap.NamespaceUID != *g.NamespaceUID || snap.GroupID != g.ID || snap.GroupRevision != g.Revision ||
		!snap.Hard.Valid() || !snap.Used.Valid() || snap.ReadStartedAt.IsZero() || time.Since(snap.ReadStartedAt) < 0 || time.Since(snap.ReadStartedAt) > 30*time.Second {
		return apperr.New(apperr.CodeConflict, "额度快照已过期或空间身份/版本变化，请重新预检")
	}
	return nil
}
func addBudget(a, b dto.ResourceBudget) (dto.ResourceBudget, error) {
	if !a.Valid() || !b.Valid() || a.CPUMilli > math.MaxInt64-b.CPUMilli || a.MemoryBytes > math.MaxInt64-b.MemoryBytes || a.GPU > math.MaxInt64-b.GPU {
		return dto.ResourceBudget{}, apperr.New(apperr.CodeConflict, "额度计算超出范围")
	}
	return dto.ResourceBudget{CPUMilli: a.CPUMilli + b.CPUMilli, MemoryBytes: a.MemoryBytes + b.MemoryBytes, GPU: a.GPU + b.GPU}, nil
}
func remainingBudget(hard, used dto.ResourceBudget) dto.ResourceBudget {
	return dto.ResourceBudget{CPUMilli: max(int64(0), hard.CPUMilli-used.CPUMilli), MemoryBytes: max(int64(0), hard.MemoryBytes-used.MemoryBytes), GPU: max(int64(0), hard.GPU-used.GPU)}
}
func fitsBudget(hard, need dto.ResourceBudget) bool {
	return need.CPUMilli <= hard.CPUMilli && need.MemoryBytes <= hard.MemoryBytes && need.GPU <= hard.GPU
}

func (s *Service) GroupQuota(ctx context.Context, actor, groupID uint64) (*GroupQuotaView, error) {
	if err := s.Require(ctx, actor, groupID, "group.quota.read"); err != nil {
		return nil, err
	}
	snap, err := s.quotaSnapshot(ctx, groupID)
	if err != nil {
		return nil, err
	}
	var view *GroupQuotaView
	err = repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		g, err := st.Group(ctx, groupID, true)
		if err != nil {
			return err
		}
		if err := validateQuotaSnapshot(g, snap); err != nil {
			return err
		}
		reserved, err := st.UnreflectedBudget(ctx, groupID, snap.ReadStartedAt)
		if err != nil {
			return err
		}
		used, err := addBudget(snap.Used, reserved)
		if err != nil {
			return err
		}
		view = &GroupQuotaView{Requested: g.QuotaRequested, Observed: g.QuotaObserved, Snapshot: snap, Reserved: reserved, Remaining: remainingBudget(snap.Hard, used)}
		return nil
	})
	return view, domainError(err)
}

func (s *Service) PrepareNotebookBudget(ctx context.Context, actor, groupID uint64, permissions ...string) (*QuotaSnapshot, error) {
	permission := "notebook.create"
	if len(permissions) > 1 {
		return nil, apperr.New(apperr.CodeInvalid, "只能指定一个预算预检权限")
	}
	if len(permissions) == 1 {
		permission = permissions[0]
	}
	if permission != "notebook.create" && permission != "notebook.start" && permission != "notebook.update" {
		return nil, apperr.New(apperr.CodeInvalid, "预算预检权限无效")
	}
	if err := s.Require(ctx, actor, groupID, permission); err != nil {
		return nil, err
	}
	return s.quotaSnapshot(ctx, groupID)
}

func (s *Service) prepareNotebookMutationBudget(ctx context.Context, actor, groupID uint64, key, permission string) (*QuotaSnapshot, error) {
	if !validator.IsIdempotencyKey(key) {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供有效的 Idempotency-Key")
	}
	if err := s.Require(ctx, actor, groupID, permission); err != nil {
		return nil, err
	}
	_, _, err := s.Store.RequestOperation(ctx, actor, key)
	if err == nil {
		return nil, nil
	} // mutate 仍会核对请求 hash 和当前对象权限。
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return s.quotaSnapshot(ctx, groupID)
}

// ReserveNotebookBudget 必须在 Notebook create/start/update 的 mutate 中调用。
// n 的 ID/revision 和资源规格应已确定，operationID 为同一持久操作单。
// 本方法只访问数据库；重复操作原值重放，不重复占用；新版本全量占用直至旧版确认释放。
func (s *Service) ReserveNotebookBudget(ctx context.Context, st *repository.Store, snap *QuotaSnapshot, n *model.Notebook, operationID string) error {
	g, err := st.Group(ctx, n.GroupID, true)
	if err != nil {
		return err
	}
	if g.Status != "ACTIVE" {
		return apperr.New(apperr.CodeConflict, "空间未启用，不能申请运行预算")
	}
	if err := validateQuotaSnapshot(g, snap); err != nil {
		return err
	}
	if n.ID == 0 || n.Revision == 0 || operationID == "" || n.CPUMilli <= 0 || n.MemoryBytes <= 0 {
		return apperr.New(apperr.CodeInvalid, "预算必须绑定已分配 ID、版本、操作单和有效资源规格")
	}
	owner, err := st.CurrentNotebookOwner(ctx, n)
	if err != nil {
		return err
	}
	if owner == nil {
		return apperr.New(apperr.CodeConflict, "实例缺少当前负责人")
	}
	if err := requireActiveMember(ctx, st, n.GroupID, *owner); err != nil {
		return err
	}
	if err := validateNotebookDataOwner(ctx, st, n, *owner); err != nil {
		return err
	}
	var existing model.QuotaReservation
	err = st.DB.WithContext(ctx).Where("operation_id=?", operationID).First(&existing).Error
	if err == nil {
		if existing.GroupID != n.GroupID || existing.NotebookID != n.ID || existing.NotebookRevision != n.Revision || existing.CPUMilli != n.CPUMilli || existing.MemoryBytes != n.MemoryBytes || existing.GPU != int64(n.GPUCount) || existing.Status == "RELEASED" {
			return apperr.New(apperr.CodeConflict, "预算幂等参数冲突或已释放")
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	reserved, err := st.UnreflectedBudget(ctx, n.GroupID, snap.ReadStartedAt)
	if err != nil {
		return err
	}
	used, err := addBudget(snap.Used, reserved)
	if err != nil {
		return err
	}
	need := dto.ResourceBudget{CPUMilli: n.CPUMilli, MemoryBytes: n.MemoryBytes, GPU: int64(n.GPUCount)}
	used, err = addBudget(used, need)
	if err != nil {
		return err
	}
	// 请求的较小 quota 立即生效为受理上限；提额在实际 hard 生效前不能借用。
	var requested dto.Quota
	if err := json.Unmarshal(g.QuotaRequested, &requested); err != nil {
		return err
	}
	cpu, cpuOK := validator.QuantityUnits(requested.CPU, 1000)
	mem, memOK := validator.QuantityUnits(requested.Memory, 1)
	if !cpuOK || !memOK || requested.GPU < 0 {
		return apperr.New(apperr.CodeConflict, "空间业务额度未配置或格式无效")
	}
	if !fitsBudget(snap.Hard, used) || !fitsBudget(dto.ResourceBudget{CPUMilli: cpu, MemoryBytes: mem, GPU: int64(requested.GPU)}, used) {
		return apperr.New(apperr.CodeConflict, "空间剩余额度不足（含并发待执行占用）")
	}
	return st.DB.WithContext(ctx).Create(&model.QuotaReservation{ID: uuid.NewString(), GroupID: n.GroupID, NotebookID: n.ID, NotebookRevision: n.Revision, OperationID: operationID, CPUMilli: need.CPUMilli, MemoryBytes: need.MemoryBytes, GPU: need.GPU, Status: "HELD"}).Error
}

func (s *Service) UpdateGroupQuota(ctx context.Context, actor, groupID uint64, key string, in dto.UpdateGroupQuotaReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if err := s.Require(ctx, actor, 0, "group.quota.update"); err != nil {
		return nil, err
	}
	if !validator.IsIdempotencyKey(key) {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供有效的 Idempotency-Key")
	}
	// 幂等重放仍走 mutate 的 hash/权限检查，但不依赖集群在线。
	var snap *QuotaSnapshot
	_, _, requestErr := s.Store.RequestOperation(ctx, actor, key)
	if errors.Is(requestErr, gorm.ErrRecordNotFound) {
		var err error
		snap, err = s.quotaSnapshot(ctx, groupID)
		if err != nil {
			return nil, err
		}
	} else if requestErr != nil {
		return nil, requestErr
	}
	return s.mutate(ctx, actor, 0, key, "group.quota.update", "group.quota.update", "group", fmt.Sprint(groupID), in, func(st *repository.Store, o *model.Operation) error {
		g, err := writableLifecycleGroup(ctx, st, groupID, in.Revision)
		if err != nil {
			return err
		}
		if g.Status != "ACTIVE" && g.Status != "DISABLED" {
			return apperr.New(apperr.CodeConflict, "当前空间状态不可调整额度")
		}
		if err := validateQuotaSnapshot(g, snap); err != nil {
			return err
		}
		reserved, err := st.UnreflectedBudget(ctx, groupID, snap.ReadStartedAt)
		if err != nil {
			return err
		}
		used, err := addBudget(snap.Used, reserved)
		if err != nil {
			return err
		}
		cpu, _ := validator.QuantityUnits(in.Quota.CPU, 1000)
		mem, _ := validator.QuantityUnits(in.Quota.Memory, 1)
		if !fitsBudget(dto.ResourceBudget{CPUMilli: cpu, MemoryBytes: mem, GPU: int64(in.Quota.GPU)}, used) {
			return apperr.New(apperr.CodeConflict, "不能将额度降至实际用量与待执行占用之下")
		}
		quota, err := json.Marshal(in.Quota)
		if err != nil {
			return err
		}
		g.Revision++
		if err := st.DB.WithContext(ctx).Model(&model.Group{}).Where("id=?", groupID).Updates(map[string]any{"quota_requested": string(quota), "revision": g.Revision}).Error; err != nil {
			return err
		}
		command := groupLifecycleCommand(g)
		command.Quota = &in.Quota
		o.Payload, err = json.Marshal(command)
		if err != nil {
			return err
		}
		o.Status, o.Step, o.Cluster, o.TargetVersion = "PENDING", "WAIT_EXECUTOR", g.Cluster, g.Revision
		// groupID=0 只用于全局权限检查，审计仍归属真实空间。
		if err := audit(ctx, st, actor, groupID, "group.quota.requested", "group", fmt.Sprint(groupID), o.ID, "ACCEPTED"); err != nil {
			return err
		}
		return result(o, map[string]any{"groupId": groupID, "revision": g.Revision, "quota": in.Quota, "status": "PENDING", "reason": in.Reason})
	})
}
