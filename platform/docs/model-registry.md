# Loom 模型注册与 KServe 集成

本文描述 Loom 的模型版本、产物管理与 KServe 发布链路。前端入口在工作空间的模型管理、推理服务和 Pipeline 运行的产物面板。通用本地文件上传器、Catalog 聚合、Custom Storage Initializer 和 `model-registry://` 协议不在本期范围。

## 数据边界

MySQL 只保存三层业务元数据：

```text
RegisteredModel
  -> ModelVersion
       -> ModelArtifact
            -> KServe InferenceService 配置修订
```

- `tb_model` 是组内逻辑模型，保存稳定名称、展示信息、负责人和业务 revision。
- `tb_model_version` 是具体版本，可关联一个 Loom Pipeline Run 作为来源；新版本从 `DRAFT` 开始。
- `tb_model_artifact` 是不可变的外部产物记录，保存 URI、存储类型、模型格式、可选 SHA256 和大小。
- 模型权重仍位于 S3、GCS、PVC 或 OCI；数据库不保存文件内容或存储凭据。
- Artifact 地址或摘要需要变化时创建新 Artifact，不原地覆盖已有记录。

Model Catalog/Hugging Face 发现层是独立的只读能力，不与当前组内 Registry 混用。当前 API 也不接受任意下载 URL、Secret、访问密钥或用户提交的 Storage Initializer。

## 生命周期

创建 Model 返回 `ACTIVE/revision=1`。创建 Version 要提交 Model 当前 revision，成功后 Model revision 递增；创建 Artifact 要提交 Version 当前 revision，成功后 Version revision 递增。所有写接口要求 `Idempotency-Key`，更新和归档使用 revision CAS。

Version 必须至少存在一个 `ACTIVE` Artifact 才能从 `DRAFT` 改为 `READY`。KServe 只允许选择 `ACTIVE` Model 下 `READY` Version 的 `ACTIVE` Artifact。归档最后一个有效 Artifact 时，READY Version 自动回到 DRAFT。

Artifact 归档不删除对象存储中的文件。Version 归档会在同一事务归档其 Artifact；Model 归档会在同一事务归档全部 Version 和 Artifact。归档不停止或删除已经发布的 KServe 服务，也不会修改历史 KServe revision；但以后发布包含已归档 Artifact 的历史 revision 会被拒绝。

归档使用 `deleted_at` 软删除，历史详情可按 ID 读取，列表通过 `includeDeleted=true` 查看。组删除依赖检查会把未归档 Model 计入依赖。

## 权限

模型模块使用以下组权限：

- `model.create`、`model.read`、`model.read.any`、`model.update`
- `model.version.create`、`model.version.update`
- `model.artifact.create`、`model.archive`

Version 和 Artifact 继承父 Model 的对象权限。创建者可操作自己的模型；读取或修改其他负责人的模型还需 `model.read.any`。内置 group_owner 拥有全部能力，group_editor 不含 `read.any`，group_viewer 只有 `model.read`。关联 `sourcePipelineRunId` 时还会校验该 Pipeline Run 的读取权限。

## API

所有路径以 `/api/v1/groups/{gid}` 开头。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / POST | `/models` | 模型列表 / 创建模型 |
| GET / PATCH | `/models/{mid}` | 模型详情 / 修改展示元数据 |
| POST | `/models/{mid}/archive` | 归档模型及其版本、产物 |
| GET / POST | `/models/{mid}/versions` | 版本列表 / 创建 DRAFT 版本 |
| GET / PATCH | `/models/{mid}/versions/{vid}` | 版本详情 / 修改描述或 DRAFT、READY 状态 |
| POST | `/models/{mid}/versions/{vid}/archive` | 归档版本及其产物 |
| GET / POST | `/models/{mid}/versions/{vid}/artifacts` | 产物列表 / 登记不可变产物 |
| GET | `/models/{mid}/versions/{vid}/artifacts/{aid}` | 产物详情 |
| POST | `/models/{mid}/versions/{vid}/artifacts/{aid}/archive` | 归档产物 |

示例流程：

```json
{"name":"fraud-detector","displayName":"欺诈检测","description":"交易风险模型"}
```

```json
{"revision":1,"name":"v1","description":"首次发布","sourcePipelineRunId":42}
```

```json
{
  "revision":1,
  "name":"onnx",
  "uri":"s3://models/fraud-detector/v1/model.onnx",
  "storageType":"S3",
  "modelFormat":"onnx",
  "modelFormatVersion":"1",
  "digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "sizeBytes":1048576
}
```

登记产物后，使用 Version 新 revision 提交 `{"revision":2,"status":"READY"}`。

## KServe 快照

KServe MODEL 请求支持两种来源：旧的直接 `storageUri` 方式继续兼容；推荐只提交 `artifactId`。提交 `artifactId` 时，调用方不能同时提交 Registry 派生字段：

```json
"model":{"artifactId":7,"runtime":"kserve-onnxserver"}
```

服务层在创建或保存 KServe 配置修订的事务中重新检查模型读取权限和三层状态，并把 `registeredModelId`、`modelVersionId`、`artifactId`、URI、存储类型、格式、摘要和大小冻结进 `tb_kserve_revision.spec`。异步 kserve-worker 只消费该不可变快照，不在执行阶段查询可变 Registry。

每次 `deploy` 历史 KServe revision 时，Loom 会确认来源 Artifact 仍可部署且数据库元数据与快照一致。已经运行的服务只依赖 revision 快照，所以后续归档模型不会造成运行中 CR 漂移。

## Pipeline 输出直接注册

`POST /models/{mid}/import-pipeline-artifact` 接收：

