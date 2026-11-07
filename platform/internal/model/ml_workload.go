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

// PipelineVersion has no update/delete API. Spec and digest are frozen together.
type PipelineVersion struct {
	ID         uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID    uint64          `json:"groupId"`
	PipelineID uint64          `json:"pipelineId"`
	Name       string          `json:"name"`
	Spec       json.RawMessage `json:"spec" gorm:"type:json"`
	SpecHash   string          `json:"specHash"`
	CreatedBy  *uint64         `json:"createdBy"`
	CreatedAt  time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
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
	PipelineID uint64 `json:"pipelineId"`
	VersionID  uint64 `json:"versionId"`
}

func (PipelineRun) TableName() string { return "tb_pipeline_run" }

type TrainerJob struct {
	MLIdentity
	MLExecutionState
}

func (TrainerJob) TableName() string { return "tb_trainer_job" }

type KServeService struct {
	MLIdentity
	MLExecutionState
	ConfigRevisionID uint64 `json:"configRevisionId"`
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
