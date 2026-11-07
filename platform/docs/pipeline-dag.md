# Pipeline DAG 与节点详情（后端）

本轮只实现后端源码和接口，不包含前端、产物内容下载或 HTML/图片/图表可视化；未测试、编译、运行或执行迁移。

## 1. 数据和权限

沿用官方 KFP，而非新建 Workflow 执行器：

- Loom 本次 Run 的 `spec.pipelineSpec` 快照提供静态 DAG，不读取最新可编辑版本。`packageDigest` 标识这一份 IR。
- 官方 KFP GetRun 提供运行与任务状态、时间、错误/状态历史、执行器及子任务引用。
- 官方 MLMD 的 `system.PipelineRun` context 提供 executions；execution 的 `inputs` / `outputs` Struct 提供已解析参数值，events 提供产物输入输出关系，Artifact / ArtifactType 提供元数据。
- 查询只使用 Loom 机器配置，不传用户 JWT 到 KFP/MLMD。MLMD 不引入第二套 Loom 用户表，也不新增本地元数据表或迁移。

两接口均使用现有 `pipeline.read` 与 Run 对象权限；查看其他负责人的运行还需要 `pipeline.read.any`，与已有 Run 详情一致。只允许读取该组内已登记的 Run。外部读取前核验提交回执的 Run ID、namespace、名称、请求摘要；返回前重新检查授权、instanceId、revision 与外部 ID，防止读取期间撤权/编辑/重建。

MLMD 的 execution 必须来自这个已核验 Run 的 context。节点详情只能查询该 context 内 execution 的 events，再查询这些 events 所引用的产物和产物类型。包括合法的缓存复用产物，但不允许前端指定任意 contextId / executionId / artifactId 来全库查询。缓存来源 ID 仅作展示，不据此自动展开其他运行。

## 2. DAG 接口

`GET /api/v1/groups/{gid}/pipeline-runs/{rid}/dag`

返回现有 `{"data": ...}` envelope，data 包含：

| 字段 | 内容 |
| --- | --- |
| instanceId / revision / externalId | 当前业务执行实例、业务修订和 KFP Run ID |
| packageDigest | 本次提交的 IR 摘要 |
| state / error / stateHistory | KFP 原始运行状态、可用的错误和历史，不混用 Loom 的期望状态 |
| runtime / metadata | 两个来源的状态和成功读取时间；不是跨 KFP/MLMD 的事务快照 |
| definitions | 静态节点：nodeId、parentNodeId、path、taskName、displayName、component、kind、executorLabel、输入输出定义和绑定、条件/缓存/重试/迭代配置 |
| edges | 静态控制依赖、参数依赖、产物依赖、final-status 依赖及 DAG 边界输入输出 |
| instances | 实际节点实例：nodeId、definitionNodeId、mapping、父节点、taskId、executionId、状态、Pod 名、时间、错误/状态历史、循环下标/数量、缓存来源及执行器信息 |

`definitions` 包含 `root` 和嵌套组件展开的节点。普通静态节点 ID 是 `spec-<路径摘要>`；`path` 是任务 key 的数组，避免按斜杠或点拆分名字。

`instances` 的 ID 优先为 `execution-<MLMD ID>`；只有 KFP 任务但尚无 executionId 时为 `task-<任务 ID 摘要>`。这是 Loom 节点详情接口的 ID，不是 Kubernetes Pod 名或 KFP taskId。

一个静态节点可对应多个实际实例。关联依据为 `task_name + parent_dag_id`，再保留 `iteration_index` / `iteration_count`；迭代子 DAG 与循环包装节点可对应同一定义。与 KFP 任务通过 executionId 精确关联，不按 displayName 猜测。无法精确关联时保留实例并标记 `mapping=UNRESOLVED`，不丢弃也不错误挂接。`parentTaskId` / `parentExecutionId` 表示嵌套关系，不能当作先后依赖。

`edges` 的 `scopeNodeId` 是所在 DAG；`kind` 为 CONTROL / PARAMETER / ARTIFACT / FINAL_STATUS。数据边可包含 sourcePort / targetPort。`boundary=INPUT/OUTPUT` 表示经过该 scope 的输入/输出端口，不是包含关系；绘图时应按 scope 的端口处理，不能把跨边界边和普通任务边混成一个全局拓扑环。条件 one-of 输出会保留所有候选来源边，实际哪个分支运行看 instances 状态，不在 Loom 重新执行 CEL。

未生成 execution 的静态定义没有实际运行状态，不能直接当作 SKIPPED；没有实例可能只是尚未调度或元数据未就绪。保留官方返回的 stateHistory 和 failed job 列表，不把一次重试伪造成新的业务 Run。

## 3. 节点详情接口

`GET /api/v1/groups/{gid}/pipeline-runs/{rid}/nodes/{nodeId}?instanceId=<DAG返回的instanceId>`

`nodeId` 必须取自本次 DAG 的 definitions 或 instances。建议携带可选的 instanceId；业务运行已重建则返回冲突，避免旧页面读取同名静态节点的新执行。不存在于当前快照/运行中的节点返回 NotFound。

返回字段：instanceId、revision、readAt、runtime、metadata、definition、instance、inputs、outputs。

- 选择 definitions 的节点：返回静态配置/绑定和声明的输入输出，`instance=null`；不会任意选择该节点某次循环/重试的值。
- 选择 instances 的节点：返回该精确执行实例的状态、错误、时间和输入输出；元数据尚未就绪时 definition 允许为 null。
- `root` 的输入可先使用已提交且已绑定默认值的运行参数，`source=SUBMITTED`。已实际执行的值优先来自 MLMD，`source=MLMD`。

输入输出的形状示例（省略 envelope 和其他字段）：

