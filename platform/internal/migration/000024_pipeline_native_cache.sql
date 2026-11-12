-- Native cache is scoped to business group, pipeline and immutable space UID.
-- Argo ConfigMaps are short-lived projections, not the authoritative database.
CREATE TABLE tb_pipeline_task_cache (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  group_id BIGINT UNSIGNED NOT NULL,
  pipeline_id BIGINT UNSIGNED NOT NULL,
  namespace_uid VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  source_task_id BIGINT UNSIGNED NOT NULL,
  source_instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  argo_node_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  outputs JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_native_cache_scope (group_id,pipeline_id,namespace_uid,fingerprint),
  KEY idx_native_cache_expiry (expires_at),
  CONSTRAINT fk_native_cache_source FOREIGN KEY (group_id,source_task_id)
    REFERENCES tb_pipeline_task_execution (group_id,id),
  CONSTRAINT ck_native_cache_outputs CHECK (JSON_TYPE(outputs)='OBJECT')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
