# Loom 集群依赖安装

和 Cube 一样，入口就是一个 [install.sh](install.sh)：按顺序直接 apply 资源、等待必要的 CRD 和控制器就绪。没有 Python/PyYAML、YAML 解析器、安装计划生成或自定义安装框架。

这里只编写、静态审阅了文件，**没有运行脚本、Kustomize 渲染、测试、编译或连接集群**。安装成功也不代表业务、浏览器和权限隔离已通过真实集成验证。

## 使用

仅面向 Linux 部署，准备 Bash、Git、kubectl 和独立 Kustomize v5.8.1。kubectl 版本需匹配集群；脚本不会安装工具、修改节点或覆盖 kubeconfig。唯一安装入口是 `install.sh`。

先审阅 `manifests/` 中的资源参数，然后执行：

```bash
cd loom/install
bash install.sh YOUR_KUBECTL_CONTEXT
```

脚本展示集群信息，要求输入完整 context 确认。每条 kubectl 都显式指定同一个 context，不切换全局 current-context。自动化可设置 `CONFIRM_INSTALL=true` 跳过交互，仍必须指定 context。

默认安装 Profile、Notebook、Istio、cert-manager。需要 KFP、KServe：

```bash
# 先复制并编辑凭据；替换所有 REPLACE_ME，不要提交真实密码。
cp manifests/pipeline/secrets.example.yaml manifests/pipeline/secrets.local.yaml

INSTALL_PIPELINE=true INSTALL_KSERVE=true bash install.sh YOUR_KUBECTL_CONTEXT
```

后续需要 Trainer 基础环境时，可单独或同时开启：

```bash
INSTALL_TRAINER=true bash install.sh YOUR_KUBECTL_CONTEXT
```

脚本顶部的开关也可直接修改：

| 开关 | 默认 | 用途 |
|---|---|---|
| INSTALL_CERT_MANAGER | true | 安装 cert-manager；已有兼容版本可设 false |
| INSTALL_ISTIO | true | 安装标准 sidecar/CNI Istio；已有兼容版本可设 false |
| INSTALL_PIPELINE | false | 安装 KFP 及 Argo、MySQL、MLMD、SeaweedFS |
| INSTALL_KSERVE | false | 安装 Standard 模式 KServe，不装 Knative |
| INSTALL_TRAINER | false | 安装 Trainer v2.3.0、JobSet v0.12.0 和官方训练运行时 |
| INSTALL_NOTEBOOK_GATEWAY | false | 应用可选 Notebook HTTPS 与 Loom ext_authz 配置 |
| ENABLE_POD_EXEC | false | 授予 API 在线终端权限 |
| ENABLE_SPACE_DELETE | false | 授予 worker 安全删除空间所需的全 API list 权限 |
| WAIT_TIMEOUT | 600s | 每条就绪等待的超时 |

这些开关只控制本次是否 apply，**设为 false 不会卸载已有组件或撤销权限**。

## 资源与版本

| 资源目录 | 内容 | 版本 |
|---|---|---|
| manifests/cert-manager | 证书控制器 | 1.21.2 |
| manifests/istio | CRD、控制面、CNI、入口策略 | 1.31.3 |
| manifests/roles | Kubeflow 与 Istio 聚合角色 | 同锁定快照 |
| manifests/profiles | Profile Controller，无 Dashboard/KFAM | v2.0.1 |
| manifests/notebook | Notebook v1 控制器，无 Notebook Web App | v1.11.1 |
| manifests/pipeline | KFP 多租户后端及执行依赖 | KFP 2.18.0、Argo v4.1.6 |
| manifests/kserve | CRD、控制器、运行时 | v0.21.0，Standard |
| manifests/trainer | TrainJob/Runtime CRD、控制器、JobSet、训练运行时 | Trainer v2.3.0、JobSet v0.12.0 |
| manifests/gateway | 可选 Notebook HTTPS 访问入口 | Loom 配置 |

本地保存 Kustomize 适配、参数和业务机器权限，官方大清单引用 community-distribution 固定 commit：

`96e316a80e257a9f645c7c1ee0e1a94b904dead7`

Argo 的上游引用固定在 v4.1.6，不跟随 master/latest。升级需要审阅并成套修改这些引用及适配，不只修改脚本里打印的版本。

脚本中的安装语句就是：

```bash
kubectl --context YOUR_KUBECTL_CONTEXT apply -f manifests/namespaces.yaml
kustomize build manifests/profiles | kubectl --context YOUR_KUBECTL_CONTEXT apply --server-side --field-manager=loom-installer -f -
kubectl --context YOUR_KUBECTL_CONTEXT wait --for=condition=Established crd/profiles.kubeflow.org --timeout=600s
```

