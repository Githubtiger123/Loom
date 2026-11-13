CREATE TABLE tb_pipeline_runtime_cache (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  group_id BIGINT UNSIGNED NOT NULL,
  cache_key CHAR(64) NOT NULL,
  run_id BIGINT UNSIGNED NOT NULL,
  instance_id CHAR(36) NOT NULL,
  execution_id BIGINT NOT NULL,
  execution_hash CHAR(64) NOT NULL,
  inventory JSON NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_runtime_cache_source (group_id, cache_key, execution_id),
  KEY idx_runtime_cache_lookup (group_id, cache_key, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
