-- 已有凭据也保守要求改密，首次配置和管理员重置始终重新置位。
ALTER TABLE tb_local_credential ADD COLUMN password_change_required BOOLEAN NOT NULL DEFAULT TRUE;

-- family 是 SQL 会话元数据，不是第三种 token，不保存凭据原文或摘要副本。
CREATE TABLE tb_auth_session (
    family_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    auth_domain CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    session_version BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(3) NOT NULL,
    last_refreshed_at DATETIME(3) NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    revoked_at DATETIME(3) NULL,
    PRIMARY KEY (family_id),
    KEY idx_auth_session_user (auth_domain, user_id, session_version, family_id),
    KEY idx_auth_session_cleanup (expires_at, family_id)
) ENGINE=InnoDB;

-- 滚动部署前停用旧认证写入者，回填保持已有 family 的撤销状态及绝对期限。
INSERT INTO tb_auth_session (family_id, auth_domain, user_id, session_version, created_at, last_refreshed_at, expires_at, revoked_at)
SELECT family_id, auth_domain, user_id, session_version, MIN(created_at), MAX(created_at), MAX(family_expires_at), MAX(revoked_at)
FROM tb_auth_token
GROUP BY family_id, auth_domain, user_id, session_version;
