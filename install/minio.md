# KFP / Argo / Loom 统一使用 MinIO

本次交付安装清单和后端源码；未运行安装、Kustomize、kubectl、测试、编译或真实训练。镜像固定 tag，但没有在本环境验证镜像下载、架构与漏洞；生产上线前须审核并在受管仓库锁定 digest。

## 安装与配置位置

安装器仍是 Linux Bash，直接 apply/wait，无宿主机 Python 或 PowerShell 依赖：

```bash
cd loom/install
cp manifests/pipeline/secrets.example.yaml manifests/pipeline/secrets.local.yaml
# 编辑实际凭据，替换全部 REPLACE_ME。
INSTALL_PIPELINE=true bash install.sh YOUR_KUBECTL_CONTEXT
```

- `manifests/minio/minio.yaml`：单副本 StatefulSet、50Gi 默认 StorageClass PVC、ClusterIP S3 9000；不暴露控制台或外部入口，不配置匿名桶。
- `manifests/minio/bootstrap/`：mc Job 创建 mlpipeline 桶、KFP 后端、日志和可视化产物只读账号。不删对象、不覆盖桶策略、不主动配置过期删除。当前 Job 为 `loom-minio-bootstrap-v4`，使用用户实测可拉取的 netvark 毫秒镜像，不依赖阿里云拉取 Secret，避免覆盖旧版不可变 Job 模板；旧 Job 不自动删除。
- `manifests/pipeline/base/minio-kfp.yaml`：KFP API/UI 地址和全局 Argo 日志仓库。
- `manifests/pipeline/base/minio-profile-controller.yaml`、`minio-profile-sync.py`：替换 SeaweedFS 专用空间凭据配置，生成空间级 Secret、launcher providers、Argo 仓库和 MLMD 配置。
- `manifests/pipeline/base/kustomization.yaml`：排除上游 SeaweedFS 工作负载、PVC、Service 与策略；不使用 prune，也不删除集群中已有资源。
- `platform/configs/config.yaml`：产物根路径与 `log_archive.format: argo`、MinIO 地址、桶和前缀。

`minio-profile-sync.py` 是运行在官方 KFP profile-controller Pod 内的 Python 3 webhook，不是安装器。mc 通过固定镜像的 init container 提供；不要求用户手工执行 Python，不重新实现 KFP 调度。

MinIO/mc 地址与版本统一由 `manifests/registry/kustomization.yaml` 映射到 `ghcr.1ms.run/netvark/minio` 和 `ghcr.1ms.run/netvark/mc`；KFP hook 的 mc 补丁也使用同一目标地址。当前公开镜像不要求 Docker 登录或阿里云 imagePullSecret，不需要把镜像 tag 回 quay.io 或导入 containerd。netvark 为第三方来源，用户成功拉取不代表已验证镜像内容、官方等价性或真实运行兼容性，生产前需核验。MySQL/MinIO 的业务 Secret 仍须按下文配置。

## 账号边界

在 secrets.local.yaml 填写以下配置，四个账号不能相同，不使用 `kfp-` 保留前缀，账号使用 3–20 字符、密码 16–40 字符：

