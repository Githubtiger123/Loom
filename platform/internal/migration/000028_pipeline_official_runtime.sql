-- Engine protocol objects use global IDs and immutable attempt ownership.
-- Existing native-v3 task, artifact and cache rows are not reinterpreted.
CREATE TABLE tb_pipeline_runtime_object (
 id BIGINT NOT NULL AUTO_INCREMENT,
 group_id BIGINT UNSIGNED NOT NULL,
 run_id BIGINT UNSIGNED NOT NULL,
 instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 object_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 parent_id BIGINT NOT NULL DEFAULT 0,
 writer_pod_uid VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 value JSON NOT NULL,
 created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
 updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
 PRIMARY KEY(id),
 UNIQUE KEY uk_runtime_object(instance_id,kind,object_key),
 KEY idx_runtime_parent(instance_id,kind,parent_id,id),
 KEY idx_runtime_group(group_id,run_id,instance_id),
 CONSTRAINT ck_runtime_value CHECK(JSON_TYPE(value)='OBJECT')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE tb_pipeline_runtime_receipt (
 instance_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 pod_uid VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 operation VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 request_id CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 response JSON NOT NULL,
 created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
 PRIMARY KEY(instance_id,pod_uid,operation,request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
