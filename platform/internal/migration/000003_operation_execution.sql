ALTER TABLE tb_operation
  ADD COLUMN cluster VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'default',
  ADD COLUMN deadline_at DATETIME(3) DEFAULT NULL,
  ADD KEY idx_cluster_schedule (cluster,status,next_attempt_at,lease_until);

UPDATE tb_operation o JOIN tb_group g ON
  (o.target_type='group' AND o.target_key=CAST(g.id AS CHAR)) OR
  (o.target_type='access' AND SUBSTRING_INDEX(o.target_key,'/',1)=CAST(g.id AS CHAR))
SET o.cluster=g.cluster;
