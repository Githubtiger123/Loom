# GitHub Actions 镜像发布

工作流为 [publish-images.yml](../../.github/workflows/publish-images.yml)。只构建并推送镜像，不运行测试、不执行数据库迁移、不连接 Kubernetes、不部署服务。这里没有额外的 lint／测试作业；生产 Dockerfile 内仍会正常编译 Go、打包前端（现有 `npm run build` 包含 TypeScript 检查），构建失败不会发布该镜像。

## 1. 配置仓库 Secrets

进入 GitHub 仓库 **Settings → Secrets and variables → Actions → Secrets → New repository secret**，添加：

| Secret               | 内容                                                          | 示例                                               |
| -------------------- | ------------------------------------------------------------- | -------------------------------------------------- |
| `REGISTRY_HOST`      | 仓库主机名，可带端口；不要填写 `https://`、命名空间或末尾 `/` | `crpi-example.cn-qingdao.personal.cr.aliyuncs.com` |
| `REGISTRY_NAMESPACE` | 单层命名空间，小写字母／数字／点／下划线／中划线              | `tiger-dev`                                        |
| `REGISTRY_USERNAME`  | 有推送权限的仓库用户名                                        | 你的仓库用户名                                     |
| `REGISTRY_PASSWORD`  | 仓库登录密码或推送 Token                                      | 不要提交到代码                                     |

需要预先创建命名空间；若镜像仓库要求手动创建 repository，请创建下面四个镜像仓库，并确保账号拥有 push 权限。仓库和网络策略必须允许 GitHub 托管 runner 通过 HTTPS 访问。工作流不关闭 TLS 校验，也不使用 Docker socket／宿主机登录状态。

## 2. 镜像名称和标签

四个镜像使用现有两个 Dockerfile 的不同 target，不复制 Dockerfile：

| 用途                          | 镜像名称            | Dockerfile target |
| ----------------------------- | ------------------- | ----------------- |
| Go 开发环境，所有后端角色共用 | `loom-platform-dev` | `development`     |
| Node/Vite 开发环境            | `loom-web-dev`      | `development`     |
| 后端生产运行镜像              | `loom-platform`     | `runtime`         |
| Nginx 前端生产镜像            | `loom-web`          | `runtime`         |

完整地址为 `<REGISTRY_HOST>/<REGISTRY_NAMESPACE>/<镜像名称>:<时间标签>`。标签按 **北京时间 Asia/Shanghai** 生成，格式 `YYMMDDhhmmss`：例如 `261112131620` 表示 2026-11-12 13:16:20。同一轮构建的镜像共用一个标签，不生成浮动 `latest` 标签。标签和各镜像成功推送的 digest 可在 Actions Summary 查看；仓库地址／命名空间按 GitHub Secret 规则隐藏。

重跑失败的 publish job 会沿用这一轮已有标签；重新运行整个工作流会重新生成时间标签。四个镜像分别推送，不是原子发布；某个 job 失败时，其他成功推送的镜像不会回滚，部署前应确认所需镜像均成功。

当前仅构建 `linux/amd64`，不包含 ARM64。为兼容个人镜像仓库，关闭额外的 provenance/SBOM attestation；保留源码 commit label 和每个镜像的独立 GitHub Actions 构建缓存。

## 3. 触发方式

- 推送到 `main`，且修改了后端、前端、Docker 配置或本工作流时，自动构建四个镜像。
- 手动进入 **Actions → Build and publish images → Run workflow**，选择 `all`、`development` 或 `production`，分别发布全部、仅开发版或仅生产版。
- Pull Request 不会触发携带仓库密码的发布，不使用 `pull_request_target`。同一分支的发布串行运行，不自动取消正在推送的镜像。

## 4. 可选前端构建参数

在同一设置页的 **Variables**（不是 Secrets）中可设置：

| Variable               | 默认值           | 用途                                              |
| ---------------------- | ---------------- | ------------------------------------------------- |
| `VITE_AUTH_MODE`       | `trusted-header` | 生产前端认证模式；本地密码登录填 `local`          |
| `VITE_NOTEBOOK_ORIGIN` | 空               | 需要 Notebook 独立来源访问时设置可信 HTTPS origin |

这些是会进入前端构建产物的公开配置，不要放密码或 Token。开发镜像本身不预打包前端，Compose 仍用 Vite 和挂载源码，且现有 Compose 的开发认证模式为 `local`。

## 5. 在 Compose 使用开发镜像

发布成功后，修改 `install/docker/.env`（替换以下占位值）：

```dotenv
LOOM_PLATFORM_DEV_IMAGE=YOUR_REGISTRY/YOUR_NAMESPACE/loom-platform-dev:261112131620
LOOM_WEB_DEV_IMAGE=YOUR_REGISTRY/YOUR_NAMESPACE/loom-web-dev:261112131620
```

在运行 Compose 的宿主机上登录相同仓库，然后拉取并启动：

```bash
docker login YOUR_REGISTRY
cd install/docker
docker compose --profile cluster pull
docker compose --profile cluster up -d
```

如需 Pipeline/KServe worker，在 `pull` 和 `up` 两条命令中都加上原先启用的对应 profile。前端依赖变化时，按 [Docker 开发说明](README.md#修改代码和更新镜像) 显式刷新 `web-node-modules` 卷；新镜像不会覆盖已有依赖卷。生产镜像供 [Kubernetes 控制面部署](../kubernetes/loom/README.md) 使用，不要填到开发 Compose 中。

本文和工作流仅完成源码配置及静态检查，没有实际触发 Actions、构建镜像、推送仓库或完成部署验证。
