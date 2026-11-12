# Pipeline 原生 Argo：精简表结构设计

最新补充：Python-v1 编译/执行、worker 分流、原生查询和终态编辑/删除已做源码装配。本文 12–19 节保留历史阶段边界；最新状态以 [Python 第一版说明](pipeline-native-python-v1.md) 为准，未编译、未实机验收。

状态：设计及分阶段实施记录。日期：2026-11-11。本文替代迁移总纲第 6 节的原 12 张新增表建议。已补充核心数据层、可选原生 Workflow 观测、静态预检、内部暂停暂存、真实节点绑定、受控首次启动、静态终态归档／操作收尾及已归档实例的显式删除／缺失观测源码，进度见第 12–19 节；未执行迁移、测试、编译、服务启动或集群命令，原生公开运行入口／IR runtime 尚未接入。

## 1. 结论与计数

采用「已有表扩展 + 两张核心表 + 三张功能表」。不把一个业务概念机械拆成一张表，也不把所有运行数据塞进一行 Workflow JSON。

| 新增物理表 | 职责 | 实施阶段 |
| --- | --- | --- |
| `tb_pipeline_task_execution` | 每次运行中每个节点实例，含输入输出及产物引用 | 核心执行 |
| `tb_pipeline_artifact` | 有稳定身份和类型的产物，含指标与可视化描述 | 核心执行 |
| `tb_pipeline_cache_entry` | 可查询、可失效、隔离的缓存索引 | 缓存阶段 |
| `tb_pipeline_schedule` | 定时配置及调度游标 | 调度阶段 |
| `tb_pipeline_experiment` | 实验分组、描述、归档等独立生命周期 | 实验管理阶段 |

推荐最终新增 **5 张**，第一阶段新增 **2 张**。5 张不是整个 Pipeline 模块的总表数；已有业务表、提交记录和公共操作表继续存在。不做实验管理时可以暂缓第五张，但那是阶段性少一项功能，不是假装用字符串完整替代实验对象。

沿用 Cube 的 JSON 快照与对象存储思路，但保留节点执行和产物索引，避免以后连节点列表、跨运行产物引用都依赖扫描整个 Workflow。

## 2. 现有表保留与扩展

### 2.1 定义与版本

| 已有表 | 处理 |
| --- | --- |
| `tb_pipeline` | 保留业务身份、空间、名称、描述和权限；不放每次运行节点状态 |
| `tb_pipeline_version` | 保留版本名称、当前 revision、spec 和 hash；spec 使用版本化包封装 |
| `tb_pipeline_version_revision` | 保留每个 revision 的不可变内容；运行必须固定 `(version_id, revision)` |

版本包封装包含 `packageFormat`、`schemaVersion`、`ir` 或受管对象引用、内容摘要、SDK/compiler 信息、兼容报告及可选展示信息。沿用原 spec 的历史解释；通过包格式版本明确区分，不把历史 KFP 请求 JSON 直接当 Pipeline IR。

版本名是用户业务版本，例如 `v1`；revision 是该版本的编辑修订。更新版本不修改已经提交的执行快照。定时任务同样固定具体 revision。

不新增 `task_definition`：节点定义已经在 IR 中，页面通过 IR 解析并生成可重建的 DAG 展示数据。

### 2.2 运行与执行尝试

`tb_pipeline_run` 仍是用户可见的运行对象，展示当前 instance 的状态。建议扩展：

| 字段 | 建议类型／规则 |
| --- | --- |
| `engine` | `VARCHAR(24)`，区分历史 KFP 与 native Argo |
| `version_revision` | `BIGINT UNSIGNED`，明确执行使用的版本修订 |
| `experiment_id` | 可空 `BIGINT UNSIGNED`，同空间实验 |
| `schedule_id`、`schedule_generation` | 可空，手动运行不设置 |
| `scheduled_at` | 可空 `DATETIME(3)`，UTC 计划时刻，不是实际提交时刻 |
| `trigger_key` | 可空 `CHAR(64)`，规范化触发身份摘要 |

新增唯一索引 `(group_id, trigger_key)`；手动触发默认 NULL，继续使用已有请求幂等机制。新增 `(group_id, experiment_id, id)`、`(group_id, schedule_id, scheduled_at)` 查询索引。

原 `kfp_*` 列保留给旧数据；新引擎的原始状态、新鲜度等保存在 attempt，并向 run 的通用状态做投影。不直接重命名旧列或改写历史 ID。

`tb_pipeline_run_attempt` 已经有 `instance_id` 主键和不可变 `snapshot`，不再新增 `pipeline_execution`。对它增加以下列：

| 字段 | 建议类型／规则 |
| --- | --- |
| `engine`、`engine_revision` | 引擎类别与编译／运行时兼容版本 |
| `version_id`、`version_revision` | 本次固定来源，历史行按可核验数据回填，否则保留未知 |
| `cluster_id`、`namespace`、`namespace_uid` | 稳定集群身份、命名空间名称及 UID；不能只用可变显示名定位 |
| `workflow_name`、`workflow_uid` | 名称预先确定，UID 在提交确认后绑定且不换绑 |
| `plan_hash`、`plan_ref` | 固定执行计划摘要和受管对象引用 |
| `phase`、`reason` | 本次执行状态及脱敏错误 |
| `runtime_summary` | 有界 JSON：节点数量、输出摘要、资源版本等 |
| `workflow_snapshot_ref`、`workflow_snapshot_hash` | 最后持久化的 Workflow 快照引用与摘要，可空 |
| `workflow_snapshot`、`workflow_archived_at` | 000023 增加；静态 v1 的有界内联最终证据与归档时间，保留原提交 snapshot 不变；此时 ref 为 NULL |
| `observation_version` | 本地单调递增版本，用于 CAS；不将 Kubernetes resourceVersion 当整数比较 |
| `observed_at`、`sync_attempted_at`、`sync_reason` | 区分成功观测时间和失败尝试，失败不抹除已知事实 |
| `started_at`、`finished_at`、`updated_at` | 本次执行的时间字段 |

`snapshot` 继续不可变，保存参数、执行身份、版本修订、IR／计划引用等提交依据；上述运行时列可按受控协议更新。不要把 mutable 状态写回原 snapshot。

唯一索引 `(cluster_id, namespace_uid, workflow_name)`、`(cluster_id, workflow_uid)`；UID 未确认时为 NULL。提供 `(group_id, run_id, created_at)` 历史查询索引。对 native Argo 新行强制完整绑定规则，不凭空补齐旧 KFP 行。

run 重启产生新 instance；Argo 同一 Workflow 内的节点 retry 不产生新 run。旧 attempt 的运行时与产物仍可读取；迟到更新不能覆盖当前 instance 的状态投影。

### 2.3 提交与公共机制

- 复用 `tb_pipeline_submission` 保存先落库、后提交的意图，增加引擎类别与受管目标；确定性 Workflow 名称在首次外部请求前持久化。
- 它现有 `external_id` 的历史语义保留；新增明确的 Workflow UID 字段，不把旧 KFP ID 当 Kubernetes UID。
- 现有全局 `run_name` 唯一约束先保留，使用 instance 派生名称避免冲突；未来放宽为目标范围唯一，必须单独迁移，不能直接删约束。
- 复用 operation／`tb_ml_execution` 的受理、租约与 fence，以及 `tb_ml_workload_history` 的业务审计。它们不是节点元数据的权威存储，也不默认兼任可重放事件队列。
- 公共 history 当前要求 operation_id 和 actor_id 非空，且 target_type 受 CHECK 限制。第一版只记录有真实操作和主体的运行审计；无对应 operation 的系统观测保存在 attempt／task，不伪造操作 ID 或用户。若后续要求 schedule／experiment 独立审计，需显式扩展公共审计契约及约束。
- runtime 的节点元数据采用同步数据库事务提交后应答，失败由调用方按协议重试；不为此新增 inbox/outbox 表。若将来要求异步接收、独立消费者与事件重放，应重新设计可靠事件持久化，不能只加个 JSON 字段宣称具备这些能力。

### 2.4 CRD 状态同步：Informer 监听后更新现有记录

