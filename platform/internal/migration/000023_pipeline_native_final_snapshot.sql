-- Final execution evidence is separate from the immutable submission snapshot.
-- Small static-v1 archives are stored inline; no external object URI is invented.
ALTER TABLE tb_pipeline_run_attempt
  ADD COLUMN workflow_snapshot JSON DEFAULT NULL,
  ADD COLUMN workflow_archived_at DATETIME(3) DEFAULT NULL,
  ADD CONSTRAINT ck_pipeline_attempt_final_snapshot CHECK (workflow_snapshot IS NULL OR
    (JSON_TYPE(workflow_snapshot)='OBJECT' AND engine='NATIVE_ARGO' AND workflow_uid IS NOT NULL
      AND workflow_snapshot_hash IS NOT NULL AND workflow_snapshot_ref IS NULL AND workflow_archived_at IS NOT NULL));
