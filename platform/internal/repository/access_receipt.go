package repository

import (
	"context"

	"gorm.io/gorm"
)

// FixedOwnerAccessReceipt 是供 EXISTS 使用的关联子查询，外层访问表必须使用别名 a。
// 只核验回执证明；调用方仍须检查固定 Owner 身份、访问状态、等级及 applied 版本。
// 先取同 desired_version 的最新操作，再检查其终态，不能回退到更早的合格回执。
func (s *Store) FixedOwnerAccessReceipt(ctx context.Context) *gorm.DB {
	latest := s.query(ctx, false).Table("tb_operation latest_access_receipt").Select("latest_access_receipt.id").
		Where("latest_access_receipt.action = ? AND latest_access_receipt.target_type = ?", "access.reconcile", "access").
		Where("latest_access_receipt.target_key = CONCAT(a.group_id,'/',a.user_id) AND latest_access_receipt.target_version = a.desired_version").
		Order("latest_access_receipt.id DESC").Limit(1)
	return s.query(ctx, false).Table("tb_operation owner_access_receipt").Select("1").
		Where("owner_access_receipt.id = (?)", latest).
		Where("owner_access_receipt.status = ? AND owner_access_receipt.step = ?", "FAILED", "EXTERNAL_ACCESS_REMAINS").
		Where("JSON_UNQUOTE(JSON_EXTRACT(owner_access_receipt.result,'$.warningCode')) = ?", "INFRASTRUCTURE_OWNER_ACCESS_RETAINED").
		Where("JSON_TYPE(JSON_EXTRACT(owner_access_receipt.result,'$.infrastructureOwnerOnly')) = 'BOOLEAN'").
		Where("JSON_UNQUOTE(JSON_EXTRACT(owner_access_receipt.result,'$.infrastructureOwnerOnly')) = 'true'")
}
