ALTER TABLE tb_model_version
  ADD COLUMN source_pipeline_instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN source_pipeline_artifact_id BIGINT UNSIGNED DEFAULT NULL,
  ADD COLUMN import_operation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD UNIQUE KEY uk_model_version_import_operation (import_operation_id);

INSERT IGNORE INTO tb_permission(code,name,scope_type,module) VALUES ('kserve.invoke','kserve.invoke','group','kserve');
INSERT IGNORE INTO tb_role_permission(role_id,permission_id)
SELECT r.id,p.id FROM tb_role r JOIN tb_permission p ON p.code='kserve.invoke'
WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor');
UPDATE tb_role SET version=version+1 WHERE builtin=1 AND code IN ('group_owner','group_editor');
UPDATE tb_user SET authorization_version=authorization_version+1
WHERE id IN (SELECT b.user_id FROM tb_role_binding b JOIN tb_role r ON r.id=b.role_id WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor'));
