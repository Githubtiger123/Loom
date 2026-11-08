# Loom 集群依赖一键安装

按 Cube 的使用方式提供一个入口：修改配置后运行脚本，按顺序安装 Loom 所需的官方 CRD、控制器和机器权限。**不运行这个脚本也能阅读、修改业务代码；只有要真正创建/执行集群资源时才需要安装。**

这是集群依赖安装器，不是 Loom API/web 的镜像发布器，不会安装第二套平台登录页。此次只编写和静态审阅文件，没有执行脚本、渲染清单、安装组件、运行测试或编译，真实集成仍需部署环境核验。

## 1. 安装内容与固定版本

版本取自当前工作区的 community-distribution，不复制 Cube 的旧组件版本。上游仓库和完整 commit 固定在 `versions.lock.json`，不跟随 master/latest。

| 组件 | 版本 | 默认 |
|---|---|---|
| Profile Controller | v2.0.1 | 安装，只装控制器，不装 Dashboard/KFAM |
| Notebook Controller v1 | v1.11.1 | 安装，匹配 Loom 的 Notebook CR |
| Istio | 1.31.3 | 安装，也可显式复用 |
| cert-manager | 1.21.2 | 安装，也可显式复用 |
| KFP | 2.18.0 | `components.pipeline: true` 启用 |
| Argo Workflows | v4.1.6 | 随 KFP 安装 |
| KServe | v0.21.0 | `components.kserve: true` 启用，Standard 模式 |

KFP 同时安装官方依赖：MySQL 8.4、MLMD、SeaweedFS S3 等。这里的 SeaweedFS 是该版本上游使用的对象存储，不会再额外部署一份 MinIO。KFP 的执行元数据保存在自己的库中，Loom 业务用户、权限和状态缓存仍在 Loom MySQL 中，符合既定的“Loom 授权 → 机器 SA 调 KFP”方案，不是合并底层 schema。

不装 Dex、oauth2-proxy、Central Dashboard、Notebook Web App、KServe UI、Hub Web App、Katib、Trainer、Knative，不创建测试用户/业务 Profile，不改动现有上游源码。

## 2. 首次准备

适合新的专用集群；不是无条件覆盖已有 Kubeflow 的迁移工具。

准备以下工具，脚本不自动修改本机环境：

- Python 3.10+、PyYAML（版本在 `requirements.txt`）。
- Git、独立 `kustomize` **v5.8.1**（与锁定上游脚本一致），不要假设 `kubectl -k` 内置版本等价。
- 与集群版本匹配的 kubectl、具有创建 CRD/ClusterRole 等权限的 kubeconfig。
- Linux Kubernetes 节点、可用 CNI、DNS、镜像拉取网络，且恰好一个可动态供给存储的默认 StorageClass。Notebook 当前未指定 storageClassName；脚本不会替你安装 NFS/hostPath 或修改默认存储。

开启 KFP/KServe 时可按 8 CPU / 16 GiB 以上的开发集群起步，另预留持久磁盘及工作负载资源；这不是生产容量保证。实际 Kubernetes 兼容范围按锁定版本上游说明确认，不承诺任意旧版本或 ARM64 镜像兼容。

新装 Istio 使用该快照的标准 sidecar + CNI 清单，会在 kube-system 部署 `istio-cni-node`。GKE、自定义 CNI 路径、已有 ambient/revision 安装等环境不能直接照用：先按集群要求部署 Istio，再选择 `reuse`，人工核对命名/身份/网关。脚本不修改内核、节点标签、kubeconfig 或系统软件。

下面均为**由部署者后续执行的命令**，本次未执行。

Linux / WSL：

```bash
cd loom/install
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
cp config.yaml config.local.yaml
```

Windows PowerShell：

```powershell
Set-Location E:\kubeflow\loom\install
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
Copy-Item config.yaml config.local.yaml
$env:LOOM_INSTALL_PYTHON = "$PWD\.venv\Scripts\python.exe"
```

