# Loom Pipeline：官方 KFP 接入主线

本文对应当前后端源码。按项目约定没有新增测试，没有测试、编译、运行、迁移或连接真实 KFP；以下是实现契约，不是联调结果。

## 1. 分工与官方模型

复用本工作区 `pipelines/backend/api/v2beta1/run.proto` 的官方 HTTP 接口，不在 Loom 内重新实现 DAG 调度器，不由 Loom 直接创建 Argo Workflow，也不执行用户提交的 Python。

流程为：Python KFP SDK 编译 IR → 导入 Loom 不可变版本 → 用户填写参数并提交 Run → Loom 持久操作 → pipeline-worker 调用官方 KFP → 官方编译与调度工作负载 → 官方 persistence agent 回写 KFP → Loom 轮询 KFP 并同步业务数据库。

| 概念 | Loom 的职责 | 官方 KFP 的职责 |
| --- | --- | --- |
| 空间 | Group 对应已确认的 Profile / namespace，Owner 仍固定为基础设施 admin | 按 namespace 隔离和校验运行权限 |
| 流水线 | 保存业务名称、负责人和不可变 IR 版本 | 接收 Run 中的内嵌 `pipeline_spec`；本轮不向 KFP Pipeline/version 目录重复上传 |
| 参数 | 从 IR 根参数定义提取类型和默认值，保存运行快照 | 按原生类型绑定到真实流程 |
| Experiment | 本轮不提供 Experiment 管理接口 | 未传 experiment_id，使用当前 namespace 的 Default Experiment |
| 运行 | 业务 ID、期望/观测状态、权限、幂等、操作单、历史 | Run ID、任务状态、实际执行与取消 |
| 状态同步 | 独立 pipeline-worker 轮询官方 Run API | 官方 persistence agent 同步执行引擎状态 |

IR 中的组件可以创建容器、网络访问和读写数据；格式校验不是代码安全沙箱。仍需运维在 namespace、ServiceAccount、网络、准入和存储侧限制真实权限。

## 2. 业务接口与权限

业务路径仍集中在 `internal/route/route.go`，前缀为 `/api/v1/groups/{gid}`，使用已有平台认证和 `pipeline.*` 权限。查询他人对象还需相应 `read.any` 权限。写入需要 `Idempotency-Key`，变更已有对象需要当前 revision。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET / POST | `/pipelines` | 列出 / 创建业务定义 |
| GET / DELETE | `/pipelines/{pid}` | 详情 / 本地归档 |
| GET / POST | `/pipelines/{pid}/versions` | 列出 / 导入不可变版本 |
| GET | `/pipelines/{pid}/versions/{vid}` | 查看版本与参数定义 |
| GET / POST | `/pipelines/{pid}/runs` | 定义下运行 / 从版本提交运行 |
| GET | `/pipeline-runs` | 空间运行列表 |
| GET / DELETE | `/pipeline-runs/{rid}` | 运行详情 / 本地终态归档 |
| POST | `/pipeline-runs/{rid}/stop` | 请求取消真实 KFP Run |
| GET | `/pipeline-runs/{rid}/runtime` | 实时读取 KFP 运行及节点状态 |
| GET | `/pipeline-runs/{rid}/history` | Loom 业务操作与状态变更历史 |

### 2.1 创建定义

POST `/pipelines`：

```json
{"name":"daily-train","displayName":"每日训练","description":"训练并评估"}
```

创建定义只是保存业务元数据，不创建 Workflow 或 Pod。

### 2.2 导入版本

POST `/pipelines/{pid}/versions`，提交 SDK 已编译的 YAML 文本：

```json
{"revision":1,"name":"v1","packageYaml":"<完整的单文档 KFP v2 IR YAML 文本>"}
```

也可将编译结果作为 JSON 对象放入 `pipelineSpec`，不能同时提供 `packageYaml`。当前是 JSON 业务接口，不是 multipart 文件上传。

