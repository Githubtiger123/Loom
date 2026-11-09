#!/usr/bin/env sh
set -eu
# 在任何 admin 写入前拒绝账号碰撞，避免把 root 或日志只读账号当作任务账号。
[ "$MINIO_ROOT_USER" != "$KFP_ACCESS_KEY" ] && [ "$MINIO_ROOT_USER" != "$LOG_ACCESS_KEY" ] &&
  [ "$KFP_ACCESS_KEY" != "$LOG_ACCESS_KEY" ] && [ "$ARTIFACT_ACCESS_KEY" != "$MINIO_ROOT_USER" ] &&
  [ "$ARTIFACT_ACCESS_KEY" != "$KFP_ACCESS_KEY" ] && [ "$ARTIFACT_ACCESS_KEY" != "$LOG_ACCESS_KEY" ] || { echo 'MinIO 四个账号必须不同。' >&2; exit 1; }
for task_password in "$MINIO_ROOT_PASSWORD" "$KFP_SECRET_KEY" "$LOG_SECRET_KEY" "$ARTIFACT_SECRET_KEY"; do
  [ "${#task_password}" -ge 16 ] && [ "${#task_password}" -le 40 ] || { echo 'MinIO 密码使用 16–40 字符。' >&2; exit 1; }
done
for task_user in "$MINIO_ROOT_USER" "$KFP_ACCESS_KEY" "$LOG_ACCESS_KEY" "$ARTIFACT_ACCESS_KEY"; do
  [ "${#task_user}" -ge 3 ] && [ "${#task_user}" -le 20 ] || { echo 'MinIO 账号使用 3–20 字符。' >&2; exit 1; }
  case "$task_user" in kfp-*) echo 'kfp- 用户名前缀保留给空间账号。' >&2; exit 1;; esac
done
# 不打印凭据；所有账号均由 Secret 明确指定，重复执行不清空桶或覆盖桶策略。
mc alias set local http://minio.kubeflow.svc.cluster.local:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null
mc mb --ignore-existing local/mlpipeline >/dev/null
mc admin policy create local loom-kfp-backend /config/backend-policy.json >/dev/null
mc admin user add local "$KFP_ACCESS_KEY" "$KFP_SECRET_KEY" >/dev/null
mc admin policy attach local loom-kfp-backend --user "$KFP_ACCESS_KEY" >/dev/null 2>&1 ||
  mc admin user info local "$KFP_ACCESS_KEY" --json | grep -Eq '"policyName"[[:space:]]*:[[:space:]]*"loom-kfp-backend"'
mc admin policy create local loom-log-reader /config/logs-policy.json >/dev/null
mc admin user add local "$LOG_ACCESS_KEY" "$LOG_SECRET_KEY" >/dev/null
mc admin policy attach local loom-log-reader --user "$LOG_ACCESS_KEY" >/dev/null 2>&1 ||
  mc admin user info local "$LOG_ACCESS_KEY" --json | grep -Eq '"policyName"[[:space:]]*:[[:space:]]*"loom-log-reader"'
mc admin policy create local loom-artifact-reader /config/artifacts-policy.json >/dev/null
mc admin user add local "$ARTIFACT_ACCESS_KEY" "$ARTIFACT_SECRET_KEY" >/dev/null
mc admin policy attach local loom-artifact-reader --user "$ARTIFACT_ACCESS_KEY" >/dev/null 2>&1 ||
  mc admin user info local "$ARTIFACT_ACCESS_KEY" --json | grep -Eq '"policyName"[[:space:]]*:[[:space:]]*"loom-artifact-reader"'
echo 'MinIO 桶和只读账号已配置。'
