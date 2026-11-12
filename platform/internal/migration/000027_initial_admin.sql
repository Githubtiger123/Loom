-- Initial local identity: admin / Loom-Dev-2026! (must change on first login).
-- Fixed issuer matches the default Compose auth.issuer=https://loom.local.
-- DML ONLY: migrate runs this file in one transaction under the bootstrap lock.
-- A known temporary password must never be exposed on a public deployment.
  -- Match the authorization mutex used by the application when it exists.
  SELECT id FROM tb_role WHERE code = 'platform_admin' FOR UPDATE;

  -- Any historical admin binding means this database has already been seeded.
  -- Do not restore a disabled/revoked admin or install a known password again.
  SET @loom_seed_admin = NOT EXISTS (
    SELECT 1 FROM tb_role_binding b JOIN tb_role r ON r.id = b.role_id
    WHERE r.code = 'platform_admin' AND b.scope_type = 'global' AND b.scope_id = 0
  ) AND NOT EXISTS (
    SELECT 1 FROM tb_user
    WHERE username = 'admin'
      OR (issuer = 'https://loom.local' AND subject = 'loom-local-admin')
  ) AND NOT EXISTS (
    SELECT 1 FROM tb_role WHERE code = 'platform_admin'
      AND (builtin <> 1 OR enabled <> 1 OR scope_type <> 'global' OR access_level <> 'none')
  );

  -- Snapshot missing roles before inserting. Existing roles and grants stay intact.
  SELECT COALESCE(JSON_ARRAYAGG(seed.code), JSON_ARRAY()) INTO @loom_seed_new_roles
  FROM JSON_TABLE(
    '["platform_admin","platform_user","auditor","group_owner","group_editor","group_viewer"]',
    '$[*]' COLUMNS (code VARCHAR(64) PATH '$')
  ) AS seed
  WHERE @loom_seed_admin AND NOT EXISTS (SELECT 1 FROM tb_role r WHERE r.code = seed.code);

  -- Keep the dictionary and new-role grants aligned with internal/rbac/permission.go.
  SET @loom_seed_permissions = (
  WITH permission_groups AS (
    SELECT 'global' AS scope_type,
      'user.read user.manage role.manage group.create group.quota.update audit.read' AS codes
    UNION ALL SELECT 'group',
      'pipeline.update pipeline.version.update pipeline.logs pipeline.exec'
    UNION ALL SELECT 'group', 'kserve.invoke kserve.logs'
    UNION ALL SELECT 'group',
      'group.read group.update group.delete group.disable group.restore group.member.manage group.owner.transfer group.quota.read notebook.create notebook.read notebook.read.any notebook.update notebook.delete notebook.stop notebook.start notebook.connect notebook.transfer notebook.transfer.any workspace.read workspace.attach workspace.delete workspace.transfer workspace.manage.any pipeline.create pipeline.read pipeline.read.any pipeline.version.create pipeline.run pipeline.stop pipeline.delete trainer.create trainer.read trainer.read.any trainer.stop trainer.delete kserve.create kserve.read kserve.read.any kserve.update kserve.deploy kserve.stop kserve.delete model.create model.read model.read.any model.update model.version.create model.version.update model.artifact.create model.archive'
  )
  SELECT JSON_ARRAYAGG(JSON_OBJECT('code', p.code, 'scope', g.scope_type))
  FROM permission_groups g
  CROSS JOIN JSON_TABLE(CONCAT('["', REPLACE(g.codes, ' ', '","'), '"]'),
    '$[*]' COLUMNS (code VARCHAR(128) PATH '$')) AS p
  );

  INSERT INTO tb_permission (code, name, scope_type, module)
  SELECT seed.code, seed.code, seed.scope_type, SUBSTRING_INDEX(seed.code, '.', 1)
  FROM JSON_TABLE(@loom_seed_permissions, '$[*]' COLUMNS (
    code VARCHAR(128) PATH '$.code', scope_type VARCHAR(16) PATH '$.scope'
  )) AS seed
  WHERE @loom_seed_admin AND NOT EXISTS (SELECT 1 FROM tb_permission p WHERE p.code = seed.code);

  INSERT INTO tb_role (code, name, scope_type, access_level, builtin, enabled, version)
  SELECT seed.code, seed.name, seed.scope_type, seed.access_level, 1, 1, 1
  FROM (
    SELECT 'platform_admin' AS code, '平台管理员' AS name, 'global' AS scope_type, 'none' AS access_level
    UNION ALL SELECT 'platform_user', '平台用户', 'global', 'none'
    UNION ALL SELECT 'auditor', '审计员', 'global', 'none'
    UNION ALL SELECT 'group_owner', '空间负责人', 'group', 'edit'
    UNION ALL SELECT 'group_editor', '空间开发者', 'group', 'edit'
    UNION ALL SELECT 'group_viewer', '空间观察者', 'group', 'view'
  ) AS seed
  WHERE JSON_CONTAINS(@loom_seed_new_roles, JSON_QUOTE(seed.code));

  INSERT INTO tb_role_permission (role_id, permission_id)
  SELECT r.id, p.id FROM tb_role r CROSS JOIN tb_permission p
  JOIN JSON_TABLE(@loom_seed_permissions, '$[*]' COLUMNS (
    code VARCHAR(128) PATH '$.code', scope_type VARCHAR(16) PATH '$.scope'
  )) AS seed ON seed.code = p.code AND seed.scope_type = p.scope_type
  WHERE JSON_CONTAINS(@loom_seed_new_roles, JSON_QUOTE(r.code)) AND (
    (r.code = 'platform_admin' AND p.scope_type = 'global')
    OR (r.code = 'platform_user' AND p.code = 'group.create')
    OR (r.code = 'auditor' AND p.code = 'audit.read')
    OR (r.code = 'group_owner' AND p.scope_type = 'group')
    OR (r.code = 'group_editor' AND p.scope_type = 'group'
      AND (p.code IN ('group.read', 'group.quota.read')
        OR SUBSTRING_INDEX(p.code, '.', 1) IN ('notebook','workspace','pipeline','trainer','kserve','model'))
      AND p.code NOT IN ('workspace.manage.any','notebook.transfer.any',
        'pipeline.read.any','trainer.read.any','kserve.read.any','model.read.any'))
    OR (r.code = 'group_viewer' AND p.scope_type = 'group'
      AND p.code IN ('group.read','group.quota.read','notebook.read','workspace.read',
        'pipeline.read','trainer.read','kserve.read','model.read'))
  );

  INSERT INTO tb_user (issuer, subject, username, display_name, status)
  SELECT 'https://loom.local', 'loom-local-admin', 'admin', '初始管理员', 'ACTIVE'
  WHERE @loom_seed_admin;
  SET @loom_seed_user_id = IF(@loom_seed_admin, LAST_INSERT_ID(), NULL);
  SET @loom_seed_role_id = (SELECT id FROM tb_role WHERE code = 'platform_admin');

  -- Argon2id v19, 64 MiB, 3 passes, parallelism 2, 16-byte salt, 32-byte hash.
  -- No plaintext credential is stored in tb_local_credential.
  INSERT INTO tb_local_credential (user_id, password_hash, password_change_required)
  SELECT @loom_seed_user_id,
    '$argon2id$v=19$m=65536,t=3,p=2$V4DzVvNq7dMEsulVk2+MvQ$9nvJm9JQOMFDhkMy2Q2UynFaJOn2EqOpiFp1YauY9C8',
    TRUE
  WHERE @loom_seed_admin;

  INSERT INTO tb_role_binding (user_id, role_id, scope_type, scope_id, status, granted_by)
  SELECT @loom_seed_user_id, @loom_seed_role_id, 'global', 0, 'ACTIVE', @loom_seed_user_id
  WHERE @loom_seed_admin;

  INSERT INTO tb_audit (actor_id, actor_name, action, target_type, target_id, outcome, after_data)
  SELECT @loom_seed_user_id, 'admin', 'platform.bootstrap', 'user', CAST(@loom_seed_user_id AS CHAR), 'SUCCEEDED',
    JSON_OBJECT('source', 'migration-000027', 'passwordChangeRequired', TRUE)
  WHERE @loom_seed_admin;

  SET @loom_seed_admin = NULL, @loom_seed_new_roles = NULL, @loom_seed_permissions = NULL,
    @loom_seed_user_id = NULL, @loom_seed_role_id = NULL;
