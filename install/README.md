# Loom 集群依赖安装

和 Cube 一样，入口就是一个 [install.sh](install.sh)：按顺序直接 apply 资源、等待必要的 CRD 和控制器就绪。没有 Python/PyYAML、YAML 解析器、安装计划生成或自定义安装框架。

脚本现在也可选发布 Loom 自身控制面。设置 `INSTALL_PLATFORM=true` 后，会通过 [Loom Kustomize 清单](kubernetes/loom/README.md) 将专用 MySQL、Redis、web、API、worker、sync、cron 和所选 ML worker 部署到独立的 `loom-system`，等待中间件后显式运行一次数据库迁移并等待 rollout；默认仍为 false，不会意外发布应用。镜像构建与本地源码映射开发见 [docker/README.md](docker/README.md)。

这里只编写、静态审阅了文件，**没有运行脚本、Kustomize 渲染、测试、编译或连接集群**。安装成功也不代表业务、浏览器和权限隔离已通过真实集成验证。

## 使用

### 手动逐组件安装与下载代理

全部组件的 GitHub 安装依赖已统一改用 `https://ghfast.top/`，包括 cert-manager、Istio、Profile、Notebook、聚合角色、Pipeline/Argo、KServe、Trainer/JobSet。上游 Kustomize 目录、配置和补丁保存在 `manifests/upstream/`，独立资源 YAML 通过代理读取，不再通过 Git 拉取远程目录。请一次性将本地整个 `install/` 更新上传到服务器 `/home/ubuntu/install/`，保留你自己的 `install.local.env`、仓库凭据和 `secrets.local.yaml`；后续继续按组件手动安装即可。已有服务器上的旧 `install.zip` 不会随源码修改自动更新，需要重新上传本次修改的文件。

```bash
cd /home/ubuntu/install
kubectl --context default apply --server-side -k ./manifests/cert-manager
kubectl --context default rollout status deployment/cert-manager -n cert-manager --timeout=600s
kubectl --context default rollout status deployment/cert-manager-webhook -n cert-manager --timeout=600s
kubectl --context default rollout status deployment/cert-manager-cainjector -n cert-manager --timeout=600s
```

Istio 保留官方锁定版本的全部 base 补丁和 Loom 外层安全补丁，包含 `istio/base/upstream/`；其他组件共享 `manifests/upstream/`，不能只上传组件入口文件。嵌套的 Argo Git 目录引用已展开成本地 base，JobSet release 文件也走代理。已确认服务器能下载 cert-manager 文件，尚未逐项验证其他文件或容器镜像的可达性。依赖来源及布局见 [上游配置说明](manifests/upstream/README.md)。

已确认此 RKE2/Calico 集群使用 `/opt/cni/bin` 和 `/etc/cni/net.d/10-calico.conflist`，与当前 Istio CNI 清单的 hostPath 一致，不改成 RKE2 默认目录。新集群仍需单独确认。手动安装前先 `kubectl apply -f manifests/namespaces.yaml`，否则 Istio base 中落到 `kubeflow` 的策略会报 namespace 不存在。然后安装 Istio：先 `kubectl apply --server-side -k manifests/istio/crds`，等待 `virtualservices.networking.istio.io`、`authorizationpolicies.security.istio.io` CRD Established，再 `kubectl apply --server-side -k manifests/istio/base`；随后检查 `kube-system/istio-cni-node` DaemonSet 和 `istio-system` 的两个 Deployment。默认入口为 ClusterIP，不修改 ingress-nginx，也不启用 Notebook 浏览器网关。

本次服务器的 Istio CNI 曾因 `couldn't initialize inotify: too many open files` 退出，提高 `fs.inotify.max_user_instances` 并重启 DaemonSet 后已恢复。这不是镜像拉取问题，也不应据此更改 CNI 路径。脚本不自动修改节点 sysctl；若当时只执行了 `sysctl -w`，可在该节点用 `sysctl fs.inotify.max_user_instances | tee /etc/sysctl.d/99-loom-inotify.conf` 保存已确认的值，避免重启后丢失。其他节点需逐台检查，不盲目覆盖系统参数。

代理是第三方传输服务，不改变上游版本，也不是容器镜像加速器。不得向代理发送 kubeconfig、Token、Secret 或私有仓库凭据；高信任生产环境应校验清单来源并将依赖落到受控本地目录或内部仓库。

### 总脚本入口

