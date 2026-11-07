# Loom KServe 主线

本文描述后端源码契约。此次没有新增前端，没有运行测试、编译、项目、迁移或真实集群联调。

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
4. CR UID 写入 `external_id`；只有 `status.observedGeneration >= metadata.generation` 且 Ready=True 才确认该 revision 为 SERVING。

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
