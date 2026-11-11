# Loom Docker 开发与镜像构建

这个目录对应 Cube 的 `install/docker`：Compose 负责本地依赖和源码映射开发，两个 Dockerfile 负责生产镜像。`platform/`、`web/` 保持纯业务源码目录。

## 本地容器开发

准备一个可在容器内独立使用的 kubeconfig。证书应内嵌，API 地址也必须能从 Docker 网络访问：

```bash
cd install/docker
cp .env.example .env
kubectl config view --raw --flatten > kubeconfig
docker compose up --build
```

如果 kubeconfig 的 API Server 是 `127.0.0.1`/`localhost`，容器无法直接复用该地址；需改为容器可访问的集群地址或 Docker Desktop 的 `host.docker.internal`，并保持证书 SAN 匹配。

默认启动 MySQL、Redis、显式迁移容器、API、cron 和 Vite 前端。浏览器访问 `https://127.0.0.1:5173`；没有 `web/certs` 时 Vite 会退回 HTTP，此时本地密码认证会按安全策略拒绝工作。

后端和前端均映射宿主机源码：

- Go 角色由 Air 监听 `.go`/配置变化并重编译、重启。
- Vite 监听 `web/` 并热更新。
- MySQL、Redis 和可选 RabbitMQ 使用相对路径 bind mount，数据直接保存在本目录的 `data/mysql`、`data/redis`、`data/rabbitmq`，并由 Git 和 Docker 构建上下文忽略。
- Go module/build cache 和 `node_modules` 仍使用 Docker named volume，不写入源码目录。

启动需要真实 Kubernetes 的普通 worker/sync：

```bash
docker compose --profile cluster up --build
```

可选角色：

```bash
docker compose --profile cluster --profile pipeline up --build
docker compose --profile cluster --profile kserve up --build
```

Pipeline profile 还需要在 `.env` 配置 `KFP_TOKEN_PATH`，其内容必须是面向 KFP audience 的短期 projected token，例如在已部署 Loom RBAC 的开发集群中执行：

```bash
kubectl -n loom-system create token loom-pipeline \
  --audience=pipelines.kubeflow.org --duration=1h > kfp-token
```

不要使用用户 JWT或提交 Token 文件。RabbitMQ 当前不是业务队列，仅在排查其可选连接时用 `--profile mq` 启动。

首次创建管理员和本地密码保持显式操作，不放进 API 启动过程：

```bash
docker compose run --rm api go run ./cmd/platform -config=configs/config.yaml \
  -cmd=bootstrap -issuer=https://loom.local -subject=YOUR_SUBJECT -username=YOUR_USERNAME
docker compose run --rm api go run ./cmd/platform -config=configs/config.yaml \
  -cmd=set-password -user-id=YOUR_USER_ID
```

停止容器但保留数据库：

```bash
docker compose down
```

`docker compose down` 和 `docker compose down -v` 都不会删除 `data/` 下的 Windows 文件；`-v` 只会删除 Go/npm 缓存用的 named volume。只有明确不再需要本地数据时，才手动处理 `data/mysql`、`data/redis`、`data/rabbitmq`。

## 生产镜像

在仓库 `loom/` 根目录构建，确保构建上下文同时包含源码和 `install/docker`：

```bash
docker build --target runtime -f install/docker/Dockerfile \
  -t YOUR_REGISTRY/loom-platform:YOUR_TAG .

docker build --target runtime -f install/docker/dockerFrontend/Dockerfile \
  -t YOUR_REGISTRY/loom-web:YOUR_TAG .
```

集群默认使用可信身份头前端。使用 Loom 本地密码登录时增加：

```bash
--build-arg VITE_AUTH_MODE=local
```

推送镜像后，将同一地址和 tag 填入 `../kubernetes/loom/overlays/<name>/kustomization.yaml`。生产部署说明见 `../kubernetes/loom/README.md`。
