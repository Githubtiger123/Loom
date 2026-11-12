-- Runtime/preflight reuse kserve.read/create. User-content diagnostics require
-- a separate permission, never inherited from read-only registry access.
INSERT IGNORE INTO tb_permission(code,name,scope_type,module)
VALUES ('kserve.logs','kserve.logs','group','kserve');
INSERT IGNORE INTO tb_role_permission(role_id,permission_id)
SELECT r.id,p.id FROM tb_role r JOIN tb_permission p ON p.code='kserve.logs'
WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor');
UPDATE tb_role SET version=version+1 WHERE builtin=1 AND code IN ('group_owner','group_editor');
UPDATE tb_user SET authorization_version=authorization_version+1
WHERE id IN (SELECT b.user_id FROM tb_role_binding b JOIN tb_role r ON r.id=b.role_id WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor'));
