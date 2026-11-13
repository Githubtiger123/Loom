# Pipeline 官方执行内核复用与 Loom 集成方案

日期：2026-11-12。状态：改造设计，尚未实施、编译或联调；本文不表示官方内核已接入。

2026-11-13 补充决策：使用固定版本官方源码加可追踪适配补丁，自建 driver/launcher 镜像；不要求官方镜像零修改，也不另写一套执行器。具体构建和接线方式见第 3.1、3.2 节。

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

### 3.1 官方包、官方镜像与本地补丁的关系

官方已经提供 Go 源码包以及预构建 driver/launcher 镜像，但预构建程序默认创建官方客户端，连接 MLMD、KFP 缓存等服务。仅修改服务地址不能把官方协议自动换成 Loom Runtime API；在业务后端实现一个 Go 接口，也不会自动改变另一个 Pod 中已编译好的程序。

本方案采用的具体方式是：

1. 从锁定 revision 的官方源码取得编译器及执行时，保留原有许可证、目录关系与语义主体。
2. 在 driver/launcher 的程序入口注入 `LoomClientManager` 或对应 metadata/cache 客户端，补齐 driver 的接口化和必要类型构造入口。
3. 通过显式配置传递内部 API 地址、TLS 信任、工作负载认证及受管存储配置，移除目标路径中对官方默认客户端的隐式初始化；不把实际密钥烘焙进镜像。
4. 基于这些源码重新编译 driver、launcher，构建 `loom-kfp-driver`、`loom-kfp-launcher` 配套镜像，而不是进入运行中的官方容器临时修改文件。
5. 发布并记录镜像 digest，由官方编译器的 `DriverImage`、`LauncherImage` 选项引用这些镜像。

接线示意如下；镜像地址是占位符，不是当前已有构建产物：

```go
options := &argocompiler.Options{
    DriverImage:   "registry.example.com/loom/loom-kfp-driver@sha256:<digest>",
    LauncherImage: "registry.example.com/loom/loom-kfp-launcher@sha256:<digest>",
}
workflow, err := argocompiler.Compile(job, kubernetesSpec, options)
```

launcher 镜像与用户组件镜像不是同一个东西。按官方模板约定，初始化容器将 launcher 二进制复制到共享卷，组件主容器运行该二进制，再由它启动用户命令。自建 launcher 镜像必须保持编译器约定的二进制路径、复制命令、参数及卷挂载契约；它通常不是额外的 sidecar，也不是为 launcher 单独创建一个用户计算 Pod。用户组件镜像仍由 Pipeline 定义并受平台策略限制，不要求每个用户镜像都预装一份自研 launcher。

如果选择官方镜像完全不改，则需要保留官方配套服务，或在服务端实现其所需的兼容协议。那是另一条取舍，并非本方案的默认路线；不能把这部分兼容成本描述为只改配置。

### 3.2 补丁边界、镜像发布与后续扩展

- **保留官方主体**：IR 到 Workflow 的语义生成、driver 的输入/条件/迭代解析、launcher 的组件执行和产物处理。不重写为 Loom 的另一套规则。
- **允许且需登记的补丁**：显式配置注入、metadata/cache 依赖接口化、必要构造/编码方法、Runtime API 客户端、机器认证和受限存储接线。补丁量需通过依赖审查确认，不保证只有几行修改。
- **发布绑定**：后续为两镜像增加独立构建目标和现有镜像发布工作流入口；同一 engine release 绑定上游 revision、补丁版本、编译包与两镜像 digest。构建发布和运行验收是不同步骤，推送成功不代表执行语义已验证。
- **升级维护**：保存未修改的上游基线与补丁清单，逐项审查上游变更，再发布新 engine release。正在运行的 attempt 继续使用冻结版本，不因 tag 或配置改变而换执行器。
- **Trainer/Volcano 扩展**：优先实现为普通流水线组件，由 launcher 启动提交/等待组件，再经受限后端 API 创建和管理训练 CR。训练 Pod 仍由相应控制器/调度器管理，不把训练业务持续塞进 launcher；组件要处理幂等提交、失败传播、停止联动和产物登记。

