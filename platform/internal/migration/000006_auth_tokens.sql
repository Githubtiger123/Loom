-- MySQL 是 Access / Refresh 的权威存储，Redis 仅缓存不可变摘要。
CREATE TABLE tb_auth_token (
    token_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    family_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    auth_domain CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    session_version BIGINT UNSIGNED NOT NULL,
    token_type VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    family_expires_at DATETIME(3) NOT NULL,
    consumed_at DATETIME(3) NULL,
    revoked_at DATETIME(3) NULL,
    replaced_by CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (token_id),
    KEY idx_auth_token_family (family_id, token_type),
    KEY idx_auth_token_user (user_id, session_version),
    KEY idx_auth_token_cleanup (family_expires_at, token_id),
    CONSTRAINT chk_auth_token_type CHECK (token_type IN ('ACCESS', 'REFRESH'))
) ENGINE=InnoDB;

CREATE TABLE tb_auth_rate_bucket (
    bucket_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    attempts INT UNSIGNED NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    PRIMARY KEY (bucket_key),
    KEY idx_auth_rate_cleanup (expires_at, bucket_key)
) ENGINE=InnoDB;