采用「SharedInformer 监听 Workflow → 标准 WorkQueue 重试 → 同步函数通过 GORM 写库」，直接运行在现有 pipeline-worker。启动通过初始 List 加载已有对象，运行中由 Watch 接收变化，重连使用 client-go 机制；具体流程见 [迁移总纲第 8.2 节](pipeline-native-argo-migration.md#82-状态观测)。

| 对象 | 同步更新内容 |
| --- | --- |
| `tb_pipeline_task_execution` | 节点身份、Argo／Pod 绑定、执行状态、时间和原因；不覆盖运行时 API 已封存的 IO |
| `tb_pipeline_run_attempt` | 对应 instance 的状态、观测时间、摘要和最终归档引用 |
| `tb_pipeline_run` | 仅投影当前 instance 的整体状态，旧执行不能覆盖新执行 |

不增加同步服务、同步表或独立调度／租约机制；复用已有事务与并发保护、持久恢复入口。Informer 的内存队列不作为权威数据或历史日志，对象缺失时不据此判成功。只记录变化后的必要状态，不把每次通知都保存为一条事件。

最终节点、必要产物引用及快照持久化完成后，在 attempt 的 `runtime_summary` 记录版本化完成确认（例如持久化状态、Workflow UID 和最终快照摘要），清理路径据此决定是否允许删除 CR，不新增一张终态确认表。定时任务的调度租约和执行操作的既有租约仍各自服务原职责，与本节的 Informer 监听不是一套新系统。

## 3. 核心新增表一：节点执行

### `tb_pipeline_task_execution`

一行是一个实际节点实例／一次 retry，包含 DAG、条件、循环、容器任务、Importer 和缓存命中的逻辑节点；不是 IR 中一个任务定义，也不要求有真实 Pod。

| 字段 | 建议类型／用途 |
| --- | --- |
| `id` | `BIGINT UNSIGNED` 主键 |
| `group_id`、`run_id`、`instance_id` | 空间、运行对象及执行尝试 |
| `node_key` | `CHAR(64)`，规范化节点身份摘要，不以显示名或 Pod 名构造 |
| `definition_path` | `TEXT`，完整 IR 定义路径 |
| `parent_id` | 可空节点 ID，关联本 instance 的父 DAG／循环／retry 节点 |
| `iteration_path` | JSON，嵌套循环的有序迭代身份，按执行实例固定 |
| `retry_index`、`node_kind` | 重试序号与节点类别；父子类型明确，不把重试组当一次执行 |
| `argo_node_id`、`pod_name`、`pod_uid` | 可空外部绑定，缓存命中等虚拟节点允许无 Pod |
| `phase`、`reason` | 状态投影及脱敏原因；保存 SKIPPED、CACHED 等逻辑状态 |
| `inputs`、`outputs` | JSON，类型化参数和端口产物引用，格式见下文 |
| `cache_source_task_id` | 可空，指向命中的原始节点，不伪造新产物的生产者 |
| `runtime_state` | 有界 JSON，执行协议检查点，不保存无限增长事件数组 |
| `writer_epoch`、`report_sequence`、`report_hash` | 元数据写入 fence、单调报告序号及幂等摘要 |
| `observation_version` | 节点投影 CAS 版本；与元数据报告序号分开 |
| `started_at`、`finished_at`、`created_at`、`updated_at` | 时间字段 |

索引与约束：

- 唯一 `(group_id, instance_id, node_key)`；node_key 来源包括定义路径、父级动态实例身份、迭代路径、retry 序号和身份算法版本，命中相同摘要时仍核对规范化身份。
- 唯一 `(group_id, instance_id, argo_node_id)`，无外部节点时为 NULL；每个 Argo 节点只绑定一个对应投影。
- 查询 `(group_id, instance_id, parent_id, id)`、`(group_id, instance_id, phase, id)`、`(group_id, cache_source_task_id)`。
- 终态输出封存后不原地修改；新重试使用新节点行。Pod／Workflow UID 绑定不随同名资源重建而换绑。
- metadata 报告和 Argo 观测分别更新自己的事实字段；最终 phase 由确定的合并规则推导，不能两个写入方无条件 last-write-wins。

输入输出结构示意：

```json
{
  "schemaVersion": 1,
  "parameters": {
    "learning_rate": {"type": "DOUBLE", "value": 0.01}
  },
  "artifacts": {
    "dataset": [
      {"artifactId": 1001, "index": 0}
    ]
  }
}
```

参数类型与值的解析、缺失／不可用状态、列表顺序必须按协议保留。大型参数使用受管引用及摘要；JSON 值和数值编码需按 KFP 类型系统校验，不能依赖示例中的 JSON number 自动保持所有 INT64 精度。

不新增 `task_io` 和 `artifact_event`。输入端口的 artifactId 表达消费边，输出端口表达生产／透传边；缓存来源、Importer、父子 DAG 透传也必须保留，不能把所有输出引用都解释为新生产。

血缘权威关系就是封存的节点 IO 和产物生产者信息，不依赖尚未确认的 Workflow 缓存快照。查询某个产物的全部消费者，第一版按授权空间及时间范围扫描 JSON；大规模跨运行反查不是此布局的高效路径，未来有真实需求时可新增可重建的边索引，而非现在预建一整套表。

## 4. 核心新增表二：产物

### `tb_pipeline_artifact`

产物是独立身份，不等同一个文件 URL；多个运行可以合法引用同一产物，URI 相同也不意味着产物相同。

| 字段 | 建议类型／用途 |
| --- | --- |
| `id` | `BIGINT UNSIGNED` 主键 |
| `group_id` | 隔离与授权范围 |
| `producer_instance_id`、`producer_task_id` | Loom 内生产者；外部导入可为空，但必须有导入出处 |
| `registration_key` | `CHAR(64)`，受信运行时生成的幂等身份 |
| `type_name`、`type_schema_version` | 类型名称与 schema 版本，支持 KFP 系统／自定义类型 |
| `type_schema`、`type_schema_hash` | JSON 类型描述及摘要，不单独建类型表 |
| `uri` | `TEXT`，受策略校验的对象位置，不是任意服务器读取 URL |
| `object_ref` | JSON，存储后端、bucket/key、版本、摘要、大小等；外部引用明确标注可变性 |
| `metadata` | JSON，类型化 metadata，含 Metrics／ClassificationMetrics 等内容 |
| `visualizations` | JSON，受支持的图表、表格、报告描述及对象引用 |
| `provenance` | JSON，生产／导入来源信息；不把外部产物伪装成本地生产 |
| `state` | STAGING、PUBLISHED、FAILED、UNAVAILABLE 等生命周期状态 |
| `created_at`、`published_at`、`updated_at` | 时间字段 |

唯一 `(group_id, registration_key)`；索引 `(group_id, producer_task_id, id)`、`(group_id, type_name, created_at, id)`。不能对 URI 加唯一约束来替代产物身份。

新输出先注册 STAGING，确认对象可读、类型合法后才 PUBLISHED；任务输出封存和产物发布在同一数据库事务内关联。MinIO 与 MySQL 无分布式事务：先上传对象并核验，再提交数据库；失败留下的孤立对象由宽限期 GC 清理。外部可变 URI 不宣称不可变，也不默认允许参与可靠缓存。

正常发布后，身份、内容引用、类型和权威 metadata 不变；更正产生新产物。可用性状态、受控失效信息可以更新。

不新增指标和可视化表：先支持单次／有限多次运行的指标读取和图表展示。大报告放对象存储；metadata 和 visualizations 保存有界描述。需要海量指标排序、筛选或高效全局对比时再建投影索引。HTML／Markdown／图片等均经过安全读取与隔离渲染，不直接信任产物内容。

`tb_model_artifact` 继续服务模型注册；可增加指向此表的同空间 artifact ID，不能把通用数据集、指标、报告全部塞进模型注册表。

## 5. 功能表一：缓存

### `tb_pipeline_cache_entry`

| 字段 | 建议类型／用途 |
| --- | --- |
| `id`、`group_id` | 主键及空间隔离 |
| `scope_key` | `CHAR(64)`，默认至少包含空间，必要时包含执行身份／数据授权域 |
| `algorithm_version`、`cache_key` | 算法版本及规范化输入摘要 |
| `source_task_id` | 成功且输出已封存的原节点 |
| `outputs`、`outputs_hash` | 有界不可变输出快照或受管引用及摘要 |
| `state`、`expires_at` | READY／INVALID 等状态及可选到期时间 |
| `created_at`、`updated_at` | 时间字段 |

唯一 `(group_id, scope_key, algorithm_version, cache_key)`；索引 `(group_id, state, expires_at)`。

这里只保存已经可用的结果索引，不设计正在计算的分布式锁：相同输入可以并发计算；数据库唯一键保证一个有效条目，冲突不覆盖已发布的有效结果，作废替换使用 CAS。若未来需要只算一次，再单独设计可恢复的计算租约。

命中前重查身份授权、来源有效性和输出对象可用性；命中后节点保存原产物引用和 `cache_source_task_id`。缓存键必须对齐既定 KFP 兼容范围，涵盖组件／镜像身份、参数、输入产物版本、执行配置及影响计算的扩展；没有可信版本的输入不能仅按 URI 命中。

## 6. 功能表二：定时任务

### `tb_pipeline_schedule`

| 字段 | 建议类型／用途 |
| --- | --- |
| `id`、`group_id`、`pipeline_id` | 主键与业务归属 |
| `name`、`description` | 同空间名称及说明 |
| `version_id`、`version_revision` | 固定来源，不自动追踪 latest |
| `experiment_id` | 可空，同空间实验分组 |
| `parameters`、`execution_config` | JSON，参数及受管执行配置，不保存凭证明文 |
| `cron_expr`、`timezone` | Cron 与 IANA 时区 |
| `generation`、`enabled` | 配置修订和开关 |
| `next_fire_at`、`start_at`、`end_at` | UTC 调度边界与持久化游标 |
| `concurrency_policy`、`max_active_runs`、`catchup_policy` | 并发及补跑语义 |
| `lease_owner`、`lease_until`、`lease_epoch` | 多实例调度租约与 fence |
| `execution_identity`、`created_by` | 执行身份引用和创建者，不等同永久授权 |
| `created_at`、`updated_at`、`deleted_at` | 生命周期 |

唯一 `(group_id, name)`；索引 `(enabled, deleted_at, next_fire_at, id)`、`(group_id, pipeline_id, id)`。更改执行相关配置或停用／恢复调度时按规则推进 generation，使旧租约失效。

不新增 `schedule_fire`：定时触发在普通 `tb_pipeline_run` 中持久化，trigger_key 来源为 `(schedule_id, generation, scheduled_at)` 的规范化摘要。同一数据库事务中锁定／CAS 检查调度配置和 lease_epoch、插入 Run 与受理所需持久化记录、推进游标，事务提交后再访问 Kubernetes。

这保证本地触发记录不重复，不宣称跨 MySQL/Kubernetes 的分布式 exactly-once；外部提交仍靠确定性名称、身份校验和观测收敛。运行数据归档时必须保留 trigger_key 去重事实，不能物理删除后允许重复补跑。按策略跳过的时刻推进游标并记必要的业务审计；只有明确需要逐时刻跳过清单才另行扩展。

每次触发重新核验空间状态、版本可用性及执行身份授权。时区夏令时重叠／缺失的处理及 catchup 上限必须在 API 语义中明确，而不是交给各 worker 随机解释。

## 7. 功能表三：实验分组

### `tb_pipeline_experiment`

| 字段 | 建议类型／用途 |
| --- | --- |
| `id`、`group_id` | 主键与空间隔离 |
| `name`、`display_name`、`description` | 业务名称、展示名和描述 |
| `state` | ACTIVE／ARCHIVED，归档不删除运行记录 |
| `revision` | 编辑并发控制 |
| `created_by`、`created_at`、`updated_at` | 审计字段 |

唯一 `(group_id, name)`，索引 `(group_id, state, id)`。Run 通过 experiment_id 关联，不建中间表；第一版一条 Run 属于零个或一个实验，一个实验可以汇集多个 Pipeline 的运行。

实验不是 Pipeline 的某个版本，也不是业务空间；不能直接复用它们的 ID。暂不开发实验管理时允许 Run 的 experiment_id 为空，不创建无意义的占位实验。

## 8. 不新增的表及其归宿

| 原提案 | 精简方案 |
| --- | --- |
| `pipeline_execution` | 扩展已有 `pipeline_run_attempt`，当前状态投影回 run |
| `pipeline_task_io` | 节点 inputs／outputs JSON |
| `pipeline_artifact_type` | artifact 内 schema 与摘要 |
| `pipeline_artifact_event` | 封存节点 IO + 产物生产者 + 缓存来源，表达血缘 |
| `pipeline_runtime_event` | 不采用异步事件队列；同步事务写节点、产物和缓存后应答 |
| `pipeline_execution_event` | 有界业务变更沿用公共 history；原始状态归档，不每次轮询追加历史 |
| `pipeline_schedule_fire` | 普通 run 的触发字段与唯一 trigger_key |
| metrics／visualization | 产物 metadata／visualizations 与对象存储 |

原 12 张中的 7 张合并／取消，保留 5 张。减少的是存储拆分，不是去掉循环、条件、参数、产物、缓存、实验、可视化等兼容目标。

## 9. 一致性与安全边界

1. 业务查询始终携带 group_id；artifact、task、experiment、schedule 的关联均校验同空间。SQL 外键可按仓库约定落地，跨空间语义必须由事务内校验和受控写路径保证。
2. 运行时凭证绑定 instance、task、writer_epoch 和允许动作，短期有效；任务不能提交任意 group_id、生产者或全局 artifactId 来越权。
3. 同一 task 的报告协议采用单写入者和单调 sequence。相同 epoch／当前 sequence／相同摘要重复提交返回已提交结果；相同序号不同内容报冲突；过期 epoch 或较旧序号不得再次应用。写入者重启必须读取检查点，必要时通过受控接管推进 epoch，不能从零覆盖。
4. 输出提交、产物发布和节点 IO 封存在 GORM 事务中关联；运行时报告不单独证明执行成功。可信观测确认成功且必要输出封存后，才在事务中推进成功状态并按需发布缓存。允许「输出已封存、执行成功仍待核验」的中间状态，缓存不可提前命中。锁顺序固定并处理死锁重试；K8s 观测也不能绕过产物协议把未发布结果标为缓存可用。
5. ACK 只在提交成功后发出；提交结果不确定时重试按检查点读取既有结果。不要求保存所有历史报告的逐条 ACK；终态封存和不可变输出是恢复依据，不能靠内存去重。
6. 节点输出提交后才允许依赖它的运行时继续解析；循环展开、条件结果等关键检查点同样持久化。这个设计不是只有数据库表就自动具备 KFP IR 执行能力。
7. Workflow 活动状态、数据库投影及 runtime 报告可能短暂不同步，API 必须展示最后成功观测时间；发现 UID 不符、事件缺口或未知状态时重新核验，不能凭超时推断成功。
8. 对象引用只允许受管后端和授权路径；URL 不直接变成后端任意网络请求。IR／日志／输入输出中不持久化 Secret 值；归档脱敏。

## 10. JSON 和对象存储的边界

JSON 仅用于结构灵活、通常随一个业务对象读取的数据：IR、节点 IO、类型描述、产物 metadata、图表描述。需要列表筛选、唯一性、并发控制和调度扫描的字段独立成列。

JSON 与对象快照都带 schemaVersion／格式版本、摘要和大小上限。大型 IR、参数、计划、完整 Workflow 和报告放 MinIO 等对象存储，数据库保留受管引用；权威节点检查点和需要事务校验的产物引用不能仅放在可变远程快照中。

不在 attempt 中每隔几秒覆盖整份大 Workflow JSON。采用增量节点更新、变化摘要和节流归档；Workflow TTL 清理前保证必要节点状态和最终归档持久化，尚未完成归档的状态必须可见。大工作流若启用 Argo offload，观测层必须有明确的 offloaded nodes 读取方案，不能假设 status.nodes 永远完整。

GC 按产物引用、缓存来源和保留策略处理。消费节点、缓存或模型注册还在引用的产物不能随原 Run 一起删除；运行软删除不等同产物可回收。

## 11. 后续实施边界

第一步补充 DTO／GORM 模型和显式版本化 SQL 迁移设计，实施时再分配最新编号；不修改已发布迁移，不启用 AutoMigrate。先实现核心 2 张及已有表扩展，再按功能阶段加入缓存、调度、实验 3 张。

设计文档本身不执行迁移或触发重启；源码实施进度以下一节为准。各阶段字段长度、约束命名和类型按现有查询与 MySQL 索引限制核对。后续只有获用户明确授权才运行测试、编译、迁移和集群验收。

与原总纲的关系：执行架构、兼容矩阵、安全要求和分阶段替代目标仍沿用；数据库物理结构以本文为准。新增表数减少不会自动缩短 IR runtime、可视化渲染和兼容验证的工作量。

## 12. 第一阶段源码交付记录

本阶段只落地核心数据层与旧引擎隔离，不宣称原生 Argo 已可运行。

| 内容 | 源码状态 |
| --- | --- |
| 核心两张表 | `000022_pipeline_native_core.sql` 新建 task_execution／artifact，含节点／注册幂等唯一键、同空间执行／父子／生产者外键及 JSON 形态约束 |
| 已有表扩展 | run 增加 engine、version_revision；attempt 增加独立引擎绑定、计划、状态、归档和观测列；submission 增加引擎及独立 Workflow 绑定 |
| GORM 模型 | 新增 PipelineTaskExecution、PipelineArtifact、公共 PipelineSubmission，扩展 Run／Attempt；不启用 AutoMigrate |
| 旧流程接入 | 新提交明确保存 KFP 引擎和版本修订；编辑清理时保留旧 attempt 的版本信息，并应用新修订；不改变旧 snapshot 内容 |
| 引擎隔离 | 现有 KFP 执行／查询／轮询入口限定 KFP，不把 NATIVE_ARGO 的 ID 当 KFP ID 使用；尚不允许 API 创建原生执行 |
| 仓储读取 | 新增按 group／run／instance 限定的 attempt 与节点读取，以及同空间产物查询；分页有上限，不替代 service 授权 |
| 第一阶段未接入 | 原生提交、IR 编译／runtime、Informer、节点和产物写入／发布协议、对外原生查询 DTO、终态归档清理闭环；第二阶段观测进度见下一节 |
| 后续功能表 | cache_entry、schedule、experiment 尚未创建，对应 Run 触发／分组列也留在后续迁移 |

迁移将既有数据标为 KFP，但不会从当前可变版本行推断历史 version_revision。旧行无法证明的版本修订、Workflow UID 和计划字段保持 NULL。STAGING 产物允许 URI 尚未确定；PUBLISHED 才要求 URI、object_ref 与 published_at 完整。visualizations 使用 JSON 数组，metadata、provenance、type_schema 使用 JSON 对象。

**部署提示：**迁移文件已被现有 embed 机制纳入版本检查。新代码在启动前需要管理员显式执行迁移；未应用 `000022_pipeline_native_core.sql` 时现有 schema Check 会拒绝启动。MySQL DDL 不是可事务回滚的变更，按项目迁移流程备份并核实 dirty 状态。本轮未执行这些操作，也未重启现有进程。

第二阶段接入受管 Workflow 的原生执行绑定确认与 Informer → 同步函数 → 数据库通路，具体边界见下一节；仅有表结构不能让 KFP 编译产物自动在 Argo 上完整执行。

## 13. 第二阶段：可选原生 Workflow 观测源码

已增加以下源码，不代表已在真实 MySQL／Kubernetes 验证：

| 层 | 本阶段内容 |
| --- | --- |
| Kubernetes | dynamic SharedInformer，监听 `argoproj.io/v1alpha1/workflows`，过滤 Loom managed-by 标签；30 秒缓存 resync，标准 List／Watch 重连 |
| worker | 原有 pipeline-worker 内可选启动 Informer 与标准类型化限流队列；初始缓存同步后启动处理，失败退避，共用进程级观测限速 |
| service | 先按持久 attempt 找执行，再复用已有目标锁主动 GET 最新 Workflow，事务内重核空间／Run／attempt／journal 身份，回填节点、attempt 和当前 Run |
| 绑定确认 | 内部 `ConfirmNativePipelineWorkflow` 仅为后续可信提交器预留；要求已存在 NATIVE_ARGO attempt 和 attempted 提交日志，核对初始 spec 摘要后一次性绑定 Workflow UID |
| 配置／装配 | `pipeline.native_observer_enabled`，环境变量 `LOOM_PIPELINE_NATIVE_OBSERVER_ENABLED`，默认 false；与 KFP enabled 独立，不启用原生提交 |
| 部署清单 | 独立 observer 只读角色仅向 pipeline 身份增加 Workflow watch 权限，不授予 API 身份；保留原 get/list，未增加 create/update/delete，也未应用到集群 |

源码主链：`internal/k8s/pipeline_native.go` → `internal/worker/pipeline_native.go` → `internal/service/pipeline_native_sync.go`。没有新增服务、同步表、迁移文件、独立扫描角色或租约协议。

### 13.1 绑定协议

数据库预先固定 cluster_id、namespace／namespace_uid、确定性 Workflow 名 `loom-<instance UUID>`、plan_hash，以及 submission 的目标和请求摘要。当前 cluster_id 使用运维配置的稳定集群键 `Service.Cluster`，不得把同一个键重指向另一集群而沿用绑定。

Workflow 要携带 `app.kubernetes.io/managed-by=loom`、`loom.ml/group-id`、`loom.ml/pipeline-run-id` 标签，以及 `loom.ml/instance-id`、`loom.ml/pipeline-namespace-uid`、`loom.ml/pipeline-plan-hash` 注解。这些只是匹配线索，不是授权凭证；空间核验、已有提交意图和 UID 才共同确定身份。

首次确认时，submission.request_hash 必须是初始受管 Workflow.spec 经 Go JSON 规范化编码后的 SHA-256，不能沿用旧 KFP 请求摘要解释；plan_hash 是独立计划摘要。第三阶段 B 进一步将 request_hash 固定为服务端 dry-run 准入后的暂停 spec 摘要，本地构造摘要单独保存在快照。后续停止可能合法改变 spec，因此已经绑定 UID 的普通观测不重新以初始 spec 摘要拒绝所有变化。未来启动／停止路径需落实可修改字段策略与对应 RBAC，不能据此开放任意 spec 编辑。

确认函数只核验及绑定既有记录，不创建业务身份，不提交 CR，也没有对外 HTTP 路由。Informer 对未绑定 UID 的 intent 不自行接管；只有未来可信提交器可调用首次确认。尚未接入的提交器负责预先登记 IR 动态节点身份与 argo_node_id，包含循环和 retry 的区分。

### 13.2 本阶段观测边界

- Workflow 的运行事实写入 attempt，且只向当前、未清理、未删除的 Run 投影。旧 attempt 不覆盖新 instance。对象缺失、读取失败或身份不符不推断成功／已停止，保留最后观测并记录失败原因。
- 节点同步只更新已有、同 group／run／instance 的 argo_node_id 绑定，不按 Argo displayName 猜 IR definition_path，也不把 driver／内部节点自动登记成业务任务。未匹配或缺失节点明确记录 `NODE_BINDINGS_INCOMPLETE`；后续编译器 source map／runtime 登记必须补齐这部分。
- 不覆盖 inputs／outputs、产物引用和 runtime 写入检查点。节点原始 Argo phase 写入 runtime_state，成功状态必须等 `outputsSealed=true` 才投影为节点成功；该标志只能由后续受信输出事务设置。
- Workflow 已成功但节点绑定或输出还不完整时，Run 的 observed_state 可反映 SUCCEEDED，而业务 status 保持 RUNNING 并注明等待原因，不能把执行结束等同元数据齐备。
- 即使已绑定节点全部完成，本阶段也只记录 `FINALIZATION_PENDING`，不直接把业务 status 推进 SUCCEEDED；所有必要逻辑节点／产物及最终快照的完成证明由后续收尾事务负责。缺失的时间戳不清空已有开始／结束时间。
- 目前仅支持直接 status.nodes，最多 4000 个节点；compressedNodes／offloadNodeStatusVersion 明确记录不支持，不能返回空 DAG 冒充完整节点。
- 本阶段不会发出最终持久化／归档完成确认，也不会删除 Workflow。已有输出、最终归档、清理及「进程离线期间对象已被删除」的持久恢复闭环仍待后续实现，不能只打开此开关就启用独立 TTL 清理。

默认配置和现有 KFP API 行为不变。本轮仅格式化与静态差异／调用检查，没有测试、编译、启动、迁移或集群操作。下一阶段是原生提交的 durable intent、节点 source map／登记及 IR compiler／runtime 的分阶段接入；普通 Python SDK 任务尚不能通过这条观测通路直接运行。

## 14. 第三阶段 A：静态 IR 预检与确定性计划

原生提交的前提是能明确说明「这份 IR 支持哪些语义、生成什么计划」。本阶段先补这部分，不把原始 IR 或用户 YAML 直接交给 Argo。以下均是源码状态，尚未编译或联调。

### 14.1 源码及能力边界

| 层 | 本阶段内容 |
| --- | --- |
| 通用 JSON 校验 | `internal/utils/validator/bounded_json.go`：对象根、UTF-8、大小、深度、token 数、重复键与尾随文档检查 |
| IR 预检 | `internal/infra/pipelineir/native.go`：类型化静态子集，精确字段名与未知字段拒绝，稳定错误码及 JSON Pointer 路径；无网络或数据库调用 |
| 执行计划 | 编译器版本 `loom-native-static-v1`、格式版本、独立 IR 摘要、计划摘要、静态 source map、排序后的模板和依赖；返回计划的深拷贝，不允许外部改写已计算摘要的内容 |
| Workflow 构造 | `internal/k8s/pipeline_native_build.go`：用固定结构及 Kubernetes Container 类型生成 DAG／容器模板，不拼 YAML，不创建 CR；规格摘要复用观测端的规范化协议 |
| 只读 API | service 复用 Pipeline 读取权限及版本归属核验，前后检查 revision；handler 仅解析请求，路由仍集中注册 |

预检只接受 `schemaVersion=2.1.0`、扁平静态 DAG、显式依赖以及纯字面量的容器 image／command／args。暂限 IR 512 KiB、32 层、60000 token、128 个任务／组件／executor；错误列表最多 64 项，并明确报告是否截断。依赖引用、重复依赖、自依赖和环必须拒绝。生成顺序不依赖 Go map 遍历顺序。

有输入输出端口、条件／触发策略、循环、嵌套 DAG、retry、Importer、启用缓存、环境变量／资源扩展或运行时占位符时，明确返回不支持，**不返回可以误提交的半成品计划**。未知字段包括大小写别名也不能悄悄忽略。字面量中含 Argo／KFP 双花括号占位符会被保守拒绝，不尝试 shell 字符串替换。

这是 `STATIC_SUBSET_PREFLIGHT`，不是已锁定上游 protobuf／SDK 的完整语义验证器。未引入或宣称复用了完整 argocompiler、driver 或 launcher；正式开放运行前仍须锁定官方 proto／SDK、建立对应解析层与兼容验证，不能用这套静态类型代替完整协议实现。普通 `dsl.component` 的 executor 输入输出协议目前不支持。重复键检查作用于 Analyze 收到的 JSON；如果旧导入流程早已规范化并丢失原始键信息，本接口不能倒推原始上传包是否含重复键，原生导入开放前须把严格检查前移至规范化之前。

### 14.2 兼容性查询

```text
GET /api/v1/groups/{gid}/pipelines/{pid}/versions/{vid}/native-compatibility?revision=1
```

revision 必须唯一且为正整数，只检查当前版本的这一修订；不支持用该接口重新解释历史 Run。沿用 `pipeline.read`、所有者／`pipeline.read.any` 和同空间版本归属检查，解析结束后再次核验权限及版本。API 不需要启用原生 observer，也不访问 Kubernetes。

主要响应字段：

| 字段 | 含义 |
| --- | --- |
| versionId／revision／packageDigest | 本次检查的业务版本和既有包摘要 |
| compilerVersion／schemaVersion／validationMode | 预检规则及其版本，不表示 SDK 全兼容 |
| compilable | 是否能生成本阶段的静态子集计划 |
| runnable | 本阶段固定 false，即使 compilable=true |
| executionReason | 固定 `NATIVE_EXECUTION_NOT_ENABLED`，不能据预检结果切换运行引擎 |
| issues／issuesTruncated | 有界错误码与字段路径；不回显原始解析错误或字段值 |
| irDigest／planDigest | 当前编译协议的摘要；与旧 KFP packageDigest 分开解释 |
| taskCount／dependencyCount | 静态规模，不含运行时动态节点 |

响应不含容器命令、参数值、凭据或完整 Workflow。无内嵌 IR 的旧受管引用返回 `PACKAGE_IR_REQUIRED`，不尝试下载引用。版本并发变化或保存的包摘要不符返回冲突，不把旧结果标成新 revision。

### 14.3 构造器与下一阶段

Workflow 构造器仅接受内部已编译 Plan、确定性执行身份和运维策略：service account、并发、CPU／内存。模板使用固定名称及静态定义摘要注解，Workflow 保留 group／run／instance、namespace UID、plan hash。静态 source map 是 IR 定义与模板／调用点的对应关系，不是实际 Argo node ID 或 Pod UID；后续必须依据真实观测核验并登记。

构造器固定资源 request／limit，限制非 root、禁止提权、移除 capabilities、使用 RuntimeDefault seccomp；镜像名语法合法不代表镜像支持这些限制、可拉取或可信。没有新增 TTL、GC、PodSpec 透传或创建权限。规格摘要基于最终 unstructured spec，**但尚未解决 admission/defaulting 对首次提交确认的规范化边界**，不能把本地摘要无条件当作经准入后的摘要。

本阶段不创建 Run／attempt／task／submission，不保存计划、不发起 Create、不设置 attempted、不绑定 UID，也不打开原生提交开关；KFP 提交及既有数据保持原路径。没有新增表、SQL 迁移、外部依赖或后台服务。

下一阶段继续接持久化计划、durable intent、准入后规格核验、确定性 Create／未知结果恢复及真实节点绑定；随后接运行时输入输出、产物发布和最终归档。仍不能宣称完整原生 Pipeline 已可用。

上述是第三阶段 A 的交付记录；后续暂停暂存源码进度见下一节。

## 15. 第三阶段 B：持久化计划与暂停暂存

本阶段增加内部 `StageNativePipelineSubmission` 原语，将「编译计划 → 持久化意图 → 创建暂停 Workflow → UID 确认」串成有界的一次处理。**它不恢复执行，不是已开放的原生运行入口。** API 仍只创建 KFP Run，queueML 的原生隔离保留，既有 pipeline-worker 没有调用这条暂存原语，也未增加原生写权限或开关。

### 15.1 处理顺序

1. 从已有 operation／执行回执重新读取命令与空间身份，复用现有授权、版本、租约与目标锁。传入 claim 的 Command／Space 不作为权威来源；只接受已存在的 NATIVE_ARGO submit 意图，不修改 KFP Run 的 engine。
2. 仅对尚未保存过意图的执行进行静态编译，并通过 Kubernetes 服务端 `dryRun=All` 校验／准入。构造器固定 `spec.suspend=true`；dry-run 不创建资源，返回的 UID／status 不作为执行证据。拒绝准入后取消暂停、设置 ttlStrategy／podGC／volumeClaimGC／shutdown 的结果。
3. 在 GORM 事务中一次性创建 attempt、submission 和静态定义记录。attempt.snapshot 采用版本化 `loom-native-staging-v1` 封装，冻结原命令、空间身份、编译器版本、计划、运维策略、本地规格摘要、准入后规格及其摘要。事务内不调用 Kubernetes。
4. 正式 Create 前先在独立事务中持久化 submission.attempted=true，事务末再次以数据库时间验证租约。K8s 调用前重新检查目标锁、执行授权和实际 Profile／namespace UID。
5. 以固定名称 `loom-<instance UUID>` GET／Create，再主动 GET，处理 Create 成功、超时或 AlreadyExists。只接受受管身份、空间标记、完整准入后规格摘要均匹配且仍暂停的对象。没有 UID 绑定时允许同名暂停请求重放；已绑定 UID 后对象缺失绝不重新创建，也不接管同名新 UID。
6. 再核验实际空间身份，以事务和 CAS 将同一个 Workflow UID 一次性保存到 journal／attempt，并记录 `STAGED_SUSPENDED`。不改写既有 UID，不回写成功状态，不完成 operation，不自动调用 resume 或清理。

单次暂存处理最多 45 秒。重试复用已冻结的 compiler／policy／plan／admitted spec，不按新配置重新 dry-run 或覆盖旧意图；恢复时用同一编译版本重算以核验冻结计划，不使用新版本悄悄重解释。尚无旧编译器版本注册表，版本不受支持时明确拒绝。

### 15.2 存储、定义登记及返回边界

小型快照暂以内嵌 JSON 保存在已有 attempt.snapshot，整个快照最多 2 MiB；单份 IR、计划及准入后 spec 各自仍受 512 KiB 上限约束。snapshot 不可变，summary 只保存暂存协议和阶段等运行时摘要，不伪造 plan_ref 或对象存储地址。大型计划对象存储写入／引用协议仍未实现。

静态定义登记与 attempt／journal 同事务：创建一个根 DAG 记录和每个静态容器任务记录，保存真实 IR definition_path、编译器 source map 和父子关系，**argo_node_id／pod_name／pod_uid 均保持 NULL**。这是定义预登记，不是实际 Argo／Pod 身份绑定。编译器证明无端口的这一小子集允许按 `loom-static-empty-v1` 协议封存空 IO；不表示任务执行成功，也不能推广为普通 SDK／产物输出的发布协议。

内部暂存回执仅表达「已核验 UID 的暂停对象」，不表达用户操作成功或运行完成。外部事实的 UID 回填可以在同一次 Create 后租约／授权失效时保存，以便后续清理，不借此授予执行权限；若整个调用已经结束且授权撤销，重新进入暂存仍会拒绝，后续清理／恢复入口须接管这类遗留意图。目前没有新增持久扫描服务，Informer 也不替代未绑定意图的恢复执行器。

native attempt 的内部快照不再由历史列表直接返回：该响应替换为明确的 redacted 标记，避免把编译命令、准入后规格和运维策略作为普通历史 DTO 暴露。历史 KFP attempt 响应不变，数据库原快照不被清空。

### 15.3 恢复及安全边界

| 中断点 | 后续处理 |
| --- | --- |
| dry-run 后、事务前 | 没有真实 Workflow，未提交的计划可重新准备 |
| 意图已提交、attempted 尚为 false | 读取冻结意图并持久化 attempted 后继续 |
| attempted=true、Create 返回结果未知 | 按固定名称 GET 核实；未绑定 UID 的暂停请求可安全重放，不另造名称 |
| Create 已发生、UID 保存失败 | 后续合法领取再次核验，原子补齐 journal／attempt UID；不先运行再补元数据 |
| 已绑定 UID，但对象不存在或同名换 UID | 报缺失／冲突，禁止自动重建／换绑 |
| 配置、准入或默认值变化 | 冻结规格不变；新对象无法精确匹配则拒绝，不能覆盖快照“修复”摘要 |

数据库与 Kubernetes 仍不是一个事务，不承诺全局 exactly-once；未绑定阶段重放只保证确定性名字下的受控暂停对象，不保证外部管理员从未删除过它。命名空间或 webhook 的管理者属于基础设施信任边界，准入必须保留暂停语义；不能声称可以防御有权限强行删除 suspend 的恶意准入控制器。

该代码没有执行实际 dry-run／Create，没有联调 Argo 或 MySQL。未新增迁移、配置、写权限、服务或 public route；新源码启动仍受既有 000022 迁移版本检查约束。后续正式装配需要明确的 create 权限（包括 dry-run）、真实节点／Pod 绑定、受控 resume、停止／删除／编辑及 operation 收尾；完整 proto／SDK、driver／launcher、产物发布、归档仍未完成。兼容性 API 的 runnable 继续为 false。

## 16. 第三阶段 C：静态真实节点与 Pod 绑定

本阶段把第三阶段 B 预登记的静态任务定义接到已有 Informer → WorkQueue → 同步事务中，补上实际 Argo node ID 和真实 Pod 身份。仅适用于已冻结的 `loom-native-static-v1` 扁平、无端口子集；**不是受控启动或完整运行闭环**，也没有为 native Run 新增公开创建入口。

### 16.1 身份证据与同步顺序

1. 仍先取得现有 Run 目标锁，核对 Run／attempt／journal 的 cluster、namespace UID、Workflow UID 和实际 Profile 身份。Informer 不建立首次 Workflow UID，也不从集群发现并创建业务 Run。
2. 从不可变 attempt.snapshot 恢复原操作、编译版本、运维策略、冻结计划和准入规格；复用暂存恢复校验，拒绝未知协议、摘要冲突或缺少冻结证据的旧记录。不能仅凭可变 task.runtime_state 里的 source map 接管节点。
3. 对已绑定的 Workflow 仍比较完整准入规格。仅将 spec.suspend 的 true／false／缺省归一为原始暂停值以进行比较；其他字段变化均拒绝，包括新加 TTL／GC／shutdown。允许观测暂停变化不代表该变化已经授权，也不代表已实现 resume。删除中的对象仍可用于保存已知事实，但删除本身不是成功或停止确认。
4. 从真实 status.nodes 读取 ID、name、type、templateName、templateScope、boundaryID。根节点须匹配本次 Workflow 名和根 DAG 模板；任务须匹配冻结计划生成的完整调用路径、容器模板及真实根节点 ID。displayName 不用于身份匹配，不重算或猜测 Argo node ID。
5. 静态子集允许未调度节点暂缺，也允许匹配定义的 Skipped／Omitted 节点；重复调用、额外节点、外部模板、错误父 DAG、retry／hook／动态结构等不受支持的形状整批拒绝。compressed／offload 节点状态继续按原限制记录不支持，不当成空节点成功。
6. 在 SQL 事务外、有界列举同 namespace 的 Workflow 标签 Pod。只有 controller ownerReference 的 API／kind／name／UID 都属于本次 Workflow，且 `workflows.argoproj.io/node-name` 对应真实调用，才可绑定；若同时存在 node-id 注解则必须相符。忽略仅标签相同但不归属本 UID 的 Pod，重复 Pod 认领同一静态节点则拒绝。最多 128 个 Pod，分页未完整时拒绝本批，不用部分结果完成绑定。
7. 在现有 Group → Run → Attempt → Submission → Task 锁序事务内，再核验冻结意图和 observation_version。读取所有静态定义（包括 argo_node_id 为 NULL 的记录），核对数量、NodeKey、definition_path、父子关系、无循环／重试、无缓存及空 IO 封存协议，然后将真实 node ID、Pod 名／UID 和节点阶段／时间与 attempt 观测原子保存。

Argo node ID、Pod name／UID 一次性绑定：只允许 NULL → 已核验身份或相同身份重读，不允许换绑。原绑定已存在但当前对象未出现时保留原值；Pod 缺失不等于可以生成名称、认领替代 Pod 或判定任务已停止。Pod 回填只根据真实列表，不依赖 Argo 可配置的 Pod 命名算法。

### 16.2 诊断与事务边界

| 情况 | 处理 |
| --- | --- |
| 静态定义尚未出现对应节点 | 保留定义／原事实，记录 NODE_BINDINGS_INCOMPLETE |
| 真实 Pod 尚未出现且未绑定过 UID | 可保存已核验节点事实，记录 POD_BINDINGS_INCOMPLETE，等待后续事件／resync |
| Workflow 已成功，但某节点仍非成功 | NODE_PHASES_INCOMPLETE，不投影业务成功 |
| 节点全部成功、空输出已封存 | 仍为 FINALIZATION_PENDING，等待最终归档／收尾协议 |
| 冻结计划、规格、节点来源或 Pod 读取失败 | 不覆盖上次执行事实，仅保存有界 sync_reason，交现有队列重试或等待 resync |
| 数据库内定义或既有节点／Pod 身份冲突 | 整批事务回滚；在独立观测版本 CAS 下记录 NODE_BINDING_CONFLICT，不半批换绑 |

runtime_summary 记录绑定协议 `loom-native-static-binding-v1`、匹配／缺失节点数、待绑定 Pod 数与非成功节点数。只直接消费 status.nodes；`nodeStatusComplete=false` 时各统计不能作为完整 DAG 或最终持久化证据。缺失时间戳不清空旧值，任务终态不被不同阶段覆盖。

静态空 IO 协议要求定义预登记时已封存且未有 runtime writer／report／cache；不把 Argo 的 outputs 当成正式产物登记，不能推广到普通 SDK 任务、可视化或有参数输出的任务。新增 Pod 读取复用现有资源只读权限，不增加集群写权限、独立 Pod Informer、扫描服务或同步表；Pod 晚于 Workflow 事件出现时依赖现有 Workflow resync 再读。

### 16.3 本阶段未完成的部分

暂存仍只创建暂停对象；没有受控 resume／启动放行检查点，没有 native worker 提交分派、停止／删除或 operation 回执收尾，没有最终快照归档和 TTL／GC 授权。完整 SDK protobuf、driver／launcher、参数／产物和可视化协议仍待实施。API 默认引擎和兼容性报告不变，runnable 仍为 false。

本次仅修改源码、格式化及静态差异检查；未新增或执行测试，未编译、运行项目、迁移数据库、重启进程或操作集群。Argo 状态字段和 Pod 注解的真实版本兼容仍需之后获准联调，不能将此阶段描述为已经上线可用。

## 17. 第三阶段 D：受控首次启动与接受确认

本阶段增加内部 `ResumeNativePipelineSubmission`，衔接已保存意图、已绑定 UID 的暂停 Workflow。**仅首次解除暂停，不是通用的再次恢复／重跑接口**；不创建 Run、不切换 KFP engine、不创建或重建 Workflow。仍没有 public route、native worker 提交分派或集群写权限装配，兼容性报告 runnable 继续为 false。

### 17.1 检查点与启动顺序

1. 必须配置 NativePipelineClient、空间验证器且 native_observer_enabled=true。从现有 operation／receipt 重新读取命令与空间身份，只接受有有效租约的 NATIVE_ARGO submit 意图。调用者传入的 Command／Space 不作为权威源。
2. 获取已有 Run 目标锁，续租并核实实际 Profile／namespace UID；恢复同一 operation 的冻结编译计划、准入规格、任务定义及配对 journal／attempt Workflow UID。不允许无 UID 或未 attempted 的记录启动。
3. 首次准备必须看到仍暂停、无启动令牌、status.phase 缺省或 Pending、无真实节点且未结束的 Workflow；数据库定义也须全部未绑定 node／Pod，phase=PENDING 且未记录开始／结束。复用静态定义校验，不临时补建缺失定义。
4. 在既有 attempt.runtime_summary.start 保存 `loom-native-start-v1` 检查点，包含 schemaVersion、operationId、Workflow UID、冻结快照／规格摘要、随机 UUID 启动令牌、**原始 resourceVersion** 和状态 PREPARED。随后在独立事务推进 ATTEMPTED，必须早于任何实际 Patch。每笔事务复用现有授权、domain／operation／receipt 锁及数据库时钟租约检查，事务中不调用 Kubernetes。
5. Patch 前再次核验目标锁、当前授权／租约和实际空间身份。只发送带 UID、原 resourceVersion、完整 spec、现有 annotations 和 suspend=true 的 JSON Patch tests；在同一个原子 Patch 内添加 `loom.ml/pipeline-start-token` 注解并设置 suspend=false。不能单独写令牌后再解除暂停，也不调用 Argo 的非条件式 resume。
6. 无论 Patch 返回成功还是结果未知，都再次 GET；只接受同 UID、同受管绑定、同冻结规格、同令牌且当前未暂停／未删除的对象。HTTP 200 不等于任务已经执行或成功。
7. 有权领取方在同一 GORM 事务内保存 CONFIRMED／首次确认 resourceVersion，绑定现有 tb_ml_execution 和 Run 的 external_id，并把原操作步骤推进到 OBSERVING，结果中仅标记 startAccepted=true、executionPending=true。操作仍为 RUNNING；不改写最新 Run 观测阶段，不增加伪造 report sequence，不设置完成时间或发出 GC 确认。

检查点状态只沿 PREPARED → ATTEMPTED → CONFIRMED 前进；token、原 resourceVersion、operation／UID／摘要不能替换。已有相同确认可以幂等重读；再次调用暂存不能擦除检查点或把已启动记录重新标成 STAGED_SUSPENDED。接受确认后 stagingState 表达历史事实 START_ACCEPTED，后续暂停由诊断表达，不抹去该事实。

### 17.2 丢失响应与外部变更

| 观测结果 | 处理 |
| --- | --- |
| Patch 前进程退出，原 RV 未变、无令牌且仍暂停 | 后续合法领取可重放同一固定 CAS 请求，不产生新令牌 |
| Patch 已生效但响应／数据库确认丢失 | 同 UID 的匹配令牌与未暂停状态证明启动被接受；GET／Informer 可补记确认，不再 Patch |
| 当前带匹配令牌，但 suspend=true | START_RESUSPENDED，保留已知事实，不自动再次恢复 |
| 确认前无令牌且 resourceVersion 已变化 | START_RESULT_UNCERTAIN，拒绝自动改用新 RV；原检查点保留 |
| 令牌不匹配、已确认令牌消失或无检查点出现运行 | START_TOKEN_CONFLICT／START_TOKEN_MISSING／START_CONFIRMATION_MISSING，不建立新的执行授权 |
| UID 改变、绑定对象缺失、规格漂移或正在删除 | 拒绝启动／换绑／重建；不把缺失当作启动成功或停止确认 |
| Patch 后租约／授权失效或新操作替代旧意图 | 原调用不能绕过 fence 写操作回执；已发生的接受事实可由后续 Informer 观测保存 |

这一版故意保守：**即使只是控制器更新 status 导致 RV 改变，也不自动重新准备请求**。因此可能需要后续显式的人工核实／冲突处理才能继续；当前没有提供重新授权／re-arm 入口。不能以“规格又变回原样”为由忽略中间发生过的启动、暂停或删除。之后若放宽这一边界，需增加可证明无执行过渡的版本协议，而不能盲目刷新 RV。

Informer 仅在已存在 ATTEMPTED 检查点和匹配 Kubernetes 令牌下补记 CONFIRMED，复用现有 attempt observation_version CAS；不持有用户启动授权也可记录这一事实，但不写 operation／receipt、不发起 Patch、不创建检查点。没有匹配证据时保存有界诊断；PREPARED 记录上的意外令牌不能被当作 Loom 已发送的启动。

### 17.3 回执隔离与剩余工作

通用 ReportMLExecution 拒绝对 native Run 产生新的执行回执，防止旧 KFP／外部适配器直接上报 SUCCEEDED 绕过任务、输出封存及最终快照条件；已提交回执的精确重放仍是只读。首次启动的内部接受确认不冒充该运行的终态回执，也不把实际 Workflow 成功降级成 RUNNING 写回已有观测。

历史 attempt API 继续隐藏内部 Snapshot，并从返回的 RuntimeSummary 移除完整 start 检查点，只保留 startState 和原有诊断统计；启动令牌、内部 operation 绑定及 CAS 请求不作为普通历史数据暴露，数据库记录不被清空。

未新增表、SQL 迁移、配置开关、定时扫描器或服务；使用现有 attempt JSON 和执行租约。部署前仍需经过明确审批与验证配置 Workflow patch 权限；当前 manifests 未增加此权限，也未真正调用 dry-run／Create／Patch。启用 observer 本身不会调用启动原语，更不等于开放原生引擎。

下一步仍需实现：最终快照与空 IO 的可证明收尾、native operation 终态、停止／删除／编辑恢复、受控 worker 分派；然后才是正式开放的引擎选择与完整 SDK／driver／launcher、参数／产物／可视化。所有成功场景继续等待 FINALIZATION_PENDING，不提供 TTL／GC 放行。

本次按项目约定只修改源码、格式化和静态差异检查，没有新增／运行测试、编译、运行项目、数据库迁移、重启或集群操作；上述协议尚未在真实 Argo／MySQL 验证。

## 18. 第三阶段 E：静态终态归档与操作收尾

本阶段补充 `pipeline_native_archive.go` 和内部 `FinalizeNativePipelineSubmission`，仅覆盖已冻结的静态无端口子集。**这是源码层的终态持久化原语，不是已经可用的原生执行产品**；public API、worker native 分派和 Workflow 写权限仍未装配，默认引擎仍为 KFP，兼容性报告 runnable 仍为 false。

### 18.1 沿用已有表与监听路径

- 继续使用现有 Informer → WorkQueue → Run 目标锁 → 同步事务，不新增轮询服务、收尾队列或业务表。
- 新增显式迁移 `000023_pipeline_native_final_snapshot.sql`，仅为已有 `tb_pipeline_run_attempt` 增加私有 JSON `workflow_snapshot` 和 `workflow_archived_at`。配合已有 `workflow_snapshot_hash` 使用，`workflow_snapshot_ref` 必须为 NULL，不伪造对象存储 URI。
- 原 `attempt.snapshot` 仍是不可变提交依据。最终证据单独保存，不能覆盖原 IR、准入规格、策略和操作绑定。没有实现对象存储写入器或大工作流归档；内联方案只服务当前有界子集。
- 新代码启动前需显式应用 **000022 和 000023**，已有 embed/schema Check 会检查它们。不得修改既有迁移校验和、跳过版本检查或依靠 AutoMigrate；本轮未执行任何迁移。DDL 不能按普通事务回滚，部署仍需按既有流程备份并核实 dirty 状态。

### 18.2 观测事务中的归档门槛

只有以下条件同时成立才归档：同一 namespace UID／Workflow UID／冻结规格；首次启动检查点 CONFIRMED 且实际启动令牌一致；Workflow 未暂停、未处于删除中；阶段为 SUCCEEDED 或 FAILED；完整、未 offload 的节点图与全部预登记定义一一对应；根 DAG 阶段与 Workflow 一致；每个节点已终结且有有效结束时间，非跳过节点有开始时间；时间前后关系有效。

成功要求所有节点成功。失败允许成功／失败／跳过节点，但仍要求完整定义、终态节点和 Pod 类型节点的真实 Pod 名／UID 已持久化。没有产生完整节点图或真实 Pod 的早期失败，当前不会直接完成 operation；保持可见的待收尾状态，不能合成节点、Pod 或结束时间。

复验静态定义、父子关系、输入输出封存协议、无缓存／runtime writer 等条件，且本 instance 的产物记录必须为零（包括 STAGING）。这里的空 IO 封存只表示编译器证明没有端口，不代表已实现一般参数、文件、指标或 SDK 产物输出。

归档协议为 `loom-native-static-final-v1`，包含原 operation／提交摘要／准入摘要／计划摘要、终态、任务数、空输出协议、任务最终事实摘要以及精简 metadata + 完整 spec/status 的 Workflow。任务摘要覆盖已登记身份、父子关系、真实 node／Pod 身份、终态、IO 摘要与时间，不包含观测计数和心跳。时间统一到 UTC 毫秒精度，避免 DATETIME(3) 的重读差异。紧凑 JSON 上限 1 MiB，读取时兼容 MySQL 序列化空白，并继续校验大小、深度、形态和摘要。

归档与节点投影、attempt.phase／时间／observation_version 在同一 SQL 事务提交。保存后设置 `finalizationState=SNAPSHOT_STORED`、`finalArchiveHash` 和 `sync_reason=FINAL_RECEIPT_PENDING`。实际 observed_state 保留成功／失败事实，但业务 status 暂为 RUNNING，不把完成事件直接当成操作完成。缺少节点／Pod／输出／启动确认或终态时间则保留相应诊断，不产生归档。

最终归档只写一次；后续 resync 可以更新诊断和观测时间，但不得替换归档、换绑身份、改写终态任务事实或开始／结束时间。违反已归档证据时整批回滚，并沿用独立的观测版本 CAS 保存有界失败诊断。后续阶段若增加产物写入，必须复用同一个 Run／attempt fence，不能在收尾检查之后绕锁写入。

### 18.3 持有租约的操作收尾

`FinalizeNativePipelineSubmission` 从持久 operation 读取命令，不信任调用者的 Command／Space／sequence，不读取今天的版本内容。取得 Run 目标锁后恢复冻结计划和最终归档；SQL 事务沿用授权栅栏 → Group → Run → Operation → receipt → Attempt → Submission → Task 锁序，并再次检查归档摘要、定义、最终任务摘要与零产物条件。

首次完成仍要求当前用户／空间授权有效、同一 instance／revision／last_operation、同一执行身份和有效数据库时钟租约。已被停止／编辑／删除替代的提交意图不能由旧收尾事务完成；授权被撤销时，监听器仍可保存事实，但不能借此绕过执行授权写完成回执。

在同一事务中完成：

1. 更新 Run 的 status／observed_state、external_id 和 applied_revision；不刷新 observed_at，归档重读不是新的 Kubernetes 观测。
2. 为原 tb_ml_execution 写入确定性 report ID、最终归档绑定摘要、递增 sequence、持有者／peer 和实际终态。
3. 在 attempt.runtime_summary 保存私有 finalizationReceipt，并推进 finalizationState=COMPLETED；清除待收尾诊断。
4. 追加一次既有 history 和 audit。Workflow 成功／失败分别令 operation 为 SUCCEEDED／FAILED，step=COMPLETED、设置 finished_at 并释放租约。
5. 最后一次 operation 条件 UPDATE 再检查 lease_owner 和数据库当前时间；已过期则全部回滚，不能半笔提交 Run、审计或检查点。

最终收尾只消费已持久化证据，不要求 Workflow 此刻仍存在，也不在 SQL 事务中调用 Kubernetes。**归档前已经丢失的对象无法靠此恢复**。相同原持有者／peer、目标 fence、归档和已提交回执的精确重放是只读，即使原租约过期、授权撤销或出现新意图，也不重复写序号、history、audit，更不覆盖新 Run；改变身份或回执不算精确重放。

### 18.4 隐私、清理与剩余范围

两个快照、归档摘要／时间和完整运行时摘要均不通过普通 attempt JSON 响应暴露；脱敏辅助函数也删除内部收尾回执。归档移除 managedFields 和无关 metadata，**不声称能自动识别并脱敏 spec argv 或 status message 中的所有敏感值**；它是私有执行证据，数据库访问、保留和加密策略仍是部署方责任，不能直接用作公共 Workflow DTO。

SNAPSHOT_STORED／COMPLETED 不是 GC 授权。本阶段不修改 ttlStrategy、不添加自动 Pod／Workflow 清理、不删除资源，也不启用独立 Argo TTL。日志归档、一般产物、offload、大快照对象存储、停止／删除／编辑恢复、worker 分派与公开引擎入口尚未实现；完整 SDK／driver／launcher、参数／产物／可视化、缓存、实验和定时任务继续待实施。

仅完成源码修改、gofmt 与静态差异检查；未新增或运行测试，未编译、启动、迁移、重启或调用集群。仍不能卸载 KFP 或宣称原生链路已经联调可用。

## 19. 第三阶段 F1：已归档实例的显式删除与缺失观测

本阶段增加内部 `DeleteArchivedNativePipelineWorkflow`。**仅支持已有完整终态归档的 DELETE／EDIT 清理请求，不是运行中停止、自动 GC 或完整删除／编辑闭环。** 不接 public route／worker 分派，不新增服务、表、SQL 迁移或集群写权限。旧 KFP 主线和原生兼容性报告不变。

最终 Workflow 证据不包含容器日志归档。本入口不承诺删除后仍能读取原 Pod 日志；若日志保留属于运行契约，必须先补齐并核验日志归档条件，不能直接装配此删除路径。

### 19.1 必须先有持久化执行证据

从持久 operation 读取真实命令和绑定空间，不信任调用者提供的 Command／Space。复用现有授权栅栏、Group／Run／operation／receipt 锁、数据库时钟租约和 Run 目标锁；只接受当前 instance／revision／last_operation 的 native DELETE 或 EDIT。沿用现有策略：已授权的清理操作可在空间冻结、请求者随后撤权时继续，但仍须满足清理租约和空间身份绑定，不能据此重新运行资源。

原 submit 的不可变快照、配对 journal／attempt UID、完整终态归档、静态任务最终事实摘要及零产物条件均需复验。清理命令必须仍指向原实例和原执行配置；编辑命令另核对 pending_spec，但不把新配置拿来认领旧执行。归档可在业务清理意图之后、实际 Delete 准备之前完成；原 submit 操作不必先成功收尾，新的显式清理授权与原提交回执是两回事。

没有完整归档、Workflow UID 未绑定、提交结果未知、运行中或过早失败而节点证据不全的实例均拒绝本入口。不会为方便删除而生成归档、伪造停止、跳过节点或从同名对象补绑 UID。

### 19.2 检查点先于删除请求

在已有 attempt.runtime_summary.cleanup 保存 `loom-native-archived-cleanup-v1` 检查点：清理 operation ID、命令／空间摘要、原提交快照摘要、最终归档摘要、Workflow UID、准入规格摘要、固定请求及阶段。阶段只沿 PREPARED → ATTEMPTED → ABSENT 前进；ATTEMPTED 必须在任何实际 Delete 之前提交。每次检查点写入都有 attempt.observation_version CAS 和事务末尾的当前租约检查。

首次准备读取实时 Workflow，比较冻结规格、完整终态图、时间、真实节点映射及原启动令牌与归档证据，再冻结当时的 resourceVersion。允许观测已被其他参与方标记删除的同 UID 终态对象，但不取消其删除。若准备时对象已经不存在，只保存 OBSERVE_ABSENCE 请求：后续若对象出现则拒绝，不能把只读缺失观测升级成删除授权。

请求固定为以下模式之一：

| 模式 | 允许行为 |
| --- | --- |
| DELETE | 对同 UID、同原始 resourceVersion 的已归档终态 Workflow 发起 foreground Delete；已在删除中则只观测 |
| OBSERVE_ABSENCE | 只核验对象及已知关联 Pod 缺失，绝不发 Delete |

执行前再次验证目标锁、租约、实际 Profile／namespace UID 及持久化任务证据。DeleteOptions 同时带 UID、resourceVersion 前置条件与 Foreground 传播策略；不 force、不移除 finalizer、不直接删除 Pod，不添加 ttlStrategy。即使只发生状态更新导致 resourceVersion 变化，也不自动换版本重试；需要后续显式冲突处理，当前没有 re-arm 入口。

### 19.3 DELETE 成功不是缺失证明

无论 Delete 成功、超时、冲突还是返回 NotFound，都主动 GET。仍存在（包括 Terminating）时仅返回待观测；同名换 UID、规格漂移或无法核验终态时拒绝。相同原请求可由同一 operation 的后续合法租约领取方继续，不需要重新生成请求。

只有 Workflow 实时 NotFound 后才检查 Pod：

- 从已锁定并复验摘要的任务记录恢复真实 Pod 名／UID，不猜测名称。
- 有界列举同 namespace 全部 Pod，最多 2048 条，不用标签选择器作为缺失证明；分页不完整时等待后续处理，不视为空集。
- 已保存的任一 Pod UID 仍存在（即使标签／owner 已移除），或任一 Pod ownerReference 仍携带原 Workflow UID（包括非 controller 引用），都阻止确认。Succeeded／Terminating Pod 同样算仍存在。
- 列表后再次 GET Workflow，检测期间同名对象重现，并复验空间／授权／租约。两类对象均缺失才能在后续受租约保护的事务中保存 ABSENT。

这只是限定静态子集下的一次缺失观测，不承诺以后永不出现外部对象，也不能证明节点隔离时宿主机进程已停止。Argo／Kubernetes 控制器、节点生命周期和基础设施管理者仍属于信任边界；不据此授予立即重跑或独占数据访问。

同一实例当前只允许一份绑定原清理 operation 的检查点：租约接管可恢复相同命令，但新 revision／新 operation 不能覆盖旧检查点。已被新操作替代的旧调用失去写权限；新操作如何验证并承接历史清理请求尚待后续协议实现，不通过擦除旧证据自动接管。

### 19.4 仍未完成的业务收尾

内部返回区分 WorkflowAbsent 与 PodsAbsent。即使两者为 true，operation 仍为 RUNNING／OBSERVING、executionPending=true；本阶段不设置 Run.deleted_at、不推进 applied_revision、不标记 execution_cleaned、不应用 pending_spec、不写终态 report sequence、history 或 audit。需要下一阶段在有效 fence 下将缺失证明与逻辑删除／编辑应用原子提交。原 submit 的成功／失败归档与清理缺失状态分别保留，不把原执行终态改写成 DELETED。

普通 Informer 的 NotFound 仍只是 RESOURCE_NOT_FOUND 诊断，不会补发 Delete、核验 Pod 清单或完成操作。丢失清理响应后依赖持久 operation 的后续合法领取调用本入口恢复；现有 worker 尚未分派此入口。普通 attempt 响应继续隐藏运行时摘要，辅助脱敏函数亦移除完整 cleanup 检查点。

本轮只改源码和文档、gofmt 及静态差异检查；未新增／执行测试，未编译、运行、迁移、重启、执行删除或操作集群。运行中终止、停止结果归档、删除／编辑最终回执、清理后元数据操作及 worker／公开入口装配仍待实施。
