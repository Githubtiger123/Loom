# 本轮后端补全说明

2026-11-13 状态观测更新：空间、Notebook、原生 Pipeline 和 KServe 已在源码中统一使用 Informer + WorkQueue，保留各自实时核验与兜底对账；外部 KFP 和 Trainer 边界不变。下文早期轮询/Watch 描述以 [资源状态观测](resource-observation.md) 为准。本次未编译、测试或部署。

本轮只修改后端源码和说明，不做前端、部署或集群底层安装；后续按用户要求补入独立 Notebook 镜像配置。遵循 GORM、单文件 route 包、公共校验集中工具包、显式 SQL 迁移的项目约定。没有新增测试，没有测试、编译、运行应用或执行迁移，因此不能把下述源码实现当成已联调交付。

## 实现范围

| 范围 | 已实现的业务链路 | 外部前提 / 边界 |
|---|---|---|
| 认证 | 设备会话、按设备撤销、过期 Access 使用有效 Refresh 注销、强制初始改密、独立 Notebook 主机 Cookie 交接 | 只用 Access JWT 和 Refresh，SQL 持久摘要，Redis 可选；网关必须落实内部认证协议 |
| Notebook | create/start/stop/update/delete 持久执行，真实观测、事件、日志、诊断、资源用量、独立镜像配置加载与快照 | 信任已有 notebook-controller；镜像拉取与运行兼容性未验证；metrics 缺失明确不可用 |
| 额度 | 实际 ResourceQuota 身份读取、创建/启动预算占用、执行确认后计入、停止确认后释放、配额调整 | SQL 预算不能代替 Kubernetes 强制准入；CPU/内存/GPU 预算不等于磁盘实际使用量 |
| 工作目录 | 独立资产、保留保护、同空间复用、关联/解除、负责人交接、安全删除 | 不能任意认领 PVC；原生 UID/实例标记不变；不提供文件浏览或磁盘文件占用采集 |
| 空间 / 用户 | 停用、恢复、安全删除；成员移除和用户禁用生成 Notebook 停止任务、保留数据并等待撤权 | 固定 Profile Owner 始终为配置中的 admin 身份；有 live ML 时先显式停止，当前不自动取消训练 |
| Pipeline | 定义、IR 导入/版本原地编辑、修订历史、参数解析、运行快照、内置 KFP 提交/停止/删除/状态轮询、运行编辑清理后手动重建、节点详情、本地查询投影 | 需兼容客户端确定性 Run ID 的官方 KFP 与独立 pipeline-worker；只接入单文档 v2 IR 主线 |
| Trainer | 训练规格、创建/停止/删除、分页、历史、操作单 | 外部 Trainer 适配器负责受管 runtime/code/data/output 解析和提交 |
| Pipeline 终端 | 公共 podexec、组内 pipeline.exec、节点/空间归属校验、WebSocket 输入/输出/缩放、准入与关闭审计 | 需显式 000019 迁移及机器 pods/exec 权限；仅运行中的普通容器和镜像自带 shell，未做前端/独立调试 Pod；见 [终端](pipeline-terminal.md) |
| Pipeline 日志 | 在线快照/follow、MinIO 离线清单与分段读取、历史和软删除 Run 授权 | 复用 pipeline.logs；无新日志表。采集端需提前上传约定格式，删除不等待归档完成；详见 [离线日志](log-archives.md) |
| KServe | 服务、不可变修订、MODEL/CUSTOM、修改/部署/停止/删除、Watch 与周期对账 | 内置 kserve-worker 管理结构化 InferenceService；仍需集群 CRD/RBAC 与真实联调 |
| 模型注册 | Model/Version/Artifact、组与对象权限、CAS 更新、软归档、Pipeline 来源、KServe Artifact 快照 | 模型文件和凭据不进 MySQL；Catalog、上传器、CSI 与前端未实现；见 [模型注册](model-registry.md) |
| 机器执行桥 | 独立 mTLS listener、领取/续租/回填/回执、证书绑定、外部 ID、单调序号、过时目标隔离 | 不是实际适配器，不保证跨系统 exactly-once；必须按执行桥契约持久幂等并收敛迟到副作用 |

## 请求受理与运行结果

写接口继续使用 `Idempotency-Key`。需要执行的请求返回操作单，必须查询 operation 和对象的 desired/observed/appliedRevision，不能以 HTTP 202 判断运行成功。

Notebook 的运行观测核对 CR、StatefulSet、Pod 的 UID 归属链。停止需实际 Pod 消失；默认删除 Notebook 保留工作目录。删除目录需显式解除保留并确认名称，还会检查业务引用和 namespace 中全部 Pod 的挂载。

空间删除先冻结并撤权，再完整发现 namespace API 与列举对象。只允许明确识别的系统对象存在，未知资源或无法完整列举会阻断删除，不递归清空用户资源。

空间停用先冻结业务并持久化 Notebook 停止任务，再等待真实停止与成员访问撤销；恢复重新检查基础设施并落实当前有效成员权限，不自动启动旧 Notebook。用户禁用和移除成员也不会把数据自动移交给管理员。

Pipeline 和 Trainer 的提交回执只代表已提交，不代表运行成功。外部 ID 固定、租约、实例/revision 和观察序号共同约束回填。需要删除实际资源的操作须确认目标真实不存在，不接受“已发出删除请求”作为完成证据；Pipeline Run DELETE 会终止、删除 KFP Run 并确认 404，Pipeline 定义 DELETE 仍只归档业务定义。

