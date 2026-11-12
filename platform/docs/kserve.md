# Loom KServe 主线

本文描述 KServe 后端源码契约。模型页与推理服务页已接入前端，新增受控推理验收接口；配置要求和未完成的真实验收见 [模型发布联调验收](model-serving-acceptance.md)。源码存在不代表已完成集群联调。

## 边界

Loom 的 `tb_kserve_service` 保存业务所有权、当前配置、期望/观测状态和当前 CR UID；`tb_kserve_revision` 保存不可变配置修订；KServe Controller 负责 Deployment、Service、Pod、伸缩和入口。Loom 不直接修改 KServe 生成的下层资源，也不接收任意 YAML、PodSpec、ServiceAccount、Secret、Volume、特权配置或用户指定部署模式。

每个服务只支持一种 predictor：

- `MODEL` 映射为 `spec.predictor.model`，支持受限 storage URI、模型格式、可选受控 Runtime 名称、协议和资源。
- `CUSTOM` 映射为单个 `kserve-container`，只开放镜像、command、args、HTTP 端口和 readiness path；平台固定注入协议、资源和收紧的 securityContext。

执行身份取 `kserve.service_account`，部署模式首期固定 `Standard`。模型存储凭据必须由该 ServiceAccount 和集群运维预先配置，业务请求不能携带明文凭据或 Secret 名称。

`MODEL` 推荐提交 Loom `artifactId`。API 在业务事务内解析 Model Registry，并把模型、版本、产物 ID、真实 URI、格式、摘要和大小冻结进不可变 KServe revision；worker 不在运行时查询 Registry。直接提交 `storageUri` 的兼容方式仍保留。模型注册流程见 [模型注册与 KServe 集成](model-registry.md)。

## 创建与发布

创建业务服务会同时生成 revision 1 和持久操作单。`kserve-worker` 领取后先复核用户授权、组状态、Profile/namespace UID，再按 namespace + 业务名称读取 CR：

1. 同名对象不存在且业务未绑定 UID时，创建带 Loom group/service/instance/revision 标识的 `InferenceService`。
2. 同名对象存在时，必须同时匹配 Loom 管理标签和 instance；已绑定 UID 时还必须匹配 UID，否则拒绝接管。
3. 更新使用 GET 后携带 `resourceVersion` 的 Update，只渲染结构化受控 spec。
4. CR UID 写入 `external_id`；Ready=True 后，还需确认受该 ISVC UID 控制的 predictor Deployment/PodTemplate 携带当前 instance/revision/spec hash，Deployment 已观测自身 generation，目标副本全部完成滚动发布，才确认该 revision 为 SERVING。Standard 模式中 ISVC 的 observedGeneration 来自 Deployment，不能直接与 ISVC generation 比较。

PATCH 只产生新配置修订并推进业务 CAS revision，不修改当前 CR，不受服务正在运行还是已停止影响。`deploy` 可选择任一保留的 `revisionId`，生成新的业务 revision 后才发布。运行状态同步按 `appliedRevision` 核对当前 CR，因此保存草稿不会把线上 CR 误报为漂移。

使用 Registry Artifact 的历史 revision 在每次 deploy 前都会重新检查 Artifact 是否仍有效且所属 Version 为 READY。归档模型不会中断已运行服务，但会阻止以后再次部署该 Artifact。

## 停止、重新发布与删除

停止语义是删除 `InferenceService`，不是缩到 0，也不保留一个假停止 CR。Delete 请求已受理不算完成；worker 必须再次 GET 并确认 NotFound，随后把业务状态写为 STOPPED、关闭同步并清空当前 `external_id`。配置修订、历史和旧操作回执仍保存旧 UID。

STOPPED 服务再次发布时允许创建同名新 CR，并绑定新的 Kubernetes UID。任何已绑定 UID 的 SERVING 对象若意外消失，只标记 `RESOURCE_NOT_FOUND`，不会按相同 revision 自动重建，避免把人为删除误判为授权发布。

删除业务服务先执行同样的 CR 删除和 NotFound 确认，再写 DELETED 与 `deleted_at`。如果 CR 从未创建，确认同名受控对象不存在即可完成。

## 状态同步

`kserve-worker` 同时运行两条只读观测链：

- Kubernetes Watch 监听 Loom 管理的 InferenceService，按 service ID、group ID、instance、namespace、name、revision 和 UID 全部匹配后即时写回。
- GORM 持久同步租约定期 GET，修复 Watch 断线、过期 resourceVersion、进程重启和漏事件。多副本通过 `SKIP LOCKED` 与 lease 避免重复集中读取，并受 `sync_concurrency`、`sync_qps` 限制。

MySQL 投影包括 generation、observedGeneration、resourceVersion、Ready、公开 URL、集群 address、清洗后的 reason、同步时间和失败次数。URL 只接受无 userinfo 的 HTTP(S)；错误只保存限长摘要，不落完整 CR、PodSpec、日志或凭据。Watch 不创建业务身份、不绑定新 UID、不改变 desired state；发布/停止/删除的完成仍由持久操作执行链确认。

## 部署要求

先显式执行 `000020_kserve.sql`，同步升级 API 和 kserve-worker，再设置 `kserve.enabled=true`。worker 的 Kubernetes 身份至少需要跨目标 namespace 对 `serving.kserve.io/v1beta1/inferenceservices` 的 get/list/watch/create/update/delete 权限，以及现有空间身份核验所需只读权限。切换前必须停止旧 KServe 外部适配器，避免两个执行端竞争同一操作或 CR。

