# 公共 Pod 日志组件与 Pipeline 在线日志

本文描述在线日志源码契约，未测试、编译、运行或执行迁移。离线日志已通过独立的 MinIO 按需读取接口实现，详见 [离线日志](log-archives.md)；不与在线日志或 exec 终端混用。

## 分层

- `internal/k8s/podlog`：公共 Reader，接收已授权的 namespace/Pod/Pod UID/container 和 Options。直接调用 Kubernetes `pods/log`，支持快照与 follow；不访问业务数据库、不识别 Notebook/Pipeline、不注册 HTTP 路由。
- `internal/k8s/pipeline_logs.go`：从已核实的 KFP 节点映射定位 Pod，检查 `pipeline/runid`、Pod ownerReference 的 Workflow UID 和 Workflow 的 Run 标签。当前只支持 Argo 下直接由 Workflow 控制的 Pod；其他执行后端、分布式任务的间接子 Pod 不自动放行。
- `internal/service/pipeline_logs.go`：业务权限、执行身份、原空间 UID 和持续会话 fence。`pod_logs.go` 定义可复用的日志会话，不把数据库事务跨越整个日志连接。
- `internal/handler/pod_logs.go`：公共参数解析、连接限额、HTTP 分块转发、写超时、取消和错误结束。路由仍统一在 `internal/route/route.go`。

Notebook 现有 `GET .../notebooks/{nid}/logs` 保持原查询参数和 JSON 响应、不新增 follow；其底层日志读取已复用公共 Reader，仍由 Notebook 自身检查 CR/StatefulSet/Pod UID 归属链和 notebook.read/connect 权限。Trainer 等模块接入时应实现自己的授权与目标解析，不能直接开放任意 namespace/Pod 日志接口。

## Pipeline 接口

```http
GET /api/v1/groups/{gid}/pipeline-runs/{rid}/tasks/{taskId}/logs
```

需要平台认证、`pipeline.read`、新增 `pipeline.logs` 及运行对象权限；读取他人/无主运行仍额外要求 `pipeline.read.any`。`taskId` 来自 runtime 返回的 taskId，不是 Pod 名称。runtime 同时新增 podName 和 executionId，尚未分配、缓存或逻辑节点可能没有可用的 Pod 映射。

本地 KFP 的 `toApiPipelineTaskDetail` 未回填 pod_name，Loom 不修改 KFP 源码：字段缺失但有正整数 executionId 时，在 namespace 内仅按该 Run 的 `pipeline/runid` 标签分页查询 Pod（最多 5 页、每页 200），精确匹配 main 容器中 `/kfp-launcher/launch` 在 `--` 分隔符之前的 `--run_id`/`--execution_id`。只接受唯一匹配，随后仍核对 Workflow UID，且会话固定该 Pod UID，不在后续复核时重复扫描或自动切换 Pod。多个重试 Pod 共用 execution ID、无 launcher 标记或缺少 executionId 时明确返回冲突，不能只按节点显示名猜测容器。该兼容路径只针对本仓库 KFP v2 launcher 契约，不代表适配所有 KFP 版本。

参数不允许重复或携带未知字段：

| 参数 | 默认值 | 约束 |
| --- | --- | --- |
| container | main | 只能选择归属已核实 Pod 中的普通/初始化容器，不开放 ephemeral containers |
| follow | false | true 使用持续 HTTP 文本流，不是 Watch，也不是 WebSocket |
| tailLines | 200 | 1–1000 |
| limitBytes | 262144 | 快照最多 1MiB；持续流最多 16MiB，是整次响应累计输出上限 |
| sinceSeconds | 3600 | 1–86400；提供 sinceTime 时不再默认填入 |
| sinceTime | 无 | RFC3339 时间，与 sinceSeconds 二选一；不能超过当前时间 15 秒以上 |
| previous | false | 上一次已终止容器实例日志，不是对象存储归档；不与 follow 同用 |
| timestamps | true | 是否保留 Kubernetes 日志时间戳 |

快照请求返回原有平台 JSON envelope，例如：

