# Loom Pipeline 改造方案：保留 KFP SDK／IR，统一 API、数据库与 Argo 执行

最新补充：Python 第一版源码已加入公开 engine 选择、同一 worker 执行与原生 DAG/日志/指标/文件查询。最新范围见 [第一版说明](pipeline-native-python-v1.md)，未编译、未实机验收；下文长期替代目标不是第一版承诺。

状态：设计及分阶段实施中。日期：2026-11-11。

官方优先复用的最新源码调整见 [官方复用改造](pipeline-native-official-reuse.md)：新协议接入官方 IR 类型、组件 Visitor 与参数输出工具；完整 driver/launcher 尚未移植，不等于已完成全部 KFP 兼容。

本文基于当前 Loom 和本工作区 KFP 源码进行静态分析；原生公开运行入口／IR runtime 尚未接入，没有运行测试、编译、迁移、集群任务或卸载操作。核心数据层、可选 Workflow Informer、静态预检、内部暂停暂存、静态真实节点绑定、受控首次启动、静态终态归档／操作收尾及已归档实例的显式删除／缺失观测源码已补充，准确边界见精简表设计第 12–19 节；其余建议不表示已经存在或已部署。

## 1. 目标与结论

目标不是给 KFP 换皮，也不是仅删除 KFP UI，而是将流水线管理和执行服务端收进 Loom：

- 保留官方 KFP Python DSL、组件定义和 Python → Pipeline IR 的编译体验。
- 对用户只提供 Loom 的登录、API、权限、页面和业务数据库。
- Loom 接收 IR，生成 Argo Workflow，直接通过 Kubernetes API 管理执行。
- 将任务输入输出、产物、指标、缓存、血缘、运行历史和定时任务纳入 Loom。
- 最终不依赖独立 KFP API Server、KFP 业务库或 MLMD 服务。
- 保留 Argo Controller、Kubernetes 和 MinIO 等基础设施，不重写 Kubernetes 调度器或 Argo DAG 调度器。

结论：技术上可行，但属于“兼容 KFP IR 的流水线执行平台”建设，不是更换一个 HTTP Client。SDK 编译成功不等于 Loom 已支持 IR 的全部运行时语义，所有兼容能力都必须有明确边界和后续验收证据。

### 1.1 “一套 API、一套数据库”的准确含义

统一的是产品 API、认证授权、数据模型、迁移和权威业务记录，不要求所有功能只能运行在一个操作系统进程中。

建议仍保留 API、pipeline-worker、cron 等运行角色，共享代码和 Loom MySQL；任务容器中的受管执行助手可以调用 Loom 的内部运行时接口。这些接口不构成第二套用户系统，不公开给浏览器，也不直接持有数据库连接。

“一个数据库”不意味着一份存储解决所有问题：

- Loom MySQL：业务对象、执行元数据、产物索引、缓存索引和历史。
- Kubernetes etcd：Argo CR 和活动集群对象。
- MinIO：IR／执行计划归档、大文件、模型、报告、日志。
- 若启用 Argo 大工作流节点状态 offload 或 archive，其存储也是显式基础设施依赖。可使用同一 MySQL 实例内的隔离 schema／账号，但不能把它与 Loom 表混写，或宣称没有额外存储依赖。

最终不允许在后台偷偷保留 KFP／MLMD 自有业务表，再以“共用一台 MySQL”宣称已经完成统一。

### 1.2 兼容性分为四层

| 层次 | 目标 |
| --- | --- |
| 编写兼容 | 使用锁定版本的 `kfp.dsl`、组件和编译器生成 IR |
| 执行兼容 | 在明确的版本／功能范围内，参数、产物、条件、循环、缓存、重试等行为一致 |
| 产品能力对齐 | 提供相应的运行管理、实验、可视化、对比、血缘等能力，界面不必逐像素复制 |
| KFP HTTP API／Client 兼容 | 默认不承诺；`kfp.Client().create_run...` 不会自动变成 Loom API 调用 |

需要额外提供薄的 Loom Python Client，完成“编译 → 上传 → 创建运行 → 查询”，复用 KFP DSL 而非另造 DSL。若以后要求原 `kfp.Client` 无修改可用，必须单列兼容 API 项目，不能与 IR 兼容混为一谈。

## 2. 当前基础与依赖现状

当前主线见 [官方 KFP 接入说明](pipeline-kfp.md)。业务运行链为：

```text
KFP Python SDK → IR → Loom API／MySQL → pipeline-worker
                                            ↓
                                      KFP API／数据库
                                            ↓
                                      Argo Workflow → Pod
                                            ↓
                                      MLMD／MinIO
```

### 2.1 可以复用的 Loom 能力

| 已有代码／数据 | 当前职责 | 改造方向 |
| --- | --- | --- |
| `internal/route/route.go`、handler | 统一路由、认证和接口 | 保持入口，不增设独立 Pipeline 用户 API 系统 |
| Pipeline／Version／Run 模型 | 定义、版本、权限、运行配置 | 保留业务 ID，增加引擎和执行绑定 |
| 版本修订、Run attempt 快照 | 编辑历史和重建记录 | 保留并补齐运行时历史，不覆盖旧快照 |
| 持久操作、租约、目标锁、fence | 并发、幂等和崩溃恢复 | 保留；将 KFP 副作用替换为受控 Argo 副作用 |
| `pipeline_package.go` | 单文档 IR 导入、摘要、根参数提取 | 扩展为版本化、完整的能力验证入口 |
| `pipeline_graph.go` | 从 IR 提取静态图 | 复用展示逻辑，不直接当作执行编译器 |
| `pipeline_execution.go`、`pipeline_sync.go` | KFP 调用与轮询 | 抽离引擎适配，新增 Argo 执行和观测 |
| `pipeline_dag.go`、`pipeline_observation.go` | KFP／MLMD 详情映射 | 改为引擎中立的节点／产物查询 |
| `infra/pipelineviz`、`infra/artifactstore` | 指标解析、报告／文件读取 | 复用安全读取与解析，替换 MLMD 数据来源 |
| Pipeline 日志、终端、归档 | 授权和执行归属校验 | 保留公共能力，重做与 KFP ID／launcher 的绑定 |
| MinIO 空间账户和初始化 hook | 空间凭据、对象存储配置 | 在删 Metacontroller 前迁移其真实职责 |

目前前端主要是定义／版本等基础页面，不能把后端已有 DAG／可视化接口等同于完整前端已交付。图编辑、运行 DAG、图表、比较和血缘页面均需按实际源码补齐。

### 2.2 必须纠正的依赖理解

`Workflow` 属于 Argo，不属于 KFP。`ScheduledWorkflow`、`Viewer` 在当前安装方案中由 KFP 相关控制器处理，`DecoratorController` 属于 Metacontroller。它们不是完整的 KFP 功能数据库，也不是保留定义后便会自动工作的能力。

