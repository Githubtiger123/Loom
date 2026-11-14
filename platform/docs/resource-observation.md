# Kubernetes 资源状态观测

2026-11-13：源码统一为 `Informer + WorkQueue`；未编译、测试、启动或部署验收。

## 公共机制

- `internal/k8s/informer.go` 创建动态 Informer，使用独立、无全局短超时的 List/Watch 客户端。普通实时 GET 保留原超时；client-go 管理 List、Watch 重连和重新列举，缓存重投递周期 5 分钟。
- `internal/worker/observer.go` 统一初始缓存同步、事件注册、tombstone、队列去重、限速、指数退避和协程回收。回调只投递键，不做数据库/网络 I/O；更新通知同时映射旧、新对象。初始缓存最多等待 1 分钟，必需 API 或 list/watch 权限缺失会导致该角色启动失败；单次业务同步最多 30 秒。
- 模块从数据库登记项解析目标身份，再复用实时读取、UID/instance/revision、目标锁或持久租约及事务回写。事件不是授权或执行结果，不自动认领同名对象、重建资源或改变业务期望。

## 接线

| 角色 | 监听资源 | 状态同步 |
| --- | --- | --- |
| sync | Profile、Namespace、ResourceQuota、ServiceAccount、RoleBinding、ClusterRole、AuthorizationPolicy；Notebook、StatefulSet、Pod、Service、PVC | 变化映射至空间/Notebook 数据库 ID，沿用现有核验；2 个处理线程，队列每秒最多启动 5 次处理，数据库 max_open 至少 6 |
| pipeline-worker（原生） | Loom 管理的 Argo Workflow | namespace/name 入队，复用官方 backend 的绑定、发布及终态核验 |
| kserve-worker | Loom 管理的 InferenceService，以及 Deployment、ReplicaSet、Pod | namespace 通知分页投递服务 ID，再逐服务领取持久租约、实时核验发布；删除通知也必须 GET 确认 |

下层资源不保证继承 Loom 标签，不能按该标签过滤；数据库登记与实时 UID 归属链才是处理边界。集群范围监听有缓存成本，规模扩大后需评估按空间拆分/元数据缓存，未宣称完成压测。

## 低频对账

分页 sweep 游标跨轮保留，查询超时不从头重扫，完整到尾后才归零；空间与 Notebook 各自有独立游标及 10 秒预算，避免慢查询长期饿死末尾记录。

- sync 启动后和每分钟按 ID 游标分页投递已登记空间、Notebook，覆盖停机删除、业务记录晚于资源通知提交及新鲜度续验。它只投递同一队列，不另起高频遍历读集群。Notebook 原两分钟过期策略保持不变；队列积压可能导致过期，缓存事件不作为新鲜证明。
- 原生 Pipeline 每分钟分页投递当前 release、当前实例且未终态归档的已绑定 Workflow。缺失仍沿用原 backend 保守处理，不推断成功、不自动接管历史引擎。
- KServe 保留 `SyncNextKServe` 持久租约调度与失败退避；事件队列单线程，启动/低频 sweep 同样分页投递服务 ID，周期取 `kserve.resync_interval` 且至少 1 分钟。事件与周期路径共用租约和写回 fence，正在执行操作期间暂不观测，后续对账补查。数据库连接预算包含执行器、周期观测、事件处理和 sweep。

无新增表/迁移。外部 KFP 仍查 KFP API；Trainer 实际适配器未接入，不宣称已有 Trainer CR Informer。模型注册是业务元数据，不增加 Kubernetes 监听。事件、日志和用量诊断继续按需读取，不全量写库。

## 发布要求

更新 `install/kubernetes/loom/base/rbac.yaml`：新增 ClusterRole、RoleBinding、AuthorizationPolicy 的 watch 权限，没有增加 Secret watch 或资源写权限。自定义 kubeconfig 身份也需具备对应 list/watch。Profile、Notebook、Istio 必需 API 未安装时 sync 会明确启动失败，不静默退回旧轮询。

升级后启动 sync 及显式启用的 pipeline-worker/kserve-worker；还需验收事件变化、Pod 崩溃、删除/同名重建、断线、重启、并发启停和权限失败。未自动更新集群或重启服务；List/Watch 连接由后端主动发起，不增加云端回连本地要求。