仅面向 Linux 部署，总脚本准备 Bash 和匹配集群的 kubectl，统一使用内置 Kustomize 的 `kubectl apply --server-side -k`，不再要求独立 Kustomize 或 Git。当前服务器的 kubectl v1.35.9+rke2r1 内置 Kustomize v5.7.1，用户已用它安装 cert-manager 和 Istio；其他组件仍需实际安装验证。脚本不会安装工具、修改节点或覆盖 kubeconfig。

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
| INSTALL_PROFILES | true | 安装 Profile Controller；逐组件安装时可设 false |
| INSTALL_NOTEBOOK | true | 安装 Notebook Controller；逐组件安装时可先设 false |
| INSTALL_PIPELINE | false | 安装 KFP 及 Argo、MySQL、MLMD、MinIO |
| INSTALL_KSERVE | false | 安装 Standard 模式 KServe，不装 Knative |
| INSTALL_TRAINER | false | 安装 Trainer v2.3.0、JobSet v0.12.0 和官方训练运行时 |
| INSTALL_PLATFORM | false | 配置准备后，发布 Loom MySQL/Redis、web/API 和后台角色 |
| PLATFORM_OVERLAY | core | `core`、`pipeline`、`kserve` 或 `full` |
| INSTALL_NOTEBOOK_GATEWAY | false | 应用可选 Notebook HTTPS 与 Loom ext_authz 配置 |
| ENABLE_POD_EXEC | false | 授予 API 在线终端权限 |
| ENABLE_SPACE_DELETE | false | 授予 worker 安全删除空间所需的全 API list 权限 |
| WAIT_TIMEOUT | 600s | 每条就绪等待的超时 |

这些开关只控制本次是否 apply，**设为 false 不会卸载已有组件或撤销权限**。

### 毫秒镜像与来源核验

根据用户分两次贴出的实际拉取结果，19 个安装期镜像最初映射到毫秒镜像；mc 后续按用户指定改为华为云地址，tag 保持不变，集中维护在 [registry/kustomization.yaml](manifests/registry/kustomization.yaml)。Profile、Notebook、KFP、MinIO、KServe 和 Trainer 的安装入口共用这一份映射。mc 初始化 Job 和 Profile hook 的直接引用也同步更新。原始上游配置保留来源地址，Loom 外层负责映射。

成功项按原路径分别使用 `ghcr.1ms.run`、`gcr.1ms.run`、`quay.1ms.run` 和 `k8s.1ms.run`，版本 tag 不变。ML Metadata 使用实测成功的 `gcr.1ms.run/tfx-oss-public/ml_metadata_store_server:1.14.0`，保留原始下划线名称。

原先失败的 3 个条目，已按后续成功结果替换：

| 镜像 | 当前完整镜像引用 |
|---|---|
| Metacontroller | ghcr.1ms.run/metacontroller/metacontroller:v4.17.6 |
| MinIO | ghcr.1ms.run/netvark/minio:RELEASE.2025-09-07T16-13-09Z |
| mc | swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/minio/mc:RELEASE.2025-08-13T08-35-41Z |

**MinIO 的 netvark 是第三方来源，不是原 minio 组织的同路径镜像。** mc 当前使用华为云 `ddn-k8s/docker.io/minio/mc` 镜像代理，版本为 `RELEASE.2025-08-13T08-35-41Z`，不是误贴的 `Z1Z`，尚未验证该地址完整拉取或内容等价性。旧毫秒 mc 地址曾在服务器下载镜像层时 TLS 超时，本次不再使用。用户本地拉取结果不能代替服务器验证，同名 tag 也不能证明内容一致。Metacontroller 没有在未核验的情况下复用上游 digest，生产应先核验来源，再将实际镜像摘要锁定到受控仓库。本次仅替换 mc 地址，不升级版本。

初始化 Job 仍名为 `loom-minio-bootstrap-v4`；集群已有旧 Job 时，因 Pod 模板不可变，必须在确认旧初始化任务已停止后删除并重建该 Job，不能只 apply 新镜像。删除该 Job 不删除 MinIO StatefulSet 或 PVC；本次未执行任何集群操作。

KFP 后续运行任务所用的 driver/launcher 已改为用户上传的阿里云一级仓库，版本保持 `2.18.0`：

```text
crpi-mctg1j954m4bry6c.cn-qingdao.personal.cr.aliyuncs.com/tiger-dev/kfp-driver:2.18.0
crpi-mctg1j954m4bry6c.cn-qingdao.personal.cr.aliyuncs.com/tiger-dev/kfp-launcher:2.18.0
```