此外，不能将聊天中列举的几个 CRD 当成所有 KFP 版本的全集：本工作区 KFP 源码还包含 `PipelineVersion` CRD 及 webhook。实施前应按锁定版本与真实集群做资源清单，不笼统断言“KFP 只有两个 CRD”。本轮没有重新查询集群。

### 2.3 上游复用基线

静态参考工作区 `pipelines/`：

- `VERSION` 和 `sdk/python/kfp/version.py` 当前均为 `2.18.0`。
- 本次阅读时 Git HEAD 为 `555bfa60a70da88c594960bdacfd78fa7c061043`。
- `api/v2alpha1/pipeline_spec.proto`：PipelineSpec、ExecutorInput／Output 等协议。
- `backend/src/v2/compiler/argocompiler/`：IR → Argo 的现有实现。
- `backend/src/v2/driver/`、`component/launcher_v2.go`：运行时求值、组件启动、产物与元数据处理。
- `backend/src/v2/expression/`、`metadata/`、`cacheutils/`、`objectstore/`：表达式、元数据、缓存、存储依赖。

这些路径是复用和依赖分析的参考，不表示可以原样链接后删除其他服务。上游编译器会生成依赖 driver／launcher 的 Workflow，而这些程序又依赖 MLMD、缓存接口及集群配置。只把编译函数搬到 Loom 内，无法实现最终目标。

正式基线还须记录 SDK wheel、protobuf schema、Kubernetes 扩展包、Argo Controller／argoexec、运行时助手和镜像 digest，不能仅锁一个 `schemaVersion` 或 tag。

## 3. 目标架构与权威数据边界

```text
用户 Python／Notebook／CI
      │ KFP SDK 编译；Loom Client 上传 IR
      ▼
Loom API ───────────────────── Loom MySQL
      │ 受理、版本、权限、持久操作      │
      ▼                               │
pipeline-worker                       │
  验证 IR → 编译执行计划 → 提交 Workflow│
      │                               │
      ▼                               │
Kubernetes／Argo Controller            │
      │                               │
      ▼                               │
driver／launcher 受管助手 ─→ Loom 内部运行时接口
      │                               │
      ▼                               │
用户组件容器 ←→ MinIO                 │
                                      │
Workflow／Pod 观测 ─→ 运行与节点投影 ──┘
                                      │
前端 ←── DAG、日志、指标、产物、血缘 ───┘
```

职责边界：

1. Loom 数据库权威保存用户意图、版本快照、执行绑定、已接收的元数据和历史。
2. Argo／Pod 是当前集群执行事实来源，数据库投影必须标记观测时间和来源。
3. MinIO 是文件字节的事实来源，数据库里的 URI 不证明文件完整或存在。
4. 任务声明的指标和产物属性属于任务数据，不是可信权限、计费或执行成功证明。
5. API 不运行用户 Python。需要在线 Python 编译服务时，必须另做隔离执行方案；初期只接收已编译 IR。

## 4. 功能迁移清单

以下为完整建设范围；“首批交付”只是阶段安排，不能用首批功能代替最终功能对齐。

| 功能域 | 当前／KFP 来源 | 目标与迁移要点 | 建议阶段 |
| --- | --- | --- | --- |
| Python DSL／组件 | KFP SDK | 保留；增加 Loom 上传／运行 Client | P1–P2 |
| Pipeline IR 导入 | Loom 基础校验，KFP 执行校验 | 类型化解析、版本能力报告、禁止静默丢字段 | P1 |
| 定义／版本／修订 | Loom 已有 | 保留；增加兼容报告、编译产物摘要 | P1–P2 |
| 实验／运行归档 | KFP；Loom 尚不完整 | Loom 实验、分组、归档、过滤、对比 | P4 |
| 提交／停止／删除／重建 | Loom → KFP | Loom → Argo，保留幂等和新旧 instance 隔离 | P2 |
| DAG／条件／循环／嵌套 | KFP 编译器与运行时 | 在 Argo 上完整落实 KFP 语义 | P2–P3 |
| 参数输入输出 | SDK executor＋KFP 运行时 | 保留类型、缺省／可选语义和运行解析值 | P2–P3 |
| Artifact 输入输出／Importer | KFP launcher、MLMD、对象存储 | 自有元数据、上传／下载、发布和引用核验 | P2–P3 |
| 重试／退出处理 | KFP＋Argo | 区分助手重试、组件重试、清理和取消 | P3 |
| 缓存 | KFP cache／MLMD | Loom 缓存索引、命中、引用、失效和隔离 | P3 |
| 元数据／血缘 | MLMD | Loom 任务实例、产物和输入输出事件 | P2–P4 |
| DAG 与节点详情 | KFP／MLMD → Loom | 数据源换为统一执行／元数据模型 | P2–P4 |
| 指标／报告／文件预览 | MLMD＋MinIO＋Loom 部分接口 | 指标、报告、安全渲染、下载、历史读取 | P4 |
| 运行比较／可视化页面 | KFP UI；Loom 尚未完整 | Loom 图表、比较、产物详情与血缘页面 | P4 |
| 在线／归档日志、终端 | Loom 已有 | 重做 Argo UID／节点／容器归属绑定 | P2–P4 |
| 定时运行 | KFP ScheduledWorkflow | Loom 调度表＋持久触发，不依赖 KFP 调度器 | P4 |
| 空间存储初始化 | KFP Profile hook／Metacontroller | Loom 维护等价初始化、轮换和清理流程 | P5 |
| 节点大规模状态／归档 | Argo 配置及 KFP 记录 | 明确 offload、历史落库和 GC 顺序 | P3–P5 |
| SDK 平台扩展 | KFP Kubernetes 插件 | 按功能开放 PVC、资源、环境变量等，统一准入 | P3–P5 |

TensorBoard、TFDV／TFMA、旧版 UI metadata、第三方可视化插件应逐项纳入能力清单：是否属于锁定版本的内置能力、需要哪个运行服务、如何鉴权、保留什么行为，均不能简单写成“所有可视化均支持”。需要时独立设里程碑，不能因为普通指标页面完成就宣布全部覆盖。

## 5. IR 验证、编译与运行时方案

### 5.1 推荐复用策略

采用“官方协议与算法复用＋Loom 服务端适配”，不从零重新发明 IR，也不原封不动复制整个 KFP API Server。

实施顺序：

1. 锁定官方 proto 和 SDK，建立 IR 能力清单及语义说明。
2. 以现有 argocompiler、expression、driver、launcher 为参考，列出每个服务调用和读写副作用。
3. 优先复用纯解析／转换逻辑；将业务存储、缓存、元数据写入改为 Loom 接口。
4. 对不可无损抽离的上游模块采用小范围受控 fork，记录来源、修改与升级差异。
5. 在 P1 完成“最小带参数和产物链”的设计验证后，再决定具体源码抽取边界，不提前承诺某个上游二进制可以直接复用。

