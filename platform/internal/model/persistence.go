package model

import (
	"encoding/json"
	"time"
)

// 显式映射既有表，不嵌入 gorm.Model，不引入隐式软删除或自动建表。
type RolePermission struct {
	RoleID       uint64 `gorm:"primaryKey;autoIncrement:false"`
	PermissionID uint64 `gorm:"primaryKey;autoIncrement:false"`
}

func (RolePermission) TableName() string { return "tb_role_permission" }

type GroupAccess struct {
	UpdatedAt      time.Time `gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
	GroupID        uint64    `gorm:"primaryKey;autoIncrement:false"`
	UserID         uint64    `gorm:"primaryKey;autoIncrement:false"`
	DesiredLevel   string    `gorm:"default:none"`
	ObservedLevel  string    `gorm:"default:unknown"`
	DesiredVersion uint64    `gorm:"default:1"`
	AppliedVersion uint64
	Status         string          `gorm:"default:PENDING"`
	Objects        json.RawMessage `gorm:"column:observed_objects;type:json"`
	ObservedAt     *time.Time
	LastError      *string
}

func (GroupAccess) TableName() string { return "tb_group_access" }

type Audit struct {
	ID          uint64          `json:"id"`
	OperationID *string         `json:"operationId"`
	ActorID     *uint64         `json:"actorId"`
	ActorName   string          `json:"actorName"`
	GroupID     *uint64         `json:"groupId"`
	Action      string          `json:"action"`
	TargetType  string          `json:"targetType"`
	TargetID    string          `json:"targetId"`
	Outcome     string          `json:"outcome"`
	BeforeData  json.RawMessage `json:"-" gorm:"type:json"`
	AfterData   json.RawMessage `json:"-" gorm:"type:json"`
	Reason      *string         `json:"-"`
	SourceIP    *string         `json:"-"`
	CreatedAt   time.Time       `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (Audit) TableName() string { return "tb_audit" }

type SchemaMigration struct {
	Name      string `gorm:"primaryKey"`
	Checksum  string
	Dirty     bool
	AppliedAt time.Time `gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (SchemaMigration) TableName() string { return "tb_schema_migration" }