这两个引用通过 [pipeline/base/kustomization.yaml](manifests/pipeline/base/kustomization.yaml) 补丁设置 API Server 的 `V2_DRIVER_IMAGE`、`V2_LAUNCHER_IMAGE`，不是普通容器 `image` 字段。用户日志显示已开始下载层，尚未展示完整拉取成功结果；未确认服务器 containerd 可达。更新安装配置只影响后续按新配置生成的任务，不重写已有 Workflow/Pod 的镜像。

Argo executor 也使用用户上传的阿里云一级仓库；BusyBox 与缓存 Alpine 暂时保持原配置：

```text
ghcr.1ms.run/containerd/busybox:1.28
crpi-mctg1j954m4bry6c.cn-qingdao.personal.cr.aliyuncs.com/tiger-dev/argoexec:v4.1.6
docker.1ms.run/library/alpine:3.23.7
```

BusyBox 位于 Istio `grpc-simple` 注入模板的文本内部，普通镜像映射不会修改这段文本，因此将同一锁定 commit 的完整 `istio/base/upstream/install.yaml` 保存在本地，仅替换模板内的 BusyBox 地址，其他原始资源和外层安全补丁不变。此模板是可选路径，不改变默认 sidecar 模板或 Istio 控制面/CNI 镜像。Argo executor 通过外层补丁修改 workflow-controller 的 `--executor-image` 参数；Alpine 通过 `pipeline/base/pipeline-config.yaml` 修改 `cacheImage`，不是替换安装期的 `alpine:3.23`。Alpine 未核验新仓库摘要，按用户指定使用 tag，不沿用原 `mirror.gcr.io` digest。

上传更新后的安装目录再 apply 对应组件才会影响集群；本轮未执行安装或重启。用户输出显示这三个镜像已开始拉取，不等于完整下载和集群运行均已验证。已有 Workflow/Pod 不会被自动重写。Pipeline 已运行时，ConfigMap 中的缓存镜像会作为 cache-server 环境变量在启动时读取，更新 ConfigMap 后需在合适窗口手动重启 `kubeflow/cache-server` Deployment 才能更新该进程的值。

不更改已装好的 cert-manager 或 Istio 控制面/CNI 镜像，也不批量迁移未提供的 Docker Hub 镜像。已上传的 Notebook、Trainer、Triton 和 llm-d 运行时按 `prepull/workload-images.txt` 使用阿里云地址；当前仍不是全离线运行配置。

当前这批公开镜像直接拉取，不需要阿里云登录、服务器 `docker pull`、改回原标签或 `ctr import`。所有组件均已移除对 `aliyun-registry` 的强制引用，脚本不再读取 Docker 登录配置或创建镜像仓库 Secret；旧的 `IMAGE_PULL_SECRET_FILE` / `IMAGE_PULL_SECRET_NAME` 本地选项不再使用。旧集群中的仓库 Secret 不会被自动删除，若要停用凭据应由管理员另行处理。Pipeline 的 MySQL/MinIO 业务凭据仍必须配置，不能把取消仓库登录误认为取消数据库/S3 鉴权。

将更新后的整个 `install/` 上传后，当前先继续安装 Profile：

```bash
cd /home/ubuntu/install
# 首次创建即可；已有本地配置不要覆盖。
cp -n install.example.env install.local.env
chmod 600 install.local.env
bash install.sh default
```

此示例跳过已完成的 cert-manager/Istio apply（仍检查它们就绪），开启 Profile、暂不开 Notebook/KFP/KServe/Trainer，保留 Namespace、聚合角色和 Loom 机器权限的应用步骤；不读取 Docker 凭据。`install.local.env` 会在后续运行时自动加载；同名环境变量可以覆盖示例值。旧 `PROFILE_CONTROLLER_IMAGE` 会提示忽略，不再覆盖统一映射。

统一配置也覆盖 KFP 的签名密钥初始化 Job、MinIO bootstrap Job 和 KFP Profile hook 的 mc init container，不只替换常驻 Deployment。MinIO bootstrap 更新为 `loom-minio-bootstrap-v4`，避免修改旧版 Job 的不可变 Pod 模板；旧 Job 不自动删除。这只是初始化 Job 的配置版本，不是升级 MinIO/mc 的镜像 tag。

毫秒镜像的成功结果来自用户本地 Docker，不代表服务器 containerd 已验证可达，也不代表已经校验镜像供应链。生产建议转存受控仓库并核对 digest；本轮不重新拉取、运行或测试镜像。