临时保留 MLMD 只可作为过渡方案，必须标为过渡依赖，不满足最终统一数据库验收。不要先按 MLMD ID 编写一整套新接口，再在项目末尾才考虑如何移除 MLMD。

### 5.2 三层转换，避免 YAML 字符串拼接

```text
原始 IR → 类型化验证／能力报告 → 不可变执行计划 → Argo Workflow
```

- 原始包与规范化包分别保留摘要和格式版本。拒绝重复键、过深递归、组件环、未知引用及资源规模超限。
- 使用对应版本 protobuf 解析已知字段；平台扩展同样按其 schema 验证。未知字段不得悄悄忽略，若允许扩展透传，必须有明确的命名空间、版本和安全策略。
- 执行计划保存静态节点路径、端口、依赖、触发策略、资源约束、运行时协议版本与 source map。
- Workflow 使用结构化 Argo 类型生成；不将参数字符串替换进 shell，再当 YAML 解析。
- IR 摘要、执行计划摘要、Workflow 受管规格摘要分开保存。算法升级不能重新解释旧 Run。
- 编译结果具有确定性；admission 注入等集群变更与 Loom 原始编译产物分别记录，不能因此错误接管其他 Workflow。

### 5.3 编译器与 driver／launcher 的分工

建议保留受管的 driver／launcher 概念，但去掉其对独立 KFP API／MLMD 的硬依赖：

- 编译器：静态依赖、模板、输入输出约定、运行时调用位置、安全策略、标签与源码映射。
- driver：需要执行期间才能求值的输入、条件、循环、缓存决策及元数据交互。
- launcher：在用户容器上下文准备 ExecutorInput、启动组件、收集 ExecutorOutput、处理文件和发布请求。
- Argo：可调度节点的执行、并发、Pod 生命周期、配置好的重试和依赖控制。

不同条件／循环语义可由编译产物或 driver 落实，但一种行为必须指定唯一的执行责任方，不能由 Loom 和 Argo 分别再求值一次。

没有独立 KFP 后端，不代表执行时不需要助手镜像。助手镜像与用户组件镜像是两类依赖，均须版本化、扫描、锁定来源和兼容范围。

### 5.4 必须明确的语义兼容项

| 项目 | 设计约束 |
| --- | --- |
| 标量／LIST／STRUCT | 不丢失 bool、null、空字符串、空列表的区别；大整数遵循协议范围，不经浮点误改 |
| 可选参数／默认值 | 未提供、显式 null、运行时缺失分别处理 |
| 条件表达式 | 锁定 CEL／对应上游求值规则；不使用 Go／Python 表达式替代 |
| 循环 | 实例路径记录迭代索引及嵌套上下文，支持动态列表和并发上限 |
| 嵌套 DAG／输出汇聚 | 保留父子实例、端口边界、收集顺序与分支缺失语义 |
| ExitHandler／final status | 明确失败、取消、超时和清理任务何时执行；不能把普通依赖边当作等价替代 |
| 重试 | 重试次数、退避、失败类型、attempt 和最终状态一致；不能重复放大副作用 |
| Artifact／Importer | 类型、schema、URI、metadata、端口列表、导入引用和发布状态均保留 |
| `dsl.component` | 支持 SDK 生成的 executor 命令与输入输出文件协议，不要求用户重写为自定义脚本 |
| 资源与平台扩展 | GPU、PVC、挂载、Secret 引用等按允许列表转换；不默认允许任意 PodSpec |

建议引擎暴露兼容报告：`sdkVersionRange`、`schemaVersion`、`compilerVersion`、`runtimeProtocolVersion`、`supportedFeatures`、`unsupportedFeatures` 和错误字段路径。最终目标是完成选定基线的功能清单；阶段性拒绝不能成为永久不实现的借口。

## 6. 数据库改造建议

坚持 GORM 和显式版本化 SQL 迁移。不修改已发布迁移，不启用 AutoMigrate，不将 MLMD 原表直接复制进 Loom 当长期模型。

2026-11-11 精简修订：不再推荐原来的 12 张新增物理表，改为「已有表扩展 + 2 张核心表 + 3 张功能表」。字段、索引、JSON 格式与一致性规则以 [精简表结构设计](pipeline-native-argo-schema.md) 为准。本节是该设计的摘要，不表示已实施迁移。

### 6.1 对现有表采用扩展迁移

| 现有对象 | 建议变更 |
| --- | --- |
| `tb_pipeline`、`tb_pipeline_version`、`tb_pipeline_version_revision` | 保留流水线与版本修订；包封装保存 IR、摘要、兼容信息，运行固定 revision |
| `tb_pipeline_run` | 保留当前执行投影；补 engine、版本修订、实验及触发字段，旧 `kfp_*` 字段仅服务历史 KFP Run |
| `tb_pipeline_run_attempt` | 保留不可变提交快照；新增独立运行时列承载引擎、Workflow 绑定、计划、新鲜度和状态摘要，不再新增 execution 表 |
| `tb_pipeline_submission` | 显式扩展提交意图与目标字段，保留历史 KFP ID 语义，Workflow UID 单独保存 |
| `tb_ml_execution`／operation／history | 继续承担受理、租约、fence、业务审计；不默认兼任节点元数据事件队列 |
| `tb_model_artifact` | 保持模型注册语义，通过关联引用流水线产物，不混成唯一通用产物表 |

不要仅把 `kfp_state` 改名为 `argo_state`，这样旧历史数据与旧读取路径会被误解释。先加新列／新表，双读取兼容，再逐步停用旧字段。

### 6.2 建议新增物理表：最终 5 张，核心阶段 2 张

| 建议名称 | 主要职责／关键字段 |
| --- | --- |
| `tb_pipeline_task_execution` | 核心：每次执行的节点实例；输入输出和产物引用采用有界、类型化 JSON；保留父级、迭代、retry、外部绑定与写入检查点 |
| `tb_pipeline_artifact` | 核心：产物身份、生产者、类型 schema、对象版本、metadata、可视化描述及发布状态 |
| `tb_pipeline_cache_entry` | 租户范围的缓存键、算法版本、来源执行、输出引用、有效状态和到期时间 |
| `tb_pipeline_schedule` | 固定版本修订、参数、时区、下一触发、并发策略、身份与停用状态 |
| `tb_pipeline_experiment` | 空间内实验、描述、归档和 Run 关联；保留独立生命周期，不以字符串假装完整替代 |

不单建 task_io／artifact_type／artifact_event：分别保存在节点 IO、产物类型描述、节点输入输出关系和缓存来源中。跨运行血缘反查初期需要有界 JSON 扫描，后续可按实际规模增加可重建索引。