修改 `config.local.yaml`：至少填写明确的 `context`、Notebook 域名；需要 Pipeline、KServe 就把两个开关设为 true。不需要提前手工下载整套 Kubeflow。

源码选择：

1. `source.local_dir` 指定目录时，只读取该目录，核对 commit 和干净 Git 状态，不修改它。
2. 留空时优先读取本工作区相邻的 `community-distribution`。
3. 没有相邻目录才下载锁定 commit 到 `.sources/`。发现本地 commit 不符/有改动会停止，不自动 reset。

KFP 上游还有指向 **Argo v4.1.6** 的远程 Kustomize 引用，首次渲染 KFP 仍需访问 GitHub；只复制 community-distribution 目录不等于全离线。完整离线方式见第 6 节。

## 3. 一个命令安装

Linux / WSL：

```bash
bash install.sh --config config.local.yaml
```

Windows：

```powershell
.\install.ps1 --config config.local.yaml
```

入口统一调用 `scripts/install.py`。先生成资源，再进行只读预检，展示目标 context 和默认存储，要求输入完整 context 确认后才应用。所有 kubectl 命令显式携带该 context，不切换全局当前 context。自动化部署可额外使用 `--yes`，但仍必须填写 context。

安装顺序：namespace → cert-manager → Istio → 聚合角色 → Profile Controller → Notebook Controller → 可选 KFP → 可选 KServe → Loom 机器权限 → 可选 Notebook HTTPS 入口。

每个组件再拆成 CRD、基础资源、证书/mesh 配置、准入 webhook、其余 CR：先等待 CRD Established，应用证书和服务间策略后等待控制器 rollout，等待 webhook CA 填充，再提交依赖它的 CR。避免将一大份 YAML 无限重试。超时/冲突停止，不自动卸载或回滚已经创建的资源。

## 4. 与 Loom 的配置配合

### 空间与 Notebook

Profile Controller 只管原生资源；固定 owner=`admin` 仍由 Loom worker 创建 Profile 时填写，不把业务组所有者改成 Kubernetes owner。用户、组和授权仍由业务接口写 MySQL，再由任务执行器落实。

控制器配置对齐现有代码：

- namespace：kubeflow；default-editor/default-viewer、kf-resource-quota 由 Profile Controller 创建。
- Profile 的 userid header/prefix 使用安装配置；Loom `identity` 必须相同。
- 网关 principal：`cluster.local/ns/istio-system/sa/istio-ingressgateway-service-account`。
- Notebook Controller principal：`cluster.local/ns/kubeflow/sa/notebook-controller-service-account`。
- KFP UI principal：`cluster.local/ns/kubeflow/sa/ml-pipeline-ui`；即使没启用 KFP，也保留该配置以匹配 Loom 的空间就绪检查。
- Notebook 路由引用 `kubeflow/kubeflow-gateway`、指定 hostname；禁用控制器自动空闲回收，启停由 Loom 业务管理。
- 新 Profile namespace 启用 sidecar、baseline PSS；按模块开关加 KFP/KServe 标签。上游控制器不覆盖既有同名标签，后续启用模块时，已有业务空间的标签需要单独审阅并补齐。

目前固定使用 `cluster.local` 服务域和信任域、固定控制器 namespace。自定义服务域/信任域时必须同时修改安装适配与 Loom 配置，不能只改一个 principal 字符串。

### 执行身份

| Loom 角色 | ServiceAccount | 权限用途 |
|---|---|---|
| api | loom-api | 集群状态/配额、Pod 日志；可选终端；启用 KFP 时的机器查询 |
| worker | loom-worker | Profile、成员 RBAC/AP、Notebook、PVC 生命周期 |
| sync | loom-sync | 只读资源观测 |
| pipeline-worker | loom-pipeline | KFP 虚拟业务资源授权、Workflow/Pod 只读 |
| kserve-worker | loom-kserve | InferenceService 生命周期及只读空间校验 |