## 官方复用与本次后端补全

继续使用官方 KServe v0.21.0 CRD、Controller、ServingRuntime/ClusterServingRuntime、Storage Initializer、Deployment/Service/HPA，不复制 Controller，也不自行加载模型或创建下层工作负载。Runtime 格式/版本、autoSelect、协议匹配参照官方 `pkg/apis/serving/v1beta1/predictor_model.go` 与 `v1alpha1/servingruntime_types.go`（Apache-2.0）；最终 Runtime 优先级、默认值和准入仍由官方控制器决定。工作负载归属和组件注解传播按官方 `components/predictor.go`、`reconcilers/deployment/deployment_reconciler.go`；S3 凭据沿用官方 `pkg/credentials/s3/s3_secret.go`。

Loom 仅补业务授权、持久命令、来源快照、发布前检查、受控内容读取与 MySQL 状态投影，不引入第二套数据库或新的服务生命周期表。

### 新增接口

| 接口（均在 `/api/v1/groups/{gid}` 下） | 权限与作用 |
| --- | --- |
| `GET /kserve-runtimes` | `kserve.read`；返回当前空间和集群 Runtime 名称、格式、版本、协议、禁用/多模型/多节点标志，不返回 PodSpec、env、Secret 或镜像凭据 |
| `POST /kserve-preflight` | `kserve.read` + `kserve.create`；body 与创建服务相同，Registry Artifact 另做 `model.read`/归属验证；只读检查，无任务/资源写入 |
| `GET /kserve-services/{sid}/diagnostics` | `kserve.read` + `kserve.logs` + 对象范围；返回当前 CR、predictor Pod/init container 状态及最多 50 条 UID 归属事件 |
| `GET /kserve-services/{sid}/logs` | 同上；必须提供 `instanceId`、业务 `revision`、`pod`、`podUID`、`container`；支持 `tailLines`、`limitBytes`、`sinceSeconds`、`previous` |

日志只允许 `storage-initializer` 与 `kserve-container`，最多 1000 行、1 MiB、最近 3600 秒，不支持 follow、sidecar、任意 namespace、exec。归属按 ISVC → Deployment → ReplicaSet → Pod 的 controller owner UID 验证；读取后重查 Pod UID、服务修订、当前授权和 namespace 身份，变更则丢弃旧内容。诊断最多展示 20 个 Pod，超限明确 `truncated`，不是伪造完整结果。新权限迁移为 `000026_kserve_diagnostics.sql`，只给内置 Owner/Editor，Viewer 不自动获得日志/事件内容访问。

### 发布前检查和存储

Preflight 检查 InferenceService API、配置的 ServiceAccount、可选存储 Secret 关联、Runtime 格式/版本/协议。`ready=true` 仅表示这些元数据检查通过；`storageAccess=NOT_VERIFIED`、`inferenceValidated=false` 明确表示没有读取模型或做推理。worker 在数据库事务外重复同一检查；缺项返回固定原因码，可修复后显式重试。停服/删除不依赖 Runtime 或存储检查。

`kserve.storage_secret` / `LOOM_KSERVE_STORAGE_SECRET` 是运维配置的现有 Secret 名称；非空时要求该名称存在于 `kserve.service_account` 的 `secrets` 引用，API 不读取 Secret 数据。空值兼容云 Workload Identity/公开存储，不表示免认证或已经验证读取。业务请求不能选择 Secret 或 SA。

独立、只读、按空间前缀限制的配置示例为 `install/manifests/kserve/model-reader.example.yaml`（不参与自动安装）。每个 Profile namespace 均需运维预配置同名 SA/Secret，并配置：

```yaml
kserve:
  enabled: true
  deployment_mode: Standard
  service_account: loom-model-reader
  storage_secret: loom-model-storage
```

这个身份与 Loom `model_store` 写账号、镜像仓库账号完全不同。示例 Secret 使用官方 `awsAccessKeyID`/`awsSecretAccessKey` 键名和 `serving.kserve.io/s3-*` 注解。内网 HTTP MinIO 使用 `s3-usehttps: "0"`；公网 S3 应使用 HTTPS 并校验证书，不用关闭 TLS 校验解决认证失败。真实凭据不要提交仓库；Loom 不会自动扩大原产物只读账号权限、创建凭据或替换已有 SA。

机器 RBAC 已补 Runtime 只读权限及单独绑定给 `loom-api` 的 `services/proxy:create`。后者是推理调用权限，不授予共享 reader 或 pipeline worker；平台 API 只访问核验后的固定 predictor Service/协议路径，不能当任意代理使用。API 本身是高信任身份；生产环境可用按业务 namespace 的 RoleBinding 缩小调用范围。

### 升级与验收边界

显式执行迁移至 000026，更新机器 RBAC、API/kserve-worker，配置 SA/存储身份后再启用功能。已存在但没有新组件注解的服务不会被虚报 Ready，需显式重新发布使官方 Controller 传播 rollout 标识。默认配置仍保持关闭，不擅自改变现有运行环境。

本轮未运行测试、类型检查、编译、项目启动、数据库迁移或集群写入。模型加载、存储权限、镜像可达、Webhook/Istio、真实推理仍需部署后验收。不宣称支持完整 KServe 的 Transformer、Explainer、Canary、ModelMesh、Knative、LLMInferenceService 或多节点模型；首期是 Standard 单 predictor（MODEL/CUSTOM）后端。
