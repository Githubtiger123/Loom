-- 不自动接管旧 PVC。观察器核对管理标记/UID 后显式登记；旧 Notebook 的 PVC
-- 字段仍纳入所有删除检查，不能因为未登记 workspace 就认为空间为空。
ALTER TABLE tb_group DROP CHECK ck_group_status,
  ADD CONSTRAINT ck_group_status CHECK (status IN
    ('CREATING','ACTIVE','DEGRADED','FAILED','DISABLING','DISABLED','RESTORING','DELETING','DELETED'));

CREATE TABLE tb_workspace (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  group_id BIGINT UNSIGNED NOT NULL,
  cluster VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  namespace VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  pvc_name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  pvc_uid VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  owner_user_id BIGINT UNSIGNED DEFAULT NULL,
  capacity_bytes BIGINT NOT NULL,
  used_bytes BIGINT DEFAULT NULL,
  ownership VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN',
  retained TINYINT UNSIGNED NOT NULL DEFAULT 1,
  status VARCHAR(24) NOT NULL DEFAULT 'UNKNOWN',
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  observed_at DATETIME(3) DEFAULT NULL,
  deleted_at DATETIME(3) DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  active_name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin
    GENERATED ALWAYS AS (IF(deleted_at IS NULL,pvc_name,NULL)) STORED,
  UNIQUE KEY uk_workspace_instance(instance_id),
  UNIQUE KEY uk_workspace_uid(cluster,pvc_uid),
  UNIQUE KEY uk_workspace_name(cluster,namespace,active_name),
  KEY idx_workspace_group(group_id,status,id),
  CONSTRAINT ck_workspace_state CHECK (status IN ('UNKNOWN','AVAILABLE','BOUND','DELETING','DELETED','FAILED')),
  CONSTRAINT ck_workspace_ownership CHECK (ownership IN ('UNKNOWN','EXCLUSIVE','SHARED')),
  CONSTRAINT ck_workspace_capacity CHECK (capacity_bytes >= 0 AND (used_bytes IS NULL OR used_bytes >= 0))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_workspace_attachment (
  workspace_id BIGINT UNSIGNED NOT NULL,
  notebook_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY(workspace_id,notebook_id),
  UNIQUE KEY uk_attachment_notebook(notebook_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_notebook_owner (
  notebook_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  owner_user_id BIGINT UNSIGNED NOT NULL,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  KEY idx_notebook_owner(owner_user_id,notebook_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_quota_reservation (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
  group_id BIGINT UNSIGNED NOT NULL,
  notebook_id BIGINT UNSIGNED NOT NULL,
  notebook_revision BIGINT UNSIGNED NOT NULL,
  operation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  cpu_milli BIGINT NOT NULL,
  memory_bytes BIGINT NOT NULL,
  gpu BIGINT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'HELD',
  accounted_at DATETIME(3) DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_reservation_operation(operation_id),
  KEY idx_reservation_group(group_id,status),
  KEY idx_reservation_notebook(notebook_id,status),
  CONSTRAINT ck_reservation_status CHECK(status IN ('HELD','ACCOUNTED','RELEASED')),
  CONSTRAINT ck_reservation_amount CHECK(cpu_milli >= 0 AND memory_bytes >= 0 AND gpu >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

