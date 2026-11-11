# Loom（织云）

基于 Kubeflow 的多租户 AI 开发平台。

后端实现进度、初始化步骤与接口说明见 [platform/README.md](platform/README.md)。当前源码包含用户权限、空间/目录/配额生命周期、Notebook 执行与诊断，以及 Pipeline / Trainer / KServe 业务 API 和机器执行桥。Notebook 镜像配置已装配，Pipeline 已接入官方 KFP v2 主线，KServe 已接入 InferenceService 执行与状态同步；Trainer 实际适配器仍未完成。

前端已从零重建为 React + TypeScript + Atomus 基础工程，包含认证、空间选择、Notebook 基础生命周期以及 Pipeline、模型、推理服务的基础页面，具体范围与环境说明见 [web/README.md](web/README.md)。尚未测试、编译、运行或验证真实集成，不是可直接生产使用的完整平台。

认证主线已加入可选的本地密码登录、MySQL 持久化 Access/Refresh、可降级 Redis 缓存和 Notebook 网关校验；Notebook Cookie 复用 Access JWT，默认仍为外部身份模式。配置、初始化和网关接入要求见 [认证与 Notebook 访问](platform/docs/authentication.md)，未部署或验证实际网关策略。

## 结构

| 目录 | 说明 |
|---|---|
| `platform/` | 后端（Go + GORM + MySQL） |
| `web/` | 前端（React + TypeScript + Atomus） |
| `install/docker/` | Docker 镜像、本地 Compose 与源码映射开发 |
| `install/kubernetes/` | Loom 控制面 Kustomize 部署 |
| `install/manifests/` | 锁定版本的 Kubeflow 集群依赖 |
| `images/` | 镜像相关 |

## 本地启动

首次连接真实集群时，先按 [集群依赖安装说明](install/README.md) 准备 Profile/Notebook 控制器等资源。资源适配在 `install/manifests/`，直接运行 `bash install/install.sh <kubectl-context>`；脚本按顺序 apply/wait，Pipeline、KServe、Trainer 可按需开启，不依赖 Python。开启 `INSTALL_PLATFORM` 后同一入口也会部署 Loom 控制面并显式执行数据库迁移。Trainer 安装包括 JobSet 与官方训练运行时，不代表其 Loom 业务执行器已完成。

开启 Pipeline 安装时，统一部署 [MinIO](install/minio.md)，不再部署上游 SeaweedFS；KFP/Argo 使用同一对象存储，Loom 离线日志可按执行 UUID 直接读取 Argo 归档，无需日志表或额外 manifest。API 的专用只读凭据与其余环境配置一起填写在 Kubernetes overlay 的 `config.local.yaml`，未进行真实集群验证。

本地开发统一通过 Docker Compose 映射源码，不要求在宿主机分别安装 Go/Node 依赖：

```bash
cd install/docker
cp .env.example .env
kubectl config view --raw --flatten > kubeconfig
docker compose --profile cluster up --build
```

Go API/worker 使用 Air 监听源码并自动重编译，前端使用 Vite 热更新；迁移由独立一次性容器完成。详细的认证初始化、可选 Pipeline/KServe profile 和生产镜像命令见 [Docker 开发说明](install/docker/README.md)。本次未实际构建或运行。

## 镜像与集群部署

容器开发和镜像构建统一放在 [install/docker](install/docker/README.md)：Compose 映射 `platform/`、`web/` 源码并运行前端、API 和各后台角色，生产镜像也从这里构建。集群控制面使用 [install/kubernetes/loom](install/kubernetes/loom/README.md) 下的 Kustomize 清单，统一安装到 `loom-system`，包含 API、web、worker、sync、cron，以及按 overlay 开启的 Pipeline/KServe worker。

## 运行角色

后端同一份代码，通过 `-cmd` 区分角色：

| 角色 | 用途 |
|---|---|
| `api` | HTTP 接口 |
| `worker` | 执行持久任务：Profile、成员访问、Notebook、空间和工作目录生命周期 |
| `sync` | 已管理空间及 Notebook 的只读状态观测（当前 GET 轮询） |
| `cron` | 授权到期处理、访问对账任务调度 |
| `migrate` | 显式执行数据库版本迁移 |
| `bootstrap` | 显式初始化首个可信管理员 |
| `set-password` | 运维显式设置既有本地用户密码，隐藏输入，不自动开户 |
