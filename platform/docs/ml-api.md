# Pipeline、Trainer、KServe 与模型注册后端业务接口

本文描述已写入的后端源码契约，未测试、编译、运行或执行迁移。Pipeline 已补入内置官方 KFP v2 主线，KServe 已补入内置 CR 执行与状态同步，详见 [KFP 流程与边界](pipeline-kfp.md) 和 [KServe 主线](kserve.md)；Trainer 实际适配器和前端仍未实现。

## 共同约定

- 以下路径均以 `/api/v1/groups/{gid}` 开头，使用平台认证及当前空间权限。
- 写请求需 `Idempotency-Key`。同操作者同键同请求返回原操作，同键不同请求冲突；重放也重新检查对象权限。
- 修改、停止、删除和归档需当前 `revision`，不接受覆盖更新。创建接口的名称是 DNS-1123 label。
- 查询返回 `{"data": ...}`；资源列表 data 为 `{"items": [], "nextAfter": ...}`，历史、版本修订和执行快照接口返回 data 数组（以各接口约定为准）。分页使用 `after`、`limit`，默认只查询本人资源；`scope=all/unowned` 额外要求对应模块的 `read.any`。
- 默认不列出已删除或归档对象，可用 `includeDeleted=true`。详情、版本、历史及操作单仍执行当前授权，不因曾经创建过对象而绕过撤权。
- `202/PENDING` 只代表持久受理，必须看操作单及对象 `desiredState/observedState/revision/appliedRevision`。同步完成的元数据操作返回 `200`，不表示外部工作负载已运行。
- 四个业务模块分别使用 `pipeline.*`、`trainer.*`、`kserve.*`、`model.*` 权限；跨负责人访问还需相应 `read.any`。工作负载执行前仍会重查启动请求人的状态和权限。

Trainer 引用字段只接受受管标识，不接受 URL、任意 Secret 名、明文凭据或任意 Kubernetes PodSpec。KServe 只接受已登记的 Artifact、受限模型 storage URI 或容器镜像及少量结构化字段；存储凭据由固定 ServiceAccount 提供。Pipeline 内置模式允许导入官方编译 IR，不执行用户 Python，也不由用户指定 KFP 端点、运行 SA 或存储根路径。

## Pipeline

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET / POST | `/pipelines` | 查询 / 创建定义 |
| GET / DELETE | `/pipelines/{pid}` | 详情 / 归档定义 |
| GET / POST | `/pipelines/{pid}/versions` | 查询 / 创建版本 |
| PUT | `/pipelines/{pid}/versions/{vid}` | 原地修改版本，保留修订历史 |
| GET | `/pipelines/{pid}/versions/{vid}/revisions` | 版本修订历史 |
| GET | `/pipelines/{pid}/versions/{vid}` | 版本详情 |
| GET / POST | `/pipelines/{pid}/runs` | 查询该定义的运行 / 从指定版本提交新运行 |
| GET | `/pipeline-runs` | 查询空间内运行 |
| GET / DELETE | `/pipeline-runs/{rid}` | 详情 / 异步停止并删除运行 |
| PUT | `/pipeline-runs/{rid}` | 清理旧执行后编辑保存，不自动启动 |
| POST | `/pipeline-runs/{rid}/start` | 手动创建新的执行实例 |
| GET | `/pipeline-runs/{rid}/attempts` | 已清理的旧执行快照 |
| GET | `/pipeline-runs/{rid}/runtime` | 官方实时运行及经筛选的节点状态 |
| GET | `/pipeline-runs/{rid}/dag` | 本次 Run 快照的 DAG、依赖边及实时执行实例 |
| GET | `/pipeline-runs/{rid}/nodes/{nodeId}` | 静态或执行节点详情、参数值、产物元数据；见 [DAG 契约](pipeline-dag.md) |
| GET | `/pipeline-runs/{rid}/tasks/{taskId}/logs` | 节点在线容器日志快照 / follow 流 |
| GET (WebSocket) | `/pipeline-runs/{rid}/tasks/{taskId}/terminal` | 在线终端；组内 pipeline.exec + 运行对象权限，首帧认证/输入/缩放见 [终端协议](pipeline-terminal.md) |
| GET | `/pipeline-runs/{rid}/log-archives` | 当前或指定历史 instanceId 的 MinIO 归档清单，需 pipeline.logs |
| GET | `/pipeline-runs/{rid}/log-archives/{entryId}` | 指定 instanceId / etag，按 offset / limitBytes 分段读取离线日志，详见 [离线日志](log-archives.md) |
| POST | `/pipeline-runs/{rid}/stop` | 请求停止 |
| GET | `/pipeline-runs/{rid}/history` | 操作历史 |

