# Loom 官方 KFP 内核

该模块复用官方编译和执行语义，不再维护另一套 IR lowering/Python runtime。固定上游 revision：`555bfa60a70da88c594960bdacfd78fa7c061043`。

## 代码中的调用链

Python SDK 编译 IR → Loom Pipeline/Version/Run → 官方 `argocompiler.Compile` → 冻结并创建暂停的 Workflow → 绑定 UID → 放行 → 官方 driver/launcher → Loom 专用 Runtime API → 同一业务数据库 → Informer 观测/终态收尾。

这是源码接线路径，**尚未构建、运行或集群验收**。默认开关全部关闭；显式配置原生执行/观测时会注册 concrete backend。`Ready` 和兼容性报告中的 `runnable` 仅检查本地装配、配置及编译条件，不是实时健康检查或 KFP 兼容认证。业务仍由 Loom API 和同一业务数据库管理，不部署第二套 KFP API/MLMD。

## 已实现的源码

- `Inspect`：官方 protobuf 解码和规模/递归保护。
- `Compile`：真正调用官方 Go argocompiler；补入 Loom 身份、SA、并发、固定 digest 的 runtime 镜像、冻结存储根、暂停状态和最终摘要；移除自动 TTL。
- `ExecuteDriver`：保留八个官方非测试 driver 文件，注入 metadata/cache/store；条件、循环、表达式和输入解析不自写第二套。
- `ExecuteComponent`：调用保留的官方 launcher，实现原有命令替换、Python 启动、文件/目录上下传与元数据发布。
- `metadata`：保留官方模型/转换代码，补私有上下文构造器和 oneof codec；从官方实现摘取 config→protobuf、DAG 索引和聚合。
- `runtimeclient.Metadata`：官方高层 metadata 接口到 Loom 协议的适配；不连接 MLMD RPC。保留的 metadata RPC 实现不由运行入口实例化。
- `runtimeapi`：固定 HTTPS origin、client-go 轮换 token、禁止重定向、协议/报文上限。平台已注册独立 TLS listener 的机器路由，不挂公开用户 API。
- 任务级认证：TokenReview audience、Pod-bound token，实时 namespace/SA/Pod/Workflow UID，再核对数据库冻结策略及 driver/launcher 角色、execution ID、真实 Argo 节点输入。适配官方 launcher 的 Command/Args 布局。
- GORM 元数据、内容寻址回执、输出原子发布、范围/数量约束；driver 重试使用逻辑任务身份，已完成发布重放不会重新执行用户命令。终态归档后拒绝新写入。
- CLI `cmd/driver` / `cmd/launcher-v2` 保留官方 flags/output/copy 契约；提供双 target Dockerfile 和 CI 发布矩阵，尚未构建。
- 平台 backend 在显式配置后注册，包含权威 operation 重读、租约/fence/目标锁、暂停创建、UID 持久绑定、丢响应恢复、UID/RV 条件停止/删除、Informer 证据与业务观测状态写回及内部终态证明，不将资源消失推断为成功。
- 终态成功要求官方 publication 与 Argo 节点一致，文件/目录对象可见；终态快照先入库再清理。Metrics 类型不伪造文件对象。
- 官方执行记录投影到现有 DAG、输入输出、数值指标、精确文件/预览与在线日志接口，旧 v3 表不混读。前端展示物理 Pod 重试历史；已发布的 executor-logs 支持 Range/ETag 离线读取，原始目录可 ZIP 下载，成功归档的产物可复制登记为模型。
- 自动重试验证同一 Argo Retry 的相邻物理节点后接管，保留 epoch/历史；输出已提交的重试只重放结果，不重新执行用户命令。
- 缓存沿用官方 fingerprint/key：候选在整个 Workflow 成功、发布及产物清单核验后可见；隔离空间 incarnation/namespace UID/流水线/创建者/引擎，复验权限和保留期，复制到目标 attempt 后登记本地产物身份。重试冻结已有 hit/miss 决定，不复用源日志。

## 仍关闭的能力与发布门槛

1. 重试接管最多 32 个 epoch；一次发布幂等性不等于任意用户副作用的 exactly-once。
2. 缓存默认关闭，开启还需独立的前缀受限复制凭据，组件镜像全部固定 digest。缓存只在整个 Workflow 成功后可见，比官方任务级缓存保守；7 天过期，过期索引及未引用复制对象尚无自动 GC。
3. ETag/清单复核是观测一致性边界，不是对象锁或内容不可变保证。目录清单最多 4096 文件、ZIP 原始文件总量最多 32 MiB、模型复制最多 10 GiB。缓存适用于无外部副作用、输出稳定的组件。
4. 离线日志只包含 launcher 已发布的用户命令日志，不含全部 init/系统日志；发布前异常退出可能没有归档。缓存命中不展示源执行日志。严格 spec 校验与 Argo admission/defaults 的兼容及故障恢复仍需真实发布验收。
5. importer、artifact iterator、Kubernetes platform/PVC/Trainer/Volcano、插件未放行，不宣称全部 SDK/IR 兼容。
6. 运行时 TLS、CA、TokenReview RBAC/网络策略提供可选部署源码；未安装到集群。固定镜像 digest、按空间存储凭据及最小权限必须由实际部署落实。
7. 两个模块的依赖 `go mod tidy` 已完成，checksum 校验保持开启；这不等于构建通过。按仓库约定未新增测试，未编译、类型检查、运行、执行迁移或集群操作。

当前完整进度以 [集成文档 §16](../../platform/docs/pipeline-official-engine-integration.md#16-2026-11-13-当前源码状态与发布边界) 为准；部署前置见 [可选 runtime 组件](../../install/kubernetes/loom/README.md#官方-pipeline-runtime可选)。新迁移为 000028 和 000029，本轮未执行。

来源、原始 SHA-256 与补丁见 `UPSTREAM-SOURCES.json` / `UPSTREAM.md`，Apache-2.0 许可见 `LICENSE`。模块通过仓库相对 replace 引用，不依赖旁边的 pipelines checkout。
