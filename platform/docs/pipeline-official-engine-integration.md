# Pipeline 官方执行内核复用与 Loom 集成方案

日期：2026-11-12。状态：改造设计，尚未实施、编译或联调；本文不表示官方内核已接入。

本文是后续替换自研转换器的目标方案，不改变现有运行默认引擎。优先复用官方编译和执行语义，不再扩展另一套 IR lowering、表达式翻译或 Python launcher。历史设计文档仍用于解释历史协议。

## 1. 决策与目标

采用 **官方 KFP Python SDK + 官方 Go argocompiler + 适配后的官方 driver/launcher + Loom API/数据库 + Argo**。

- 一个面向用户的 Loom API、一套业务数据库、现有前端和权限体系。
- 编译包嵌入 Go 后端，不新增常驻编译服务。
- driver/launcher 是 Workflow 中的执行程序/镜像，不是第二套面向用户的后端。
- 不以 KFP API Server、独立 MLMD 服务、独立 KFP 缓存 RPC 为目标依赖。
- 保留官方参数解析、DAG/循环/条件处理、ExecutorInput/Output、产物执行协议；只改服务依赖、配置与授权边界。
- 不承诺“导入官方包即可完全兼容”。完整兼容需要配套执行时和数据语义验证，不是编译函数的返回值保证。

```text
用户 Python ──官方 kfp SDK──> Pipeline IR
                                  │ 导入版本，仍用现有页面
                                  v
Loom API / pipeline-worker ──内嵌官方编译包──> Argo Workflow
        │                                          │
        │                                  driver / launcher
        │                                          │
        ├──内部 Runtime API <──受限机器身份───────────┘
        │          │
        │          └──现有业务数据库 / 对象存储授权
        │
        └──Informer + WorkQueue──> 观测 Workflow/Pod，回写运行状态
```

对象存储字节传输仍由执行时完成；API 负责授权、登记和必要的核验，不把大文件都中转进业务 API。

## 2. 本地官方源码已确认的事实

基线为本地 `pipelines` checkout，revision `555bfa60a70da88c594960bdacfd78fa7c061043`。不能将“目录里的源码存在”当作依赖已构建成功。

| 官方位置 | 已确认行为 | 对集成的影响 |
| --- | --- | --- |
| `backend/src/v2/compiler/argocompiler/argo.go` | 已有 `Compile(PipelineJob, SinglePlatformSpec, Options) -> Workflow` | 不必重写 IR 到 Workflow 的转换 |
| `argocompiler/container.go` | 生成 driver 模板、launcher 镜像和相关执行参数 | 编译器不能与配套 runtime 分版本随意混用 |
| `driver/root_dag.go` | 根 DAG 已接受 `metadata.ClientInterface`，还读取集群存储配置 | 有注入入口，但仍需替换配置来源 |
| `driver/container.go`、`dag.go`、`resolve.go`、`cache.go`、`k8s.go` | 多处接收具体 `*metadata.Client`，不全是接口 | 必须统一依赖接口；不能只替换 CLI 中的地址 |
| `component/launcher_v2.go` | `NewLauncherV2` 可注入 `ClientManagerInterface` | 可提供 Loom 客户端管理器，保留执行主体 |
| `client_manager/client_manager.go` | 客户端分为 Kubernetes、Metadata、Cache | 解耦边界实际存在，不必把整个 KFP API Server 搬进来 |
| `metadata/client.go` | Pipeline/Execution 有私有字段，类型使用 MLMD protobuf；现有接口不涵盖 driver 的全部调用 | 需要数据构造/序列化入口，并按调用图补齐接口；不能声称改一个参数类型就结束 |
| `cacheutils/cache.go` | 已有缓存接口、指纹逻辑，默认客户端调用 KFP TaskService | 保留指纹算法，替换缓存存取，不实现第二套完整 TaskService |

编译器也引用 KFP 公共配置、环境变量、代理配置、插件与平台扩展。需要做传递依赖清单，而不是将整个后端依赖树原样导入后宣称独立 SDK。

## 3. 包、镜像及 fork 边界

建议在仓库建立独立 Go module `engine/kfp/`，与业务 `platform/` 隔离依赖；这是代码组织，不是新增服务。最终路径可在依赖审查后调整。