不单建 runtime_event：运行时采用带 fence／单调序号的同步事务写入，提交后应答，调用方可重试；不承诺异步事件队列和全量事件回放。不单建 execution_event：必要业务审计复用 history，状态快照节流归档。不单建 schedule_fire：使用普通 run 的触发字段和唯一 trigger_key。

是否拆指标表按查询需要决定：当前读单个运行可以使用产物 metadata；做跨运行高效对比时新增受控指标投影和索引，保留源 artifact 关联及数值合法性，不能解析 stdout 充当权威指标。

### 6.3 身份和索引约束

- 所有业务查询携带 `group_id`；执行元数据同时绑定 `instance_id`，禁止按全局 artifact ID 裸查。
- `instance_id` 是 Loom UUID；Workflow UID 是 Kubernetes UID，二者不同，不能复用一个字段表达。
- 节点实例身份需包含父 DAG、迭代路径和 retry attempt，不能以显示名称或 Pod 名唯一定位。
- 执行绑定使用唯一约束防重复提交；已绑定 UID 永不换绑，同名新 UID 必须冲突。run 重启换 instance，旧 attempt 可读。
- 调度触发身份来自 `(schedule_id, generation, scheduled_at)`，以 run 上唯一 trigger_key 去重；推进游标和受理记录同事务，外部提交再靠确定性身份收敛。
- 缓存键包括 scope 和算法版本；产物注册有幂等键，节点写入有 epoch／sequence／hash，状态同步有到期索引。
- 大型 IR／计划／节点状态可归档 MinIO，数据库保存经过核验的引用与摘要；不能每个轮询重复写巨型 JSON。
- 新迁移的实际编号需实施时按仓库最新状态分配；本文不预占或覆盖已有编号。

## 7. API 与源码改造边界

### 7.1 对外 API 尽量保留

保留现有 `/api/v1/groups/{gid}/pipelines`、版本、运行、`dag`、节点、日志、终端和可视化接口。现有权限、revision、Idempotency-Key 和运行 instance 检查继续生效。

建议新增：

- 引擎／IR 能力查询与版本兼容报告。
- 导入 IR 的校验／预览接口，不产生 Workflow。
- 实验、运行归档、运行比较、定时任务和血缘查询。
- 精确指定历史 instance／attempt 的节点、产物与日志读取。

新增接口的具体 DTO、权限点和路由需单独设计；不把原始 Workflow API、PodSpec 或对象存储代理直接开放给普通用户。

`execution-<MLMD ID>`、`taskId` 等当前标识带有 KFP 来源语义。旧运行继续由旧适配器解释；新运行使用引擎中立 ID，并在响应中声明 ID 版本。前端不得靠字符串前缀推断所有执行事实。

### 7.2 内部运行时 API

建议提供受保护的内部接口，职责限定为读取已授权执行输入、申请缓存、登记输出、提交元数据、确认发布等；URI 和协议此处不固定。

- 使用独立内部监听或网关策略，复用 Loom 同一业务服务／数据库，不加第二套账号系统。
- 认证绑定集群、namespace UID、Workflow UID、task execution、attempt、有效期和操作范围。
- 不使用浏览器 JWT，不给普通任务集群管理员凭据或数据库账号。
- 单凭请求内 `runId`／`podName`／label 不足以授权，需核对执行绑定和实际 Pod／Workflow UID。
- 用户组件与 launcher 同 Pod 时，投射 token 的受信边界最多是该 Pod，不能因“来自 sidecar”就视为可信。用短时、限任务能力令牌，并在服务端强制所有权和状态检查。
- 元数据回调可以声明产物和指标，但执行完成仍由受信观测确认；伪造输出不得解锁其他运行、写任意 URI、发布跨租户缓存或冒充上游任务。
- 请求有协议版本、幂等键、序号和 fence；旧 attempt 的迟到回调不得覆盖新实例。
- API 重启／暂时不可达时，助手保留有界重试／恢复证据，上传完成但回执丢失可以安全重放。

### 7.3 建议代码拆分

以下是职责建议，不要求一开始创建所有目录或抽象框架：

| 位置 | 改造职责 |
| --- | --- |
| `internal/service/pipeline_*.go` | 保留业务授权、状态机、执行计划编排与历史 |
| `internal/infra/kfp` | 过渡期保留旧运行适配器，最终退出 |
| 建议 `internal/infra/pipelineengine` | 小型引擎契约，避免 Service 持有具体 `*kfp.Client` |
| 建议 IR／编译模块 | 类型化验证、能力报告、受控上游编译代码与 source map |
| `internal/k8s` | Argo CR 提交／停止／删除／观测与精确 UID 校验 |
| `internal/worker/pipeline.go` | 按每次 execution 固定的引擎分派，保留租约与恢复 |
| 建议运行时模块／命令 | 受管 driver／launcher、协议和对象存储处理 |
| 元数据与缓存 service／repository | 使用 GORM 写同一 Loom 数据库，事务不跨网络调用 |
| `internal/infra/pipelineviz` | 保留解析器，逐步消除 MLMD 传输类型耦合 |
| `internal/route/route.go` | 继续集中注册所有对外／内部路由，handler 不注册路由 |
| `web/src/features/...` | 按现有 api／types／policy／页面／组件规范增加流水线模块 |

引擎接口表达 Submit、Observe、Stop、Delete 和绑定核验等操作；元数据／产物读取用各自契约，不强迫一个万能接口承担所有职责。Submit 不保证一次调用内完成绑定，必须允许持久恢复。

## 8. 执行生命周期与恢复设计

### 8.1 提交

1. API 在一个业务事务中验证用户、空间、版本修订和参数，保存不可变执行快照及持久操作。
2. worker 领取任务并复核租约、revision、Profile／namespace UID 和授权。
3. 验证／编译得到执行计划，保存编译版本、摘要和执行身份；在创建 CR 前提交 durable intent。
4. Workflow 使用确定性名称，例如 `loom-<instance UUID>`，不使用 `generateName` 处理未知提交结果。
5. 创建成功或请求超时均按确定性名称读取，核验 namespace UID、受管标记、规格摘要和绑定信息；对象冲突时拒绝接管。
6. 保存 Workflow UID，后续一律按 UID 核实。标记只是定位辅助，不是防伪安全凭据，需结合 RBAC 限制修改权。
7. 观测器将集群事实投影到 Loom，只有实际确认才推进运行状态。

数据库与 Kubernetes 仍然不是一个事务，目标是可恢复、可核验和避免重复运行，不声称全链路 exactly-once。用户容器对外部系统的副作用仍需业务幂等。

### 8.2 状态观测

