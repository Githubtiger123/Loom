# Loom Pipeline：官方 KFP 接入主线

本文对应当前后端源码。按项目约定没有新增测试，没有测试、编译、运行、迁移或连接真实 KFP；以下是实现契约，不是联调结果。

## 1. 分工与官方模型

复用本工作区 `pipelines/backend/api/v2beta1/run.proto` 的官方 HTTP 接口，不在 Loom 内重新实现 DAG 调度器，不由 Loom 直接创建 Argo Workflow，也不执行用户提交的 Python。

流程为：Python KFP SDK 编译 IR → 导入 Loom 版本 → 用户填写参数并提交 Run → Loom 持久操作 → pipeline-worker 调用官方 KFP → 官方编译与调度工作负载 → 官方 persistence agent 回写 KFP → Loom 轮询 KFP 并同步业务数据库。

版本现支持原地编辑；运行支持“停止并清理旧实例 → 应用编辑 → 用户手动重建”，详见 [编辑与重建契约](pipeline-edit.md)。版本 ID 保持不变，revision 与历史内容保留；执行快照仍独立保存。

| 概念 | Loom 的职责 | 官方 KFP 的职责 |
| --- | --- | --- |
| 空间 | Group 对应已确认的 Profile / namespace，Owner 仍固定为基础设施 admin | 按 namespace 隔离和校验运行权限 |
| 流水线 | 保存业务名称、负责人、可编辑 IR 版本及修订历史 | 接收 Run 中的内嵌 `pipeline_spec`；本轮不向 KFP Pipeline/version 目录重复上传 |
| 参数 | 从 IR 根参数定义提取类型和默认值，保存运行快照 | 按原生类型绑定到真实流程 |
| Experiment | 本轮不提供 Experiment 管理接口 | 未传 experiment_id，使用当前 namespace 的 Default Experiment |
| 运行 | 业务 ID、期望/观测状态、权限、幂等、操作单、历史及本地查询投影 | Run/任务状态、实际执行、取消与运行记录删除 |
| 状态同步 | 独立 pipeline-worker 轮询官方 Run API | 官方 persistence agent 同步执行引擎状态 |

物理上仍然是两套持久化：KFP 的数据库只服务执行引擎，Loom MySQL 保存用户、组、权限、Pipeline 业务关系及运行状态投影。用户只登录 Loom，Loom 用机器 ServiceAccount 调 KFP；KFP 不承担第二套平台用户系统。列表、按用户/组/定义筛选、统计和业务联表全部查询 Loom MySQL，不在请求时跨库 JOIN KFP；只有 `runtime` 详情实时读取 KFP。轮询允许短暂延迟，但把 KFP 状态持续写回 `tb_pipeline_run`，所以常规查询不会受两库联表限制。

IR 中的组件可以创建容器、网络访问和读写数据；格式校验不是代码安全沙箱。仍需运维在 namespace、ServiceAccount、网络、准入和存储侧限制真实权限。

## 2. 业务接口与权限

业务路径仍集中在 `internal/route/route.go`，前缀为 `/api/v1/groups/{gid}`，使用已有平台认证和 `pipeline.*` 权限。查询他人对象还需相应 `read.any` 权限。写入需要 `Idempotency-Key`，变更已有对象需要当前 revision。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET / POST | `/pipelines` | 列出 / 创建业务定义 |
| GET / DELETE | `/pipelines/{pid}` | 详情 / 本地归档 |
| GET / POST | `/pipelines/{pid}/versions` | 列出 / 导入版本 |
| PUT | `/pipelines/{pid}/versions/{vid}` | 原地编辑版本 |
| PUT | `/pipeline-runs/{rid}` | 清理旧实例后应用编辑，等待启动 |
| POST | `/pipeline-runs/{rid}/start` | 使用新执行 ID 手动重建 |
| GET | `/pipelines/{pid}/versions/{vid}` | 查看版本与参数定义 |
| GET / POST | `/pipelines/{pid}/runs` | 定义下运行 / 从版本提交运行 |
| GET | `/pipeline-runs` | 空间运行列表 |
| GET / DELETE | `/pipeline-runs/{rid}` | 运行详情 / 异步停止并删除 KFP Run |
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
5. 保存版本和首个修订快照，递增父定义 revision。已有运行不跟随后续版本变化。

