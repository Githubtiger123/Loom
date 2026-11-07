CREATE TABLE tb_model (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  group_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  description VARCHAR(2000) NOT NULL DEFAULT '',
  created_by BIGINT UNSIGNED DEFAULT NULL,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at DATETIME(3) DEFAULT NULL,
  active_name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin
    GENERATED ALWAYS AS (IF(deleted_at IS NULL, name, NULL)) STORED,
  PRIMARY KEY (id),
  UNIQUE KEY uk_model_name (group_id, active_name),
  KEY idx_model_owner (group_id, created_by, id),
  CONSTRAINT ck_model_revision CHECK (revision > 0),
  CONSTRAINT ck_model_status CHECK (status IN ('ACTIVE','ARCHIVED')),
  CONSTRAINT ck_model_archive CHECK ((status='ARCHIVED') = (deleted_at IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_model_version (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  group_id BIGINT UNSIGNED NOT NULL,
  model_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  description VARCHAR(2000) NOT NULL DEFAULT '',
  source_pipeline_run_id BIGINT UNSIGNED DEFAULT NULL,
  created_by BIGINT UNSIGNED DEFAULT NULL,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at DATETIME(3) DEFAULT NULL,
  active_name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin
    GENERATED ALWAYS AS (IF(deleted_at IS NULL, name, NULL)) STORED,
  PRIMARY KEY (id),
  UNIQUE KEY uk_model_version_name (group_id, model_id, active_name),
  KEY idx_model_version_page (group_id, model_id, id),
  KEY idx_model_version_source_run (group_id, source_pipeline_run_id),
  CONSTRAINT ck_model_version_revision CHECK (revision > 0),
  CONSTRAINT ck_model_version_status CHECK (status IN ('DRAFT','READY','ARCHIVED')),
  CONSTRAINT ck_model_version_archive CHECK ((status='ARCHIVED') = (deleted_at IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tb_model_artifact (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  group_id BIGINT UNSIGNED NOT NULL,
  model_id BIGINT UNSIGNED NOT NULL,
  version_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  uri VARCHAR(2048) NOT NULL,
  model_format VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  model_format_version VARCHAR(64) NOT NULL DEFAULT '',
  digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  size_bytes BIGINT UNSIGNED DEFAULT NULL,
  storage_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_by BIGINT UNSIGNED DEFAULT NULL,
  revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at DATETIME(3) DEFAULT NULL,
  active_name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin
    GENERATED ALWAYS AS (IF(deleted_at IS NULL, name, NULL)) STORED,
  PRIMARY KEY (id),
  UNIQUE KEY uk_model_artifact_name (group_id, model_id, version_id, active_name),
  KEY idx_model_artifact_page (group_id, model_id, version_id, id),
  CONSTRAINT ck_model_artifact_revision CHECK (revision > 0),
  CONSTRAINT ck_model_artifact_storage CHECK (storage_type IN ('S3','GS','PVC','OCI')),
  CONSTRAINT ck_model_artifact_status CHECK (status IN ('ACTIVE','ARCHIVED')),
  CONSTRAINT ck_model_artifact_archive CHECK ((status='ARCHIVED') = (deleted_at IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO tb_permission(code,name,scope_type,module) VALUES
('model.create','model.create','group','model'),
('model.read','model.read','group','model'),
('model.read.any','model.read.any','group','model'),
('model.update','model.update','group','model'),
('model.version.create','model.version.create','group','model'),
('model.version.update','model.version.update','group','model'),
('model.artifact.create','model.artifact.create','group','model'),
('model.archive','model.archive','group','model');

INSERT IGNORE INTO tb_role_permission(role_id,permission_id)
SELECT r.id,p.id FROM tb_role r JOIN tb_permission p ON p.scope_type='group'
WHERE r.builtin=1 AND (
  (r.code='group_owner' AND p.code IN ('model.create','model.read','model.read.any','model.update','model.version.create','model.version.update','model.artifact.create','model.archive')) OR
  (r.code='group_editor' AND p.code IN ('model.create','model.read','model.update','model.version.create','model.version.update','model.artifact.create','model.archive')) OR
  (r.code='group_viewer' AND p.code='model.read')
);

UPDATE tb_role SET version=version+1
WHERE builtin=1 AND code IN ('group_owner','group_editor','group_viewer');

UPDATE tb_user SET authorization_version=authorization_version+1
WHERE id IN (
  SELECT b.user_id FROM tb_role_binding b JOIN tb_role r ON r.id=b.role_id
  WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor','group_viewer')
);
