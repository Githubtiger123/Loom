-- 只读观测与执行操作单解耦。租约与下一次观测时间持久化，支持多副本/重启恢复。
ALTER TABLE tb_pipeline_run
  ADD COLUMN kfp_state VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN kfp_synced_at DATETIME(3) DEFAULT NULL,
  ADD COLUMN kfp_sync_attempted_at DATETIME(3) DEFAULT NULL,
  ADD COLUMN kfp_sync_reason VARCHAR(48) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN sync_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN sync_next_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  ADD COLUMN sync_lease_owner CHAR(36) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN sync_lease_until DATETIME(3) DEFAULT NULL,
  ADD COLUMN sync_failures INT UNSIGNED NOT NULL DEFAULT 0,
  ADD KEY idx_pipeline_sync_due (cluster, sync_enabled, deleted_at, sync_next_at, id);

-- 提交回执不确定也要观测确定性 ID；从未尝试提交的本地记录不占用到期索引。
UPDATE tb_pipeline_run n SET n.sync_enabled=TRUE
WHERE n.deleted_at IS NULL AND (n.external_id IS NOT NULL OR EXISTS (
  SELECT 1 FROM tb_pipeline_submission ps
  WHERE ps.instance_id=n.instance_id AND ps.attempted=TRUE
));

-- 原执行回执提供已确认的空间身份，不依赖原操作单仍处于运行状态。
ALTER TABLE tb_ml_execution ADD KEY idx_ml_execution_instance (instance_id, target_type);
