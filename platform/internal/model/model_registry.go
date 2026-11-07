package model

import "time"

// RegisteredModel is the group-owned logical model. Binary model data remains
// in object storage; Loom only persists searchable business metadata.
type RegisteredModel struct {
	ID          uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID     uint64     `json:"groupId"`
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName"`
	Description string     `json:"description"`
	CreatedBy   *uint64    `json:"createdBy"`
	Revision    uint64     `json:"revision" gorm:"default:1"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt   time.Time  `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
	DeletedAt   *time.Time `json:"deletedAt"`
}

func (RegisteredModel) TableName() string { return "tb_model" }

type ModelVersion struct {
	ID                  uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID             uint64     `json:"groupId"`
	ModelID             uint64     `json:"modelId"`
	Name                string     `json:"name"`
	Description         string     `json:"description"`
	SourcePipelineRunID *uint64    `json:"sourcePipelineRunId"`
	CreatedBy           *uint64    `json:"createdBy"`
	Revision            uint64     `json:"revision" gorm:"default:1"`
	Status              string     `json:"status"`
	CreatedAt           time.Time  `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt           time.Time  `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
	DeletedAt           *time.Time `json:"deletedAt"`
}

func (ModelVersion) TableName() string { return "tb_model_version" }

// ModelArtifact is immutable storage metadata. Replacing a binary or URI means
// creating a new artifact, so every KServe revision can retain an exact source.
type ModelArtifact struct {
	ID                 uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID            uint64     `json:"groupId"`
	ModelID            uint64     `json:"modelId"`
	VersionID          uint64     `json:"versionId"`
	Name               string     `json:"name"`
	URI                string     `json:"uri"`
	ModelFormat        string     `json:"modelFormat"`
	ModelFormatVersion string     `json:"modelFormatVersion"`
	Digest             string     `json:"digest"`
	SizeBytes          *uint64    `json:"sizeBytes"`
	StorageType        string     `json:"storageType"`
	CreatedBy          *uint64    `json:"createdBy"`
	Revision           uint64     `json:"revision" gorm:"default:1"`
	Status             string     `json:"status"`
	CreatedAt          time.Time  `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt          time.Time  `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
	DeletedAt          *time.Time `json:"deletedAt"`
}

func (ModelArtifact) TableName() string { return "tb_model_artifact" }
