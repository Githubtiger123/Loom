# 上游安装配置与下载代理

这里保存 Loom 安装所需的上游 Kustomize 目录结构、参数、补丁及转换配置。资源 YAML 通过 `https://ghfast.top/` 读取锁定版本，不再通过 Git clone 获取远程目录。

版本来源：

- kubeflow/community-distribution：`96e316a80e257a9f645c7c1ee0e1a94b904dead7`。本地来源仓库 HEAD 已与该 commit 核对一致，许可证保存在 `community-distribution/LICENSE`。
- argoproj/argo-workflows：`v4.1.6`。保留 minimal CRD、workflow-controller 和 cluster workflow-controller-rbac 的完整资源列表；原始目录清单通过代理读取后写成本地配置。
- kubernetes-sigs/jobset：`v0.12.0` release `manifests.yaml`，由 Trainer 的本地配置通过代理引用。

此目录保留上游版本和来源镜像。上游相对目录仍保留层级，跨仓库依赖改为指向本目录下的本地 base；独立文件资源统一改为代理 URL。Loom 的控制面镜像仓库适配在外层 `manifests/registry/kustomization.yaml` 统一维护，无需修改这里的原始来源地址。KFP driver/launcher 环境变量、Argo executor 启动参数和缓存 ConfigMap 中的镜像由外层 `manifests/pipeline/base` 补丁维护。独立的 `manifests/istio/base/upstream/install.yaml` 从同一锁定 commit 保存本地副本，只改嵌入式 grpc-simple 模板的 BusyBox 地址，避免普通 images 转换无法深入模板文本的问题。没有引入新的安装程序或预下载脚本。

此目录是部署所需依赖，上传到 Linux 时需与整个 `install/manifests` 一起上传。不能只上传顶层 `kustomization.yaml` 或只上传某个组件目录。不要使用 `kubectl apply -R -f manifests` 安装此树，必须逐个组件执行 `kubectl apply --server-side -k manifests/<组件入口>`，让 Loom 的覆盖、删除和安全补丁生效。

这是本地配置与在线代理资源组合，不是全离线包。外层 `registry` 维护 19 个控制面镜像映射：mc 后续按用户指定改为华为云 `swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/minio/mc:RELEASE.2025-08-13T08-35-41Z`，其余保留对应 `1ms.run` 地址。MinIO 仍为 netvark 第三方来源；mc 的新地址尚未验证拉取，均未核验内容与官方等价性。当前不再引用私有仓库凭据组件，不要求阿里云登录。Docker Hub 和业务运行时镜像未统一迁移，不能把 GitHub 文件下载代理前缀套到镜像地址上。

升级时应从新版本重新审阅配置、补丁和资源列表，不能只替换 URL 里的版本号。按项目要求，本轮仅整理源码和配置，未运行 Kustomize 渲染、测试、编译、安装或集群操作。