本节只确立后续源码适配、镜像构建及发布方案；本次未创建 Dockerfile/CI 任务、未编译镜像、未发布仓库或修改集群。

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

## 14. 2026-11-13 主要框架重构源码状态

本节覆盖上文尚未实施、保留旧运行恢复实现等原先计划。用户本次明确选择删除旧自研执行实现、先重建主要框架，不是完成全量功能迁移。

### 已改的源码

- 新增独立 `engine/kfp` Go 模块，固定上游源码 revision `555bfa60a70da88c594960bdacfd78fa7c061043`，显式引用 API/kubernetes_platform 嵌套模块；不依赖工作区外的绝对路径 replace。
- 编译入口真实调用官方 `argocompiler.Compile`；保留完整官方模板，不移除 driver、不自写 Python 包装器。平台仅增加受管身份、暂停状态、SA/并发、镜像 digest、路径和资源规模限制。Kubernetes platform 扩展先拒绝，策略/准入仍待完整适配。
- launcher 入口真实调用官方 `NewLauncherV2/Execute` 并注入官方 ClientManagerInterface；尚无生产 metadata/cache 客户端，也未提供 launcher CLI/镜像。
- Pipeline 兼容性接口改走官方 schema/编译；配置缺失时只报告 schema 检查。即使编译成功，`runnable` 仍固定 false，不将能力清单当成运行验收结果。
- 删除 `infra/pipelineir`、旧 `internal/thirdparty/kfp` 摘录、自研 Python runtime、原生模板构造及执行 service；保留通用集群读取/Informer/日志身份核验和业务 API/模型/仓储/版本化迁移。
- 现有 KFP 服务端适配继续保留。旧原生 SQL 证据只读，不用新编译器重解释旧节点；原生日志归档读取需要对应旧版本，不猜测对象路径。
- 新原生提交在受理前返回 unavailable；原生停止/清理同样拒绝。没有装配官方 backend 时，原生 worker/observer 启动明确报错。不能只开开关就绕过未实现适配。
- 工具链声明、Docker 与 Compose 的模块路径随官方依赖调整；尚未交付 driver/launcher 镜像流水线。

### 尚未完成

上游 driver 仍有具体 metadata 客户端依赖；接口抽取、运行级 Runtime API/GORM、Pod 认证、输出/缓存、受控提交及恢复、节点投影、终态收尾、运行时 CLI/镜像、前端对新能力状态的适配均未完成。原生功能因此存在明确中断，不能用新二进制管理仍在运行的旧 v3 attempt。升级前应先保留旧部署/使用对应版本处理存量，不要直接替换生产执行器。

依赖元数据核验尚未完成：国内 Go 代理返回 sumdb 404，改用官方代理后连接超时；新增依赖的 go.sum 尚未补齐。未关闭 checksum 校验、未伪造校验和。未编译、测试、启动、迁移、部署或调用 Kubernetes。本次只交付重构框架，不表示 P2 最小执行闭环通过。

下一步以 P2 为主：先完成无缓存、两组件传参及结果登记，再沿官方协议接产物、动态控制流和缓存。不要回到另一套自研 IR 解释器。

## 15. 2026-11-13 driver 接口、传输与机器身份源码进展

本节更新 §14 的后续状态；仍未通过 P2 最小执行闭环。

### 本次已实现的源码