## 入口与代码位置

- 所有业务和机器 URL 注册仍在 `internal/route/route.go`。`route.New` 是用户入口，`route.NewExecutor` 仅挂独立机器 mTLS 监听，公网 mux 不注册内部路径。
- `cmd/platform/main.go` 装配 API、worker、pipeline-worker、sync、cron；`business_dependencies.go` 注入额度、Notebook 只读客户端和 KFP 空间身份核验器。
- Profile/访问/Notebook/空间/目录执行使用原 worker 持久队列。内置 KFP/KServe 分别由独立 pipeline-worker/kserve-worker 领取；Trainer 继续由机器执行桥供外部适配器领取。
- 源码入口分别位于 `internal/service`、`internal/worker`、`internal/k8s`、`internal/executor`，持久化使用独立 model/repository 和版本化 SQL。

详细契约分别见 [认证](authentication.md)、[Notebook](notebook-api.md)、[生命周期与工作目录](lifecycle-api.md)、[ML 业务](ml-api.md)、[KFP 主线](pipeline-kfp.md)、[KServe](kserve.md)、[模型注册](model-registry.md)、[机器执行桥](../internal/executor/README.md)。

## 升级与配置

已有库保留 000001–000006，新增迁移按文件名排序：

| 文件 | 内容 |
|---|---|
| 000007_auth_sessions.sql | 强制改密标记、设备会话元数据、旧 family 回填 |
| 000009_workspace_lifecycle.sql | 空间状态、目录、目录关联、Notebook 负责人、额度占用 |
| 000010_ml_workloads.sql | Pipeline/版本/运行、Trainer、KServe/修订、历史 |
| 000011_ml_execution.sql | 机器执行回执 |
| 000012_backend_permissions.sql | 显式升级权限字典与内置角色，不改自定义角色 |
| 000013_ml_observation.sql | 执行空间身份与部署完成后的 KServe 健康观测回执 |
| 000014_notebook_server_type.sql | Notebook 类型持久化及旧 Jupyter 规格兼容回填 |
| 000015_pipeline_kfp.sql | Pipeline DELETED 状态约束，以及内置 KFP 提交意图、请求摘要和确定性 Run ID 恢复日志 |
| 000016_pipeline_sync.sql | 独立状态观测调度、租约、索引和同步新鲜度；不依赖操作单或原用户权限 |
| 000017_pipeline_edit.sql | 版本原地编辑及修订历史、运行 pendingSpec/清理标记、旧执行快照和编辑权限 |
| 000018_pipeline_logs.sql | Pipeline 在线日志独立权限及内置角色显式升级；公共 Reader 同时复用至 Notebook 日志快照 |
| 000019_pipeline_exec.sql | Pipeline 组内终端权限，显式授予内置 owner/editor，不新增终端表 |
| 000020_kserve.sql | KServe CR 身份、地址、就绪状态和 Watch/GET 同步租约投影 |
| 000021_model_registry.sql | Model/Version/Artifact 表、模型权限与内置角色显式升级 |

000008 未使用，迁移器不要求编号连续。升级前停止旧认证写入者，禁止旧版认证与新版会话逻辑混跑。显式迁移后再部署新版进程，启动只检查版本和 checksum，不自动建表。既有本地用户下一次登录需改密，成功改密撤销旧会话并重新登录。

Notebook 独立主机配置 `auth.notebook_origin`，须为不同 hostname 的 HTTPS origin，不能只换端口。返回的 access URL 使用该 origin，Refresh 只留平台域名。浏览器隐私策略可能限制真正跨站 Cookie，建议使用独立同站子域；网络和 Cookie 隔离不替代容器/namespace 隔离。

机器入口由 `executor.enabled` 显式开启，配置独立地址、服务证书/私钥、客户端 CA 和客户端完整叶证书 DER 的 SHA256 指纹。只允许 TLS1.3 和双向证书认证，不接收用户 JWT 或转发证书头。两监听任一失败会触发共同关闭。

KServe 内置 worker 通过 Watch 持续写回健康，并以周期 GET 修复漏事件。停止会删除 CR，确认不存在后清空当前 UID 绑定；后续显式发布可绑定新 UID。观测时间不再新鲜时不能将历史 SERVING 当成实时健康证明。

## 未包含内容

Pipeline 后端已补充 [DAG 与节点详情](pipeline-dag.md)，按本次运行快照与官方 MLMD 读取结构、执行实例、错误、参数和产物元数据，不新增业务数据库表。产物下载/可视化、已删除 KFP 执行的完整 DAG 元数据归档仍未包含。

前端、任意历史资源自动迁移、Trainer 实际适配器、受管训练引用解析、Model Catalog/文件上传器/CSI、KFP Experiment/定时调度/产物下载等完整功能、网关部署、集群 RBAC 安装、MFA/自助找回均不在本轮完成范围。空闲回收也未开启。

Notebook 命令行入口现已按 `images.path` 装配镜像目录。Pipeline/KServe 需分别显式启用并部署独立 worker；它们不依赖机器 listener。Trainer 仍需外部适配器，未接执行端时继续等待，不伪造运行结果。
