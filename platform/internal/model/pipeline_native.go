package model

import (
	"encoding/json"
	"time"
)

const (
	PipelineEngineKFP        = "KFP"
	PipelineEngineNativeArgo = "NATIVE_ARGO"
)

// PipelineSubmission records durable intent before external submission.
// ExternalID retains its historical KFP meaning. WorkflowUID is a separate,
// write-once binding for the native engine, not a replacement KFP run ID.
type PipelineSubmission struct {
	InstanceID   string `gorm:"primaryKey"`
	RequestHash  string
	RunName      string
	Attempted    bool
	ExternalID   *string
	Engine       string `gorm:"default:KFP"`
	ClusterID    *string
	Namespace    *string
	NamespaceUID *string   `gorm:"column:namespace_uid"`
	WorkflowUID  *string   `gorm:"column:workflow_uid"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
}

func (PipelineSubmission) TableName() string { return "tb_pipeline_submission" }

// PipelineTaskExecution is a dynamic node instance, including one retry or
// loop iteration. A cached/logical node need not have an Argo node or Pod.
// These persistence models are not public response DTOs: IO, storage references
// and runtime protocol checkpoints must pass authorization and redaction first.
type PipelineTaskExecution struct {
	ID                 uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID            uint64          `json:"groupId"`
	RunID              uint64          `json:"runId"`
	InstanceID         string          `json:"instanceId"`
	NodeKey            string          `json:"nodeKey"`
	DefinitionPath     string          `json:"definitionPath"`
	ParentID           *uint64         `json:"parentId,omitempty"`
	IterationPath      json.RawMessage `json:"-" gorm:"type:json"`
	RetryIndex         uint32          `json:"retryIndex"`
	NodeKind           string          `json:"nodeKind"`
	ArgoNodeID         *string         `json:"-" gorm:"column:argo_node_id"`
	PodName            *string         `json:"-"`
	PodUID             *string         `json:"-" gorm:"column:pod_uid"`
	Phase              string          `json:"phase" gorm:"default:PENDING"`
	Reason             *string         `json:"reason,omitempty"`
	Inputs             json.RawMessage `json:"-" gorm:"type:json"`
	Outputs            json.RawMessage `json:"-" gorm:"type:json"`
	CacheSourceTaskID  *uint64         `json:"-"`
	RuntimeState       json.RawMessage `json:"-" gorm:"type:json"`
	WriterEpoch        uint64          `json:"-"`
	ReportSequence     uint64          `json:"-"`
	ReportHash         *string         `json:"-"`
	ObservationVersion uint64          `json:"-"`
	StartedAt          *time.Time      `json:"startedAt,omitempty"`
	FinishedAt         *time.Time      `json:"finishedAt,omitempty"`
	CreatedAt          time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt          time.Time       `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
}

func (PipelineTaskExecution) TableName() string { return "tb_pipeline_task_execution" }

// PipelineArtifact has stable identity independently of its URI. Published
// identity/content/type/metadata are immutable. Availability may subsequently
// change; object-store data and SQL rows are not a distributed transaction.
type PipelineArtifact struct {
	ID                 uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID            uint64          `json:"groupId"`
	ProducerInstanceID *string         `json:"producerInstanceId,omitempty"`
	ProducerTaskID     *uint64         `json:"producerTaskId,omitempty"`
	RegistrationKey    string          `json:"-"`
	TypeName           string          `json:"typeName"`
	TypeSchemaVersion  string          `json:"typeSchemaVersion"`
	TypeSchema         json.RawMessage `json:"-" gorm:"type:json"`
	TypeSchemaHash     string          `json:"-"`
	URI                *string         `json:"-" gorm:"column:uri"`
	ObjectRef          json.RawMessage `json:"-" gorm:"type:json"`
	Metadata           json.RawMessage `json:"-" gorm:"type:json"`
	Visualizations     json.RawMessage `json:"-" gorm:"type:json"`
	Provenance         json.RawMessage `json:"-" gorm:"type:json"`
	State              string          `json:"state" gorm:"default:STAGING"`
	CreatedAt          time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	PublishedAt        *time.Time      `json:"publishedAt,omitempty"`
	UpdatedAt          time.Time       `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
}

func (PipelineArtifact) TableName() string { return "tb_pipeline_artifact" }