后端执行以下处理：

1. 限制原始包和规范化 IR 不超过 512 KiB，拒绝多文档 YAML、Argo/Kubernetes 资源和 platformSpec 扩展。
2. 检查 `pipelineInfo`、`schemaVersion`、`root.dag.tasks`、`components` 等必要结构。
3. 从 `root.inputDefinitions.parameters` 提取参数类型、必填性、默认值；不信任请求另外填写的 schema。
4. 规范化 JSON，生成 SHA256 和 `inline-<摘要前24位>` 引用，并保存完整 IR。MySQL JSON 改写空白与键序不会导致执行摘要失配。
5. 保存不可变版本，递增父定义 revision。已有运行不跟随后续版本变化。

这是导入入口校验，不是完整的官方 protobuf/编译器验证。DAG 引用、组件、镜像及执行语义最终仍由官方 KFP 校验。暂不支持根 artifact 输入、带第二文档 Kubernetes 扩展的包、v1 Argo YAML 或直接运行 Python 源码。

内置 KFP 启用后，不能只提交 packageRef / digest 来创建版本；旧版引用型版本保留可读，但必须重新导入 IR 后才能由内置 worker 运行。关闭内置 KFP 时，旧引用协议仍为独立外部适配器保留，并不表示平台已实现这些引用的解析器。

### 2.3 提交运行

POST `/pipelines/{pid}/runs`：

```json
{"name":"daily-train-001","versionId":1,"parameters":[{"name":"epochs","value":"10"}]}
```

从版本获取权威参数定义，拒绝未知参数、缺失必填值和类型错误，补齐默认值。版本 IR、摘要和已绑定参数一起复制进运行快照；提交后不能改参数，要用新 Run 表达新的执行。

| SDK / IR 类型 | Loom 类型 | value 示例 |
| --- | --- | --- |
| STRING | STRING | `"hello"` |
| NUMBER_INTEGER | INTEGER | `"10"` |
| NUMBER_DOUBLE | DOUBLE | `"0.01"` |
| BOOLEAN | BOOLEAN | `"true"` |
| LIST | LIST | `"[1,2]"` |
| STRUCT | STRUCT | `"{\"threshold\":0.5}"` |

API 的 value 仍是字符串；适配器按 schema 转成原生 JSON 值后调用 KFP，不会把所有参数都传成字符串。公开参数单值最多 4 KiB、最多 64 个。敏感命名参数不能通过公开值通道携带凭据；内置主线没有 credentialRef 解析器。JSON 数值按官方 protobuf Struct 的双精度语义处理，不能用它精确传输任意大整数；此类标识应声明为 STRING。

受理返回持久操作单，202 不表示 KFP 已提交或任务已运行。用户需查询 `GET /api/v1/operations/{oid}` 和运行详情。提交前及执行前检查当前用户权限、空间状态；worker 还检查真实 Profile / namespace 的实例标记及 UID。

### 2.4 运行详情、节点与停止

`runtime` 返回官方原始运行状态，以及节点 taskId、name、state、parentTaskId、startTime、endTime。只返回筛选后的字段，不透传任意官方 API、PodSpec、输入输出和错误体。外部读取结束后再次检查用户授权与运行身份。尚无外部 ID 时返回冲突；KFP 未配置或不可读时明确返回不可用，不把历史记录当作实时结果。

POST `/pipeline-runs/{rid}/stop`：

```json
{"revision":1}
```

停止会生成新 revision 并替代原提交操作。worker 与提交使用同一目标锁，从持久提交日志恢复可能迟到的外部 ID。调用官方 `:terminate` 只是发起取消，只有 GET 确认 CANCELED 才记录 STOPPED。已经自然完成则保留 SUCCEEDED / FAILED，不伪造“取消成功”。

DELETE 是 Loom 本地归档，不调用官方 DeleteRun 或 ArchiveRun。只允许已确认终态且无活动操作的运行归档；归档定义前先归档其所有运行。原始版本、执行证据和外部 KFP 记录保留。

