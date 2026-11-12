package model

import (
	"encoding/json"
	"time"
)

type PipelineTaskCache struct {
	ID               uint64 `gorm:"primaryKey"`
	GroupID          uint64
	PipelineID       uint64
	NamespaceUID     string
	Fingerprint      string
	SourceTaskID     uint64
	SourceInstanceID string
	ArgoNodeID       string
	Outputs          json.RawMessage `gorm:"type:json"`
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

func (PipelineTaskCache) TableName() string { return "tb_pipeline_task_cache" }
