# 原生 Python 第一版：实现范围与人工验收

2026-11-12：本文为第一版历史边界；新提交使用 v3，循环、条件、缓存、重试、目录产物、停止和归档的源码行为见 [v3 说明](pipeline-native-v3.md)，不再沿用本文“全部拒绝”的清单。尚未运行验收。

本说明优先于迁移文档中较早的“尚无公开提交/worker 装配”等阶段记录。
2026-11-11 补充：新 attempt 已改用 `loom-native-kfp-v2`，接入官方 protobuf/Visitor 和抽取的参数输出工具。旧协议保留恢复；详见 [官方复用改造](pipeline-native-official-reuse.md)。本文第一版功能范围不因此扩展。
本轮是源码集成，**未编译、未运行测试、未执行样例、未部署或迁移**，不能据此认定集群已可用。

## 1. 主流程

1. 用户使用 KFP 2.x Python SDK 的 `dsl.component` / `dsl.pipeline`，在自己的环境编译单文档 IR。
2. 沿用 Loom 创建 Pipeline、版本、版本修订 API；版本保存 IR 与摘要，不运行上传的 Python。
3. 提交现有 `POST /api/v1/groups/{gid}/pipelines/{pid}/runs`，增加 `"engine":"NATIVE_ARGO"`。默认仍为 KFP，不改变历史运行。
4. API 校验 IR 支持范围、绑定根参数默认值、纯内存渲染检查规格大小，然后写现有 Run/operation。
5. 同一个 `pipeline-worker` 领取持久 operation，按数据库 Run.engine 分流。冻结计划和准入规格，创建 suspend Workflow，绑定 UID，再写启动 token 并恢复。
6. Argo 调度基本 DAG。组件容器用嵌入式 Python launcher 启动 SDK 原始命令，JSON executor input 作为一个 argv 参数传递。
7. 上游输出参数以有界 base64 JSON bundle 传给下游；文件由 Argo executor 上传 S3/MinIO，再下载到下游受管路径。组件不接收存储密钥。
8. Informer + WorkQueue 触发实时 GET，核验固定 namespace/Profile/Workflow/Pod UID、冻结规格后回写现有任务表。只有 Pod 成功（含 Argo 上传成功）才注册 PUBLISHED 产物和指标。
9. 保存完整终态证据，现有 operation 最终事务推进 Run/receipt/history/audit。节点失败不会变成流水线成功。

## 2. 支持矩阵

| 能力 | 第一版源码行为 |
| --- | --- |
| 普通 Python 组件 | 轻量 Python SDK 组件/含 Python 3 的自定义组件镜像；沿用编译 command/args |
| 参数 | string、int64、double、bool；根参数、常量、任务输出、默认值；禁止凭据值 |
| 文件产物 | 单个普通文件；Artifact/Dataset/Model；受管路径、SHA256/大小、元数据、上下游传递 |
| DAG | 平铺 DAG、显式依赖、参数/文件依赖推导、循环检测，最多 128 任务、每组件最多 32 端口 |
| 日志 | 现有 task logs 接口，固定真实 Pod UID 与 Workflow UID，main 容器实时/历史 Pod 日志 |
| 指标 | `Output[Metrics]` + `log_metric`，有限数值标量，入现有 artifact 元数据，由现有 visualizations 接口返回 |
| 查询 | 原有 runtime、DAG、node、visualizations、artifact download 接口；原生路径不调用 KFP API/MLMD |
| 版本 | 原有 PipelineVersion/revision；运行冻结版本修订与 IR，不读取后续修改的包 |
| 终态编辑/删除 | 归档后 foreground 清理，Workflow 和 Pod 均缺失才应用编辑或逻辑删除；产物记录保留 |

不支持：嵌套 DAG、循环、条件、重试策略、缓存、importer、根文件输入、根输出、artifact list、目录/多文件产物、LIST/STRUCT 参数、任意占位符、GPU/自定义 env/resources、在线停止。SDK 默认缓存需在示例中显式关闭。

在线停止明确返回冲突；不会只改数据库伪装已停止。终态清理会移除 Pod，之后在线日志不可读；本版不自动 GC，不承诺离线日志归档。富图表、HTML 报告、旧 KFP 数据迁移不属于这个版本。