这是导入入口校验，不是完整的官方 protobuf/编译器验证。DAG 引用、组件、镜像及执行语义最终仍由官方 KFP 校验。暂不支持根 artifact 输入、带第二文档 Kubernetes 扩展的包、v1 Argo YAML 或直接运行 Python 源码。

内置 KFP 启用后，不能只提交 packageRef / digest 来创建版本；旧版引用型版本保留可读，但必须重新导入 IR 后才能由内置 worker 运行。关闭内置 KFP 时，旧引用协议仍为独立外部适配器保留，并不表示平台已实现这些引用的解析器。

### 2.3 提交运行

POST `/pipelines/{pid}/runs`：

```json
{"name":"daily-train-001","versionId":1,"parameters":[{"name":"epochs","value":"10"}]}
```

从版本获取权威参数定义，拒绝未知参数、缺失必填值和类型错误，补齐默认值。版本 IR、摘要和已绑定参数一起复制进运行快照；不热修改正在执行的 KFP Run。编辑业务运行需先清理旧实例，保存后由用户手动启动新的 KFP Run。

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

DELETE 会生成 `DELETED` 新 revision，并可替代尚未完成的提交或停止操作。若提交尚未发起，worker 直接确认不存在；若 CreateRun 回执丢失，则按确定性 Run ID 恢复或幂等重放同一请求，避免迟到 Run 变成孤儿；若 Run 已存在则先请求 terminate，轮询到终态后调用官方 DeleteRun，再以 GET 404 确认不存在。只有确认不存在后才把 Loom 运行标记为 `DELETED` 并写入 `deletedAt`。因此支持“创建后立即删除”，但 DELETE 返回的 202 仍只是持久受理，前端必须查询操作单。

删除运行保留 Loom 的版本快照、操作、审计和状态历史，但删除 KFP 的 Run 业务记录。活动 Run 已在此前确认进入终态；关联 Workflow 的最终清理由官方 DeleteRun/TTL/GC 负责，Loom 不绕过 KFP 直接删除 Argo 资源。归档 Pipeline 定义前，仍需先删除其所有未删除运行。

## 3. 执行、恢复与状态同步

pipeline-worker 通过现有 GORM 持久领取、租约、权限复核、实例/revision fence 和回报事务执行，但不走公网请求或机器 HTTP listener。普通 worker 仍只处理原 Profile/访问/Notebook 等任务。

对官方 KFP 使用：

- POST `/apis/v2beta1/runs`：传 namespace、固定 service_account、内嵌 pipeline_spec 和 runtime_config。
- GET `/apis/v2beta1/runs/{run_id}`：同步真实状态及读取节点详情。
- POST `/apis/v2beta1/runs/{run_id}:terminate`：取消运行。
- DELETE `/apis/v2beta1/runs/{run_id}`：删除已终止/终态运行，再用 GET 404 确认不存在。

官方字段使用 snake_case；IR 内部继续使用官方编译格式，不将业务 camelCase 字段直接透传。

### 提交幂等与兼容边界

Loom 将每个 KFP `run_id` 固定为对应的 Loom `pipeline_run.instance_id`。本工作区配套的 KFP 源码会保留客户端提供的 Run ID，并在相同 ID 已存在时返回/校验已有运行；因此 CreateRun 响应丢失后，可以先 GET 该 ID，404 时再幂等重放完全相同的 CreateRun。这里是当前配套 KFP 版本的明确兼容契约，不宣称所有上游或历史 KFP 版本都支持客户端指定 `run_id`；部署必须固定并验证兼容版本。

`tb_pipeline_submission` 保存跨 Loom 数据库和 KFP 调用的恢复证据：

| 字段 | 作用 |
| --- | --- |
| instance_id | 每个 Loom Run 的 UUID，提交与停止跨 revision 共用 |
| request_hash | KFP endpoint、运行规格、执行 SA、存储根路径及参数的摘要 |
| run_name | 固定 `loom-<instance UUID>`，与用户可读业务名分离 |
| attempted | POST 前先持久化的提交意图 |
| external_id | 经身份核对后绑定的 KFP Run ID，必须等于 instance_id |

首次 POST 前写 `attempted`。之后遇到超时、崩溃或业务库回填失败，worker 只读取确定性 ID，必要时重放同 ID、同请求摘要的 CreateRun，并核对 run_id、namespace、name、description。已绑定 ID 永不更换。即使原提交操作刚被删除意图替代，删除 worker 也能从日志找回并清理该 Run。

