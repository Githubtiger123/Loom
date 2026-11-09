# Pipeline 离线日志：MinIO 按需读取

实现范围是后端只读接口。没有新增数据库表、迁移、worker 轮询或日志正文落库；没有测试、编译、运行或部署。在线日志仍使用 Kubernetes `pods/log`；离线读取单独访问 MinIO/S3，不会混合两种来源或自动回退。

## 流程与保留

1. install 中 MinIO 和 KFP/Argo 的配置使用同一个 mlpipeline 桶；全局和空间级 Argo `archiveLogs: true` 自动上传 main 日志。
2. `format: argo` 根据 `pipeline/runid` 将日志归档到执行 UUID 目录，后端直接列举这一次执行的目录，不要求另传 `manifest.json`。旧采集器协议另保留为 `format: manifest`。
3. 用户请求 Loom；Loom 检查当前用户、组权限、运行对象权限和 `pipeline.logs`。
4. Loom 从当前 Run 或已有 `tb_pipeline_run_attempt.snapshot` 确认执行实例，读取该实例的日志列表，再按条目读取日志。实例不能跨 Run、组或集群选择。
5. 返回内容前再次检查当前业务授权；本地认证模式也再次检查原 Access JWT/会话。用户移除、角色撤销、注销不能凭历史创建者身份绕过检查。

编辑清理旧实例后，使用 attempts 返回的旧 `instanceId` 查归档；手动启动产生新 ID，两个实例不会混读。软删除 Run 后，业务记录仍保留，已授权用户可读取其归档；不要求旧 KFP Run、Workflow、Pod 或 namespace 存在。业务权限被撤销时归档访问也被拒绝。

安装和配置见 [MinIO 集成](../../install/minio.md)。源码已配好自动归档和读取路径，但没有执行安装或联调。未归档的日志在 Pod/节点清理后无法由此接口恢复。当前删除/编辑不会等待归档完成，必须由 Argo 和运维保留策略保障上传时机。若要求“归档成功才能删”，需要另行添加清理前归档确认流程。

这里配置的是当前 install 的日志目录，不自动识别任意旧 Argo 桶或旧 Workflow-name 路径。sidecar、init container、重启前日志不在 Argo main 日志保证范围；没有实际归档的内容不会伪造。

## 配置

总配置文件的 `log_archive` 独立于 `pipeline.enabled`，便于 KFP 不可用或已关闭后查询历史归档：

```yaml
log_archive:
  enabled: true
  format: argo
  endpoint: http://minio.kubeflow.svc.cluster.local:9000
  bucket: mlpipeline
  prefix: logs
  region: us-east-1
  timeout: 15s
```

凭据通过 Secret 注入 `LOOM_LOG_ARCHIVE_ACCESS_KEY`、`LOOM_LOG_ARCHIVE_SECRET_KEY`；安装器已将专用只读账号复制为 loom-system/loom-log-archive，合并 `install/examples/log-archive-api-pod-spec.yaml` 到 API Pod。临时凭据可另传 `LOOM_LOG_ARCHIVE_SESSION_TOKEN`。凭据在进程启动时读取，轮换后重启 API。其余覆盖变量为 `LOOM_LOG_ARCHIVE_ENABLED/FORMAT/ENDPOINT/BUCKET/PREFIX/REGION/TIMEOUT`。私有 CA 使用系统信任，不提供跳过 TLS 校验选项。format 未指定时保持旧 manifest 行为；默认示例文件已改为 argo，enabled 仍关闭以免未注入凭据时启动失败。

argo 模式 API 需要日志前缀内 `s3:ListBucket` 和 `s3:GetObject`（包括 HEAD），manifest 模式只需 GetObject；两种模式都不需要 PutObject/DeleteObject/建桶/admin 权限。任务只获本空间前缀的读写身份，不发 root。API 不创建桶、不上传、不扫描整个桶，不向客户端发凭据、原始 key 或签名链接。存储过期清理由运维配置生命周期策略，与业务软删除独立。

配置 bucket 限 3–63 字符 DNS label；prefix 为安全相对路径。endpoint 限服务根地址，HTTP 仅用于受信内网；生产建议 HTTPS。

## Argo 自动归档模式

```text
{prefix}/{cluster}/{namespace}/{instanceId}/{podName}/main.log
```

安装默认 prefix=logs、cluster=default，必须与 platform.cluster、空间 webhook 的 LOG_ARCHIVE_CLUSTER、全局 Argo keyFormat 和 MinIO 权限策略一致。cluster 不是前端参数。instanceId 是不可复用的 KFP Run UUID。Argo 归档配置的 keyFormat 是目录，main.log 由 Argo 追加。

后端按这个精确前缀 ListObjects，不查 KFP，不访问其他执行。列表最多检查 10000 个对象，超限返回错误，不截断冒充完整结果。只识别一层 Pod 目录下的 main.log/main.log.gz/main.log.tgz，不暴露通用对象浏览。entryId 是相对 key 的 SHA256，前端只能选择接口返回的 ID。

返回 `source=ARGO`；这里只能确定 Pod 名和 main 容器，taskId、podUID 为空，不根据 Pod 名猜 KFP 节点。`complete=false` 恒表示不能证明全部归档完成。空 entries 不代表执行无日志，也可能尚未上传/已清理/命中缓存。