```text
engine/kfp/
  compiler/          对 Loom 暴露编译入口，包装官方 argocompiler
  upstream/          按固定 revision 纳入官方编译器/driver/launcher 及必要依赖
  runtimeclient/     Runtime API 客户端、官方 metadata/cache 接口适配
  cmd/driver/        官方 driver 的依赖注入入口
  cmd/launcher/      官方 launcher 的依赖注入入口
  UPSTREAM.md        revision、许可证、复制文件与补丁原因

platform/internal/
  service/          现有业务与新增 runtime service
  repository/       GORM 数据读写
  handler/          Runtime API 请求处理
  route/route.go    按项目约定集中注册新路由
```

优先保持官方包的内部目录关系，避免为了“文件少”把逻辑打散重写。Apache-2.0 许可及原有声明保留；未改文件与本地补丁分开登记。独立 module 的可构建性和依赖冲突是第一阶段必须验证的门槛，不在方案阶段预先保证。

新增 driver、launcher 两个配套镜像构建产物；镜像使用不可变 digest。编译包 revision、补丁集版本、两镜像 digest、官方 proto/SDK 兼容范围组成一个 engine release。不要追随 `latest`，不要只升级其中一个镜像。

## 4. 对业务层暴露的接口

以下是拟议接口，不是现有已实现 API：

```go
result, err := kfpengine.Compile(ctx, CompileRequest{
    PipelineIR:     frozenIR,
    Parameters:     parameters,
    KubernetesSpec: permittedPlatformSpec,
    RunIdentity:    identity,
    Options:       engineOptions,
})
// result.Workflow / result.EngineRevision / result.IRDigest / result.Diagnostics
```

包装器完成严格 IR 解码、构造官方 PipelineJob、显式配置注入、调用官方 Compile、输出策略检查。可行时先复用官方 GetPipelineSpec/GetDeploymentConfig 等入口。不得把 IR 再投影到旧 Loom 平铺计划后调用自研生成器。

允许的本地编译补丁：将全局配置改成显式选项、固定镜像、注入受管身份信息及存储根路径、限制高权限平台扩展。循环/条件/依赖/重试的模板生成仍由官方维护。

策略检查不重写 IR 语义：privileged、hostPath、hostNetwork、任意 SA、任意 Secret/PVC、资源上限、访问域、Workflow 大小等必须按租户政策限制；不能因为官方支持某个字段就直接开放。

## 5. Driver/Launcher 如何真正脱离独立 MLMD/KFP API

### 5.1 最小 fork：接口化数据存取，而不是重写执行语义

1. 根据 driver、launcher、importer、已启用插件的实际调用图列出 MetadataStore 方法集合。
2. 将 driver 里的具体 `*metadata.Client` 依赖替换为该接口；补上现有 ClientInterface 没有覆盖的方法，如输入产物读取、插件属性更新。
3. 给官方 metadata 的 Pipeline、Execution、DAG 增加明确构造/编码入口，解决私有字段问题；继续复用官方方法和 protobuf 数据表示。
4. 实现 `LoomMetadataClient`、`LoomCacheClient`、`LoomClientManager`，经内部 API 访问 Loom，不让 Pod 直接持有数据库 DSN。
5. 保留 driver 的输入解析、条件计算、迭代和输出准备逻辑；保留 launcher 的输入下载、命令执行、输出采集及上传逻辑。
6. 根 driver 的 ConfigMap/存储配置改为部署方受管配置入口。兼容 ConfigMap 可以保留，但不需要完整 KFP 安装。

使用 MLMD protobuf 类型不等于运行 MLMD 服务。协议 DTO 可以保留；MLMD 的客户端实现、RPC 和存储换成 Loom 适配器。反过来，不应为兼容少量调用去实现完整 MLMD gRPC API。

### 5.2 内部 Runtime API 的责任

建议新命名空间 `/internal/v1/pipeline-runtime/...`，只给受认证执行时访问。示例操作分组（具体 DTO 在第一阶段冻结）：

| 操作组 | 处理内容 |
| --- | --- |
| run-context ensure/get | 幂等取得 Pipeline/Run context、pipeline root 和非敏感存储配置 |
| executions create/get/list | DAG/组件执行登记，按 execution ID 或稳定名称查询，记录父 DAG/迭代 |
| executions inputs/outputs/publish | 读取上游输入输出、预发布、成功/失败/跳过/缓存结果发布 |
| artifacts ensure/get/events | schema/类型、URI/元数据、输入输出端口事件及血缘 |
| cache lookup/publish | 官方指纹的受限查询、待发布/可命中状态管理 |

这是官方存取接口的业务适配，不再造一套用户 Run/Pipeline CRUD。现有 `ml-executions/claim` 是另一个操作执行桥，不能未经授权语义设计直接拿来充当所有 driver 的执行上下文接口。

