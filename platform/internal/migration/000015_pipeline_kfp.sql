-- 提交日志必须先于 KFP CreateRun 持久化；不声称跨数据库和 KFP exactly-once。
CREATE TABLE tb_pipeline_submission (
  instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  run_name VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  attempted BOOLEAN NOT NULL DEFAULT FALSE,
  external_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (instance_id),
  UNIQUE KEY uk_pipeline_submission_name (run_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