```json
{"data":{"pod":"train-abc","podUID":"...","container":"main","text":"...","truncated":false,"observedAt":"..."}}
```

`truncated=true` 表示达到输出字节上限，不保证是从日志源获取了全部内容。默认查询本身也受 tailLines 和时间范围限制。

持续请求示例：

```http
GET /api/v1/groups/1/pipeline-runs/12/tasks/34/logs?container=main&follow=true&tailLines=200&limitBytes=1048576
Authorization: Bearer <access-token>
```

成功后返回 `text/plain; charset=utf-8`，前端用 fetch 的流式读取和流式 UTF-8 解码即可；token 不放查询字符串。响应包含 X-Loom-Pod-UID、X-Loom-Log-Container。设置 `X-Accel-Buffering: no`，代理仍必须允许流式转发、关闭响应缓冲，并配置适合连接生命周期的超时。

首个成功响应前的错误仍返回平台 JSON。持续响应开始后不再拼接 JSON 错误；通过关闭流和可选 HTTP trailer `X-Loom-Log-End` 表示结束，值包括 eof、byte-limit、deadline-or-disconnect、source-error、access-or-identity-changed。浏览器/代理不一定暴露 trailer，不应把所有 HTTP 200 的连接结束都当成任务正常完成，应结合运行状态并按需重连。

## 生命周期和安全

- 普通快照整体上限 15 秒；持续会话最长 5 分钟，达到字节上限也结束。结束后可用 sinceTime 重连；时间边界可能重复，前端自行去重，不承诺断线期间恰好一次日志交付。
- 公共 Reader 使用独立的无固定 HTTP 总超时客户端，避免普通 Kubernetes 客户端 10 秒总超时截断 follow。建立日志流限时 10 秒；整个流仍受调用者 context 和公共最大时长约束。
- HTTP 慢写限制 15 秒；请求取消、到期或校验失败都取消上游并关闭 body。每个 API 进程最多 128 个 Pipeline 日志请求，同用户最多 4 个，快照和 follow 共用额度，超限返回 429。多副本总配额需由网关另行限制。
- 开始交付前、快照读取后再次核对业务授权和身份；持续会话约每 10 秒重新校验一次（单轮校验最多 5 秒）。本地认证还重新验证原 Access JWT/会话，过期、注销、强制改密或撤权会结束连接。不是瞬时撤权保证。
- 原执行回执须匹配当前 Group instanceId/namespace UID/Profile UID，真实集群 namespace 也要匹配。编辑、删除、重新启动等 revision/instanceId 变化会结束旧连接。
- Pod UID 在打开前后和持续期间重复检查。Kubernetes pods/log 没有 UID 前置条件，这些复核只能缩小同名 Pod 替换的竞态窗口，不能宣称原子绑定。不同容器重启的日志是否保留仍取决于 Kubernetes 的实际保留策略。
- 不经过 worker，不落日志正文到 MySQL，不向浏览器发 Kubernetes token，也不把任意 Pod 名称当作业务授权。

## 部署与未包含范围

显式执行 `000018_pipeline_logs.sql`（及此前未应用迁移），同步升级 API。新增权限默认授予内置 group_owner/group_editor，不自动升级自定义角色，也不默认授予 group_viewer；平台管理员沿用现有授权逻辑。

API 机器身份需要目标空间的 `pods: get`、`pods/log: get`、`workflows.argoproj.io: get`，以及现有空间核验所需的 `namespaces: get`；KFP 缺失 pod_name 的兼容路径还需要 `pods: list`。仅为读日志不需要 pods 的 create/delete/exec 权限。本次不修改真实集群 RBAC、不安装组件。

[在线终端](pipeline-terminal.md) 已独立实现，使用组内 pipeline.exec，不要求日志权限，也不将其当作只读能力。独立调试 Pod、单节点执行和前端仍待后续实现。离线日志使用 MinIO manifest 和不可变文本文件按需读取，不新增日志索引表。采集端需在 Pod 存在时提前归档；当前清理不等待上传，不保证删除前归档完成。旧 attempts 快照并不等于保留旧日志；在线接口不会在 Pod 消失后伪造空日志，也不自动回退归档。
