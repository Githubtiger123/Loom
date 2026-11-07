package model

import "time"

// 不按 TTL 释放额度。失败/取消也必须确认原生资源不存在才能释放。
type QuotaReservation struct {
	ID               string     `json:"id" gorm:"primaryKey"`
	GroupID          uint64     `json:"groupId"`
	NotebookID       uint64     `json:"notebookId"`
	NotebookRevision uint64     `json:"notebookRevision"`
	OperationID      string     `json:"operationId"`
	CPUMilli         int64      `json:"cpuMilli"`
	MemoryBytes      int64      `json:"memoryBytes"`
	GPU              int64      `json:"gpu"`
	Status           string     `json:"status"`
	AccountedAt      *time.Time `json:"accountedAt"`
	CreatedAt        time.Time  `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (QuotaReservation) TableName() string { return "tb_quota_reservation" }