这些都是跨 namespace 的高信任后台身份，不应交给用户 Pod。用户 Pod 使用 Profile 下的 default-editor。在线终端默认不授予，开启 `rbac.pod_exec` 后只绑定给 API。

`rbac.space_delete_inventory` 默认关闭：当前安全删除空间会枚举所有可发现的 namespaced API，因此确需 `*/* list`，这也能读取 Secret 内容。关闭时空间删除会因权限不足失败关闭，不代表可以安全删除；需要这个功能时显式开启并接受高信任执行器边界。

**上游 Profile Controller 目前仍使用 cluster-admin 绑定。**脚本沿用该控制器权限，没有把它包装成最小权限安装；生产需要单独做权限收敛审计。

### Pipeline / KServe

安装开关不自动修改应用配置。启用后在 Loom 配置中对应设置：

```yaml
pipeline:
  enabled: true
  endpoint: http://ml-pipeline.kubeflow.svc.cluster.local:8888
  metadata_endpoint: http://metadata-envoy-service.kubeflow.svc.cluster.local:9090
  token_file: /var/run/secrets/kfp/token
  service_account: default-editor
  pipeline_root: s3://mlpipeline/loom/pipelines
kserve:
  enabled: true
  service_account: default-editor
  deployment_mode: Standard
```

KFP API 的认证用 projected SA token，audience=`pipelines.kubeflow.org`（锁定 KFP 的默认 TokenReview audience），不使用用户 Access JWT。Pod 配置合并示例见 `examples/pipeline-worker-pod-spec.yaml`：普通 Kubernetes token 与 KFP audience token 分开挂载，API 进程用自己的 loom-api SA，独立 pipeline-worker 用 loom-pipeline SA。双方都需 mesh sidecar，实际 Pod/Service 标签与部署配置需一致。

KFP 多租户 TokenReview/RBAC 保留；不公开上游 KFP VirtualService。MLMD grpc-web 原先关闭 sidecar，安装适配为启用 sidecar，再只放行 Loom 的 API/pipeline 机器身份，避免新增一条裸露元数据入口。

Argo 默认开启 `artifactRepository.archiveLogs: true`，官方 S3 配置保留。**这不等于 Loom 自定义离线日志接口已经接通**：Loom 当前还要求 `manifest.json` 索引契约，原生 Argo 日志对象结构不同，仍需采集/索引适配，不自动开启 `log_archive.enabled`。

KServe 使用 Standard，不部署 Knative。推理业务表仍由 Loom 管理，控制器部署 InferenceService。服务公网路由、模型桶权限和推理鉴权不是 Notebook Cookie 鉴权，不会自动匿名开放。

## 5. Notebook 浏览器入口

默认 `gateway.enabled: false`，不会安装上游通配 HTTP Gateway；新装 Istio ingress 为 ClusterIP，入口策略不放行浏览器请求。因此“依赖装完”不意味着 Notebook 打开链路已经完成。

启用前需准备：

1. 自己构建并部署 Loom API，namespace=loom-system、Pod label `app: loom-api`、Service 与 `gateway.auth_service/auth_port` 一致，开启 Istio sidecar。
2. 完成 Loom MySQL 迁移/管理员初始化、认证密钥和 MySQL/Redis/队列配置；本脚本不执行这些业务初始化。
3. 配置平台 HTTPS origin、独立 Notebook HTTPS origin，Notebook hostname 与安装配置一致；在 istio-system 事先创建匹配域名的 TLS Secret。可先跑一遍基础依赖安装，再准备证书，再启用入口重跑。
4. 给 Loom 设置最小 `identity.trusted_proxy_cidrs`，核对 mTLS、CNI NetworkPolicy 是否生效，再启用 gateway。不要信任任意集群 Pod/CIDR。

可选入口会生成 HTTPS Gateway、`loom-auth` HTTP ext_authz provider、只覆盖 `/notebook/*` 的 CUSTOM 策略，以及精确的 Notebook session POST/OPTIONS 路由。网关校验失败关闭；原始 Host/Cookie/Origin/Sec-Fetch-* 交给 `/verify`，响应中的 userid/sub/Cookie/Authorization 覆盖上游请求。不公开 `/verify` 路由，不在 URL 放 token。

