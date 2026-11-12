# 原生 Pipeline v3：循环、条件、缓存与执行生命周期

日期：2026-11-12。本文覆盖当前新增的源码能力，优先于第一版文档中的“不支持”清单。

**这是未运行验证的源码集成，不是已可部署的验收结论。** 本轮按项目约定没有新增/运行测试，没有编译、执行组件、迁移数据库、重启服务或修改集群。仅做源码对照、格式化、Python 语法解析及差异检查；开关继续默认关闭。

## 1. 架构与官方复用

仍然只有一套 Loom API、业务数据库和 operation worker。用户使用官方 KFP Python SDK 编译 Pipeline IR，Loom 解析 IR 并生成原生 Argo Workflow。版本、权限、运行、任务、产物、指标均由现有接口和数据库管理。

复用官方 protobuf schema、Python `executor_main`、OutputPath 参数处理工具及 CEL 表达式环境。新协议为 `loom-native-kfp-v3`，输出为 `loom-python-output-v2`；旧 static-v1、python-v1、kfp-v2 attempt 按原版本恢复。

完整官方 driver/launcher、MLMD 服务及缓存 RPC **没有**接入。它们依赖 KFP 的服务体系；这里保留其 IR/ExecutorInput 约定，对 Argo、Loom 权限和 SQL 做适配。来源和改动记录见 [UPSTREAM.md](../internal/thirdparty/kfp/UPSTREAM.md)。

## 2. 七项能力的源码行为

| 能力 | 实现方式 | 边界 |
| --- | --- | --- |
| 循环 | `dsl.ParallelFor` 的 parameterIterator 转嵌套 DAG + `withParam`，支持常量和上游列表、嵌套循环、参数收集 | 不是 Python 的任意 while；不支持 artifactIterator 或产物列表收集 |
| 条件 | 官方 CEL 环境检查 AST，受限转换为 Argo `when`；不满足条件的节点记 SKIPPED | 支持基本比较、布尔组合、索引和常用类型转换；不支持任意 CEL 函数、`dsl.OneOf` 条件输出合并 |
| 缓存 | 官方 IR 的 enableCache → Loom SQL 条目 → 每次 Workflow 独立 Argo memoization ConfigMap | 仅叶子组件，24 小时 TTL，最多预置 128 条/256 KiB；不是 KFP 缓存库互通 |
| 自动重试 | IR retryPolicy 转 Argo retryStrategy/backoff | 仅叶子组件，最多额外重试 10 次；每次真实 Pod 独立保存，不抹掉失败历史 |
| 目录/多文件产物 | 一个声明的 Artifact 可为普通文件或目录；确定性 tar.gz 传输，保留目录结构 | 不支持 symlink/hardlink/设备文件；不是 `List[Artifact]`；每个产物最多 10000 项、10 GiB |
| 在线停止 | 现有 stop 操作持久化令牌后，UID/RV 条件 PATCH `spec.shutdown=Stop` | 必须启动已确认；不是强删 Pod/CR；观测终态并完成归档后才完成 stop 操作 |
| 自动日志归档 | Argo executor 上传 main 日志到固定 S3/MinIO 前缀，Loom 读取归档 | API/worker 必须配置归档；不在组件里传存储密钥，不额外部署日志服务 |

目录内部可含多个文件，组件可声明多个独立输出 Artifact；这两种都不等于 KFP artifact list。

## 3. 任务身份与状态同步

Informer 只负责投递受管 Workflow 的队列 key；worker 在现有目标锁下执行实时 GET，再核验冻结规格和 namespace/Profile/Workflow UID。并非为每个任务启动一条独立长连接。

v3 区分静态定义与实际执行实例。循环 TaskGroup、每次迭代 DAG、Retry 包装及每次 Pod 都登记为任务实例。运行时身份来自 Argo 实际 node ID、边界和父子关系，不从展示名称或 loop 值猜 ID。

同一事务中先登记真实 Pod 输出，再登记缓存命中，最后登记由内到外的 DAG/Retry 输出别名；别名复用原产物 ID，不重复登记同一个文件。随后解析实际输入参数与产物关联。循环值、重试序号和缓存来源都进入终态证据。

Argo 状态节点最多 4000 个，静态任务定义最多 128 个，嵌套深度最多 16。状态压缩/offload 暂不支持，不会把不完整状态当作成功。Workflow 仍有 512 KiB 规格上限，实际可容纳任务数也受组件代码大小影响。

## 4. 缓存数据与一致性

新增迁移 `000024_pipeline_native_cache.sql`，只增加一张 `tb_pipeline_task_cache`。主要字段为业务 group/pipeline/namespaceUID、fingerprint、source_task_id、source_instance_id、argo_node_id、outputs、created_at、expires_at。任务表已有 cache_source_task_id，无需再为循环、重试、停止、日志各建一张表。

缓存指纹覆盖编译/输出协议、镜像、command/args、输入输出类型，以及已解析的输入参数和产物摘要/元数据；不使用输出存储路径。它借鉴官方 KFP cacheutils 的身份构成，但不是字节兼容指纹。

成功的真实执行才发布缓存。跨运行只预置同一项目、Pipeline、不可变 namespace UID 范围内的未过期条目。Workflow 的 ConfigMap 是短期执行投影，所有权绑定 Workflow UID；权威数据仍在 Loom SQL。