使用独立 Kustomize 再交给 apply，是为了固定 Kustomize 行为；不是另写安装器。若以后统一核对了 kubectl 的内置 Kustomize 版本，也可等价使用 `kubectl apply -k`。

安装顺序是 Namespace → cert-manager → Istio → 聚合角色 → Profile → Notebook → 可选 KFP/KServe/Trainer → Loom 机器权限。KFP 先装 Workflow、DecoratorController 等 CRD；KServe 先装 CRD、控制器、证书和 webhook，等就绪后再应用运行时 CR。Trainer 先装自身 CRD，再装控制器及 JobSet，等待 CRD、控制器和 webhook CA 就绪后应用训练运行时。

**当前不是全离线资源包**：Kustomize 会访问锁定的 GitHub 资源，集群也需能拉取对应镜像。离线环境需要提前将官方资源和镜像镜像化/归档，将引用改为审阅过的本地路径；不能只复制 install 目录就假设已离线打包。

## 部署前提与参数修改

用于新的专用集群；已有 Kubeflow/共享组件不能直接当迁移工具使用。脚本直接 apply，不再包含旧 Python 安装器的资源归属比对、凭据自动生成/一致性检查或 bundle 模式。相同配置重跑不会主动删除资源，但 apply 可能更新已有同名对象；先审阅清单，遇到 SSA 冲突应人工确认，不加 force-conflicts 抢占。

集群需要 Linux 节点、正常 CNI/DNS、镜像网络、创建 CRD/ClusterRole 等权限，以及一个可动态供给存储的默认 StorageClass。Loom 当前 PVC 未指定 storageClassName，脚本不安装 hostPath/NFS、不替换默认存储。KFP/KServe 开发集群可按 8 CPU / 16 GiB 以上起步，另外预留工作负载和持久磁盘；不保证任意旧 Kubernetes 或 ARM64 兼容。

标准 Istio 清单会在 kube-system 部署 CNI。GKE、自定义 CNI 路径、已有 ambient/revision 安装需按实际环境预装并复用；脚本不为这些环境修改系统路径。

常用配置直接改文件：

| 参数 | 文件 |
|---|---|
| Notebook hostname、网关名、关闭自动空闲回收 | manifests/notebook/params.env |
| userid header/prefix、机器 principal | manifests/profiles/params.env |
| 新业务 namespace 的注入和模块标签 | manifests/profiles/namespace-labels.yaml |
| KFP 的同名身份配置 | manifests/pipeline/base/pipeline-config.yaml |
| KFP MySQL/S3 凭据 | manifests/pipeline/secrets.local.yaml |
| KServe 部署模式、路由域名 | manifests/kserve/base/deployment-mode.yaml |
| Notebook TLS、域名、API 目标 | manifests/gateway/notebook.yaml |
| 入口域名放行范围 | manifests/istio/notebook-auth/kustomization.yaml |
| ext_authz 服务、身份头及完整 mesh 值 | manifests/istio/notebook-auth/mesh.yaml |

默认使用 cluster.local 服务域/信任域、kubeflow 控制面 namespace，域名占位值为 notebooks.example.com。修改域名时同步修改 Notebook、入口规则、Gateway 和 KServe 相应文件。修改身份头/prefix 时，Loom、Profile、KFP 和网关必须一致。

Profile owner=`admin` 仍由 Loom worker 在创建 Profile 时填写；脚本不创建业务用户/空间，不改业务组 Owner。用户、权限和状态缓存仍在 Loom MySQL，官方控制器负责集群资源。Profile namespace 预留 KFP/KServe 标签，标签本身不安装组件；已有 Profile 的标签变化需按上游控制器规则单独处理。

## 凭据和机器权限

KFP 的两个 Secret 不采用上游空密码和 minio/minio123。先准备 secrets.local.yaml，脚本单独 apply；官方 Kustomize 中的同名 Secret 被删除，不会覆盖你填写的值。MySQL 初始化环境引用 mysql-secret；SeaweedFS 使用 mlpipeline-minio-artifact。

**已有 MySQL/PVC 时必须沿用原密码**。apply Secret 不等于修改数据库内部密码；脚本不再帮你生成、比较或轮换凭据。妥善限制本地文件权限并备份，不开 bash -x，不公开失败 patch/Secret。旧安装器留下的 .state/.generated 不会被清理，若已有凭据应安全迁移，不能重新填另一套密码。