## 3. 执行、恢复与状态同步

pipeline-worker 通过现有 GORM 持久领取、租约、权限复核、实例/revision fence 和回报事务执行，但不走公网请求或机器 HTTP listener。普通 worker 仍只处理原 Profile/访问/Notebook 等任务。

对官方 KFP 使用：

- POST `/apis/v2beta1/runs`：传 namespace、固定 service_account、内嵌 pipeline_spec 和 runtime_config。
- GET `/apis/v2beta1/runs/{run_id}`：同步真实状态及读取节点详情。
- GET `/apis/v2beta1/runs`：按 namespace + 精确 display_name 恢复提交回执。
- POST `/apis/v2beta1/runs/{run_id}:terminate`：取消运行。

官方字段使用 snake_case；IR 内部继续使用官方编译格式，不将业务 camelCase 字段直接透传。

### 提交幂等边界

官方普通 CreateRun 没有 Loom 可用的全局幂等键，不能假设“超时后再 POST”安全。新增 `tb_pipeline_submission`：

| 字段 | 作用 |
| --- | --- |
| instance_id | 每个 Loom Run 的 UUID，提交与停止跨 revision 共用 |
| request_hash | KFP endpoint、运行规格、执行 SA、存储根路径及参数的摘要 |
| run_name | 固定 `loom-<instance UUID>`，与用户可读业务名分离 |
| attempted | POST 前先持久化的提交意图 |
| external_id | 经身份核对后绑定的真实 KFP Run ID |

首次 POST 前写 attempted。之后遇到超时、崩溃或回填失败，只按固定名称查找并核对 namespace / name / description 中的实例和请求摘要，不盲目重新 POST。已绑定外部 ID 不允许更换。外部副作用的 ID 独立落日志，即使提交操作刚被停止替代，停止操作仍可找到它。

查询零结果不能证明没有创建。此时返回 SUBMISSION_UNCERTAIN 并继续查询；重复候选或身份不符明确失败为 IDENTITY_CONFLICT。已知 ID 返回 404 时记录 RESOURCE_NOT_FOUND，不当成停止完成，也不自动重建。

这不是跨系统 exactly-once：如果进程在 attempted 落库后、POST 真正发送前崩溃，或请求被拒且没有 Run，可能一直查不到。管理员需要核查官方 KFP、Workflow、业务操作和提交日志后处理；本轮没有自动清零 attempted、任意绑定 Run ID 或“强制再提交”接口。未排除旧运行前不能用新 Run 掩盖问题，避免重复训练。

配置 hash 固定了执行身份及输入。不要在活动运行中随意切换 endpoint、service_account 或 pipeline_root，也不要同时运行配置不一致的 worker。确需迁移时先完成/停止运行并制定迁移方案。

### 状态映射

| 官方 Run 状态 | Loom observedState |
| --- | --- |
| PENDING | PENDING |
| RUNNING / CANCELING / PAUSED | RUNNING |
| SUCCEEDED / SKIPPED | SUCCEEDED |
| FAILED | FAILED |
| CANCELED | STOPPED |
| 未知值 | UNKNOWN，不推断成功 |

runtime 保留官方原始值，因而能区分 CANCELING / PAUSED；业务状态是较粗粒度投影。运行中暂时回退/未知不会覆盖已确认的 RUNNING。正常轮询只刷新观测时间，状态/原因不变时不反复写完整 IR 历史快照。终态结束操作，非终态持久化下次轮询时间并释放线程。

默认轮询间隔 15 秒，可配置 5–60 秒。这里是 GET 轮询，不是 Loom 新增的 K8s WATCH 或 WebSocket。pipeline-worker 停止后业务状态可能滞后；KFP 不可用不等于任务停止。原 sync 角色不替代此运行同步。

## 4. 配置、身份与部署前提

