-- KServe CR 的受控身份、地址和状态投影。执行操作与长期观测解耦。
ALTER TABLE tb_kserve_service
  ADD COLUMN kserve_generation BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN kserve_observed_generation BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN kserve_resource_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN kserve_ready BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN kserve_url VARCHAR(2048) DEFAULT NULL,
  ADD COLUMN kserve_address VARCHAR(2048) DEFAULT NULL,
  ADD COLUMN kserve_reason VARCHAR(1024) DEFAULT NULL,
  ADD COLUMN kserve_synced_at DATETIME(3) DEFAULT NULL,
  ADD COLUMN kserve_sync_attempted_at DATETIME(3) DEFAULT NULL,
  ADD COLUMN sync_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN sync_next_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  ADD COLUMN sync_lease_owner CHAR(36) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN sync_lease_until DATETIME(3) DEFAULT NULL,
  ADD COLUMN sync_failures INT UNSIGNED NOT NULL DEFAULT 0,
  ADD KEY idx_kserve_sync_due (cluster, sync_enabled, deleted_at, sync_next_at, id),
  ADD CONSTRAINT ck_kserve_generation CHECK (kserve_generation >= 0 AND kserve_observed_generation >= 0);

UPDATE tb_kserve_service
SET sync_enabled=TRUE, sync_next_at=UTC_TIMESTAMP(3)
WHERE deleted_at IS NULL AND desired_state='SERVING';