已绑定 Run 在提交动作中返回 404 时不会重建，避免把已执行过的历史任务再次执行；停止动作可把已绑定但确认不存在的 Run 收敛为 STOPPED；删除动作则收敛为 DELETED。未绑定但 `attempted=true` 的停止/删除会先按确定性 ID 恢复，必要时幂等重放创建，再终止/删除，从而封闭“请求已到 KFP、响应却丢失”的孤儿窗口。

这仍不是数据库与 KFP 的分布式事务。安全性依赖兼容 KFP 对客户端 Run ID 的保留和同 ID 重放语义，也依赖所有 Pipeline 副作用都经过同一目标锁和提交日志。若更换为不满足该契约的 KFP，必须停用 worker，不能退回按 display_name 猜测或生成随机 ID。

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

命令操作本身的轮询间隔默认为 15 秒，可配置 5–60 秒；此外，pipeline-worker 内置独立状态同步协程，不依赖操作单存活、创建者仍有权限或用户打开页面。原 sync 角色不替代此运行同步。

### 独立状态同步与开销

在第一次尝试 POST 前，提交日志的 `attempted` 和运行的 `sync_enabled` 在同一事务写入。同步器仅领取 Loom 已尝试提交、尚未逻辑删除的 Run，按持久化 `sync_next_at` 索引逐个调度；不拉取 KFP 全集群运行，也不扫描版本/Pipeline 目录。回执丢失也会按确定性 ID 只读查询，不创建或停止资源。

- 未结束运行默认每 5 秒观测，终态默认每 5 分钟对账；stop/delete 尚未完成时保持快速观测。增加最多 20% 抖动，避免集中请求。
- 默认两个独立同步协程，共享每进程 10 次/秒的领取/观测限速；无每 Run 常驻 goroutine，积压时实际延迟增加。命令执行的 GET/POST/DELETE 不计入这个限额，多副本总限额约为副本数乘以 sync_qps。
- 采用 60 秒数据库观测租约、SKIP LOCKED 和与命令执行器共用的目标锁，支持多副本与重启恢复；请求位于事务外，写回重新检查空间实例/UID、Run 身份、revision 和租约。
- 观测不要求原用户仍有权限，不推进启动/清理意图，不结束或重开操作单。原始 RUNNING 提交可据已核验的 Run 记录 applied_revision；stop/delete 完成仍由执行器确认。
- 超时、404、机器凭据被拒或身份冲突均保留最后确认状态，写独立同步原因；连续故障按 15 秒起指数退避，最高 5 分钟再加抖动。不会仅因一次 404 逻辑删除运行。
- 成功响应保存 `kfpState/kfpSyncedAt`，每次完成观测保存 `kfpSyncAttemptedAt/kfpSyncReason`。原始 KFP 状态可反映外部 Retry 等变化；未知状态或 RUNNING 暂退 PENDING 时保留上次业务状态。终态后 KFP 被外部修改也会在下一次对账更新现实，操作回执保持历史证据。
- 只在实际业务状态变化时写历史，不在每次成功轮询追加完整 IR。已逻辑删除的 Loom Run 不再持续对账；未删除历史运行一直进行低频观测。

目标请求量约为 `活动数 / 活动间隔秒 + 历史终态数 / 终态间隔秒`，还需加上命令执行器请求。例如 100 个活动 Run、1,000 个终态 Run，对应约 23.3 次/秒；默认同步限速为 10 次/秒时会排队，不能保证全部达到目标间隔。逐个 GET 仍会带回 KFP 的运行/任务详情，实际负载还取决于节点数量、响应体、KFP 数据库和网络延迟，未做压测，不承诺容量。规模增加可延长终态间隔、调整并发/QPS，或后续增加轻量批量状态 API/事件订阅。

这里是持续 GET 轮询，不是 K8s WATCH/WebSocket。KFP 和 worker 的采集/排队意味着最终一致，不保证瞬时同步；KFP 不可用不等于任务停止，应结合同步时间与原因判断新鲜度。

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
  sync_interval: 5s
  terminal_sync_interval: 5m
  sync_concurrency: 2
  sync_qps: 10