主方案确定为：**SharedInformer 监听 Workflow CRD 变化 → 同步函数更新数据库**。运行在现有 `pipeline-worker` 中，不新增 persistence-agent 服务、同步表或独立调度／租约系统。借鉴 KFP 的 Informer 与标准队列用法，不照搬 agent 调用另一套 API Server 的部署结构。

源码进度：第二阶段已接入默认关闭的 Informer、标准队列、已绑定执行的状态回填和内部绑定确认入口。节点仅更新已登记的 IR／Argo 映射；登记、原生提交、产物发布和最终归档清理仍待实施。实现细节与限制见 [第二阶段记录](pipeline-native-argo-schema.md#13-第二阶段可选原生-workflow-观测源码)。

第三阶段 A 已补静态 IR 子集预检、确定性计划／source map、只读版本兼容性 API 与纯 Workflow 构造器；这不是完整官方 protobuf／SDK 兼容层，不开放原生运行。该阶段只负责预检与构造，后续暂停暂存进度见第三阶段 B；完整真实节点绑定及 driver／launcher 仍待接入，详见 [第三阶段 A 记录](pipeline-native-argo-schema.md#14-第三阶段-a静态-ir-预检与确定性计划)。

第三阶段 B 补充内部暂停暂存原语：冻结计划和准入后规格，事务保存 intent 与静态定义、创建前记录 attempted，按确定性名称 Create／GET 并一次性绑定 UID。未接 public API／worker 分派，不恢复执行；实际节点／Pod 绑定和完整运行／清理仍待实施，详见 [第三阶段 B 记录](pipeline-native-argo-schema.md#15-第三阶段-b持久化计划与暂停暂存)。

第三阶段 C 补充静态真实节点与 Pod 身份绑定：核验冻结计划和准入规格，以实际 status.nodes 的调用路径／模板／boundaryID 对应预登记定义，再通过真实 Pod 的控制器 owner UID 与节点注解保存一次性绑定；事务内回填任务阶段与时间。缺失保留原事实，歧义或换绑整批拒绝。暂不开放运行，也不完成 operation 或最终归档，详见 [第三阶段 C 记录](pipeline-native-argo-schema.md#16-第三阶段-c静态真实节点与-pod-绑定)。

第三阶段 D 增加内部 `ResumeNativePipelineSubmission`，复用现有授权／租约／目标锁，先保存固定启动请求，再 CAS 解除暂停并主动 GET 确认。Informer 可恢复接受事实，有权领取方仅推进操作到 OBSERVING；接受启动不等于运行完成，通用 ReportMLExecution 被隔离，原生最终收尾仍未实现。版本改变、令牌缺失／冲突或再次暂停均不自动重发，不开放 native API／worker 分派，详见 [第三阶段 D 记录](pipeline-native-argo-schema.md#17-第三阶段-d受控首次启动与接受确认)。

第三阶段 E 补充静态终态归档与内部 `FinalizeNativePipelineSubmission`：完整终态节点／真实 Pod 身份／空 IO 复验后，在同一观测事务保存有界最终 Workflow 证据；原持有者在有效授权、租约及版本 fence 下原子完成 Run、operation、receipt、history 和 audit。精确重放只读，收尾不伪造新观测时间，也不要求已归档 Workflow 仍存在；归档前丢失或证据不全仍不能完成。新增 000023 仅扩展已有 attempt 的私有快照／归档时间，不新增表。未开放 native API／worker 分派或 TTL／GC，详见 [第三阶段 E 记录](pipeline-native-argo-schema.md#18-第三阶段-e静态终态归档与操作收尾)。

第三阶段 F1 补充内部 `DeleteArchivedNativePipelineWorkflow`：仅处理完整终态归档对应的显式 DELETE／EDIT 清理命令，先持久化固定检查点，再以 UID／原 resourceVersion 前置条件 foreground Delete，主动 GET 并核对有界、无标签过滤的 Pod 清单；已知 Pod UID 或原 Workflow UID 的依赖仍在时不确认缺失。使用既有 attempt JSON，不新增表／迁移／权限；只推进操作到 OBSERVING，不做逻辑删除、应用编辑或终态回执。运行中停止及完整清理收尾仍待实现，详见 [第三阶段 F1 记录](pipeline-native-argo-schema.md#19-第三阶段-f1已归档实例的显式删除与缺失观测)。

```text
Workflow CRD 新增／变化
        ↓
SharedInformer → 标准 WorkQueue（合并通知、失败重试）
        ↓
同步函数读取最新对象并核验执行身份
        ↓
GORM 事务更新 TaskExecution、Attempt、Run
```

具体职责：

- 复用现有 Kubernetes dynamic client，为 Workflow GVR 建 SharedInformer，按集群及授权命名空间组织共享监听，并限制为 Loom 受管资源；不为每个 Run 建一个 Watch。
- 启动时由初始 List 加载已有 Workflow，等待缓存首次同步后处理队列。运行中通过 Watch 收到变化；断线重连、resourceVersion 过期后的重新 List 等机制使用 client-go 标准实现，不手写另一套 Watch 框架。
- Add／Update 回调只把对象键放进标准限流 WorkQueue，worker 出队后读取最新缓存对象；写库暂时失败就退避重试。这是进程内实现细节，不是新增消息服务或持久事件队列。
- 同步函数核验 namespace UID、Workflow UID、instance，按节点身份幂等新增／更新 `tb_pipeline_task_execution`，更新 `tb_pipeline_run_attempt` 的状态和观测摘要；只有仍为当前 instance 时才更新 `tb_pipeline_run`。不同输入来源的更新沿用既有事务／并发保护，不另外设计同步租约协议。
- Delete 通知只触发核查，支持 client-go 的 tombstone；对象不在缓存中不代表运行成功或已正常停止，保留最后已知事实并核验绑定／清理记录。
- Informer 是当前对象状态缓存，不是完整事件历史。重启通过初始 List 恢复尚存对象；提交结果不确定、对象缺失和归档未完成等问题复用已有持久恢复入口，必要时主动 GET 核验，不新增一套周期扫描服务。resync 仅重处理缓存，不视为重新读取 API Server 的保证。

此通道负责执行状态；参数、产物、指标继续由内部运行时 API 事务写入，不要求从 Workflow status 推导全部元数据。两种来源按第 9 节发布规则合并，不能以最后写入者覆盖一切。

终态清理顺序固定为：观测执行结束 → 必要节点状态、产物引用和最终快照持久化 → 在已有 attempt 的运行时摘要记录完成确认 → 才允许清理 Workflow。清理路径必须检查该确认，不能让独立 Argo TTL 在持久化之前删除对象；Informer 无法恢复已经删除且从未保存的历史。

观测字段至少包括来源、采集时间、最后成功时间、错误原因、Workflow UID 和实例。引擎未知状态映射 UNKNOWN，不把网络不可达、未读到 Pod 或一次 404 解释为成功／已停止。

大工作流可能存在节点状态压缩／offload，不能假定所有节点一直在 `status.nodes` 内。必须配置受支持的读取通道或在发布前明确规模限制，不能返回空 DAG 冒充完整状态。

### 8.3 停止、删除、编辑与重试

- 区分 graceful shutdown 与立即 terminate，明确清理节点的行为；停止请求受理不代表容器已经退出。
- 删除先处理提交结果不确定窗口，确认执行终止，再按 UID 前置条件删除 CR。历史落库与产物保留先于资源 GC。
- 删除 Workflow 不自动删除 MinIO 产物、PVC、日志或被缓存引用的数据；分别遵守保留策略和引用关系。
- 编辑沿用当前“清理旧执行 → 保存新配置 → 手动启动新 instance”的约定，不修改运行中 IR。
- 节点 retry、整次新 Run、业务操作重试和进程恢复是四种不同动作。首批不支持的动作明确拒绝；后续支持节点原地 retry 时记录新的 attempt，不能覆盖旧输出。
- 防止 Argo 层和 driver 层对同一失败双重重试，导致次数和外部副作用成倍增长。

## 9. 元数据、产物与缓存

### 9.1 产物发布

建议流水线产物保留 Dataset、Model、Metrics、ClassificationMetrics、HTML、Markdown 等类型和自定义 schema。引用关系至少包含 Run、task execution、方向、端口和下标。

采用“临时上传／对象确认 → 元数据发布”的协议；精简表设计使用 STAGING、PUBLISHED、FAILED、UNAVAILABLE 等状态。只有 URI 或文件路径不等于 PUBLISHED。对象存在性、长度、checksum／版本号与内容边界应按存储能力确认，multipart ETag 不能普遍当作文件 MD5。

参数文件、ExecutorInput／Output 和本地挂载布局需遵循对应 SDK 组件协议。不能把 KFP artifact 路径简单改成 Argo 参数字符串，令 Python 组件在运行时找不到文件。

### 9.2 缓存

缓存不是“相同 YAML 就不运行”。缓存设计包括：

- 按上游基线理解输入、组件、命令、环境、资源／PVC 等参与因素；自定义 cache key 的上游语义单独对齐。
- 每个缓存算法有版本，并强制租户／namespace 实例的 scope；用户自定义 key 不能越过租户边界。
- 使用真实解析后的参数、产物身份／版本及必要执行上下文，不能误用提交前占位符。
- 可变镜像 tag、外部 URI、PVC 内容或外部服务都可能导致不纯执行，需明确禁用／失效策略。
- 同时命中／填充需处理并发和失败发布；第一版允许重复计算，以唯一键／CAS 管理结果条目，不增加计算租约；只有经核验的完整成功输出可成为候选。
- 命中后复制的是经过授权的产物／参数引用，并记录 `cache_source_task_id` 和血缘，不是伪造一次新 Pod 成功。
- 源 Run 删除、空间重建、产物清理或权限变化时，缓存引用和可读性必须重新核验。

如为安全选择比 KFP 更严格的缓存隔离，须在兼容报告中标记行为差异；不能同时宣传无差异复刻。跨空间复用默认关闭。

## 10. 可视化完整迁移

### 10.1 已有能力与新增工作

当前 Loom 后端已包含部分指标解析和文件预览，详见 [普通可视化](pipeline-visualizations.md)，但输入依赖 MLMD，且前端不是完整可视化产品。

| 类型／页面 | 后端改造 | 前端工作 |
| --- | --- | --- |
| 静态 DAG／实时节点 | IR＋Loom 节点实例，处理循环、嵌套、缓存、retry | 图布局、状态、节点详情、历史实例切换 |
| `system.Metrics` | 从产物 metadata 提取合法数值 | 指标卡片、列表、跨运行比较 |
| 分类指标／切片 | 复用 ROC、混淆矩阵解析并接新数据源 | ROC 曲线、矩阵、切片选择与边界提示 |
| HTML／Markdown | 受控读取、内容类型、大小限制 | 隔离 iframe、Markdown 消毒、错误和加载状态 |
| 图片／JSON／CSV／文本 | 复用有界预览和下载 | 文件预览、表格、截断说明 |
| Dataset／Model | 明确预览 schema；不加载用户模型执行 | 数据样本／模型摘要，关联模型注册 |
| 产物详情／血缘 | task－artifact 事件、缓存引用、历史 | 输入输出关系图、上游／下游、来源导航 |
| 运行对比 | 类型对齐、固定参数和指标来源 | 实验筛选、并排比较、缺失值区分 |
| TensorBoard 等运行服务 | 单独生命周期、PVC／存储、路由和授权 | 服务打开／停止、状态和到期行为 |
| v1 UI metadata／专有插件 | 有实际需求时做格式和服务适配，不默认执行任意插件 | 显式展示支持范围 |

### 10.2 安全与历史读取

- 每个产物先证明属于当前有权访问的 Run／实例／任务，再读取固定桶中的受控 Key。
- 不接受浏览器传来的任意 S3／HTTP URI；禁止 SSRF、路径逃逸和以缓存引用跨租户读文件。
- HTML 不使用平台页面 `innerHTML`；使用无同源权限的 sandbox iframe，下载响应提供安全 Content-Type／CSP。Markdown 禁止原始 HTML 或可靠消毒。
- 不把 MinIO root 凭据、签名查询参数、Secret 或用户 JWT 写入元数据、日志及可视化响应。
- 元数据未就绪、文件已 GC、类型不支持、超限和无权限必须区分，不统一返回成功空数组。
- Worker 采集关键节点元数据并持久化；Workflow／Pod GC 后，DAG、指标与血缘仍可查询，文件按保留策略可读。
- 现有接口关于清理后历史不足的限制要真正补齐，而不只是把实时 MLMD 查询改成另一条实时请求。

## 11. 调度、身份与部署

### 11.1 定时任务推荐以 Loom 为权威

推荐由 Loom schedule 表和 cron 角色生成普通 Run，再进入同一受理／执行链。每次触发固定版本 revision、参数、执行身份与触发键；创建 Run 时重新检查空间可用性及执行授权。

必须设计 IANA 时区、夏令时、错过触发的补跑窗口、暂停／恢复、并发策略、最大活跃数和调度 owner 被禁用／移除后的行为。不要依赖“浏览器用户仍登录”。

Argo `CronWorkflow` 可作为后续另一种调度后端，但它直接生成子 Workflow 时也必须先建立可靠的 Loom 运行记录与收敛机制。不能绕过数据库和授权链，否则系统又出现第二套不可追踪的运行来源。首期不同时维护两套定时真相。

### 11.2 RBAC 和网络

- 新 Argo worker 需要受控的 Workflow 创建／读取／更新／删除权限；原有 KFP API 虚拟资源权限不能直接替代。
- Argo Controller 与任务执行 SA 保持分离；使用 `default-editor` 仍需审查其权限，不因为已有就视为最小权限。
- 读 Workflow、读 Pod 日志、pods/exec、Secret 分发分别授权，不把管理员 kubeconfig 挂入用户 Pod。
- Workflow 规格强制 namespace、SA、镜像策略、资源、网络、卷类型和安全上下文；拒绝用户自选跨空间身份、hostPath、特权及未经允许的模板引用。
- 当前日志／终端解析依赖 `pipeline/runid` 和 `/kfp-launcher/launch` 参数；改造必须保持 UID 链验证，不可降级为只校验 label。
- 内部运行时接口应有 TLS／mTLS 或等效受控认证、NetworkPolicy、限流和租户配额。
- 本地后端能用 kubeconfig 访问 Kubernetes，不代表集群任务能访问本地运行时 API。正式执行需部署集群内可达的 Loom 内部端点，或另行设计受保护的开发访问通道。

### 11.3 集群组件取舍

| 组件 | 最终处理 |
| --- | --- |
| Argo Controller／argoexec／Workflow CRD | 保留并固定兼容版本；不要另外装一个控制同一批 Workflow 的 Controller |
| MinIO | 保留；空间凭据、保留与 GC 职责迁入 Loom 或专用基础设施管理流程 |
| KFP API／UI／persistence agent／scheduledworkflow controller | 等旧运行排空、历史可读和新能力对齐后退出 |
| MLMD／metadata writer／metadata envoy | 完成元数据迁移与新运行协议后退出 |
| KFP cache／viewer／profile hook | 功能替代、依赖审计完成后逐项退出 |
| Metacontroller／DecoratorController | 确认没有其他消费者，先迁完空间初始化再考虑退出 |
| ScheduledWorkflow／Viewer／PipelineVersion 等 KFP CRD | 先盘点对象及所有使用者，导出／迁移后单独审批，不提前删除 |
| Notebook／Profile／KServe／Trainer／JobSet | 与本次替换无直接绑定，保留 |

重新整理安装目录，把 Argo 独立基础设施与 KFP 可选安装解耦。保留现有运行所需的 artifact repository、日志归档、ConfigMap、ServiceAccount、Secret 和网络策略。停用 KFP 时尤其不能误删正在被 Argo 或其他系统使用的 MySQL、PVC、MinIO、共享 namespace。

## 12. 新旧系统共存、迁移与回滚

### 12.1 每次执行固定引擎

增加 `engine=kfp|argo-native` 和引擎版本，保存于 execution／attempt，而不是仅依赖一个全局配置开关。旧 KFP Run 的查询、停止、删除继续经过旧适配器，新 Run 才进入原生 Argo。

默认引擎只影响新执行。对已排队但尚未提交的旧任务也须有明确引擎归属，不能在开关切换后被另一个 worker 意外领取。

### 12.2 历史迁移

1. 只导入有 Loom 业务归属、明确 namespace／Run 身份的旧运行；不能无授权全量接管 KFP 历史。
2. 保留 KFP Run ID、MLMD context／execution／artifact ID 到 Loom 新 ID 的带来源映射。
3. 分页导出节点、输入输出、指标、产物事件和缓存来源；统一校验隔离范围与数量／摘要。
4. 旧运行 IR 不一定包含实际运行全部配置；应记录迁移完整度，缺失数据明确不可恢复，不凭静态图捏造执行。
5. 保留原对象存储位置、对象版本及权限；不能为了统一前缀而破坏旧 artifact URI。
6. 活跃 KFP Run 继续由旧引擎跑完或按原链停止，不移交半执行 DAG 给新引擎，不重放历史训练任务。
7. 迁移可重入、可分批、可记录 checkpoint；历史产物已不存在时标记缺失，不伪造迁移成功。

### 12.3 灰度与回滚

- 先按管理员／空间／新运行灰度，不对整个集群一次性替换。
- 可做不提交任务的“影子编译”比较；未经明确授权不能双执行训练／推理任务。
- 回滚首先停用新引擎的新增提交，旧引擎重新承担新 Run；已有原生 Argo Run 仍需其适配器收尾，不能直接交回 KFP。
- 采用 expand／migrate／contract 的数据库变更流程，回滚窗口内不删除旧字段、旧表和旧服务。
- 旧 KFP 依赖退出需要单独的备份、恢复和审批清单。删除 CRD 可能连带删除其 CR 对象，不能作为安装脚本中的顺手清理。

## 13. 分阶段实施计划

下表是实施任务，不是本轮已经执行的工作。每阶段都要交付功能说明、兼容边界、升级／恢复说明和后续获授权的验证证据。

### P0：锁定基线与功能账本

- 盘点真实使用的 SDK／IR、工作流、组件、可视化类型与平台扩展。
- 锁定源码／镜像基线，列明内置能力、外部插件和不属于 KFP 的周边系统。
- 建立逐项兼容账本：现状、目标、责任模块、阶段、依赖、差异与验收证据。
- 盘点旧运行、MLMD 规模、对象存储布局、控制器和 CRD 共享情况。

退出条件：能明确回答“功能一样”具体包含哪些项，不以一句支持 KFP v2 替代清单。

### P1：服务端解耦与 IR／运行时垂直方案

- 将 `Service.PipelineClient *kfp.Client` 和 MLMD 类型依赖改成窄的执行／元数据契约，保留现有 KFP 行为。
- 设计 execution／task／artifact 结构和显式迁移；引擎归属不可变。
- 抽离／适配官方 IR 验证、Argo 编译和必要运行时；锁定最小协议。
- 设计两个容器节点的参数＋产物链，证明不存在隐藏 KFP／MLMD 服务调用。
- 列出编译器、driver、launcher 的复用文件及 fork 边界，形成依赖图和许可证记录。

退出条件：方案覆盖完整纵向数据流；“生成了 Workflow YAML”本身不算通过。

### P2：原生 Argo 最小业务闭环

- 实现创建、观测、停止、删除、编辑清理后重建，以及提交结果不确定恢复。
- 支持基础 DAG、参数、容器组件、基础产物传递、日志、节点详情与元数据发布。
- 新旧引擎共存，只对新执行灰度，旧运行行为不变。
- 保留当前认证、空间隔离、幂等、revision、lease／fence 和 UID 检查。

退出条件：获授权的真实环境中形成可恢复闭环，且没有跨空间权限回退。此时仍不能卸载 KFP。

### P3：完整执行语义、缓存与规模恢复

- 补齐条件、循环、嵌套、收集、Importer、ExitHandler、final status、retry、超时及取消语义。
- 完成缓存、血缘、并发发布和产物生命周期。
- 完成必需的平台扩展和准入约束。
- 覆盖 Workflow 大状态、offload／规模边界、重连和资源 GC。

退出条件：锁定版本的执行兼容账本无未声明缺口；不允许静默降级执行。

### P4：产品能力和可视化对齐

- 完成实验、定时任务、运行比较、归档、历史实例／attempt 查询。
- 前端完成运行 DAG、参数、日志、指标、报告、文件预览、下载和血缘。
- 对 TensorBoard／专有 viewer 等逐项完成所承诺范围。
- 补齐运行可观测性、同步新鲜度、管理员诊断和审计。

退出条件：用户可全程只用 Loom 界面完成约定流程，不需要打开 KFP UI 补操作。

### P5：历史迁移与 KFP 退役

- 将 KFP／MLMD 历史导入 Loom，旧活跃运行排空。
- 迁移空间存储初始化、凭据轮换、清理和调度职责。
- 重组独立 Argo 安装包，审计共享依赖与剩余调用。
- 分批停用旧服务，保留回滚窗口；最终删除动作另行审批。

退出条件：去除 KFP API／数据库／MLMD 后，新任务、历史 DAG、指标、报告和血缘仍按约定工作，且没有剩余用户／控制器调用旧依赖。

## 14. 后续验证与验收要求

按当前项目约定，本轮不新增或运行测试，不编译／启动项目，也不执行迁移。本节仅定义未来获明确授权后需要的验收，不授权现在执行。

1. 协议兼容：同一锁定 SDK 编译的 IR 在新旧引擎对照参数、产物、分支、循环、缓存与最终状态，允许的差异显式记录。
2. 恢复：提交前后崩溃、Create 超时、数据库回填失败、助手重复回调、租约失效和迟到输出。
3. 生命周期：提交后立即停止／删除、自然完成与取消竞态、节点 retry、Run 重建、Pod／Workflow GC。
4. 安全：跨空间 artifact／Pod 伪造、同名不同 UID、旧实例回调、无权缓存引用、恶意 URI／HTML、权限撤销期间读取。
5. 数据：迁移 checkpoint、孤立事件、缺失产物、缓存来源保留、历史 attempt 和字段兼容。
6. 规模：大量循环节点、巨型状态、Argo offload、断线重连、批量产物和对比查询，给出受支持上限。
7. 前端：真实加载／失败／无权限／缺失数据状态，不用模拟成功替代运行结果。
8. 退役：冻结并备份旧系统，验证无隐藏 MLMD／KFP 连接后再停用；恢复步骤有真实证据。

在得到这些证据前，只能表述为“源码已实现／静态审阅”，不能宣称已无损替代 KFP 或生产可用。

## 15. 投入与风险评估

当前 Loom 已有权限、持久操作和部分业务表，可减少重复建设；但运行时语义、元数据、缓存、历史和完整前端仍是主要投入。

作为规划占位，可按以下人月区间评估，不构成工期承诺，各项可能交叉，也会因真实兼容范围上浮：

| 工作包 | 初步量级 |
| --- | --- |
| 基线、依赖分析、数据／协议设计 | 1–2 人月 |
| 引擎解耦、IR 编译、基础 Argo 闭环 | 2–4 人月 |
| 高级语义、运行时、元数据和缓存 | 4–7 人月 |
| 前端 DAG／可视化／实验／对比／调度 | 3–5 人月 |
| 历史迁移、安全加固、故障恢复与退役 | 2–4 人月 |

完整目标宜以约 12–22 人月作为前期预算讨论区间，由 P0／P1 重新估算；多人并行不意味着日历时间线性缩短。需要熟悉 Go／Kubernetes 的后端、Python／KFP 运行时、前端和部署运维能力。只做基础 DAG 会明显更小，但不满足本项目的完整迁移目标。

主要风险按优先级排序：

1. 只迁编译器，漏掉运行时对 MLMD／缓存服务的依赖。
2. 外表执行成功，但循环、重试、参数或产物语义悄悄变化。
3. 租户隔离、回调认证、缓存复用和 HTML 预览扩大权限边界。
4. 同步与 GC 顺序导致历史节点、报告或血缘永久丢失。
5. 新旧引擎混用 ID／提交日志，产生重复或孤儿运行。
6. 过早卸载 KFP，破坏已有运行、存储凭据或共享 Argo 配置。
7. 上游升级失控；fork 后不再跟踪安全修复和协议变化。

## 16. 当前建议与下一项实际工作

建议采纳此目标架构，但先做 P0／P1，不直接删除 KFP，也不把现有 `pipeline_execution.go` 的 HTTP 调用简单替换成 Create Workflow。

下一项交付建议是“兼容账本＋运行时依赖拆分设计”，明确：

- 第一套锁定 SDK／IR／Argo／助手镜像基线。
- 官方编译器／driver／launcher 的实际复用与改造边界。
- 新 execution／task／artifact 模型和内部运行时协议。
- 功能迁移优先级，以及完整目标必须覆盖的可视化类型。
- 现有 API 的兼容方案、旧运行的固定引擎策略和迁移保留范围。

本方案不变更 Notebook、Profile、Trainer 或 KServe 的既有职责，也不扩大本轮为其他模块重写。

## 17. 源码与现有文档索引

Loom：

- [当前 KFP 主线](pipeline-kfp.md)、[编辑与重建](pipeline-edit.md)、[DAG 与节点](pipeline-dag.md)、[可视化](pipeline-visualizations.md)。
- [在线日志](pod-logs.md)、[归档日志](log-archives.md)、[终端](pipeline-terminal.md)、[模型注册](model-registry.md)。
- `internal/config/pipeline.go`、`cmd/platform/business_dependencies.go`、`internal/model/ml_workload.go`。
- `internal/service/pipeline_package.go`、`pipeline_execution.go`、`pipeline_sync.go`、`pipeline_observation.go`、`pipeline_graph.go`、`pipeline_visualizations.go`。
- `internal/k8s/pipeline_logs.go`、`pipeline_terminal.go`、`internal/infra/kfp/`、`mlmd/`、`pipelineviz/`。
- 既有迁移 `000015`–`000019` 及基础 ML 模型迁移；实施前以实际仓库清单为准，不只修改这些文件。
- `install/manifests/pipeline/base/`、`install/manifests/pipeline-client-rbac.yaml`、`install/kubernetes/loom/base/rbac.yaml`。

KFP 工作区参考：`pipelines/api/v2alpha1/pipeline_spec.proto`、`sdk/python/kfp/`、`backend/src/v2/compiler/argocompiler/`、`driver/`、`component/`、`metadata/`、`client_manager/`、`cacheutils/`、`objectstore/`。

复用上游代码须保留 Apache-2.0 等适用许可证、版权和 NOTICE，记录修改及锁定来源；依赖不能仅凭 KFP 顶层许可证一概处理。源码参考不是本轮完成的网络核验或集成验证。