| Secret | 用途 |
|---|---|
| mysql-secret | KFP 执行数据库，不是 Loom 业务数据库 |
| minio-root | MinIO 服务、初始化 Job、受信任空间凭据 webhook；不下发给任务或 Loom API |
| mlpipeline-minio-artifact（kubeflow） | KFP 后端 Get/List 产物，只读账号 |
| minio-log-reader（kubeflow） | 只读 mlpipeline/logs/*，包含受前缀限制的 ListBucket |
| minio-artifact-reader（kubeflow） | 只读 mlpipeline/loom/pipelines/* 的精确对象，不允许列目录或写入 |

脚本将日志只读凭据复制为 loom-system 的 loom-log-archive，产物只读凭据复制为 loom-artifact-store，复制过程不打印凭据。API Deployment 分别合并 `examples/log-archive-api-pod-spec.yaml`、`examples/artifact-store-api-pod-spec.yaml`，Pod label 使用 `app: loom-api`，再启用相应读取功能。root 凭据不得代替只读账号。

每个开启 pipelines 标签的 namespace，由 webhook 通过 MinIO admin 管理独立账号及策略。账号名由 namespace 派生，密码绑定 namespace UID；同名空间重建会使旧密码失效。任务只能访问自己的 `private-artifacts/<namespace>/*`、`loom/pipelines/<namespace>/*`、`logs/default/<namespace>/*`。`kfp-launcher.providers` 明确配置 S3/minio endpoint 和本空间 Secret，不沿用官方代码里的 SeaweedFS 默认地址。

空间凭据的信任边界是整个 namespace，并非每个 Loom 用户/每次 Run；同空间有存储写权限的任务可能修改该空间的产物和日志，所以归档不构成不可篡改审计。空间删除后 MinIO 账号/策略及历史对象不自动删除；管理员应另做账号停用、审计和保留策略。重建 namespace 不代表清理旧数据。

## 自动归档与读取

Argo 的全局与空间级 `archiveLogs: true` 都指向 MinIO，keyFormat 是日志目录，Argo 在目录后追加 main.log：

```text
mlpipeline/logs/default/<namespace>/<KFP run UUID>/<Pod name>/main.log
```

`pipeline/runid` 标签提供 UUID，不依赖 Workflow 名称猜测。后端从受权业务 Run/attempt 得到 namespace 和 instanceId，再列举唯一执行前缀并读取文件；不要求 KFP Run/Pod 仍存在，不建日志数据库表，不需要额外 manifest 或采集器。接口与大小限制见 `platform/docs/log-archives.md`。

这里只配置自动归档 main 容器日志；节点异常、wait 失败、抢先删 Pod、存储不可达仍可能导致未归档。编辑/删除不等待上传成功。init、sidecar、重启前日志不自动覆盖。图片/HTML/指标产物仍由任务生成；[普通可视化后端](../platform/docs/pipeline-visualizations.md) 已补相应读取接口，前端和 TensorBoard 未实现。

## 必须保持一致的参数

- 桶：bootstrap policy、profile hook 的 BUCKET、全局 Argo、Loom 的 pipeline_root/log_archive.bucket。
- 日志：全局 Argo 的 logs/default、profile hook 的 LOG_ARCHIVE_CLUSTER、账号策略、Loom 的 log_archive.prefix 和 platform.cluster（默认 default）。
- 产物：Loom pipeline_root 默认 s3://mlpipeline/loom/pipelines，worker 再追加 namespace/UUID；修改根路径时必须调整空间策略。
- 地址：当前使用 minio.kubeflow.svc.cluster.local:9000 和 us-east-1。更改 namespace/domain/TLS 时须成套改 MinIO、KFP、profile hook/providers、Argo、Loom 与网络策略。

## 安全与重跑边界

MinIO 不注入 sidecar，S3 9000 通过 NetworkPolicy 仅向控制面、本平台 API 和启用 Pipeline 的空间开放；兼容无 sidecar 的 Argo Pod。流量目前是内网 HTTP，靠 S3 签名鉴权，不是链路加密。生产跨节点网络应另配 TLS；没有公开控制台、匿名访问或关闭验签。

默认是单机 MinIO，用于开发/集成，不是 HA、备份或容灾方案。PVC 不随脚本删除。Kubernetes 必须已有可用默认 StorageClass，50Gi 容量和权限需适配实际存储。

这是新集群方案，脚本检测到 SeaweedFS 会拒绝继续 KFP 安装。已有数据需人工迁移桶、namespace 凭据和历史路径，旧路径不自动识别。webhook 检测到不匹配的已有空间 Secret 会返回错误，且不会先修改 MinIO 用户。

bootstrap 是有版本的持久 Job：正常重跑保留 Complete 状态，不重复初始化；修改账号、policy、bootstrap 脚本后不能只重跑安装器并假定生效，必须安排凭据轮换/受控新 Job。root 兼作空间密码派生种子，禁止在线随意更改；应先停任务、迁移空间 Secret 并验证后再恢复。已有共享 MinIO/同名账号不能直接用此初始化步骤接管。
