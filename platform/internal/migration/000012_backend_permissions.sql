-- 显式升级权限字典与内置角色，不改自定义角色或绑定，不在启动时自动扩权。
INSERT IGNORE INTO tb_permission(code,name,scope_type,module) VALUES
('group.quota.update','group.quota.update','global','group'),
('group.disable','group.disable','group','group'),
('group.restore','group.restore','group','group'),
('notebook.transfer','notebook.transfer','group','notebook'),
('notebook.transfer.any','notebook.transfer.any','group','notebook'),
('workspace.read','workspace.read','group','workspace'),
('workspace.attach','workspace.attach','group','workspace'),
('workspace.delete','workspace.delete','group','workspace'),
('workspace.transfer','workspace.transfer','group','workspace'),
('workspace.manage.any','workspace.manage.any','group','workspace'),
('pipeline.create','pipeline.create','group','pipeline'),
('pipeline.read','pipeline.read','group','pipeline'),
('pipeline.read.any','pipeline.read.any','group','pipeline'),
('pipeline.version.create','pipeline.version.create','group','pipeline'),
('pipeline.run','pipeline.run','group','pipeline'),
('pipeline.stop','pipeline.stop','group','pipeline'),
('pipeline.delete','pipeline.delete','group','pipeline'),
('trainer.create','trainer.create','group','trainer'),
('trainer.read','trainer.read','group','trainer'),
('trainer.read.any','trainer.read.any','group','trainer'),
('trainer.stop','trainer.stop','group','trainer'),
('trainer.delete','trainer.delete','group','trainer'),
('kserve.create','kserve.create','group','kserve'),
('kserve.read','kserve.read','group','kserve'),
('kserve.read.any','kserve.read.any','group','kserve'),
('kserve.update','kserve.update','group','kserve'),
('kserve.deploy','kserve.deploy','group','kserve'),
('kserve.stop','kserve.stop','group','kserve'),
('kserve.delete','kserve.delete','group','kserve');

INSERT IGNORE INTO tb_role_permission(role_id,permission_id)
SELECT r.id,p.id FROM tb_role r JOIN tb_permission p ON p.scope_type=r.scope_type
WHERE r.builtin=1 AND (
 (r.code='platform_admin' AND p.code IN ('group.quota.update')) OR
 (r.code='group_owner' AND p.code IN ('group.disable','group.restore','notebook.transfer','notebook.transfer.any','workspace.read','workspace.attach','workspace.delete','workspace.transfer','workspace.manage.any','pipeline.create','pipeline.read','pipeline.read.any','pipeline.version.create','pipeline.run','pipeline.stop','trainer.create','trainer.read','trainer.read.any','trainer.stop','trainer.delete','kserve.create','kserve.read','kserve.read.any','kserve.update','kserve.deploy','kserve.stop','kserve.delete')) OR
 (r.code='group_editor' AND p.code IN ('notebook.transfer','workspace.read','workspace.attach','workspace.delete','workspace.transfer','pipeline.create','pipeline.read','pipeline.version.create','pipeline.run','pipeline.stop','trainer.create','trainer.read','trainer.stop','trainer.delete','kserve.create','kserve.read','kserve.update','kserve.deploy','kserve.stop','kserve.delete')) OR
 (r.code='group_viewer' AND p.code IN ('workspace.read','pipeline.read','trainer.read','kserve.read'))
);

INSERT IGNORE INTO tb_role_permission(role_id,permission_id)
SELECT r.id,p.id FROM tb_role r JOIN tb_permission p ON p.code='pipeline.delete'
WHERE r.builtin=1 AND r.code IN ('group_owner','group_editor') AND r.scope_type='group';

UPDATE tb_role SET version=version+1 WHERE builtin=1 AND code IN ('platform_admin','group_owner','group_editor','group_viewer');
UPDATE tb_user SET authorization_version=authorization_version+1
WHERE id IN (SELECT b.user_id FROM tb_role_binding b JOIN tb_role r ON r.id=b.role_id
 WHERE r.builtin=1 AND r.code IN ('platform_admin','group_owner','group_editor','group_viewer'));
