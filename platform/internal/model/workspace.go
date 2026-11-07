package model

import "time"

// Workspace 是数据资产登记；Notebook 删除不删除此记录或默认解除保留保护。
type Workspace struct {
	ID            uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	InstanceID    string     `json:"instanceId"`
	GroupID       uint64     `json:"groupId"`
	Cluster       string     `json:"cluster"`
	Namespace     string     `json:"namespace"`
	PVCName       string     `json:"pvcName"`
	PVCUID        *string    `json:"pvcUID"`
	OwnerUserID   *uint64    `json:"ownerUserId"`
	CapacityBytes int64      `json:"capacityBytes"`
	UsedBytes     *int64     `json:"usedBytes"`
	Ownership     string     `json:"ownership"`
	Retained      bool       `json:"retained"`
	Status        string     `json:"status"`
	Revision      uint64     `json:"revision" gorm:"default:1"`
	ObservedAt    *time.Time `json:"observedAt"`
	DeletedAt     *time.Time `json:"deletedAt"`
	CreatedAt     time.Time  `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt     time.Time  `json:"updatedAt" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
}

func (Workspace) TableName() string { return "tb_workspace" }

// NotebookOwner 与历史 created_by 分开；未登记的旧实例回退到 created_by。
type NotebookOwner struct {
	NotebookID  uint64 `gorm:"primaryKey;autoIncrement:false"`
	OwnerUserID uint64
	Revision    uint64
}

func (NotebookOwner) TableName() string { return "tb_notebook_owner" }

type WorkspaceAttachment struct {
	WorkspaceID uint64    `gorm:"primaryKey;autoIncrement:false"`
	NotebookID  uint64    `gorm:"primaryKey;autoIncrement:false"`
	CreatedAt   time.Time `gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (WorkspaceAttachment) TableName() string { return "tb_workspace_attachment" }