- `engine/kfp/internal/upstreamdriver` 保留固定 revision 的八个官方非测试文件。具体 metadata 客户端改成接口，补入官方 launcher 接口遗漏的两项 driver 方法；根 DAG 的存储配置显式注入，删除隐式 Kubernetes 配置读取与部分参数 debug dump。循环、条件、输入解析、缓存决策沿用官方代码，没有重新开发语义。其余日志仍是上游行为，不能宣称已全面脱敏。
- `ExecuteDriver` 统一 ROOT_DAG/DAG/CONTAINER 调用，要求已注入受限客户端和固定 pipeline root，拒绝未适配的 Kubernetes platform 操作，插件使用官方 NoOpDispatcher。它不是 CLI 或提交器，也不代替授权。
- `runtimeapi.Client` 使用官方 client-go TLS/轮换 token 文件，固定 HTTPS origin、禁止重定向、限制操作/报文/超时、校验协议，错误不返回 token 或原始响应体。内容寻址 requestId 仅用于后续幂等处理，服务端必须连同已验证身份及操作作用域校验，不能视为权限证明。
- 平台通过官方 TokenReview 验证专用 audience 的 Pod-bound token，读取实时 namespace、SA、Pod 和 Workflow owner 身份。再用 GORM 查询唯一 attempt，核对 engine release、Workflow UID、空间实例及不可变 snapshot 中的 `runtimePolicy`，不采信请求自报 runId 或标签。
- 上述认证只返回**运行级 scope**。driver/launcher 角色、分配的 task/execution ID、输入读取和输出发布权限仍必须单独校验，并在持久化事务内复核归属。
- `runtimeclient.Cache` 实现官方 cache 接口，纯 key/fingerprint 算法摘自官方代码，查询/发布走 Runtime API；显式区分禁用、真实 miss 与请求失败。服务端必须把 launcher 的缓存发布先作为候选，输出原子发布成功后才能命中，不能信任客户端提交的 execution ID。
- `UPSTREAM-SOURCES.json` 记录原始上游文件哈希、revision、摘录方法和修改清单，保留 Apache-2.0 许可及修改标记。新增直接依赖版本与该上游 revision 对齐，但 go.sum 尚未完成核验。

### 尚未连接，不能视为已可用

1. 官方 metadata 的 Pipeline/Execution 私有上下文字段需要最小构造器/codec 适配；随后实现真正的 Loom metadata 客户端。不能借用测试假客户端、unsafe 或伪造对象绕过。
2. Runtime API 服务端操作、GORM 幂等读写、任务级授权、输出发布及缓存可见性仍未实现；没有注册路由或启动内部监听器。
3. 提交器尚未冻结并写入 `runtimePolicy`（protocol、group instance、SA 名称/UID、engine revision、plan digest）；旧记录缺少策略时拒绝认证，不从当前可变配置猜测补齐。
4. 未安装 TokenReview/Pod/Workflow 查询 RBAC、projected token、TLS/网络策略；未提供 driver/launcher CLI、镜像与官方节点投影，Backend 仍未注册。

下步依次实现 metadata 构造与客户端 → scoped Runtime API/GORM 无缓存读写 → runtime CLI/镜像及受控提交 → 两组件闭环 → 产物/动态控制流/缓存。没有额外部署整套 MLMD 或 KFP 后端，没有引入第二个数据库。

本次仅做源码对照、格式化与差异检查；按项目约定未新增测试、未编译或运行项目，未执行数据库或集群操作。原生入口继续禁用，不将保留官方语义的源码当作已验收功能。

## 16. 2026-11-13 当前源码状态与发布边界

本节是当前状态，覆盖 §14/§15 的“backend 未注册、缓存不可用、metadata/CLI/投影未接通、依赖未锁定”等历史描述。这里的“已实现”仅指源码，不表示 P2/P3/P4 运行验收通过。

### 16.1 已连接的主链路