```

对应环境变量为 `LOOM_PIPELINE_ENABLED`、`LOOM_PIPELINE_ENDPOINT`、`LOOM_PIPELINE_TOKEN_FILE`、`LOOM_PIPELINE_SERVICE_ACCOUNT`、`LOOM_PIPELINE_ROOT`、`LOOM_PIPELINE_TIMEOUT`、`LOOM_PIPELINE_POLL_INTERVAL`。

独立同步配置对应 `LOOM_PIPELINE_SYNC_INTERVAL`、`LOOM_PIPELINE_TERMINAL_SYNC_INTERVAL`、`LOOM_PIPELINE_SYNC_CONCURRENCY`、`LOOM_PIPELINE_SYNC_QPS`。活动间隔允许 5–60 秒，终态间隔允许 1 分钟–24 小时，同步并发 1–16，每进程 QPS 1–100。

API 与 pipeline-worker 使用同一配置。API 用于实时详情及限制混用执行器，worker 用于提交、停止、删除和独立状态同步。运维另启 `-cmd=pipeline-worker`，执行并发使用已有 `-worker-concurrency`（1–16）；数据库 max_open 至少为执行并发 + sync_concurrency + 2。每轮处理有界，长任务依靠持久状态恢复，不要求一条连接等待训练结束。

两种 SA 不要混淆：

- token_file 是 Loom 访问官方 KFP 的机器身份。应挂载面向 KFP 的 projected token；每次调用重读以支持轮换，不使用 Loom 用户 Access JWT / Refresh，不把 token 写数据库或日志。
- service_account 是 KFP 在目标 namespace 中执行任务的身份，由运维固定配置，用户不能覆盖。pipeline_root 也由运维固定，Loom 追加 namespace / Run UUID，用户不能指定任意产物根路径。

多租户 KFP 需给调用方目标 namespace 的 runs create/get/terminate/delete 权限，首次建立 Default Experiment 可能还需 experiments create 权限；按集群的 ServiceAccount use 策略授权运行 SA。Loom 的集群身份需读取 Profile / namespace 以核对归属。实际角色、token audience、网络访问、TLS/服务网格、对象存储凭据、KFP/Argo 安装均由运维落实，本轮未自动创建。

对照的是工作区当前 KFP v2beta1 协议，其中 Run 含 namespace；不是宣称兼容所有历史 KFP 版本。部署前需核对官方返回字段、身份认证及命名空间隔离。响应缺少身份信息时拒绝绑定，不降级为跨空间查询。

上线顺序：停止旧 Pipeline 写入者并核对未完运行 → 确认部署的 KFP 支持客户端 `run_id` 与同 ID 幂等恢复 → 按顺序显式执行 `000015_pipeline_kfp.sql`、`000016_pipeline_sync.sql`、`000017_pipeline_edit.sql`（连同此前未应用迁移）→ 同步升级 API 与独立 pipeline-worker。内置模式占用 pipeline.run.submit / pipeline.run.stop / pipeline.run.delete / pipeline.run.edit，外部 mTLS 执行器不能再领取这四类动作；Trainer/KServe 继续使用原机器执行桥。无需为内置 KFP worker 开启 executor.enabled。

独立观测还需先显式应用 `000016_pipeline_sync.sql`，新增调度索引、观测租约及同步新鲜度字段；未执行迁移，不自动建表或回填未知外部归属。

不要在迁移中直接让内置 worker 接管旧引用型运行或尚在工作的外部适配器。旧任务没有本实现的提交日志和 IR 身份，需人工核验并先收敛，平台不自动猜测外部归属。

## 5. 本轮尚未覆盖

Experiment 管理、向官方 Pipeline/version 目录上传同步、RecurringRun 定时调度、artifact 根输入、平台扩展、多文档包、凭据引用解析、产物下载/MLMD 浏览、官方缓存管理接口、无损暂停恢复、失败节点原地 Retry、前端和集群安装不在此次主线范围。在线节点日志已独立接入 Kubernetes pods/log，需显式应用 000018_pipeline_logs.sql，详见 [公共日志组件](pod-logs.md)。[离线日志](log-archives.md) 已补 MinIO 按需读取接口，复用该权限，不增加日志索引表；采集端需先上传约定格式，编辑/删除不等待归档完成。可提交新业务运行，也可编辑清理后手动重建；不能借操作重试复用终态 KFP ID。

核心源码：`internal/infra/kfp/client.go`、`internal/service/pipeline_package.go`、`internal/service/pipeline_execution.go`、`internal/service/pipeline_sync.go`、`internal/worker/pipeline.go`、`internal/config/pipeline.go` 和 `internal/migration/000015_pipeline_kfp.sql`、`000016_pipeline_sync.sql`。本轮未改动官方 KFP 引擎、Notebook 生命周期、Trainer/KServe 实际执行逻辑。
