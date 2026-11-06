# Loom（织云）

基于 Kubeflow 的多租户 AI 开发平台。

## 结构

| 目录 | 说明 |
|---|---|
| `platform/` | 后端（Go） |
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
| `sync` | 同步 K8s 资源到数据库 |
| `cron` | 定时任务（回收、对账） |