1. Python 继续使用官方 KFP SDK 生成单文档 Pipeline IR；浏览器不执行 Python。定义、版本、运行、权限、operation 和审计仍由 Loom 管理。
2. `engine/kfp` 真正调用官方 `argocompiler.Compile`。输入解析、表达式、循环、条件、DAG 和 launcher 参数/文件协议沿用固定 revision 的官方代码；无法直接依赖具体客户端的 driver/component 做最小接口化保留，来源、哈希和补丁见 `UPSTREAM-SOURCES.json`。
3. 持久化官方计划和冻结策略 → 创建暂停 Workflow → 持久化 UID 绑定 → 有效租约/fence 下放行。后续恢复读取冻结计划，不用新配置重编译已有运行；停止/删除使用 UID/RV 条件，删除前持久归档，资源消失本身不证明成功。
4. 官方 driver/launcher 通过独立 TLS Runtime API 访问同一 Loom 业务库，不连接 KFP API 或 MLMD。机器 token 绑定 audience/Pod，结合 TokenReview、实时 namespace/SA/Pod/Workflow UID、真实 Argo 节点和数据库冻结策略逐请求认证。
5. Runtime 事务按 group → run → attempt 加锁，元数据、输出和回执原子写入；远程集群/对象存储 IO 不在 SQL 事务内。终态封存后拒绝新写入；精确回执重放不重复执行发布。
6. Workflow Informer + WorkQueue 合并事件并退避重试，更新业务观测状态和已应用修订；不替代操作租约、不改变期望状态、不将 202 或“已创建 Pod”当成成功。通用 HTTP 回报不能伪造官方终态证明。

### 16.2 本轮补齐的源码能力

| 能力 | 当前实现与边界 |
| --- | --- |
| 参数、普通 Python、DAG、条件、循环 | 官方编译器和 driver/launcher 主线，平台执行规模和能力白名单限制；未证明所有嵌套组合兼容 |
| 自动重试 | 只在真实同一 Argo Retry 的相邻物理节点间接管，保存 epoch 和历史；上一个 launcher 必须已结束。已提交 COMPLETE 的系统失败重试只重放输出，不重跑 Python。用户外部副作用不保证 exactly-once |
| 缓存 | 官方 fingerprint/key 不变；launcher 发布候选，整个 Workflow 成功且输出/清单通过核验才写可见索引。重试保留原命中决定，缓存命中用目标 attempt 的新产物身份，不复用源日志 |
| 文件和目录 | 官方原始对象布局；精确文件读取、目录清单与 ZIP 下载，非旧 v3 tar 协议；目录清单最多 4096 文件、ZIP 原文件合计最多 32 MiB |
| 日志 | 保留真实 Pod 在线日志；从已发布 executor-logs 构建离线目录，Range/ETag 分段读取，包含物理重试历史。未发布日志显示不完整，不伪造归档 |
| Metrics/可视化 | 复用现有参数/产物/数值指标和受限预览接口；Metrics 元数据型产物不强求不存在的对象。不是完整 KFP 可视化组件库 |
| 模型登记 | 从成功且归档的输出证明解析来源，worker 按清单条件复制文件/目录到模型存储，再登记版本产物。最多 10 GiB，不完整复制不登记，未引用对象尚无自动回收 |
| 前端 | 复用现有版本、运行、DAG、IO、Metrics、在线/离线日志和模型登记面板；新增重试 epoch/Pod 历史、目录 ZIP 提示及明确的“配置允许不等于已验收”说明 |

缓存额外限制：同一空间 incarnation、namespace UID、流水线、创建者和引擎版本内匹配；命中前重新核验源运行未删、当前读权限、7 天有效期和源清单，复制到新 attempt 的 `__cache` 子目录。更改镜像标签不能作为稳定缓存代码身份，因此开启全局缓存时所有组件镜像须固定 `@sha256`。可见性比官方任务级缓存保守；过期索引、部分复制及未引用对象无自动 GC。使用自定义 cache key 时仍按上游语义由作者负责键的有效性。

存储一致性使用 ETag 条件读/复制及结束清单复核，不等于对象锁、历史版本钉住或加密内容证明。拥有组件凭据/namespace 管理权限的人不是该运行内部的隔离租户。缓存仅适合输出稳定、无外部副作用组件；存储保留/写隔离策略必须由部署方落实。

### 16.3 数据库、镜像与显式配置

