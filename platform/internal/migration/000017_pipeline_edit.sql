ALTER TABLE tb_pipeline_version
  ADD COLUMN revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  ADD COLUMN updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  ADD CONSTRAINT ck_pipeline_version_revision CHECK (revision > 0);

CREATE TABLE tb_pipeline_version_revision (
  version_id BIGINT UNSIGNED NOT NULL,
  revision BIGINT UNSIGNED NOT NULL,
  group_id BIGINT UNSIGNED NOT NULL,
  spec JSON NOT NULL,
  spec_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_by BIGINT UNSIGNED DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (version_id, revision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO tb_pipeline_version_revision(version_id,revision,group_id,spec,spec_hash,created_by,created_at)
SELECT id,revision,group_id,spec,spec_hash,created_by,created_at FROM tb_pipeline_version;

ALTER TABLE tb_pipeline_run
  ADD COLUMN pending_spec JSON DEFAULT NULL,
  ADD COLUMN execution_cleaned BOOLEAN NOT NULL DEFAULT FALSE;

-- 编辑清理完成后保留旧执行快照；手动启动会换 instance_id，新旧 KFP ID 不混用。
CREATE TABLE tb_pipeline_run_attempt (
  instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  run_id BIGINT UNSIGNED NOT NULL,
  revision BIGINT UNSIGNED NOT NULL,
  group_id BIGINT UNSIGNED NOT NULL,
  snapshot JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (instance_id),
  UNIQUE KEY uk_pipeline_attempt_revision (group_id,run_id,revision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO tb_permission(code,name,scope_type,module) VALUES
('pipeline.update','pipeline.update','group','pipeline'),
('pipeline.version.update','pipeline.version.update','group','pipeline');
INSERT IGNORE INTO tb_role_permission(role_id,permission_id)
SELECT r.id,p.id FROM tb_role r JOIN tb_permission p ON p.scope_type=r.scope_type
WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor')
  AND p.code IN ('pipeline.update','pipeline.version.update');
UPDATE tb_role SET version=version+1 WHERE builtin=1 AND code IN ('group_owner','group_editor');
UPDATE tb_user SET authorization_version=authorization_version+1
WHERE id IN (SELECT b.user_id FROM tb_role_binding b JOIN tb_role r ON r.id=b.role_id
 WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor'));
