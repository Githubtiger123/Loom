# 镜像预拉取清单

这些文件按当前锁定版本静态整理。`workload-images.txt` 中用户已经上传的 Notebook、KFP 执行、KServe 和 Trainer 镜像使用阿里云 `tiger-dev` 一级仓库名；尚未上传的镜像保留上游地址。没有运行 Kustomize、连接集群或实际拉取镜像。

- `install-images.txt`：开启全部安装开关时，控制面和初始化任务会使用的镜像。建议先拉这一份。
- `workload-images.txt`：创建 Notebook、Pipeline、KServe 或 Trainer 工作负载后才可能使用的默认镜像。包含多个 GPU/LLM 镜像，体积很大，应按实际功能删减。
- `docker-images.txt`：本地 Compose 和 Docker 构建所需镜像，不需要预拉到 Kubernetes 节点。

Loom 自己的两个镜像没有列入，因为 overlay 仍是 `YOUR_REGISTRY/loom-platform:YOUR_TAG` 和 `YOUR_REGISTRY/loom-web:YOUR_TAG`；构建并填写实际地址后再拉取。

本目录是预拉取参考。阿里云地址已同步到 Notebook 目录、KFP 执行镜像补丁、KServe Runtime 映射和 Trainer Runtime 映射；没有改动已经安装完成的控制面镜像映射。

在每个可能调度工作负载的 Kubernetes 节点执行预拉取。通用 `crictl` 示例：

```bash
grep -vE '^\s*(#|$)' install-images.txt | while IFS= read -r image; do
  sudo crictl pull "$image"
done
```

确实需要所有默认工作负载镜像时再执行：

```bash
grep -vE '^\s*(#|$)' workload-images.txt | while IFS= read -r image; do
  sudo crictl pull "$image"
done
```

RKE2 若没有全局 `crictl`，使用 `/var/lib/rancher/rke2/bin/crictl`，并按节点实际安装指定其 CRI 配置。镜像缓存是节点本地的，多节点集群必须逐节点处理。`imagePullPolicy: Always` 的工作负载即使已有缓存仍会访问仓库检查 manifest；预拉取主要减少层下载时间，不能替代仓库连通性和鉴权。

KServe 使用 Standard 模式，不包含 Knative/ModelMesh。当前外层清单删除了 `kserve-localmodelnode-agent`，因此没有把它列入；kernel-cache agent 默认没有可调度节点，但保留在按需清单中。KFP 已排除 SeaweedFS，因此也没有列 SeaweedFS 镜像。
