package model

import (
	"encoding/json"
	"time"
)

// PipelineRuntimeObject stores engine protocol objects, not user-facing models.
// IDs are globally allocated so cached source execution IDs stay unambiguous.
type PipelineRuntimeObject struct {
	ID           int64 `gorm:"primaryKey;autoIncrement"`
	GroupID      uint64
	RunID        uint64
	InstanceID   string
	Kind         string
	ObjectKey    string
	ParentID     int64
	WriterPodUID string          `gorm:"column:writer_pod_uid"`
	Value        json.RawMessage `gorm:"type:json"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (PipelineRuntimeObject) TableName() string { return "tb_pipeline_runtime_object" }

type PipelineRuntimeReceipt struct {
	InstanceID string          `gorm:"primaryKey"`
	PodUID     string          `gorm:"primaryKey;column:pod_uid"`
	Operation  string          `gorm:"primaryKey"`
	RequestID  string          `gorm:"primaryKey"`
	Response   json.RawMessage `gorm:"type:json"`
	CreatedAt  time.Time
}

func (PipelineRuntimeReceipt) TableName() string { return "tb_pipeline_runtime_receipt" }

// Cache entries become visible only after terminal successful observation, not
// when the launcher records its candidate before deferred output publication.
type PipelineRuntimeCache struct {
	ID            int64 `gorm:"primaryKey;autoIncrement"`
	GroupID       uint64
	CacheKey      string
	RunID         uint64
	InstanceID    string
	ExecutionID   int64
	ExecutionHash string
	Inventory     json.RawMessage `gorm:"type:json"`
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

func (PipelineRuntimeCache) TableName() string { return "tb_pipeline_runtime_cache" }