定义、版本和运行分表保存；版本可原地修改并递增自身 revision，运行复制版本及参数快照，不随共享版本修改。运行编辑先清理旧 KFP 实例，再保存为 STOPPED 待启动；手动 start 换新 instanceId/KFP Run ID。完整请求、权限及并发语义见 [编辑与重建](pipeline-edit.md)。

在线日志由 Loom 授权后直接读取 Kubernetes pods/log，不经过 KFP 日志代理或 worker；需要 pipeline.logs 权限。普通读取返回 JSON 快照，follow=true 返回 HTTP 纯文本流（不使用 JSON envelope）。参数、连接上限和归属校验见 [公共日志组件](pod-logs.md)。

创建定义示例：

```json
{"name":"daily-train","displayName":"每日训练","description":"业务训练流程"}
```

导入版本示例（`revision` 是定义当前版本；YAML 文本需替换成 SDK 编译的完整单文档 KFP v2 IR）：

```json
{
  "revision": 1,
  "name": "v1",
  "packageYaml": "<SDK 编译的完整单文档 KFP v2 IR YAML>"
}
```

提交运行示例：

```json
{"name":"daily-train-001","versionId":1,"parameters":[{"name":"epochs","value":"10"}]}
```

也可提交 `pipelineSpec` JSON 对象，与 packageYaml 二选一。后端从 IR 提取参数 schema、默认值及摘要，不再要求用户手填；支持 STRING / INTEGER / DOUBLE / BOOLEAN / LIST / STRUCT。公开值仍以字符串传输，提交 KFP 时按类型转换为原生 JSON 值。旧协议另有 NUMBER / CREDENTIAL，但内置主线没有凭据引用解析器，不能传明文凭据或仅传 packageRef。操作重试不复用终态执行，编辑清理后的手动 start 会创建新的 KFP Run。

启用 pipeline 配置并独立运行 pipeline-worker 后，提交调用官方 CreateRun，停止调用 TerminateRun，删除在终态后调用 DeleteRun，GET 轮询同步状态。KFP Run ID 固定为 Loom `instanceId`，响应丢失时按同 ID 恢复或幂等重放。使用 namespace 的默认 Experiment 和内嵌 pipeline_spec，不向官方 Pipeline/version 目录重复上传。详细兼容条件、配置与故障恢复见 [KFP 主线](pipeline-kfp.md)。

运行列表/详情还返回 `kfpState`、`kfpSyncedAt`、`kfpSyncAttemptedAt`、`kfpSyncReason`，分别表示最近核验的原始状态、成功读取时间、最近尝试时间及同步异常。独立观测在操作结束或原用户撤权后继续进行；超时/404 不会伪造失败或删除。默认活动运行约 5 秒、终态约 5 分钟观测，受并发/限速和 KFP 自身延迟影响。上述字段需显式执行 `000016_pipeline_sync.sql`。

停止和删除请求示例：

```json
{"revision":1}
```

`pipeline.stop` 负责停止，两个 DELETE 使用 `pipeline.delete`。删除 Pipeline 定义仍是本地归档；删除 Pipeline Run 则是持久异步操作：可替代尚未完成的提交，必要时恢复确定性 KFP Run，先终止活动运行，再删除 KFP Run，并在确认 GET 404 后写入 Loom `DELETED/deletedAt`。所以创建后立即删除可安全收敛。Loom 的版本快照、操作和历史保留；先删除定义下所有运行，才能归档定义。

## Trainer

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET / POST | `/trainer-jobs` | 查询 / 创建训练作业 |
| GET / DELETE | `/trainer-jobs/{jid}` | 详情 / 删除 |
| POST | `/trainer-jobs/{jid}/stop` | 请求停止 |
| GET | `/trainer-jobs/{jid}/history` | 操作历史 |

创建示例：

```json
{
  "name":"train-001",
  "runtimeRef":"pytorch-runtime-001",
  "codeRef":"code-snapshot-001",
  "datasetRefs":["dataset-001"],
  "outputRef":"output-001",
  "workers":2,
  "resources":{"cpu":"2","memory":"4Gi","gpu":0},
  "parameters":[{"name":"epochs","value":"10"}]
}
```

`resources` 是每个 worker 的请求。适配器负责在授权范围内解析 runtime/code/data/output，校验代码和资源策略，再提交真实作业。提交成功不等于训练成功。停止、删除传 `{"revision":1}`；删除必须等待真实目标不存在的执行回执，不以删除请求已发出作为完成。

