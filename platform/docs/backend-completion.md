# 本轮后端补全说明

本轮只修改后端源码和说明，不做前端、部署或集群底层安装；后续按用户要求补入独立 Notebook 镜像配置。遵循 GORM、单文件 route 包、公共校验集中工具包、显式 SQL 迁移的项目约定。没有新增测试，没有测试、编译、运行应用或执行迁移，因此不能把下述源码实现当成已联调交付。

## 实现范围

| 范围 | 已实现的业务链路 | 外部前提 / 边界 |
|---|---|---|
| 认证 | 设备会话、按设备撤销、过期 Access 使用有效 Refresh 注销、强制初始改密、独立 Notebook 主机 Cookie 交接 | 只用 Access JWT 和 Refresh，SQL 持久摘要，Redis 可选；网关必须落实内部认证协议 |
| Notebook | create/start/stop/update/delete 持久执行，真实观测、事件、日志、诊断、资源用量、独立镜像配置加载与快照 | 信任已有 notebook-controller；镜像拉取与运行兼容性未验证；metrics 缺失明确不可用 |
| 额度 | 实际 ResourceQuota 身份读取、创建/启动预算占用、执行确认后计入、停止确认后释放、配额调整 | SQL 预算不能代替 Kubernetes 强制准入；CPU/内存/GPU 预算不等于磁盘实际使用量 |
| 工作目录 | 独立资产、保留保护、同空间复用、关联/解除、负责人交接、安全删除 | 不能任意认领 PVC；原生 UID/实例标记不变；不提供文件浏览或磁盘文件占用采集 |
| 空间 / 用户 | 停用、恢复、安全删除；成员移除和用户禁用生成 Notebook 停止任务、保留数据并等待撤权 | 固定 Profile Owner 始终为配置中的 admin 身份；有 live ML 时先显式停止，当前不自动取消训练 |
| Pipeline | 定义、不可变 IR 导入、参数解析、运行快照、内置 KFP 提交/停止/状态轮询、节点详情、终态归档 | 需官方 KFP 与独立 pipeline-worker；只接入单文档 v2 IR 主线，归档不删除外部记录 |
| Trainer | 训练规格、创建/停止/删除、分页、历史、操作单 | 外部 Trainer 适配器负责受管 runtime/code/data/output 解析和提交 |
| KServe | 服务、不可变修订、修改/部署/停止/删除、历史 | 外部适配器负责 model/runtime 解析、真实部署与观测 |
| 机器执行桥 | 独立 mTLS listener、领取/续租/回填/回执、证书绑定、外部 ID、单调序号、过时目标隔离 | 不是实际适配器，不保证跨系统 exactly-once；必须按执行桥契约持久幂等并收敛迟到副作用 |

## 请求受理与运行结果

写接口继续使用 `Idempotency-Key`。需要执行的请求返回操作单，必须查询 operation 和对象的 desired/observed/appliedRevision，不能以 HTTP 202 判断运行成功。

Notebook 的运行观测核对 CR、StatefulSet、Pod 的 UID 归属链。停止需实际 Pod 消失；默认删除 Notebook 保留工作目录。删除目录需显式解除保留并确认名称，还会检查业务引用和 namespace 中全部 Pod 的挂载。

空间删除先冻结并撤权，再完整发现 namespace API 与列举对象。只允许明确识别的系统对象存在，未知资源或无法完整列举会阻断删除，不递归清空用户资源。

空间停用先冻结业务并持久化 Notebook 停止任务，再等待真实停止与成员访问撤销；恢复重新检查基础设施并落实当前有效成员权限，不自动启动旧 Notebook。用户禁用和移除成员也不会把数据自动移交给管理员。

Pipeline 和 Trainer 的提交回执只代表已提交，不代表运行成功。外部 ID 固定、租约、实例/revision 和观察序号共同约束回填。需要删除实际资源的操作须确认目标真实不存在，不接受“已发出删除请求”作为完成证据；Pipeline DELETE 仅为终态本地归档，不删除外部 KFP 资源。

## 入口与代码位置

- 所有业务和机器 URL 注册仍在 `internal/route/route.go`。`route.New` 是用户入口，`route.NewExecutor` 仅挂独立机器 mTLS 监听，公网 mux 不注册内部路径。
- `cmd/platform/main.go` 装配 API、worker、pipeline-worker、sync、cron；`business_dependencies.go` 注入额度、Notebook 只读客户端和 KFP 空间身份核验器。
- Profile/访问/Notebook/空间/目录执行使用原 worker 持久队列。内置 KFP 启用后由独立 pipeline-worker 领取 Pipeline 动作；Trainer/KServe 由机器执行桥供外部适配器领取，不混入原 Kubernetes worker。
- 源码入口分别位于 `internal/service`、`internal/worker`、`internal/k8s`、`internal/executor`，持久化使用独立 model/repository 和版本化 SQL。

详细契约分别见 [认证](authentication.md)、[Notebook](notebook-api.md)、[生命周期与工作目录](lifecycle-api.md)、[ML 业务](ml-api.md)、[KFP 主线](pipeline-kfp.md)、[机器执行桥](../internal/executor/README.md)。

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
| 000015_pipeline_kfp.sql | 内置 KFP 提交意图、请求摘要及外部 Run ID 恢复日志 |

000008 未使用，迁移器不要求编号连续。升级前停止旧认证写入者，禁止旧版认证与新版会话逻辑混跑。显式迁移后再部署新版进程，启动只检查版本和 checksum，不自动建表。既有本地用户下一次登录需改密，成功改密撤销旧会话并重新登录。

Notebook 独立主机配置 `auth.notebook_origin`，须为不同 hostname 的 HTTPS origin，不能只换端口。返回的 access URL 使用该 origin，Refresh 只留平台域名。浏览器隐私策略可能限制真正跨站 Cookie，建议使用独立同站子域；网络和 Cookie 隔离不替代容器/namespace 隔离。

机器入口由 `executor.enabled` 显式开启，配置独立地址、服务证书/私钥、客户端 CA 和客户端完整叶证书 DER 的 SHA256 指纹。只允许 TLS1.3 和双向证书认证，不接收用户 JWT 或转发证书头。两监听任一失败会触发共同关闭。

KServe 部署完成后可用机器 `/internal/v1/ml-executions/{oid}/observe` 持续回报健康，独立序号不重新开启部署操作，不允许修改外部 UID。当前 stop 合同要求适配器确认停止服务且保留原生 UID；仅支持通过删除来停用的运行环境不能虚报 STOPPED，应显式删除旧业务服务再新建。观测时间不再新鲜时不能将历史 SERVING 当成实时健康证明。

## 未包含内容

前端、镜像管理接口、任意历史资源自动迁移、Trainer/KServe 实际适配器、受管引用解析、KFP Experiment/定时调度/产物下载等完整功能、网关部署、集群 RBAC 安装、MFA/自助找回、LIST/WATCH 游标均不在本轮完成范围。空闲回收也未开启：没有可信 kernel 活动源时不能根据页面访问或旧 annotation 自动停机。

Notebook 命令行入口现已按 `images.path` 装配镜像目录，配置细节见 [镜像配置](notebook-images.md)。配置不能替代镜像拉取、运行和网关联调。Pipeline 需显式启用 `pipeline.enabled` 并独立部署 pipeline-worker；它不依赖机器 listener。Trainer/KServe 仍需部署外部适配器，未接执行端时继续等待，不伪造运行结果。
