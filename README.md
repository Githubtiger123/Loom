# Loom（织云）

基于 Kubeflow 的多租户 AI 开发平台。

后端实现进度、初始化步骤与接口说明见 [platform/README.md](platform/README.md)。当前源码包含用户权限、空间/目录/配额生命周期、Notebook 执行与诊断，以及 Pipeline / Trainer / KServe 业务 API 和机器执行桥。Notebook 镜像配置已装配，Pipeline 已接入官方 KFP v2 的导入/提交/停止/状态同步主线。前端本轮不做；Trainer/KServe 实际适配器及真实环境集成仍未完成，不是可直接生产使用的完整平台。

认证主线已加入可选的本地密码登录、MySQL 持久化 Access/Refresh、可降级 Redis 缓存和 Notebook 网关校验；Notebook Cookie 复用 Access JWT，默认仍为外部身份模式。配置、初始化和网关接入要求见 [认证与 Notebook 访问](platform/docs/authentication.md)，未部署或验证实际网关策略。

## 结构

| 目录 | 说明 |
|---|---|
| `platform/` | 后端（Go + GORM + MySQL） |
| `web/` | 前端（React） |
| `install/` | 部署清单 |
| `images/` | 镜像相关 |

## 本地启动

```bash
# 后端
cd platform
go run ./cmd/platform -cmd=api

# 前端
cd web
npm install
npm run dev
```

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