机器身份在 platform-worker-rbac.yaml 和 manifests/*-rbac.yaml：

| 角色 | ServiceAccount |
|---|---|
| API、Pod 日志与可选终端 | loom-api |
| Profile/成员/Notebook/PVC worker | loom-worker |
| 只读观测 | loom-sync |
| KFP 执行器 | loom-pipeline |
| KServe 执行器 | loom-kserve |

这些跨空间身份不交给用户 Pod，用户执行仍使用 Profile 下的 default-editor。Profile Controller 沿用上游 **cluster-admin**，不是最小权限组件。ENABLE_SPACE_DELETE 会授予全 API list，**包括 Secret 内容**；关闭时当前空间安全删除会因权限不足失败关闭。生产需单独审计这些高信任边界。

KFP API 使用 SA TokenReview，不使用 Loom 用户 JWT；挂载示例见 examples/pipeline-worker-pod-spec.yaml，KFP token audience=`pipelines.kubeflow.org`，普通 Kubernetes token 单独挂载。API 与执行器都需要 Istio sidecar。MLMD grpc-web 启用 sidecar，通过 pipeline-access.yaml 只放行 Loom 的 API/pipeline 机器身份。

应用配置也需单独开启对应模块：

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

KFP 仍保留自己的执行数据库，不合并 Loom/KFP schema。其对象存储是该版本官方 SeaweedFS，不再加一套 MinIO。Argo archiveLogs 默认开启，但原生日志结构与 Loom 离线日志 manifest.json 契约不同，不能因此宣称离线日志接口已接通，也不自动启用 log_archive。

## Notebook 浏览器入口

默认不开放官方通配 HTTP Gateway，ingress 为 ClusterIP，匿名请求不放行。装好依赖不代表浏览器已经能打开 Notebook。

启用 INSTALL_NOTEBOOK_GATEWAY 前：

1. 部署 Loom API：namespace=loom-system、Pod label=`app: loom-api`、开启 sidecar，Service=loom-api:8080。
2. 完成 Loom 业务库迁移、管理员初始化、认证密钥等应用配置。
3. 配好平台 HTTPS origin、独立 Notebook origin，替换清单中的占位域名，在 istio-system 准备 loom-notebook-tls Secret。可以先安装基础依赖再准备证书。
4. 设置 Loom 最小可信网关网段，并核对 mTLS/NetworkPolicy 确实生效。

可选清单包含 HTTPS Gateway、HTTP ext_authz provider、仅覆盖 /notebook/* 的 CUSTOM 策略、精确的 Notebook session POST/OPTIONS 路由；不公开 /verify，也不把 token 放 URL。凭据、Host/authority、头覆盖、跨源 Cookie、CORS 和 WebSocket 行为仍需真实环境核验。

该清单还限制 app=loom-api 的 mTLS 与网络入口，**不是完整官网网关**：普通业务 API、前端静态资源、内部执行器监听需在应用部署阶段补齐成套策略，不可为了放行普通 API 全局公开 /verify。KServe 推理鉴权也不复用 Notebook Cookie。

不部署 Loom API/web 镜像、不执行数据库迁移，不装 Dex、Dashboard、Notebook Web App、Hub UI 或 Knative。失败后保留已创建资源，修正原因后重跑；没有自动回滚、卸载、prune 或 PVC 删除。

## Trainer 后续支持预留

INSTALL_TRAINER 仅安装集群基础设施，不代表 Loom 已实现训练执行器。安装内容为：

- trainer.kubeflow.org/v1alpha1 下的 TrainJob、TrainingRuntime、ClusterTrainingRuntime CRD。
- kubeflow-system 中的 Trainer v2.3.0、JobSet v0.12.0 控制器、Webhook 和官方配套权限。证书由控制器原生管理，不额外创建 cert-manager Certificate。
- 官方训练运行时目录：PyTorch、DeepSpeed、JAX、XGBoost、MLX，以及 Torchtune 模板；不创建训练任务，不拉起 GPU 工作负载，不安装 GPU 驱动/设备插件。

使用的是新 Trainer API，不安装旧 Training Operator v1 的 PyTorchJob/TFJob。LeaderWorkerSet、Kueue/Volcano、数据缓存等扩展不在本次默认依赖范围内；需要相应能力时另做适配。

Loom 的用户和组权限不变；官方 Trainer 聚合角色供 Profile 的原生权限体系使用。没有提前给 loom-worker/API 授予 TrainJob 写权限或增加一个尚未实现的训练 worker。

新增的 kubeflow-system namespace 关闭 sidecar 注入，让控制器使用自身 TLS 机制，避免现有 mesh deny-all 阻断 webhook/status listener。这不是所有网络入口已隔离的保证；未来训练执行接入仍需核对网络策略、状态上报和分布式通信。已有共享 kubeflow-system/JobSet 的集群应先审阅 namespace 标签及现有控制器，不能重复安装另一份 JobSet，也不通过删除 Deployment 强行升级。

后续仍需实现 Loom 的 TrainJob 创建/启停/删除、状态同步及对象权限适配。此轮没有增加业务 API、表、前端或测试，没有运行脚本或验证真实训练。