已有临时补丁或其他安装器管理同一字段时，SSA 可能报所有权冲突。脚本不会自动 `--force-conflicts` 抢占，请确认差异及现有值后人工处理。安装超时不删除资源，修正镜像或凭据后使用相同本地配置重跑。

## 资源与版本

| 资源目录 | 内容 | 版本 |
|---|---|---|
| docker | 本地 Compose、源码映射开发、后端/前端生产镜像 | Loom 源码版本 |
| kubernetes/loom | Loom web/API/worker 控制面 Kustomize | Loom 源码版本 |
| manifests/cert-manager | 证书控制器 | 1.21.2 |
| manifests/istio | CRD、控制面、CNI、入口策略 | 1.31.3 |
| manifests/roles | Kubeflow 与 Istio 聚合角色 | 同锁定快照 |
| manifests/profiles | Profile Controller，无 Dashboard/KFAM | v2.0.1 |
| manifests/notebook | Notebook v1 控制器，无 Notebook Web App | v1.11.1 |
| manifests/pipeline | KFP 多租户后端及执行依赖 | KFP 2.18.0、Argo v4.1.6 |
| manifests/minio | 持久对象存储、桶与最小权限账号初始化 | MinIO RELEASE.2025-09-07T16-13-09Z；mc RELEASE.2025-08-13T08-35-41Z |
| manifests/kserve | CRD、控制器、运行时 | v0.21.0，Standard |
| manifests/trainer | TrainJob/Runtime CRD、控制器、JobSet、训练运行时 | Trainer v2.3.0、JobSet v0.12.0 |
| manifests/gateway | 可选 Notebook HTTPS 访问入口 | Loom 配置 |

本地保存 Kustomize 适配、参数、补丁、依赖目录和业务机器权限，官方资源 YAML 通过代理引用 community-distribution 固定 commit：

`96e316a80e257a9f645c7c1ee0e1a94b904dead7`

Argo 的上游引用固定在 v4.1.6，不跟随 master/latest。升级需要审阅并成套修改这些引用及适配，不只修改脚本里打印的版本。

脚本中的安装语句就是：

```bash
kubectl --context YOUR_KUBECTL_CONTEXT apply -f manifests/namespaces.yaml
# Profile 现使用公开毫秒镜像，不需要阿里云 Secret。
kubectl --context YOUR_KUBECTL_CONTEXT apply --server-side --field-manager=loom-installer -k manifests/profiles
kubectl --context YOUR_KUBECTL_CONTEXT wait --for=condition=Established crd/profiles.kubeflow.org --timeout=600s
```

使用 kubectl 内置 Kustomize，升级 kubectl 时应同时核对 `kubectl version` 显示的 Kustomize 版本。镜像映射作为共享 Kustomize Component 已直接接入各安装目录，上游清单保持不变。

安装顺序是 Namespace → cert-manager → Istio → 聚合角色 → Profile → Notebook → 可选 KFP/KServe/Trainer → Loom 机器权限。KFP 先装 Workflow、DecoratorController 等 CRD；KServe 先装 CRD、控制器、证书和 webhook，等就绪后再应用运行时 CR。Trainer 先装自身 CRD，再装控制器及 JobSet，等待 CRD、控制器和 webhook CA 就绪后应用训练运行时。

**当前不是全离线资源包**：Kustomize 会通过 `ghfast.top` 访问锁定的官方资源 YAML，集群也需能拉取对应镜像。当前安装依赖链不再直接 Git clone GitHub。离线环境仍需将资源和镜像归档，将引用改为审阅过的本地路径；不能只复制 install 目录就假设已离线打包。

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

KFP/MinIO 的 Secret 不采用上游空密码和 minio/minio123。先准备 secrets.local.yaml，脚本单独 apply；官方 Kustomize 中的同名 Secret 被排除，不会覆盖你填写的值。MySQL 使用 mysql-secret。MinIO root、KFP 后端只读、Loom 日志只读、Loom 产物只读为四个不同账号，空间工作负载另有独立账号，详见 [MinIO 配置](minio.md)。

**已有 MySQL/PVC 时必须沿用原密码**。apply Secret 不等于修改数据库内部密码；脚本不再帮你生成、比较或轮换凭据。妥善限制本地文件权限并备份，不开 bash -x，不公开失败 patch/Secret。旧安装器留下的 .state/.generated 不会被清理，若已有凭据应安全迁移，不能重新填另一套密码。

