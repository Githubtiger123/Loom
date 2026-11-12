# 迁移失败与 dirty 恢复

MySQL DDL 可能隐式提交，迁移失败不代表整个文件已回滚。`tb_schema_migration.dirty=1` 会阻止重放，必须检查实际表结构后才能修复。不要删库、删除数据目录、批量清除迁移记录，或直接把失败记录改成 `dirty=0`；后者会跳过尚未执行的 SQL。

## 000023：注释分号导致 1064

已知旧执行器使用 `strings.Split(body, ";")`，错误地把以下行注释中的分号当成语句结束：

```sql
-- Small static-v1 archives are stored inline; no external object URI is invented.
```

于是 `no external object URI is invented.` 被发送到 MySQL，触发 1064。该文件只有一条 ALTER，此错误发生在 ALTER 真正执行之前，但恢复仍须以数据库检查为准，不能只凭日志清除 dirty。

修复在执行器中处理引号、行／块注释与语句边界，**没有修改 000023 或其他已发布 SQL 文件**，checksum 继续按原文件字节计算。已有成功迁移不需要重新执行；已有 dirty 记录不会自动放行。

### 1. 先准备与停止自动执行

备份业务数据库（至少保留表结构和 `tb_schema_migration` 记录），停止并发迁移／发布。先把修复后的源码同步到 Compose 挂载目录；使用生产镜像时须先发布并选择包含执行器修复的新镜像。以下命令都在 `install/docker` 目录执行，不会自动由文档运行。

### 2. 只读核对

进入 Compose 的 MySQL（`-p` 会交互提示密码）：

```bash
docker compose exec mysql mysql -u root -p loom
```

执行：

```sql
SELECT name, checksum, dirty, applied_at
FROM tb_schema_migration
WHERE name = '000023_pipeline_native_final_snapshot.sql';

SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = 'tb_pipeline_run_attempt'
  AND COLUMN_NAME IN ('workflow_snapshot', 'workflow_archived_at');

SELECT CONSTRAINT_NAME, CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = DATABASE()
  AND TABLE_NAME = 'tb_pipeline_run_attempt'
  AND CONSTRAINT_NAME = 'ck_pipeline_attempt_final_snapshot';
```

只有第一项为该文件的 `dirty=1`，并且后两项都无记录，才适用下一步。如果列或约束已经存在、只有部分存在、没有 dirty 记录，或错误并非上面的 1064，请停止，结合 `SHOW CREATE TABLE tb_pipeline_run_attempt;` 单独核实，不能套用重试流程。

### 3. 仅移除这一次未执行成功的登记

确认满足上面的条件后，在同一 MySQL 会话执行以下有条件删除。只移除一条失败迁移登记，不删除业务数据；结构已经变化时不会删除记录：

```sql
DELETE FROM tb_schema_migration
WHERE name = '000023_pipeline_native_final_snapshot.sql'
  AND dirty = 1
  AND EXISTS (
    SELECT 1 FROM information_schema.TABLES
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'tb_pipeline_run_attempt'
  )
  AND NOT EXISTS (
    SELECT 1 FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'tb_pipeline_run_attempt'
      AND COLUMN_NAME IN ('workflow_snapshot', 'workflow_archived_at')
  )
  AND NOT EXISTS (
    SELECT 1 FROM information_schema.TABLE_CONSTRAINTS
    WHERE CONSTRAINT_SCHEMA = DATABASE()
      AND TABLE_NAME = 'tb_pipeline_run_attempt'
      AND CONSTRAINT_NAME = 'ck_pipeline_attempt_final_snapshot'
  );
SELECT ROW_COUNT() AS removed_failed_migration_record;
```

必须看到影响 **1 行**。若为 0，不要扩大删除条件。可以从事先备份恢复原迁移登记，但不要在已成功重跑 ALTER 后恢复旧 dirty 状态。

### 4. 显式重跑并核对

退出 MySQL，确认使用修复后的源码／镜像，再运行一次性迁移容器：

```bash
docker compose run --rm --no-deps migrate
```

该命令会继续执行后续尚未应用的迁移，成功退出后再次核对 000023 的 `dirty=0`、两列及 CHECK 约束均存在，再用原先的 profile 启动服务。若失败，保留新日志和 dirty 状态继续排查，不反复删除登记。

本文仅是恢复操作说明；未实际连接数据库、执行恢复、迁移或启动服务。源码修复不等于已有数据库已恢复。