同时为 `app: loom-api` 安装严格 mTLS、来自 ingress 的 ALLOW 与 NetworkPolicy；这套入口策略仅允许网关访问校验与 Notebook session 路径。**不是完整的平台官网网关**：前端静态资源、普通 `/api/v1`、执行器 mTLS listener 的网络隔离需要在应用部署阶段单独配置。若 API 同时提供这些功能，必须成套补齐相应策略，不能全局放开内部 `/verify`。不能把相同标签应用给需要其他网络入口的无关服务。

实际 Host/authority、头覆盖与凭据剥离、跨源 Cookie/WebSocket、证书、CORS 仍需部署环境核验。已有 Istio 选 reuse 时不会修改它的 mesh 或替换认证策略，需人工按 [认证契约](../platform/docs/authentication.md) 合并。

## 6. 只生成资源 / 离线搬运

```bash
bash install.sh render --config config.local.yaml
```

`render` 不调用 kubectl、不连接集群，但可能下载锁定源码及 Argo 版本引用。输出在 `.generated/<本次目录>/`：各阶段 YAML 与 plan.json，包含文件摘要、锁定版本、context 和配置摘要。资源内含 Secret，不要贴进聊天、日志或提交 Git。

完全离线安装：在联网机器准备工具/镜像、执行 render，将生成的整个目录和对应配置安全搬到部署机，保持 context 名称和配置一致，再运行：

```bash
bash install.sh install --config config.local.yaml --bundle /secure/path/to/generated-bundle
```

bundle 模式不需要 Git/Kustomize，也不重新生成密码，但仍需 Python/PyYAML、kubectl、相应镜像和可用集群。配置/文件摘要不一致会拒绝安装；不要手工改 bundle，改配置后重新生成。后续若要重新 render，需同时安全备份/恢复 `.state/` 中的凭据。

## 7. 凭据、重复执行与故障处理

- KFP MySQL 和 S3 密码首次随机生成到 `.state/<installation-context摘要>/pipeline-credentials.json`。MySQL 不再使用空密码，S3 不再使用 minio/minio123。
- 后续复用同一份凭据，若与集群 Secret 不一致则停止；已有 MySQL PVC 但 Secret 丢失也停止。不要删除状态目录来“重试”，应恢复原凭据及数据库备份。
- `.state/` 与 `.generated/` 都敏感。POSIX 下私有目录/文件权限为 700/600；Windows 的 chmod 不等价于 ACL，部署者需额外限制 NTFS 权限、备份位置和日志访问。
- 每个资源标记安装器与 installation。重复执行只更新同一安装器的资源，不带 force-conflicts、不 prune、不卸载、不删除 PVC。冲突会停止，失败后按相同配置重跑；部分已装资源保留。
- 已有其他安装器管理的同名资源不接管。Namespace 允许预创建，但仅补充兼容的标签，冲突的 injection/PSS 标签拒绝覆盖；这不意味着接管已有 namespace 中的全部应用。
- `reuse` 只核对既定命名的 CRD/控制器就绪，不验证任意旧版本兼容，不替换已有认证。与本快照不一致时由部署方审阅，不会偷偷升级共享组件。
- 从 true 改 false **不是撤权/卸载操作**。之前装过的组件、ClusterRoleBinding、Gateway 等不会自动删除；需要运维明确审核后撤销，避免误删数据。
- 脚本不输出 kubectl 的敏感失败响应，以免错误 patch 带出密码。根据最后打印的阶段，后续由部署者用明确 context 查看 Pod、事件、证书和 rollout；不要直接打印 Secret 或整份失败 patch。

安装成功信息只表示脚本的资源应用、控制器就绪及相关证书/webhook 等待完成，不证明业务 CRUD、权限隔离、Pipeline 执行、浏览器访问或生产安全已经通过验证。
