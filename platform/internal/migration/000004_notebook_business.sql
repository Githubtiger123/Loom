ALTER TABLE tb_notebook
  ADD COLUMN observed_state VARCHAR(32) NOT NULL DEFAULT 'UNKNOWN',
  ADD COLUMN applied_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  ADD COLUMN last_operation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN workspace_ownership VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN',
  ADD CONSTRAINT ck_notebook_workspace_ownership CHECK (workspace_ownership IN ('UNKNOWN','EXCLUSIVE','SHARED')),
  ADD COLUMN active_name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin
    GENERATED ALWAYS AS (IF(deleted_at IS NULL, name, NULL)) STORED,
  DROP INDEX uk_notebook_name,
  ADD UNIQUE KEY uk_notebook_active_name (cluster, namespace, active_name),
  ADD UNIQUE KEY uk_notebook_uid (cluster, uid),
  ADD KEY idx_notebook_idle (status, last_active_at),
  ADD KEY idx_notebook_operation (last_operation_id);
