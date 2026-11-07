# Loom 模型注册与 KServe 集成

本文描述 Loom 对照 Kubeflow Model Registry/Hub 逻辑实现的业务模型管理后端。前端、模型文件上传器、Catalog 聚合、CSI 和 `model-registry://` 协议不在本期范围。

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
