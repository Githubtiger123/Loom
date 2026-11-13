# Loom Kubernetes 控制面部署

Loom 自身统一部署到 `loom-system`。这个 namespace 只承载平台控制面：web、API、持久 worker、只读 sync、cron，以及可选的 Pipeline/KServe worker。上游 Profile、Notebook、KFP 继续在 `kubeflow`；每个业务空间仍由 Profile 创建独立 namespace，不复制 Loom 控制面。

四个官方 overlay 都同时部署 Loom 专用的 MySQL 8.4 和 Redis 7.4。两者使用 StatefulSet 和默认 StorageClass 动态 PVC，分别通过 `loom-mysql:3306`、`loom-redis:6379` 提供集群内服务；不复用 KFP 的 MySQL。RabbitMQ 当前没有业务消费者，保持不部署、`mq.enabled=false`。

## 1. 构建并推送镜像

在 `loom/` 目录执行，替换仓库和 tag：

```bash
docker build --target runtime -f install/docker/Dockerfile -t YOUR_REGISTRY/loom-platform:YOUR_TAG .
docker build --target runtime -f install/docker/dockerFrontend/Dockerfile -t YOUR_REGISTRY/loom-web:YOUR_TAG .
docker push YOUR_REGISTRY/loom-platform:YOUR_TAG
docker push YOUR_REGISTRY/loom-web:YOUR_TAG
```

默认集群镜像使用 Kubeflow 可信身份头模式。若要使用 Loom 本地密码认证，应在构建阶段明确选择 local，并同时配置 HTTPS、后端 public origin 和签名密钥：

```bash
docker build --target runtime -f install/docker/dockerFrontend/Dockerfile \
  --build-arg VITE_AUTH_MODE=local -t YOUR_REGISTRY/loom-web:YOUR_TAG .
```

然后修改一个且仅一个 overlay 中的 `images`：

| overlay | 常驻角色 |
|---|---|
| `core` | API、web、worker、sync、cron |
| `pipeline` | core + pipeline-worker |
| `kserve` | core + kserve-worker |
| `full` | core + pipeline-worker + kserve-worker |

Pipeline/KServe overlay 只启动 Loom 适配器，不代替 KFP/KServe 本身的安装。

## 2. 配置

与 Cube 一样，Kustomize 使用 `configMapGenerator` 将完整配置文件生成 ConfigMap：

```text
base/config/config.yaml               公共完整配置
base/config/images.yaml               Notebook 镜像目录
overlays/<name>/config/config.local.example.yaml  可提交的环境模板
overlays/<name>/config/config.local.yaml          本地环境覆盖与凭据（Git 忽略）
components/middleware/config/middleware.env.example  可提交的中间件模板
components/middleware/config/middleware.env          MySQL/Redis 初始化凭据（Git 忽略）
```

选择一个 overlay，先复制两份模板，再填写真实配置：

```bash
cp overlays/<name>/config/config.local.example.yaml overlays/<name>/config/config.local.yaml
cp components/middleware/config/middleware.env.example components/middleware/config/middleware.env
```

真实的 `config.local.yaml` 和 `middleware.env` 都被 Git 和 Docker 构建上下文忽略。`config.local.yaml` 写相对公共基线需要覆盖的环境配置，包括数据库 DSN、Redis 密码、认证签名密钥、对象存储凭据和该 overlay 的功能开关；`middleware.env` 为 MySQL/Redis 容器提供初始化账号。两处的 `REPLACE_ME_DB_PASSWORD` 必须替换为同一个值，两处的 `REPLACE_ME_REDIS_PASSWORD` 也必须一致。

两份私有配置都会由 Kustomize 生成 ConfigMap，不单独生成 Secret。这与 Cube 的部署方式一致，但意味着有 ConfigMap 读取权限的人可以看到这些凭据；不要公开本地文件或 Kustomize 渲染结果。MySQL 的 `MYSQL_ROOT_PASSWORD`、`MYSQL_USER`、`MYSQL_PASSWORD` 只在空 PVC 首次初始化时生效。已有 PVC 必须沿用原密码；修改 ConfigMap 不会自动修改数据库内部账号。

公共配置和环境配置会分别挂载为：

```text
/app/configs/config.yaml
/app/configs/config.local.yaml
/app/configs/images.yaml
```

Loom 启动时先读 `config.yaml`，再合并 `config.local.yaml`。ConfigMap 名称带内容哈希，任一配置文件变化都会修改 Pod 模板并触发 rollout。生产数据库和 Redis 若在集群内自建，放在 `loom-system`，不要放进任一用户 Profile namespace。

MySQL 默认申请 `10Gi`、Redis 默认申请 `2Gi` 的 `ReadWriteOnce` PVC。部署前确认集群存在默认 StorageClass；没有默认 StorageClass 时，应在两个 StatefulSet 的 `volumeClaimTemplates` 中填写实际 `storageClassName`。删除 StatefulSet 不会自动删除数据 PVC，不要为了重装直接删除 PVC。

## 3. 发布

推荐通过总安装脚本发布，它会先检查所选 overlay 配置中没有 `REPLACE_ME`，再显式创建一次性迁移 Job并等待所有 Deployment 就绪：

```bash
INSTALL_PLATFORM=true PLATFORM_OVERLAY=core bash install.sh YOUR_CONTEXT
```

脚本会在创建数据库迁移 Job 前等待 `loom-mysql` 和 `loom-redis` StatefulSet 就绪。直接 `kubectl apply -k` 时也应先确认两者 Ready，再从暂停的 `loom-db-migrate` CronJob 显式创建迁移 Job。

