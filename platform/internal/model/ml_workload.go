package model

import (
	"encoding/json"
	"time"
)

// MLIdentity is shared ownership metadata, not a universal workload table.
// Nil CreatedBy remains unowned and requires the independent *.read.any grant.
type MLIdentity struct {
	ID        uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID   uint64     `json:"groupId"`
	Name      string     `json:"name"`
	CreatedBy *uint64    `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt time.Time  `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
	DeletedAt *time.Time `json:"deletedAt"`
}

type Pipeline struct {
	MLIdentity
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Revision    uint64 `json:"revision"`
}

func (Pipeline) TableName() string { return "tb_pipeline" }

// 版本可原地编辑；每个 revision 和 Run 已提交的快照分别保留。
type PipelineVersion struct {
	ID         uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID    uint64          `json:"groupId"`
	PipelineID uint64          `json:"pipelineId"`
	Name       string          `json:"name"`
	Revision   uint64          `json:"revision" gorm:"default:1"`
	Spec       json.RawMessage `json:"spec" gorm:"type:json"`
	SpecHash   string          `json:"specHash"`
	CreatedBy  *uint64         `json:"createdBy"`
	CreatedAt  time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt  time.Time       `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
}

func (PipelineVersion) TableName() string { return "tb_pipeline_version" }

// MLExecutionState separates persisted intent from externally verified state.
// ExternalID is an opaque KFP run ID or Kubernetes UID, never a user-supplied URL.
type MLExecutionState struct {
	InstanceID      string          `json:"instanceId"`
	Cluster         string          `json:"cluster"`
	Namespace       string          `json:"namespace"`
	ExternalID      *string         `json:"externalId"`
	Revision        uint64          `json:"revision"`
	AppliedRevision uint64          `json:"appliedRevision"`
	DesiredState    string          `json:"desiredState"`
	ObservedState   string          `json:"observedState"`
	Status          string          `json:"status"`
	StatusReason    *string         `json:"statusReason"`
	RequestedSpec   json.RawMessage `json:"spec" gorm:"type:json"`
	LastOperationID *string         `json:"operationId"`
	ObservedAt      *time.Time      `json:"observedAt"`
}

type PipelineRun struct {
	MLIdentity
	MLExecutionState
	PipelineID       uint64          `json:"pipelineId"`
	VersionID        uint64          `json:"versionId"`
	PendingSpec      json.RawMessage `json:"pendingSpec,omitempty" gorm:"type:json"`
	ExecutionCleaned bool            `json:"executionCleaned"`
	// 最近一次已核验的 KFP 原始状态。失败时保留最后成功结果，并单独暴露新鲜度。
	KFPState           *string    `json:"kfpState"`
	KFPSyncedAt        *time.Time `json:"kfpSyncedAt"`
	KFPSyncAttemptedAt *time.Time `json:"kfpSyncAttemptedAt"`
	KFPSyncReason      *string    `json:"kfpSyncReason"`
}

func (PipelineRun) TableName() string { return "tb_pipeline_run" }

type PipelineVersionRevision struct {
	VersionID uint64          `json:"versionId" gorm:"primaryKey;autoIncrement:false"`
	Revision  uint64          `json:"revision" gorm:"primaryKey;autoIncrement:false"`
	GroupID   uint64          `json:"groupId"`
	Spec      json.RawMessage `json:"spec" gorm:"type:json"`
	SpecHash  string          `json:"specHash"`
	CreatedBy *uint64         `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (PipelineVersionRevision) TableName() string { return "tb_pipeline_version_revision" }

type PipelineRunAttempt struct {
	InstanceID string          `json:"instanceId" gorm:"primaryKey"`
	RunID      uint64          `json:"runId"`
	Revision   uint64          `json:"revision"`
	GroupID    uint64          `json:"groupId"`
	Snapshot   json.RawMessage `json:"snapshot" gorm:"type:json"`
	CreatedAt  time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (PipelineRunAttempt) TableName() string { return "tb_pipeline_run_attempt" }

type TrainerJob struct {
	MLIdentity
	MLExecutionState
}

func (TrainerJob) TableName() string { return "tb_trainer_job" }

type KServeService struct {
	MLIdentity
	MLExecutionState
	ConfigRevisionID         uint64     `json:"configRevisionId"`
	KServeGeneration         int64      `json:"kserveGeneration" gorm:"column:kserve_generation"`
	KServeObservedGeneration int64      `json:"kserveObservedGeneration" gorm:"column:kserve_observed_generation"`
	KServeResourceVersion    *string    `json:"kserveResourceVersion" gorm:"column:kserve_resource_version"`
	KServeReady              bool       `json:"kserveReady" gorm:"column:kserve_ready"`
	KServeURL                *string    `json:"kserveUrl" gorm:"column:kserve_url"`
	KServeAddress            *string    `json:"kserveAddress" gorm:"column:kserve_address"`
	KServeReason             *string    `json:"kserveReason" gorm:"column:kserve_reason"`
	KServeSyncedAt           *time.Time `json:"kserveSyncedAt" gorm:"column:kserve_synced_at"`
	KServeSyncAttemptedAt    *time.Time `json:"kserveSyncAttemptedAt" gorm:"column:kserve_sync_attempted_at"`
	SyncEnabled              bool       `json:"-" gorm:"default:false"`
	SyncNextAt               time.Time  `json:"-" gorm:"default:CURRENT_TIMESTAMP(3)"`
	SyncLeaseOwner           *string    `json:"-"`
	SyncLeaseUntil           *time.Time `json:"-"`
	SyncFailures             uint32     `json:"-" gorm:"default:0"`
}

func (KServeService) TableName() string { return "tb_kserve_service" }

type KServeRevision struct {
	ID        uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID   uint64          `json:"groupId"`
	ServiceID uint64          `json:"serviceId"`
	Revision  uint64          `json:"revision"` // resource revision when this config was created
	Spec      json.RawMessage `json:"spec" gorm:"type:json"`
	SpecHash  string          `json:"specHash"`
	CreatedBy *uint64         `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (KServeRevision) TableName() string { return "tb_kserve_revision" }

// MLWorkloadHistory is append-only. It stores intents and sanitized observations,
// and preserves stop/delete history after logical deletion of a workload.
type MLWorkloadHistory struct {
	ID            uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID       uint64          `json:"groupId"`
	TargetType    string          `json:"targetType"`
	TargetID      uint64          `json:"targetId"`
	Revision      uint64          `json:"revision"`
	OperationID   string          `json:"operationId"`
	Event         string          `json:"event"`
	Action        string          `json:"action"`
	ActorID       uint64          `json:"actorId"`
	DesiredState  string          `json:"desiredState"`
	ObservedState string          `json:"observedState"`
	Status        string          `json:"status"`
	Spec          json.RawMessage `json:"spec" gorm:"type:json"`
	CreatedAt     time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (MLWorkloadHistory) TableName() string { return "tb_ml_workload_history" }