幂等键至少含空间 UID、run attempt、engine release、执行类型、稳定执行名称。driver 重试返回同一逻辑 execution ID；launcher 的真实 Pod 重试单独保留证据。不同 payload 复用同一幂等键要冲突，不可覆盖。上游查询必须读主库已提交结果，不依赖最终一致的 informer 投影。

## 6. 数据库复用及必须补的语义

不导入整套 KFP 数据库表，不建立第二套面向用户的模型。优先复用现有表，以下是目标映射而不是“现有字段已经全部够用”的承诺：

| 官方概念 | Loom 目标 | 必须核对/补充 |
| --- | --- | --- |
| Pipeline/Run context | Pipeline、Run、RunAttempt | 官方数值 context ID、稳定名称及归属关系 |
| DAG/Container execution | `tb_pipeline_task_execution` + 运行时映射 | 官方 execution ID、type、父 DAG、properties、自定义属性、状态与真实尝试 |
| Artifact | `tb_pipeline_artifact` | 官方 artifact ID/type ID、schema、metadata、自定义属性、生命周期 |
| INPUT/OUTPUT event | 端口绑定/血缘关系 | 方向、参数名、列表下标、execution/artifact 关联，支持一个产物多次消费 |
| 缓存 | `tb_pipeline_task_cache` 的扩展/隔离协议 | 官方指纹、来源 execution、可命中状态、策略作用域 |
| 对象、日志、可视化 | 现有对象存储和展示接口 | 官方 URI/目录布局及 Metrics 等类型映射 |

第一阶段必须输出逐方法“字段来源—查询条件—事务边界”清单，再确定新增 DDL。可能需要专用 runtime context/ID 映射及 artifact event/type 记录；不能为强行零新增表把全部可查询关系塞进一个 JSON。反之也不因采用 MLMD DTO 就照搬 MLMD 的所有表。

执行上下文是下游 driver 的运行依赖，不只是 UI 旁路记录。所有必需关系必须持久化且可按受限作用域查询。官方 int64 ID 必须由服务端分配并稳定保存，不拿字符串哈希或不受限 uint64 强转充当 ID。

现有表约束需要重点处理：`tb_pipeline_artifact` 的 PUBLISHED 要求对象 URI/object_ref，但纯 Metrics 等官方产物不一定对应实体文件。需通过新增版本迁移明确区分“文件对象”与“元数据产物”，不得伪造 URI。只迁移新增字段/约束，不修改已执行迁移文件。

官方 execution 状态作为运行时语义保留；业务 Task phase 为展示投影，不用一个字符串来兼任两套状态机。逻辑 execution 与 Argo 重试实际 Pod 不是一对一，不能拿 driver Pod 当用户任务，也不能把同一逻辑 execution 的新尝试覆盖旧 Pod 日志。

## 7. 缓存、存储、日志、可视化

### 缓存

- 复用官方 CacheKey/指纹算法，替换 Get/Create 的远程存取；旧 Loom v3 指纹不能当作官方指纹使用。
- 现有缓存唯一键未包含协议版本，需要新增 engine/cache 协议维度或等价隔离，避免串用；镜像需固定 digest。
- 官方 launcher 存在“请求创建缓存后，defer 才发布 execution”的顺序。API 必须先记待发布条目，仅在来源 execution 成功、输出登记完成且对象可用时允许命中，不能直接将收到的 CreateCache 请求视为成功缓存。
- 复用官方算法不等于开放跨租户缓存：额外以 group/pipeline/namespace UID/访问策略隔离查询，且固定 pipeline root 策略。缓存引用源输出需授权并保证保留期。

### 存储

- 尽量保留官方 objectstore 及文件/目录上下传逻辑，不继续维护自研 tar/bundle 协议作为新路径。
- pipeline root 由服务端固定在当前空间/attempt 前缀，不接受租户任意写 URI 或跨空间 Secret。
- 官方 runtime 可能读取 namespace 的存储 Secret。目标优先使用短期、限前缀的存储身份；若环境暂不支持，则使用专用空间级账号，并明确 namespace 是互信边界。绝不下发 MinIO root 或全平台凭据。
- URI、索引和 schema 来源都不能被视为授权依据；服务端下载仍核对 endpoint、bucket、prefix、归属和记录状态。旧原生 URI 与新官方布局分别读取，不就地改写历史对象。

### 日志与可视化