## 3. 运维配置

API 与 pipeline-worker 使用同一份配置。`pipeline.native_enabled: true` 与 `native_observer_enabled: true` 必须同时设置。仅原生模式可以保持 `pipeline.enabled: false`（不需要 KFP 服务）。

示例配置已补在 `configs/config.yaml` 和安装配置；填写实际 native_artifact_endpoint（host:port，无协议/路径）、bucket、region、secret、native_service_account、并发、CPU、内存。`native_artifact_insecure: true` 表示 HTTP 存储，不是跳过 HTTPS 证书校验。

每个业务 namespace 单独准备：

- `install/kubernetes/loom/examples/native-pipeline-space.yaml` 中的执行 SA/最小 Argo taskresult Role。
- 名为 native_artifact_secret 的 Secret，键为 `accessKey`、`secretKey`。必须使用专用空间写账号；权限限定 `loom-native/<groupId>/<namespaceUID>/`，不要使用 MinIO root 或跨租户写账号。
- Argo 控制器能读取上述 Secret，节点能拉到组件与 Argo executor 镜像。
- API 的 `artifact_store` 使用同 endpoint/bucket 的只读账号，并授予对应 `loom-native/` 前缀读取权限；与旧 KFP prefix 规则分开核验。
- 应用已有显式 SQL 迁移 000022/000023。此轮未执行。

worker 的 Workflow create/patch/delete RBAC 已加入安装源码；API 身份不增加原生写权限。不要卸载旧 KFP/MLMD，直到旧运行迁移另行完成。

## 4. API 与人工验收

沿用创建定义和导入版本接口，IR 通过 packageYaml 导入。先请求版本的 native-compatibility（必须带 revision），再提交：

```json
{"name":"python-first-run","engine":"NATIVE_ARGO","versionId":123,"versionRevision":1,"parameters":[{"name":"count","value":"5"}]}
```

示例源码：`examples/pipeline-native/python_files.py`。它包含两个 Python 组件，传递 count 参数和 Dataset 文件，生成报告与 row_count/sum 指标；多输出使用 `first.outputs["Output"]` 显式选择参数端口。

由使用者授权并在真实环境执行后，验收应观察：

1. operation 从排队到观察，再进入 SUCCEEDED；Run 与 Workflow UID 一致。
2. DAG 两个容器节点都有实际绑定 Pod，第二步依赖第一步。
3. runtime taskId / DAG nodeId 可读 main 日志，包含 produced / summary written。
4. 第二步节点输入含 count=5 和数据文件关联，输出包含 report 与 metrics。
5. visualizations 返回 row_count=5、sum=10；report 可下载且内容为 rows=5 sum=10。
6. 错参数/循环/缓存等在提交前拒绝；组件异常则 Run FAILED，不伪造文件或指标。
7. worker 重启继续同一个实例，不创建第二个 Workflow；终态查询不依赖 KFP/MLMD。

可下载单文件上限沿用 32 MiB。摘要由组件报告，下载时服务器重新核验 SHA256/大小；不把组件提供的摘要称为可信服务端计算证明。组件代码本身在租户工作空间内执行，仍需运行时隔离、NetworkPolicy、namespace 配额和专用存储账号。

## 5. 持久协议与已知边界

新增 `loom-native-python-v1` 编译协议，旧 `loom-native-static-v1` 保留原解释及原 resourceVersion 重放规则。Python-v1 第一次启动允许 Argo 在 suspend 状态写入结构 root 后更新状态 RV，但只在无任务/Pod 已启动、UID/规格/annotations 不变且 token 未写入时执行新 CAS；已确认 token 永不重新启动。

组件 JSON bundle 最大 48 KiB，IR/Workflow 规格均有界，最终 archive 上限沿用原值。超过大小会拒绝或保留诊断，不能放大到无限对象。Pod/Workflow 被外部提前删除、offload/compressedNodes、外部修改冻结规格等会保留未完成状态等待运维处理，不自行重建或伪造成功。

未做的实际验收是明确剩余项，不计入已完成；启用开关默认 false。在线终止、自动日志归档和异常清理接管属于后续增强，不应通过强删/移除 finalizer 实现。