同一 Workflow 内相同指纹以 Argo mutex 串行检查缓存，避免相同输入的并发循环项重复执行和覆盖来源。命中后核对冻结条目/当前运行条目、实际输出 bundle、来源任务成功与输出封存状态，再复用原产物 ID。缓存命中节点可能没有 Pod、开始时间或日志，不能为其制造日志。

长运行期间缓存 TTL 可能到期并重新执行；新的命中允许引用本次运行重新发布的条目。已经确认的命中保存来源任务和实际输出摘要，后续观测不跟随可变缓存条目改绑。产物别名和输入血缘同时核对存储 endpoint/bucket/key、类型、摘要、大小、目录格式及元数据。

镜像 tag 可变、外部服务/时间/随机数等隐藏输入会导致缓存过时；需要固定镜像 digest，或在 SDK 使用 `.set_caching_options(False)`。缓存并不保证副作用去重。

对象存储必须至少保留缓存 TTL 和相关运行需要的产物；清理 Workflow 不会清理产物。Loom 目前没有后台对象修复、缓存失效 API或独立缓存清理服务。对象被外部提前删除时，后续下载/消费会失败，不能伪装缓存可用。

## 5. 停止与自然完成竞态

stop 不直接宣告 Run=STOPPED。先在现有 operation/lease/权限事务中记录 stop token，再对原 Workflow UID 和 resourceVersion 做条件更新。worker 重试复用同一令牌。

Argo 完成 shutdown 后，受授权的停止终态映射为 STOPPED；同时自然成功则保留 SUCCEEDED。已经自然失败的执行保留 FAILED。保存实际任务终态、产物和日志证据后，才推进 stop receipt、Run.applied_revision 和审计。尚未启动确认的运行返回冲突，不能把暂停的 Workflow 意外放开运行。

## 6. 自动日志归档配置与权限

API 与 `pipeline-worker` 使用一致配置：

```yaml
pipeline:
  native_enabled: true
  native_observer_enabled: true
  native_service_account: loom-pipeline-task
  native_artifact_secret: loom-native-artifacts
  native_log_archive_secret: loom-native-logs
log_archive:
  enabled: true
  format: argo
  endpoint: http://minio.kubeflow.svc.cluster.local:9000
  bucket: mlpipeline
  prefix: logs
  region: us-east-1
  timeout: 15s
```

以上是节选，其余资源、S3 endpoint、桶、并发等沿用配置模板。后端 `LOOM_LOG_ARCHIVE_ACCESS_KEY/SECRET_KEY` 是只读账号；业务 namespace 中 `loom-native-logs` Secret 的 `accessKey/secretKey` 是 Argo 写账号。不要使用 root 凭据。可通过 `LOOM_PIPELINE_NATIVE_LOG_ARCHIVE_SECRET` 指定名称。

日志路径为 `<prefix>/<cluster>/<namespace>/<instanceUUID>/<podName>/main.log`。终态完成前检查实际执行 Pod 的归档是否存在；对于已失败 Pod，只有 kubelet 状态明确证明 main 从未启动时才记录 `MAIN_NEVER_STARTED` 并豁免，不伪造日志。缺少 Pod 或容器状态本身不是这项豁免。存储暂不可用则 operation 保持等待，不丢证据或伪报完成。大于 32 MiB 的日志不支持当前在线归档读取接口。

权限分工：

- Loom pipeline-worker：Workflow create/get/list/watch/patch/delete，ConfigMap get/create；API 不新增缓存写权限。
- Argo controller：业务 namespace 的 ConfigMap get/create/update/patch，以及受管存储 Secret 的读取；参见独立示例。
- 组件执行 SA：只保留 workflowtaskresults create/patch，不授予 ConfigMap 写权限。
- 每个循环声明的 parallelism 使用 Workflow 独立 semaphore，控制整个迭代 DAG 并发；并受全局 Pod parallelism 上限约束。

Secret、Argo namespace/SA 名称必须由运维核实。此轮仅更新安装源码/示例，没有在集群中授予权限。

## 7. 仍明确拒绝的范围

这不是完整 KFP 兼容声明：不支持 importer/exit handler、`dsl.OneOf`、根产物输入/根输出、artifactIterator/Artifact List、任意 PodSpec/GPU/自定义 env/resources 和任意 CEL。LIST/STRUCT 参数为有界 JSON；单个参数最多 8 KiB、列表最多 128 项。

文件/目录传输上限与在线下载上限不同：组件间最多 10 GiB，当前 API 在线下载仍为 32 MiB；目录下载返回 tar.gz，不直接预览目录。不代表 HTML/ROC/混淆矩阵等可视化已全量实现；现有数值指标读取保留。

## 8. 后续授权运行时应验收的组合

本轮没有执行以下步骤。运行验收应至少覆盖：普通文件和目录上下游、标量/对象循环及嵌套、条件 SKIPPED、重试失败后成功和重试耗尽、同运行与跨运行缓存、缓存与重试组合、运行中停止及自然成功竞态、worker 重启恢复、Pod 删除后的日志归档读取、跨 namespace UID 的拒绝。

升级需显式执行已有 22/23 及新 24 迁移，再部署一致版本 API/worker、核实 Argo v4.1.x 协议和 RBAC、存储读写权限，最后按环境启用开关。没有运行验收前，不应把“源码入口齐全”称为“七项已在集群可用”。