机器身份在 `kubernetes/loom/base/rbac.yaml` 和 `manifests/*-rbac.yaml`：

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

KFP 仍保留自己的执行数据库，不合并 Loom/KFP schema。安装适配排除官方 SeaweedFS，统一使用 MinIO；部署顺序为 Secret → MinIO/PVC → 桶和账号初始化 → KFP/Argo。Argo archiveLogs 默认开启并按执行 UUID 归档，Loom 的 argo 模式直接读取，不再要求额外 manifest。Loom API 的日志与产物只读账号填写在所选 overlay 的 `config/config.local.yaml`，与其余平台配置一起生成 ConfigMap；未填写并启用前相关读取功能保持关闭。

## Notebook 浏览器入口

默认不开放官方通配 HTTP Gateway，ingress 为 ClusterIP，匿名请求不放行。装好依赖不代表浏览器已经能打开 Notebook。

启用 INSTALL_NOTEBOOK_GATEWAY 前：

1. 部署 Loom API：namespace=loom-system、Pod label=`app: loom-api`、开启 sidecar，Service=loom-api:8080。
2. 完成 Loom 业务库迁移、管理员初始化、认证密钥等应用配置。
3. 配好平台 HTTPS origin、独立 Notebook origin，替换清单中的占位域名，在 istio-system 准备 loom-notebook-tls Secret。可以先安装基础依赖再准备证书。
4. 设置 Loom 最小可信网关网段，并核对 mTLS/NetworkPolicy 确实生效。

可选清单包含 HTTPS Gateway、HTTP ext_authz provider、仅覆盖 /notebook/* 的 CUSTOM 策略、精确的 Notebook session POST/OPTIONS 路由；不公开 /verify，也不把 token 放 URL。凭据、Host/authority、头覆盖、跨源 Cookie、CORS 和 WebSocket 行为仍需真实环境核验。

该清单还限制 app=loom-api 的 mTLS 与网络入口，**不是完整官网网关**：普通业务 API、前端静态资源、内部执行器监听需在应用部署阶段补齐成套策略，不可为了放行普通 API 全局公开 /verify。KServe 推理鉴权也不复用 Notebook Cookie。

`INSTALL_PLATFORM=false` 时不部署 Loom API/web 镜像，也不执行 Loom 业务库迁移；开启后由 `kubernetes/loom` 清单和显式迁移 Job 完成。脚本仍不安装 Dex、Dashboard、Notebook Web App、Hub UI 或 Knative。失败后保留已创建资源，修正原因后重跑；没有自动回滚、卸载、prune 或 PVC 删除。

## Trainer 后续支持预留

INSTALL_TRAINER 仅安装集群基础设施，不代表 Loom 已实现训练执行器。安装内容为：

- trainer.kubeflow.org/v1alpha1 下的 TrainJob、TrainingRuntime、ClusterTrainingRuntime CRD。
- kubeflow-system 中的 Trainer v2.3.0、JobSet v0.12.0 控制器、Webhook 和官方配套权限。证书由控制器原生管理，不额外创建 cert-manager Certificate。
- 官方训练运行时目录：PyTorch、DeepSpeed、JAX、XGBoost、MLX，以及 Torchtune 模板；不创建训练任务，不拉起 GPU 工作负载，不安装 GPU 驱动/设备插件。

使用的是新 Trainer API，不安装旧 Training Operator v1 的 PyTorchJob/TFJob。LeaderWorkerSet、Kueue/Volcano、数据缓存等扩展不在本次默认依赖范围内；需要相应能力时另做适配。

Loom 的用户和组权限不变；官方 Trainer 聚合角色供 Profile 的原生权限体系使用。没有提前给 loom-worker/API 授予 TrainJob 写权限或增加一个尚未实现的训练 worker。

新增的 kubeflow-system namespace 关闭 sidecar 注入，让控制器使用自身 TLS 机制，避免现有 mesh deny-all 阻断 webhook/status listener。这不是所有网络入口已隔离的保证；未来训练执行接入仍需核对网络策略、状态上报和分布式通信。已有共享 kubeflow-system/JobSet 的集群应先审阅 namespace 标签及现有控制器，不能重复安装另一份 JobSet，也不通过删除 Deployment 强行升级。

后续仍需实现 Loom 的 TrainJob 创建/启停/删除、状态同步及对象权限适配。此轮没有增加业务 API、表、前端或测试，没有运行脚本或验证真实训练。
