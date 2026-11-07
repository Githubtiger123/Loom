-- 旧实例使用原 Jupyter 路径规则，只回填业务类型，不修改历史操作单或集群对象。
ALTER TABLE tb_notebook
  ADD COLUMN server_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'jupyter',
  ADD CONSTRAINT ck_notebook_server_type CHECK (server_type IN ('jupyter','group-one','group-two'));

UPDATE tb_notebook
SET requested_spec = JSON_SET(requested_spec, '$.serverType', 'jupyter')
WHERE JSON_EXTRACT(requested_spec, '$.serverType') IS NULL;