- 在线日志继续使用绑定真实 Workflow/Pod UID 的 Kubernetes 日志接口；区分系统 driver、用户任务及重试。
- 离线日志优先接官方 launcher 的日志上传，Loom 读取对应注册产物；平台也要处理镜像拉取失败、launcher 未启动等没有归档的情况。
- 数值 Metrics、ClassificationMetrics、HTML、Markdown、表格、模型等类型分别投影到现有可视化接口。先复用官方产物协议与可复用组件，缺少的前端 renderer 再补；复用编译器不会自动得到官方 UI 的全部展示能力。
- HTML 等主动内容必须隔离 origin/sandbox，不能直接在带平台 Cookie 的页面注入任意 HTML。

## 8. 状态同步：保留 Informer，不再让它解释 IR

两条职责分明的数据路径：

- driver/launcher 经 Runtime API 写执行元数据，直接供下游执行读取。
- Informer + WorkQueue 观测 Workflow/Pod 的真实状态，更新业务 Run、Pod 绑定和故障信息，并做重启对账。

Informer 不是另一个 IR 解释器，也不能单凭 Pod Complete 就捏造全部输出。现有官方 v3 的冻结计划/节点解析不能直接套到官方模板：官方新增 system driver 节点、动态 podSpecPatch、execution ID 及缓存决策。需要按官方模板契约做投影适配。

提交前冻结 IR、参数、engine release、受管编译结果及策略摘要；提交后绑定 Workflow UID。明确区分不可变提交字段与 Argo/driver 合法运行时字段。对 driver 产生的动态 Pod 配置做策略约束/集群准入，不把“整个运行对象完全不变”当作唯一安全校验。

Run 成功需要 Workflow 成功且所需执行元数据/输出发布完成；收尾中的元数据同步失败进入待对账，不伪造成功。已确认运行失败则可先展示失败及不完整元数据诊断，不无限掩盖失败。停止仍经 operation 持久化、UID/RV 条件更新及终态观测，不强删后直接写 STOPPED。

## 9. 机器身份与多租户边界

- Runtime API 使用独立内部入口/TLS，不挂到公开用户路由；所有请求做运行级授权，不只是“证书合法即可访问任意 run”。
- 首选 projected SA token，固定 runtime audience，服务端 TokenReview；服务端核验实际 Pod UID、SA、namespace、Workflow owner UID 与 attempt 绑定，不能相信 body/label 自报的 runId。配置有界授权缓存、超时和重试预算。
- 可利用现有独立机器 listener 的基础设施，但不能将同一张全平台 mTLS 客户端私钥挂到所有用户组件。mTLS 若采用，需受限、可轮换、可绑定工作负载身份的方案。
- driver 仅操作所属工作流上下文；launcher 仅发布分配给该任务的结果，读取被授权输入与缓存源。拒绝任意 metadata 查询/修改和跨组 ID 猜测。
- 用户代码与 launcher 在同一组件 Pod 时，不把 Pod 身份视为“用户代码无法获得”的秘密；限制到当前任务的数据权限。用户拥有 namespace 原生管理权限时本身属于该空间互信边界，不能声称提供 namespace 内恶意成员的绝对隔离。
- 任务发布成功不自动授予模型 READY/上线权限；现有模型注册、发布审批与存储验证继续生效。

本地后端运行时需有集群 Pod 可访问的受保护内部地址，不能配置 Pod 访问宿主机 localhost。认证、NetworkPolicy、CA 和存储网络是首条真实验收链的一部分。

## 10. 现有代码保留、替换与删除

| 当前部分 | 处理 |
| --- | --- |
| Pipeline/Version/Revision/Run CRUD、权限、operation、审计、前端 | 保留对外接口，新增 engine release/诊断展示，尽量不改用户操作 |
| `infra/pipelineir/*lower*`、自研表达式到 Argo 转换 | 新引擎不再使用；旧协议只读/恢复路径保留 |
| `runtime*.py`、自研输入输出 bundle、tar 处理 | 新引擎替换为官方 driver/launcher；旧运行暂保留对应解释器 |
| `k8s/pipeline_native_build/python/advanced...` 模板生成 | 新引擎绕过，接收官方 Workflow；保留有用的提交授权和集群访问封装 |
| Informer、operation、UID/RV、防重复提交、归档/停止生命周期 | 保留思想及可复用实现，按官方节点/输出协议调整 |
| 自研缓存实现 | 仅用于旧引擎；新引擎用官方指纹和 Runtime API，禁止双写两套可命中缓存 |
| 原有 KFP engine 与历史 KFP Run | 在新内核验收前保留，不因本次方案卸载集群组件 |

