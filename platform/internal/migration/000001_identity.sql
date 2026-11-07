CREATE TABLE tb_user (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  issuer VARCHAR(255) COLLATE utf8mb4_bin NOT NULL,
  subject VARCHAR(255) COLLATE utf8mb4_bin DEFAULT NULL,
  username VARCHAR(255) COLLATE utf8mb4_bin NOT NULL,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  email VARCHAR(255) DEFAULT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'PENDING',
  authorization_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  last_login_at DATETIME(3) DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
    ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_identity (issuer, subject),
  UNIQUE KEY uk_username (username),
  KEY idx_status (status, id),
  CONSTRAINT ck_user_status CHECK
    (status IN ('PENDING','ACTIVE','DISABLED','DELETING','DELETED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_group (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  cluster VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  display_name VARCHAR(128) NOT NULL,
  description VARCHAR(1000) NOT NULL DEFAULT '',
  created_by BIGINT UNSIGNED NOT NULL,
  infrastructure_owner VARCHAR(255) COLLATE utf8mb4_bin NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'CREATING',
  observed_state VARCHAR(24) NOT NULL DEFAULT 'UNKNOWN',
  profile_uid CHAR(36) CHARACTER SET ascii DEFAULT NULL,
  namespace_uid CHAR(36) CHARACTER SET ascii DEFAULT NULL,
  quota_requested JSON DEFAULT NULL,
  quota_observed JSON DEFAULT NULL,
  observed_at DATETIME(3) DEFAULT NULL,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  last_error VARCHAR(1024) DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
    ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_instance (instance_id),
  UNIQUE KEY uk_cluster_name (cluster, name),
  KEY idx_creator (created_by, created_at),
  KEY idx_status (status, updated_at),
  CONSTRAINT ck_group_status CHECK
    (status IN ('CREATING','ACTIVE','DEGRADED','FAILED','DELETING','DELETED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_permission (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  name VARCHAR(128) NOT NULL,
  scope_type VARCHAR(16) NOT NULL,
  module VARCHAR(32) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code),
  CONSTRAINT ck_permission_scope CHECK (scope_type IN ('global','group'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_role (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  name VARCHAR(128) NOT NULL,
  scope_type VARCHAR(16) NOT NULL,
  access_level VARCHAR(16) NOT NULL DEFAULT 'none',
  builtin TINYINT UNSIGNED NOT NULL DEFAULT 0,
  enabled TINYINT UNSIGNED NOT NULL DEFAULT 1,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  description VARCHAR(1000) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
    ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code),
  CONSTRAINT ck_role_scope CHECK (scope_type IN ('global','group')),
  CONSTRAINT ck_role_access CHECK (access_level IN ('none','view','edit')),
  CONSTRAINT ck_global_access CHECK (scope_type <> 'global' OR access_level = 'none')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_role_permission (
  role_id BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (role_id, permission_id),
  KEY idx_permission (permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_role_binding (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  scope_type VARCHAR(16) NOT NULL,
  scope_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
  expires_at DATETIME(3) DEFAULT NULL,
  granted_by BIGINT UNSIGNED NOT NULL,
  granted_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  revoked_by BIGINT UNSIGNED DEFAULT NULL,
  revoked_at DATETIME(3) DEFAULT NULL,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  PRIMARY KEY (id),
  UNIQUE KEY uk_binding (user_id, role_id, scope_type, scope_id),
  KEY idx_scope (scope_type, scope_id, status, user_id),
  KEY idx_expiry (status, expires_at),
  CONSTRAINT ck_binding_scope CHECK
    ((scope_type = 'global' AND scope_id = 0) OR
     (scope_type = 'group' AND scope_id > 0)),
  CONSTRAINT ck_binding_status CHECK (status IN ('ACTIVE','REVOKED','EXPIRED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_group_access (
  group_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  desired_level VARCHAR(16) NOT NULL DEFAULT 'none',
  observed_level VARCHAR(16) NOT NULL DEFAULT 'unknown',
  desired_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  applied_version BIGINT UNSIGNED NOT NULL DEFAULT 0,
  status VARCHAR(24) NOT NULL DEFAULT 'PENDING',
  observed_objects JSON DEFAULT NULL,
  observed_at DATETIME(3) DEFAULT NULL,
  last_error VARCHAR(1024) DEFAULT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
    ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (group_id, user_id),
  KEY idx_pending (status, updated_at),
  CONSTRAINT ck_desired_access CHECK (desired_level IN ('none','view','edit')),
  CONSTRAINT ck_observed_access CHECK
    (observed_level IN ('unknown','none','view','edit','conflict')),
  CONSTRAINT ck_access_status CHECK
    (status IN ('PENDING','APPLYING','READY','REVOKING','FAILED','DRIFTED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_operation (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  operation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  actor_id BIGINT UNSIGNED NOT NULL,
  request_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  payload_hash CHAR(64) CHARACTER SET ascii NOT NULL,
  action VARCHAR(48) NOT NULL,
  target_type VARCHAR(32) NOT NULL,
  target_key VARCHAR(191) NOT NULL,
  target_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  status VARCHAR(24) NOT NULL DEFAULT 'PENDING',
  step VARCHAR(64) NOT NULL DEFAULT 'ACCEPTED',
  payload JSON NOT NULL,
  result JSON DEFAULT NULL,
  attempt INT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  lease_owner VARCHAR(128) DEFAULT NULL,
  lease_until DATETIME(3) DEFAULT NULL,
  last_error VARCHAR(1024) DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
    ON UPDATE CURRENT_TIMESTAMP(3),
  finished_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_operation (operation_id),
  UNIQUE KEY uk_request (actor_id, request_key),
  KEY idx_schedule (status, next_attempt_at, lease_until),
  KEY idx_target (target_type, target_key, target_version),
  CONSTRAINT ck_operation_status CHECK
    (status IN ('PENDING','RUNNING','RETRYING','SUCCEEDED','FAILED','SUPERSEDED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_audit (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  operation_id CHAR(36) CHARACTER SET ascii DEFAULT NULL,
  actor_id BIGINT UNSIGNED DEFAULT NULL,
  actor_name VARCHAR(255) NOT NULL,
  group_id BIGINT UNSIGNED DEFAULT NULL,
  action VARCHAR(64) NOT NULL,
  target_type VARCHAR(32) NOT NULL,
  target_id VARCHAR(191) NOT NULL,
  outcome VARCHAR(24) NOT NULL,
  before_data JSON DEFAULT NULL,
  after_data JSON DEFAULT NULL,
  reason VARCHAR(1024) DEFAULT NULL,
  source_ip VARCHAR(64) DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_actor (actor_id, created_at),
  KEY idx_group (group_id, created_at),
  KEY idx_operation (operation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_sync_cursor (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  cluster VARCHAR(64) CHARACTER SET ascii NOT NULL,
  api_group VARCHAR(128) CHARACTER SET ascii NOT NULL,
  api_version VARCHAR(32) CHARACTER SET ascii NOT NULL,
  resource VARCHAR(64) CHARACTER SET ascii NOT NULL,
  namespace VARCHAR(63) CHARACTER SET ascii NOT NULL DEFAULT '',
  resource_version VARCHAR(128) CHARACTER SET ascii DEFAULT NULL,
  last_full_sync_at DATETIME(3) DEFAULT NULL,
  last_event_at DATETIME(3) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_watch_scope (cluster, api_group, api_version, resource, namespace)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