```json
{
  "inputs": {
    "parametersStatus": "AVAILABLE",
    "parameters": {
      "epochs": {"available": true, "value": 10, "source": "MLMD"},
      "note": {"available": true, "value": "", "source": "MLMD"}
    },
    "artifactsStatus": "AVAILABLE",
    "artifacts": {
      "dataset": [{
        "artifactId": "123",
        "status": "AVAILABLE",
        "eventType": 3,
        "path": ["dataset", 0],
        "metadata": {
          "artifactId": "123", "name": "dataset",
          "typeId": "4", "type": "system.Dataset",
          "typeVersion": "0.0.1", "typeDescription": "",
          "typeProperties": {},
          "uri": "s3://bucket/path/dataset",
          "state": "LIVE",
          "createdAtEpochMs": "1800000000000",
          "updatedAtEpochMs": "1800000000000",
          "properties": {}, "customProperties": {"display_name": "dataset"}
        }
      }]
    }
  }
}
```

参数的 value 保留 JSON 类型（string/number/bool/list/object/null）。`available=false` / `source=UNRESOLVED` 与有效值 null、false、0、空字符串有明确区别。MLMD 中没有该值时不从占位符、其他循环或上游“最新值”猜测，也不在 Loom 重新求值 CEL。parametersStatus=AVAILABLE 表示已读到该 execution 的参数映射，个别声明参数是否有值仍看 available。

产物按端口分组为列表，保留 MLMD event 的 path 和 type，包括声明输入/输出和 pending output；不能因为有元数据或 URI 就认为文件已经写完，需看 metadata.state 和执行状态。已有 KFP artifact ID 而 MLMD 尚不可读时返回 REFERENCES_ONLY / NOT_FOUND，不伪造完整元数据。产物不存在的情况可能是尚未发布或已被清理。

Artifact 元数据含 ID、名称、类型/版本/描述/属性 schema、URI、状态、时间、properties / customProperties。连接配置 `store_session_info` 被去除，URI 去除 userinfo、query、fragment，不能作为带签名下载链接；不解包任意 protobuf Any。任务仍不应把密码或令牌写入普通参数、错误消息或自定义业务元数据。这些内容是供授权用户读取的纯数据，后续前端必须文本转义，不执行其中的 HTML/脚本。

ID 和毫秒时间使用字符串，避免 JavaScript int64 精度丢失；普通参数按 KFP Struct 数值语义处理。stateHistory / childTasks / executor 中沿用官方字段名（如 update_time、task_id、failed_main_jobs）。KFP 若仅在 stateHistory 提供错误，失败/取消节点取同终态的最近错误作为摘要；中途失败后成功的节点不会错误显示为最终失败。

取得 instance.taskId 后仍可调用原有 `/tasks/{taskId}/logs`、`/tasks/{taskId}/terminal`；这些操作另外检查 pipeline.logs / pipeline.exec 及真实 Pod 归属，不因为 DAG 可读就允许执行。MLMD-only 或缓存节点不保证有 taskId/在线 Pod。

## 4. 配置、降级和开销

`pipeline.metadata_endpoint` 指向官方 metadata-envoy 的 **grpc-web HTTP(S)** 入口，示例为 `http://metadata-envoy-service.kubeflow.svc.cluster.local:9090`，不是 KFP apiserver 的 8888 端口，也不是 MLMD 原生 gRPC 的 8080 端口。

可通过 `LOOM_PIPELINE_METADATA_ENDPOINT` 和 `LOOM_PIPELINE_METADATA_TOKEN_FILE` 覆盖。可选 metadata_token_file 只供受鉴权机器代理使用，支持文件 token 轮换；空值用于受集群网络策略保护的内部 Envoy。不要将原生全权限 MLMD / Envoy 入口暴露给用户，Loom 只开放上述受控业务接口，不开放 MLMD 写 API 或任意代理。

- 未配置 MLMD：metadata.status=NOT_CONFIGURED，仍返回 IR 与 KFP 可用状态。
- MLMD 尚未建立 context、超时、格式错误、规模超限或不可达：metadata.status=NOT_READY_OR_UNAVAILABLE，明确降级，不使用伪造的空元数据表示成功。下次查询可重试。
- KFP 已绑定但不可读取/身份不匹配：接口返回错误，不把 MySQL 旧状态冒充 KFP 实时结果。
- 尚未绑定 KFP Run：返回静态 DAG，runtime/metadata 标记 NOT_SUBMITTED。
- 执行已被编辑清理：只返回当前配置的静态 DAG，标记 CLEANED。**本轮没有为已删除的旧 KFP Run 新增运行 DAG/元数据历史快照**，原 attempts 仍只是业务执行配置快照。

按请求读取，不新建全量扫描 worker，也不 watch Kubernetes。DAG 只查询本次 Run 的 executions，不获取全部产物；节点详情仅为所选 execution 查询 events 和产物元数据。executions 按 100 条分页，产物/类型按 100 个批量查询；MLMD 读取每进程并发最多 4。整体接口有界 15 秒，KFP 读取最多 8 秒，MLMD 最多 10 秒且给返回前授权留时间。静态展开最多 2000 节点/32 层，MLMD 最多 2000 executions/4000 artifacts/10000 events，每次 RPC 最多 8 MiB、整次 MLMD 查询最多 16 MiB；超限不静默截断为完整图。

这些是分别读取的近实时结果，不保证 KFP 与 MLMD 恰好同一时刻一致。后续客户端可仅对正在看的运行约每 10 秒刷新 DAG、选中节点时读取详情；不要对所有历史运行高频轮询。必要时可再加服务端按实例缓存/增量接口，本轮没有落地另一套权威状态库。