```json
{
  "revision": 3,
  "name": "v2",
  "description": "来自训练产物",
  "runId": 42,
  "instanceId": "由运行详情返回的 UUID",
  "artifactId": "由产物面板返回的 ID",
  "artifactName": "weights",
  "fileName": "model.joblib",
  "modelFormat": "sklearn",
  "modelFormatVersion": ""
}
```

该入口只支持当前原生 `NATIVE_ARGO` 运行，不伪装兼容旧 KFP/MLMD 执行模式。浏览器不提交 endpoint、bucket 或 key。后端核对运行读取权限、输出事件关联、PUBLISHED 记录、执行实例和 namespace UID；缓存复用的产物还要核对原生产者的 namespace UID。创建 DRAFT 版本并保存来源 run/instance/artifact 和 `importOperationId`，递增父模型 revision，在同一事务生成 `model.pipeline.import` 持久操作。

通用 `worker` 执行导入，不需要额外新 worker 角色。它使用产物只读账号读源归档，校验存储大小及 SHA256，再进行两遍 tar.gz 扫描：先完整校验、再发布文件。拒绝绝对路径、目录穿越、链接、特殊设备、重复条目、缺失父目录；最多压缩/解压 10 GiB、10000 项，并限制头部/尾部填充。压缩包暂存到随机私有临时文件，不向宿主机解包成员路径；worker 主机需有相应临时磁盘空间。

单文件转换到 `fileName`，目录去除传输的 `data/` 根后保留内部布局。文件写入独立前缀：

```text
s3://<model-bucket>/loom-models/<group-id>/<namespace-uid>/<operation-id>/<source-sha256>/
```

所有文件写入完成，且租约、当前权限、模型/版本和来源身份仍有效，才在短事务中创建 ACTIVE ModelArtifact 并把版本 revision 从 1 升至 2。版本保持 DRAFT，需要人工确认布局后标记 READY。单文件 digest 是解包后文件 SHA256；目录不伪造单文件 digest，原归档摘要保留在不可变导入命令中。导入期间改动版本会阻止原任务覆盖。失败任务可从版本页重试原操作，仍校验来源和当前权限。

对象存储与 MySQL 不是分布式事务。失败/撤权可能留下未被 Registry 引用的部分前缀，不会注册为可部署模型；重试使用相同命令重写相同字节。禁止自动清理一个仍有操作租约的前缀；后续运维清理需核对操作终态、无引用和保留期。

### 存储配置与权限

新增显式迁移 `000025_model_pipeline_import.sql`，仅为版本表追加来源与导入操作字段，并增加 `kserve.invoke` 权限；无新业务表。API 与通用 worker 都要升级并使用一致配置：

```yaml
model_store:
  enabled: true
  endpoint: http://minio.kubeflow.svc.cluster.local:9000
  bucket: mlpipeline
  prefix: loom-models
  region: us-east-1
  timeout: 30s
```

- 同时启用 `artifact_store`，其只读账号需要读取 `loom-native/*`；不会给原可视化读取账号增加写权限。
- `LOOM_MODEL_STORE_ACCESS_KEY` / `LOOM_MODEL_STORE_SECRET_KEY`（可选 session token）注入独立写账号，仅允许目标 bucket 的 `loom-models/*` 写入及所需 multipart 操作。
- KServe `kserve.service_account` 在各业务 namespace 中预先关联独立的存储读取 Secret，需能 list/get 已发布模型前缀；配置与 `model_store` 一致的 endpoint、TLS、region。浏览器不能提供 Secret 名称或明文密钥。
- 后端本地运行时，`.svc.cluster.local` 不一定可达。应使用可信的可达地址或端口转发，并使 Native 源 manifest endpoint 与 `artifact_store.endpoint` 的 host:port 一致；不可随意绕过匹配。

## 页面与验收入口

1. 模型页：创建/编辑模型，创建版本，版本页编辑描述、登记文件 URI、标记 READY、归档版本/产物；历史列表含分页。
2. Pipeline 运行“指标与产物”：选择原生已发布输出 → 注册模型版本 → 选目标模型和格式 → 查看后台导入进度。无需手抄 URI。
3. READY 版本选择产物 → 发布服务 → 配置服务名、协议、Runtime、资源和副本。创建即排队发布；保存配置仅新增不可变修订，在“配置修订”页显式发布/回滚。
4. 服务详情：期望/观测、generation、同步时间、错误原因、配置、发布历史、停止/删除和“推理验收”。HTTP 202、Registry READY、KServe Ready 是不同层次，不互相代替。

`POST /kserve-services/{sid}/predict` 接收 `{revision, instanceId, payload}`，需 `kserve.read`、`kserve.invoke` 及服务对象权限。内置 Owner/Editor 获得 invoke，Viewer 不自动获得。后端核验 namespace、CR UID 和 KServe Ready，通过固定 Standard predictor Service 的 Kubernetes proxy 发请求，检查 Service 控制器 UID 和 app selector，不访问用户/CR 提供的任意 URL。API Kubernetes 身份需目标 namespace 的 `services/get`、`services/proxy/create` 与既有 InferenceService/空间读取权限。Istio/NetworkPolicy 也需允许此路径；不能通过放宽 TLS 来替代网络授权。

v1 请求 `/v1/models/<service-name>:predict`；v2 请求 `/v2/models/<service-name>/infer`。输入最多 256 KiB、响应最多 1 MiB、20 秒超时，不归档推理输入/输出。返回前再次核对授权、实例和业务 revision，页面切换/撤权后不保留旧响应。真实推理有计算成本，本接口不提供推理幂等重放保证，也不自动重试业务请求。

验收清单与当前未完成项见 [模型发布联调验收](model-serving-acceptance.md)。