`configs/config.yaml` 默认关闭内置执行：

```yaml
pipeline:
  enabled: true
  endpoint: http://ml-pipeline.kubeflow.svc.cluster.local:8888
  token_file: /var/run/secrets/kfp/token
  service_account: default-editor
  pipeline_root: s3://actual-bucket/loom/pipelines
  timeout: 15s
  poll_interval: 15s
```

对应环境变量为 `LOOM_PIPELINE_ENABLED`、`LOOM_PIPELINE_ENDPOINT`、`LOOM_PIPELINE_TOKEN_FILE`、`LOOM_PIPELINE_SERVICE_ACCOUNT`、`LOOM_PIPELINE_ROOT`、`LOOM_PIPELINE_TIMEOUT`、`LOOM_PIPELINE_POLL_INTERVAL`。

API 与 pipeline-worker 使用同一配置。API 用于实时详情及限制混用执行器，worker 用于提交/停止/同步。运维另启 `-cmd=pipeline-worker`，并发使用已有 `-worker-concurrency`（1–16）；数据库 max_open 至少为该进程并发 + 2。每轮处理有界，长任务依靠持久状态恢复，不要求一条连接等待训练结束。

两种 SA 不要混淆：

- token_file 是 Loom 访问官方 KFP 的机器身份。应挂载面向 KFP 的 projected token；每次调用重读以支持轮换，不使用 Loom 用户 Access JWT / Refresh，不把 token 写数据库或日志。
- service_account 是 KFP 在目标 namespace 中执行任务的身份，由运维固定配置，用户不能覆盖。pipeline_root 也由运维固定，Loom 追加 namespace / Run UUID，用户不能指定任意产物根路径。

多租户 KFP 需给调用方目标 namespace 的 runs create/get/list/terminate 权限，首次建立 Default Experiment 可能还需 experiments create 权限；按集群的 ServiceAccount use 策略授权运行 SA。Loom 的集群身份需读取 Profile / namespace 以核对归属。实际角色、token audience、网络访问、TLS/服务网格、对象存储凭据、KFP/Argo 安装均由运维落实，本轮未自动创建。

对照的是工作区当前 KFP v2beta1 协议，其中 Run 含 namespace；不是宣称兼容所有历史 KFP 版本。部署前需核对官方返回字段、身份认证及命名空间隔离。响应缺少身份信息时拒绝绑定，不降级为跨空间查询。

上线顺序：停止旧 Pipeline 外部适配器并核对未完运行 → 显式执行新增 `000015_pipeline_kfp.sql`（连同此前未应用迁移）→ 同步部署 API 配置 → 独立部署 pipeline-worker。内置模式占用 pipeline.run.submit / pipeline.run.stop，外部 mTLS 执行器不能再领取这两类动作；Trainer/KServe 继续使用原机器执行桥。无需为内置 KFP worker 开启 executor.enabled。

不要在迁移中直接让内置 worker 接管旧引用型运行或尚在工作的外部适配器。旧任务没有本实现的提交日志和 IR 身份，需人工核验并先收敛，平台不自动猜测外部归属。

## 5. 本轮尚未覆盖

Experiment 管理、向官方 Pipeline/version 目录上传同步、RecurringRun 定时调度、artifact 根输入、平台扩展、多文档包、凭据引用解析、产物下载/MLMD 浏览、节点日志、官方缓存管理接口、暂停恢复、失败节点原地 Retry、前端和集群安装不在此次主线范围。业务运行重跑请提交新 Run，不借操作重试重新启动终态任务。

核心源码：`internal/infra/kfp/client.go`、`internal/service/pipeline_package.go`、`internal/service/pipeline_execution.go`、`internal/worker/pipeline.go`、`internal/config/pipeline.go` 和 `internal/migration/000015_pipeline_kfp.sql`。本轮未改动官方 KFP 引擎、Notebook 生命周期、Trainer/KServe 实际执行逻辑。