- `000028_pipeline_official_runtime.sql`：`tb_pipeline_runtime_object` 与 `tb_pipeline_runtime_receipt`；沿用已有 Pipeline、版本、运行和 attempt，避免为每种官方 protobuf 建一组业务表。
- `000029_pipeline_official_cache.sql`：`tb_pipeline_runtime_cache`，只保存已确认成功执行的缓存索引及对象清单。默认禁用缓存也需要完整 schema，启动不 AutoMigrate。
- `install/docker/Dockerfile.kfp-runtime` 两个 target 与发布 Actions 已提供 driver/launcher 镜像定义；不是直接使用上游未打适配补丁的镜像。实际镜像未在本轮构建。
- `ConfigureOfficialPipeline` 仅在显式 native/observer 配置时注册 concrete backend；默认 `runtime_enabled/native_enabled/native_observer_enabled/native_cache_enabled=false`。`native_enabled` 需要 Runtime 和 observer；observer 也需要 Runtime。
- 启动检查包含 DB/store、集群客户端、空间 verifier、产物 reader，缓存开启另需复制 reader/writer。它只是本地依赖/配置检查，不会证明服务可达、权限正确或存储可用；`runnable=true` 同样不是验收结果。
- Runtime 可选 Kustomize component 和按空间 SA/凭据/CA 示例已提供，未自动纳入所有 overlay 或授予所有 namespace 权限。配置、TLS/RBAC/NetworkPolicy 与存储账户前置见 `install/kubernetes/loom/README.md`。

存储凭据保持隔离：后端 `artifact_store` 主账户只读，组件使用各空间的 Secret 写受限路径，缓存复制另用 `cache_copy_*` 源读/目标写账户，模型存储使用独立 `model_store` 目标写账户。不给组件数据库账号，不把凭据或内部对象 URI 传给浏览器。缓存凭据环境变量为 `LOOM_ARTIFACT_STORE_CACHE_COPY_ACCESS_KEY/SECRET_KEY/SESSION_TOKEN`。

### 16.4 仍未放行及发布验收

importer、artifact iterator、Kubernetes platform/PVC/workspace、Trainer/Volcano、插件未接入白名单；不承诺完整 KFP SDK/IR/可视化兼容。旧 v3 数据只保留相应历史 SQL 证据；不能用新 runtime 恢复其执行或猜测日志/对象布局。

严格冻结规格只允许 suspend/shutdown 生命周期差异。当前未把服务器 dry-run/admission 后规格作为新基线；若集群修改了其他 spec，运行会拒绝继续，而不是放宽身份验证。Argo 默认值/准入兼容必须在发布前检查；也未覆盖 offloaded/compressed 大型 Workflow status。

最低发布核对清单（本轮均未执行）：

1. 构建两个应用模块及 runtime 镜像；显式迁移 000028/000029；部署匹配 engine release 的 API/worker/driver/launcher 和 per-space CA/Secret/SA。
2. 两任务普通 Python 传参、文件产物、Metrics 和真实 Pod 日志闭环；确认不访问 KFP API/MLMD，无第二数据库。
3. 条件、循环、嵌套 DAG、普通失败重试及成功发布后的系统失败重试；API/worker 重启和响应丢失恢复。
4. 缓存 miss/hit、参数/目录结果、重试决定稳定、源删除/过期/清单改变、撤权和跨空间拒绝；缓存无源日志串接。
5. 离线日志分段读取、未完整归档提示、目录 ZIP、模型目录登记，以及停止/删除/成功竞态。
6. 审查剩余上游错误日志是否泄露参数，验证 TLS/SAN/CA、TokenReview 和最小 RBAC、NetworkPolicy、存储读写/保留策略及 admission 行为。

本轮已完成两个模块依赖 tidy，保持 checksum 校验；其余只做源码对照、gofmt/前端格式化、来源哈希和差异检查。按仓库约定未新增或运行测试，未编译、类型检查、启动项目、执行迁移、构建镜像或操作集群。因此不能把源码清单表述为“全部已经可用”，P2/P3/P4 的运行验收仍待执行。