不要为了“减少文件”提前删除恢复历史运行必需的代码。最终可减少的是新执行主线的语义实现维护，不是宣称全部旧文件立即消失。

## 11. 兼容切换与回退

- 初期仍使用 `engine=NATIVE_ARGO`，由部署配置选择新提交的 engine release；每个 attempt 冻结实际版本，不热切换已有运行。
- 新协议命名如 `loom-kfp-official-v1+<upstream-sha>.<patch-version>`；同时记录镜像 digest 和输出协议。已有 engine_revision/plan snapshot 能否容纳完整信息需审查。
- 首次灰度只给指定空间/流水线；不做重复执行的双跑。影子编译只读比较结构和策略，不提交第二个 Workflow。
- v3 历史 attempt 继续走旧编译/读取路径；官方引擎历史 attempt 继续走其冻结版本。旧文件下载、日志归档和缓存不能跨协议解释。
- 回退开关只影响尚未提交的新运行；已提交的官方 Workflow 由对应 runtime 继续服务到终态。需要保留 runtime API/镜像/数据库兼容支持，不能关闭服务制造“回退成功”。
- 若官方路径暂不可用，提交明确失败或管理员显式选旧引擎，不能静默退回另一套语义。

## 12. 实施顺序与验收门槛

| 阶段 | 交付 | 完成标准 |
| --- | --- | --- |
| P0 依赖与语义清单 | 官方 package 依赖树、方法/字段映射、fork 补丁清单、确切 DDL、鉴权契约 | 所有必需调用都有去向；不以“稍后补 metadata”掩盖运行依赖 |
| P1 官方编译包 | 内嵌 Compile、固定 engine release、受限配置、诊断与冻结结果 | IR 直接走官方编译；不经自研 lowering；能列出所有生成镜像和服务依赖 |
| P2 最小执行闭环 | 接口化 driver、官方 launcher、Runtime API/GORM 存储、无缓存双任务流水线 | `python_simple.py` 的两任务能在真实集群传参、打印 5、登记 Metrics；不访问 KFP API/MLMD；这是第一个关键门槛 |
| P3 执行语义和产物 | 文件/目录、嵌套 DAG、条件/循环、重试、官方缓存、停止、日志归档 | 官方支持且平台放行的案例有等价行为；重启、重试不重复发布；缓存不串租户 |
| P4 产品兼容和切换 | UI DAG/日志/可视化/模型登记、灰度、旧运行回放、回退演练 | 外部 API 保持兼容；对外声明能力来自已验收矩阵，而不是编译器字段清单 |
| P5 收敛维护面 | 移出新主线中的旧转换/launcher/cache，保留必要历史恢复 | 新主线不再增加自研 IR 语义；官方升级只集中修改可追踪补丁和适配层 |

P0 应先锁定一个官方 SDK/proto/compiler/runtime 组合，不能先承诺所有 KFP 2.x 任意组合兼容。验证 importer、artifact list、exit handler、workspace/PVC 和平台扩展时，区分“官方支持”“本平台策略允许”“前端可展示”三个维度。

按当前项目约定，本次只写方案，没有运行、编译或新增测试。上表中的运行及故障验证是后续需另行授权的验收工作，不得在未执行时标记完成。建议按阶段批准验证，不再积累大量未经运行的执行内核代码。

关键故障验收：driver 重试、launcher 发布响应丢失、API 重启、存储上传失败、缓存源对象丢失、Workflow UID 被替换、循环/重试并发、停止与成功竞态、跨空间 ID 请求、旧引擎回放。实际投入评估应在 P0 依赖审查、P2 闭环后修订，不给无依据的“已完成百分比”或极短工期保证。

## 13. 明确不采用的方案

1. 仅把旧自研代码换个目录叫 SDK：不减少语义维护。
2. 只换官方编译器，删除 driver 节点，继续用旧 launcher：破坏官方执行协议。
3. 给每个组件发数据库账号：绕过业务授权和事务边界。
4. 假装 MLMD 只是 UI 元数据，靠 informer 终态后补就能运行：下游 driver 本身依赖这些数据。
5. 为“兼容官方”重新部署整个 KFP UI/API/数据库而不做集成：不符合统一系统的目标。
6. 一次性承诺支持全部官方功能：编译器复用并不自动补齐存储、插件、可视化、鉴权和运维。

**结论：新主线以官方编译器和执行语义为核心；Loom 只维护业务控制面、数据存取适配和安全策略。复用对象必须是“编译器 + 执行时”这一组，不只是一个函数壳。**