安装脚本不会自动创建首个管理员或设置密码。首次部署仍按 `platform/README.md` 显式执行 bootstrap/set-password。`loom-db-migrate` 是永久暂停的 CronJob，只作为迁移 Job 模板，不会定时修改数据库。

完成 HTTPS 和认证入口配置后，外部流量只需指向 `loom-web` Service；Nginx 会同源代理 `/api/` 到 `loom-api`。临时检查静态页面可以使用：

```bash
kubectl --context YOUR_CONTEXT -n loom-system port-forward svc/loom-web 8080:80
```

本地密码登录要求 HTTPS 和完全匹配的 `auth.public_origin`，普通 HTTP port-forward 只适合健康检查和静态页面核对。

可信身份头模式下，外部网关必须先删除客户端提交的 `kubeflow-userid`/`oidc-sub`，再注入已经验证的身份；不要把 `loom-web` 或 `loom-api` 直接暴露为可伪造身份头的公网入口。正式 Istio 路由也可以将 `/api/` 直接指向 `loom-api`、其余路径指向 `loom-web`，由同一个可信入口完成身份注入。

## GitOps

直接 `kubectl apply -k overlays/<name>` 会创建控制面和暂停的迁移模板，但不会自动执行迁移。GitOps 环境应在同步 Deployment 前单独运行同一镜像的 `-cmd=migrate` Job，并在它成功后继续 rollout；不要把迁移放进每个 Pod 的 initContainer。

## 官方 Pipeline Runtime（可选）

这是 Loom 内嵌官方 argocompiler/driver/launcher 的原生主线，不要求另装 KFP API/MLMD 或第二个数据库；现有外部 KFP 模式可继续保留。当前交付源码，未构建/部署/验收，不应直接替换仍管理旧 v3 运行的部署。完整状态见 [集成文档 §16](../../../platform/docs/pipeline-official-engine-integration.md#16-2026-11-13-当前源码状态与发布边界)。

仅在准备好下列条件后，将 `components/pipeline-runtime` 显式加入所选 overlay 的 `components`；已有 pipeline/full 角色提供 pipeline-worker，但不会因加入此组件自动开启执行：

1. 使用 `install/docker/Dockerfile.kfp-runtime` 的 `driver`、`launcher` target 或 Actions 输出，准备与 API/worker 同一 engine release 的镜像；在配置中填入真实 `@sha256` digest。不能用未接 Loom 客户端的原版官方二进制代替。
2. 备份并显式执行完整 schema 迁移（包括 000028/000029），再升级应用；启动只检查，不自动迁移。本轮未执行迁移。
3. 在 `loom-system` 准备 Secret `loom-pipeline-runtime-tls`（`tls.crt/tls.key`）。证书 SAN 包含 `loom-pipeline-runtime.loom-system.svc` 或实际配置的内部 DNS。不要关闭证书验证；私钥不复制进业务空间。
4. 在每个已核验业务 namespace 配置 `loom-pipeline-runtime-ca` ConfigMap 的 `ca.crt`、`loom-pipeline-task` SA 及最小 Role/Binding，参考 `examples/native-pipeline-space.yaml`；配置各空间 `loom-native-artifacts` Secret 的 `accessKey/secretKey`。组件 SA 只读这一 Secret，不能访问数据库或任意 namespace 的凭据。
5. 仅对这些空间添加 `loom.ml/pipeline-runtime-access=true`；可选 NetworkPolicy 只放行这些 namespace 的 Argo Workflow Pod 到独立 8444 端口。TokenReview RBAC 只绑定 API，不绑定用户组件或 worker；标签不替代逐请求身份校验。需确保 CNI 实际支持 NetworkPolicy，并按集群策略配置到 Kubernetes API/存储的出站访问。
6. 合并 `examples/official-pipeline-runtime.config.yaml` 到私有配置，配置 Pod 可达的 HTTPS origin、TLS 文件、存储地址/桶/根路径和镜像 digest。该文件不是 Kubernetes 资源。`artifact_store` 的固定 endpoint/bucket 必须与组件写策略匹配，prefix 覆盖 `loom-official`。
7. 准备独立的存储权限：后端 `artifact_store.access_key/secret_key` 只读；各空间组件 Secret 仅写对应 group/namespace UID 前缀并允许读取运行所需输入；模型登记使用 `model_store` 目标写账号。缓存开启额外配置 `artifact_store.cache_copy_access_key/cache_copy_secret_key`（可选 session token），只允许源 Get 与新 attempt 的 `__cache` 目标 Put，不能扩大原只读账号。用 Secret 注入对应 `LOOM_ARTIFACT_STORE_CACHE_COPY_*` 环境变量或受保护私有配置，不提交真实凭据。
8. 初始设置 `runtime_enabled=true`、`native_observer_enabled=true`、`native_enabled=true`，缓存先保持 false；确认执行验收后才另行考虑开启缓存。开关默认均为 false，启动配置检查和兼容性 `runnable` 不代表服务可达或真实执行通过。API、pipeline-worker 必须使用一致配置及 artifact reader。

缓存开启时所有组件镜像需固定 digest。缓存条目只在整个 Workflow 成功后可见，7 天过期，过期索引/未引用复制对象暂不自动清理；目录下载、日志及模型复制也有明确规模上限。存储需自行落实输出稳定、保留和写隔离策略，ETag/清单复核不等于不可变对象保证。

正式切换前需要核验 Argo 默认值/准入对冻结 Workflow spec 的影响，以及 Python/参数/产物/条件循环/重试/停止/缓存/离线日志和模型登记闭环。本轮没有调用 Kubernetes、构建镜像或执行这些步骤。