## KServe

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET / POST | `/kserve-services` | 查询 / 创建并请求部署 |
| GET / PATCH / DELETE | `/kserve-services/{sid}` | 详情 / 新配置修订 / 删除 |
| POST | `/kserve-services/{sid}/deploy` | 部署指定配置修订 |
| POST | `/kserve-services/{sid}/stop` | 请求停止 |
| GET | `/kserve-services/{sid}/revisions` | 不可变配置修订 |
| GET | `/kserve-services/{sid}/history` | 操作历史 |

MODEL 创建示例：

```json
{
  "name":"classifier",
  "deploymentType":"MODEL",
  "protocol":"v2",
  "minReplicas":1,
  "maxReplicas":2,
  "resources":{"cpu":"1","memory":"2Gi","gpu":0},
  "model":{"storageUri":"s3://models/classifier/v1","modelFormat":"sklearn","modelFormatVersion":"1"}
}
```

推荐使用模型注册产物替代直接 URI：`"model":{"artifactId":7}`。后端会核对 Model/Version/Artifact 状态与对象权限，并将完整产物元数据冻结进 KServe 配置修订；接口和生命周期见 [模型注册](model-registry.md)。直接 URI 方式继续兼容。

CUSTOM 使用同一外层字段，将 `deploymentType` 设为 `CUSTOM`，并提供 `custom`：`{"image":"registry.example.com/team/server:v1","command":["/app/server"],"args":["--port=8080"],"port":8080,"healthPath":"/ready"}`。两类配置严格互斥；不接受任意 YAML、ServiceAccount、Secret、Volume 或 securityContext。

PATCH 提交完整规格并加当前 `revision`，只产生不可变修订，不修改正在运行的 CR。部署使用 `{"revision":2,"revisionId":1}`，显式选择要发布的历史配置修订；停止和删除使用当前 revision。发布受理不等于 Ready，需继续读取业务状态。

启用 `kserve.enabled` 后，独立 kserve-worker 使用固定 ServiceAccount 和 Standard 部署模式创建/更新 CR。停止会删除 `InferenceService`，确认 GET 404 后清空当前 UID 绑定，但保留业务配置和历史；再次部署创建新 CR 并绑定新 UID。删除业务服务同样先确认 CR 不存在，再软删除业务记录。Watch 实时同步 Ready、generation、URL 和错误，周期 GET 负责断线修复；历史 SERVING 不等于当前仍健康。详见 [KServe 主线](kserve.md)。

## 模型注册

Loom 按 Model、Version、Artifact 三层保存组内模型业务元数据，模型文件仍在 S3、GCS、PVC 或 OCI。Version 从 DRAFT 开始，有有效 Artifact 后才能切换 READY；Artifact 不允许原地修改。模型、版本和产物提供列表、详情、CAS 更新或归档接口，全部复用当前组权限、对象 owner、幂等操作单和审计。完整 API、请求示例、归档级联和 KServe 快照规则见 [模型注册与 KServe 集成](model-registry.md)。

## 操作、空间与执行适配器

查询 `GET /api/v1/operations/{oid}`，人工重试 `POST /api/v1/operations/{oid}/retry`，后者同样带幂等键。只重试当前实例和 revision 对应的失败执行，不允许旧操作覆盖新意图或重跑已结束的作业。

创建、提交、修改和部署要求 ACTIVE 空间。已有资源停止、删除和归档可在 ACTIVE / DEGRADED / DISABLING / DISABLED 中处理，不能借清理接口恢复运行。空间停用、用户禁用和成员移除遇到活动或未确认终态的 ML 资源时会阻断，先显式停止并确认再重试，不自动取消用户训练。

独立 mTLS 机器入口提供 claim / renew / report / observe / receipt；不使用用户 JWT、Cookie 或转发证书头，不挂公网业务 mux。租约绑定证书、实例、revision 和空间 UID；回报使用单调序号，外部 ID 一旦确认不能替换。副作用发生前须重新检查授权及租约，处理重领与迟到结果，不能假设跨系统 exactly-once。

完整执行桥协议见 [执行器契约](../internal/executor/README.md)。启用内置 KFP/KServe 后，对应动作分别由专用 worker 领取，不与外部机器执行器混用。Trainer 或未配置内置执行的模块继续等待外部适配器，平台不会伪造结果。迁移需要部署方显式执行，本轮未执行。