读取验证最新 ETag/size，并用 If-Match 固定对象。支持明文、gzip 文本、单文件 tar.gz（main.log），不解压到磁盘。对象和解压结果均限制 32 MiB，超限明确报错；每进程最多 4 个并发内容读取。为支持无索引压缩格式，每次内容请求有界读取/解压对象，再返回最多 1 MiB；不是大文件下载接口，超大日志需运维日志系统。列表 sizeBytes 是存储对象大小，内容 sizeBytes/offset/nextOffset 是解码后的字节数，不可混用。分页使用 nextOffset。

同空间任务共享 namespace 级存储权限，归档不是不可篡改审计；本接口的用户与对象授权不替代存储侧工作负载隔离。

## 旧 manifest 存储协议 v1

以下仅适用于 `format: manifest`，需要独立采集器按协议上传；不是新安装默认模式。

```text
{prefix}/{cluster}/{namespace}/{instanceId}/manifest.json
{prefix}/{cluster}/{namespace}/{instanceId}/{podUID}/{container}/main.log
```

`instanceId` 等于该次 KFP Run ID，不能用可复用的业务 rid 或名称代替。日志文件为普通 UTF-8 文本，不是 gzip/tar；已有压缩归档须由适配端解压或转换。一个容器可有多个不可变文件（分片、重启次数等），每个条目使用不同 ID。manifest 示例：

```json
{
  "version": 1,
  "cluster": "default",
  "namespace": "team-a",
  "instanceId": "9e64cb60-61ac-4e9e-a1cd-b3474cda3a32",
  "complete": true,
  "entries": [
    {
      "id": "task34-main-0",
      "taskId": "34",
      "pod": "train-abc",
      "podUID": "3982deaa-11c2-4ace-a08a-b6a5b1afef21",
      "container": "main",
      "key": "3982deaa-11c2-4ace-a08a-b6a5b1afef21/main/main.log",
      "sizeBytes": 12,
      "etag": "REPLACE-WITH-ACTUAL-ETAG"
    }
  ]
}
```

`sizeBytes` 为上传后的文件字节数，`etag` 为对象存储实际 ETag（去掉外层双引号，分段上传可含 `-`；不能简单假定总是 MD5）。`key` 只接受该次执行前缀内、以 `{podUID}/{container}/` 开头的安全相对路径，不能放 URL、绝对路径、转义、`..`、其他实例路径。Loom 同时核对 manifest 内的 cluster/namespace/instanceId。清单最大 2 MiB、最多 10000 个条目，条目 ID 不能重复。

`complete=false` 表示采集仍在进行，返回的是目前已上传的文件；`complete=true` 表示采集端宣布该次归档已结束，不是运行成功标记。发布过的文件应保持不可变；新增文件后更新清单，不原地追加已有文件。ETag/size 不一致返回冲突，防止翻页时混合不同内容。若启用桶版本化，本接口仍读取当前对象，不按 versionId 恢复被覆盖/删除的历史版本。

## API

所有路径以前缀 `/api/v1/groups/{gid}/pipeline-runs/{rid}` 开始，使用正常平台认证；不接受 URL 查询参数传 token。

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/log-archives?instanceId=...` | 按需列出归档日志；argo 模式直接列举，manifest 模式读清单；不返回 key |
| GET | `/log-archives/{entryId}?instanceId=...&etag=...&offset=0&limitBytes=262144` | 读取该条目的日志文本片段 |

列表省略 instanceId 时选择当前 Run 的 ID。内容请求必须提供列表返回的 instanceId、entryId 和 ETag，避免用户翻页时恰逢重建切换执行。历史实例来源于 `/attempts`，不接受任意实例 UUID、Pod、bucket 或对象 key。

列表返回 `data` 包含 version、source、cluster、namespace、instanceId、complete、entries。内容响应示例（manifest 模式）：

```json
{"data":{"cluster":"default","namespace":"team-a","instanceId":"9e64cb60-61ac-4e9e-a1cd-b3474cda3a32","entryId":"task34-main-0","etag":"...","offset":0,"nextOffset":12,"sizeBytes":12,"eof":true,"text":"hello world\n"}}
```

默认每次 256 KiB，最大 1 MiB；offset 为解码后日志字节偏移，下一页使用服务端 nextOffset，直到 eof=true。正常 UTF-8 分页会避开末尾半个字符；非法源字节显示替换符，text 的编码长度不能代替 nextOffset。读取从指定 offset 向后，不是在线日志的 tailLines/sinceTime，不支持 follow、原始压缩包下载或任意 Range Header；argo 模式在服务端解码 gzip/tar.gz，manifest 模式仍只支持纯文本。前端可循环读取后拼接，单次响应始终有界。

列表和内容与在线日志共用每进程 128、每用户 4 个并发额度，超限 429；多副本总限额由网关负责。存储单次操作超时按配置 1–30s，服务请求整体最多 30s。返回前核对权限，响应带 no-store；不是瞬时撤权或原子交付保证。

| 返回 | 含义 |
|---|---|
| 400 | 无效/重复/未知参数、偏移越界、缺少 instanceId/ETag |
| 403 | 用户或运行对象权限不足 |
| 404 | 不属于该 Run 的实例/条目，或文件尚未归档/已过期 |
| 409 | 清单身份或文件 size/ETag 不一致，需刷新列表/检查采集端 |
| 503 | 未启用归档、桶/凭据配置问题、存储失败或超时 |

不把存储故障伪装为空日志。无需新 SQL；复用已有 `pipeline.logs` 权限（未应用的项目仍需显式执行之前的 `000018_pipeline_logs.sql`）。
