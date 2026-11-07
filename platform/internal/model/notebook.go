package model

import (
	"encoding/json"
	"time"
)

// Notebook 同时保存业务期望和执行层观测；请求受理不代表集群已经生效。
type Notebook struct {
	ID                  uint64          `gorm:"primaryKey;autoIncrement"` // 自增主键
	InstanceID          string          // Loom 实例标识，用于核对集群对象归属，防止接管同名旧对象
	Cluster             string          // 目标集群标识
	Namespace           string          // 所在 namespace，等于项目空间名
	GroupID             uint64          // 所属项目空间 ID
	Name                string          // Notebook 名，同时是 K8s CR 名和访问路径的一段
	UID                 *string         // 观测到的 Notebook CR UID，未确认时为空
	CreatedBy           *uint64         // 历史创建人，不随负责人转移而改变
	OwnerUserID         *uint64         `gorm:"->;-:migration"` // 当前负责人，只读列，由关联查询填充
	OwnerRevision       uint64          `gorm:"-"`              // 负责人版本，不落库
	WorkspaceID         uint64          `gorm:"-"`              // 关联的工作目录记录 ID，不落库
	WorkspaceInstanceID string          `gorm:"-"`              // 工作目录实例标识，不落库
	DisplayName         string          // 展示名，可修改
	Description         string          // 业务描述
	Image               string          // 已固定的实际镜像地址，由可信映射解析
	ImageID             string          // 业务侧镜像 ID，客户端只能提交这个
	ServerType          string          // jupyter / group-one / group-two，创建后固定
	CPUMilli            int64           // CPU 请求，单位毫核
	MemoryBytes         int64           // 内存请求，单位字节
	GPUCount            uint            // GPU 卡数
	WorkspacePVC        string          // 工作目录 PVC 名
	WorkspaceUID        *string         // 观测到的 PVC UID，未确认时为空
	WorkspaceOwnership  string          `gorm:"default:UNKNOWN"` // 目录归属：UNKNOWN / EXCLUSIVE / SHARED
	WorkspaceSizeBytes  int64           // 工作目录容量，单位字节
	IdleStopMinutes     uint            // 空闲自动停止分钟数，0 表示不自动停止
	RequestedSpec       json.RawMessage `gorm:"type:json"`        // 请求规格原始 JSON，供执行器复现
	ObservedSpec        json.RawMessage `gorm:"type:json"`        // 观测规格摘要，不含环境变量明文
	DesiredState        string          `gorm:"default:RUNNING"`  // 期望状态：RUNNING / STOPPED / DELETED
	Status              string          `gorm:"default:CREATING"` // 业务状态：CREATING/STARTING/RUNNING/STOPPING/STOPPED/UPDATING/DELETING/DELETED/FAILED
	StatusReason        *string         // 当前状态的失败或阻塞原因
	Revision            uint64          `gorm:"default:1"`       // 业务意图版本，每次写操作递增
	ObservedState       string          `gorm:"default:UNKNOWN"` // 观测状态，UNKNOWN 表示尚未观测到
	AppliedRevision     uint64          // 已实际应用到集群的版本，落后于 Revision 表示未生效
	LastOperationID     *string         // 最近一次操作单 ID，用于进度查询和重试
	ObservedAt          *time.Time      // 最近一次观测成功时间，超过两分钟视为过期
	LastActiveAt        *time.Time      // 最近活跃时间，供空闲回收判断
	StartedAt           *time.Time      // 最近一次开始运行时间
	StoppedAt           *time.Time      // 最近一次停止时间
	CreatedAt           time.Time       `gorm:"default:CURRENT_TIMESTAMP(3)"`                      // 记录创建时间，UTC
	UpdatedAt           time.Time       `gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"` // 记录更新时间，由代码显式维护
	// 普通指针映射已有列，不使用 gorm.DeletedAt 的隐式过滤。
	DeletedAt *time.Time // 软删除时间，非空表示业务已删除
}

func (Notebook) TableName() string { return "tb_notebook" }

type SyncCursor struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement"` // 自增主键
	Cluster         string     // 集群标识
	APIGroup        string     // API 组，例如 kubeflow.org
	APIVersion      string     // API 版本，例如 v1
	Resource        string     // 资源名，例如 notebooks
	Namespace       string     // 监听范围，空字符串表示集群级
	ResourceVersion *string    // 已处理到的 resourceVersion，不比较大小
	LastFullSyncAt  *time.Time // 最近一次全量 LIST 完成时间
	LastEventAt     *time.Time // 最近一次收到事件的时间
}

func (SyncCursor) TableName() string { return "tb_sync_cursor" }
