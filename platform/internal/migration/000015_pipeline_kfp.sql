-- Pipeline 删除是异步外部删除；三个状态约束必须允许确认后的 DELETED。
ALTER TABLE tb_pipeline_run
  DROP CHECK ck_pipeline_run_desired,
  DROP CHECK ck_pipeline_run_observed,
  DROP CHECK ck_pipeline_run_status,
  ADD CONSTRAINT ck_pipeline_run_desired CHECK (desired_state IN ('RUNNING','STOPPED','DELETED')),
  ADD CONSTRAINT ck_pipeline_run_observed CHECK (observed_state IN ('UNKNOWN','PENDING','RUNNING','SUCCEEDED','FAILED','STOPPED','DELETED')),
  ADD CONSTRAINT ck_pipeline_run_status CHECK (status IN ('UNKNOWN','PENDING','RUNNING','SUCCEEDED','FAILED','STOPPED','DELETED','ERROR'));

-- 提交日志必须先于 KFP CreateRun 持久化。确定性 run_id 允许安全重放，
-- 但 Loom MySQL 与 KFP 数据库之间仍不是分布式事务。
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
