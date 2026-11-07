# Notebook 镜像配置

镜像目录独立维护在 `configs/images.yaml`，不新建镜像表。主配置 `configs/config.yaml` 只保留引用：

```yaml
images:
  path: "configs/images.yaml"
```

`LOOM_IMAGES_PATH` 可以覆盖路径。相对路径基于进程工作目录，不是主配置所在目录；从其他目录启动时应配置绝对路径。API 和 worker 启动时读取并校验，文件缺失、未知字段、重复 ID、非法地址或分类/类型不匹配均拒绝启动。不支持热更新，修改后需重启所有 API/worker 副本，保持使用同一份目录。sync、cron 及数据库运维命令不读取镜像目录文件。

## 配置条目

```yaml
images:
  - id: vscode-python
    category: vscode
    name: VS Code · Python
    serverType: group-one
    image: ghcr.io/kubeflow/kubeflow/notebook-servers/codeserver-python:v1.11.1
    enabled: true
```

| 字段 | 含义 |
| --- | --- |
| `id` | 稳定业务标识，请求的 `imageId`，不是数据库外键 |
| `category` | 展示分类：`jupyter`、`vscode`、`rstudio` |
| `name` | 展示名称，非空，最长 128 字 |
| `serverType` | CR 访问类型，三个分类分别对应 `jupyter`、`group-one`、`group-two` |
| `image` | 实际容器镜像引用，不接受 URL、凭据或空白；digest 支持 sha256 |
| `enabled` | 是否允许新建，未填写时默认 false |

预置 7 个条目：Jupyter SciPy、PyTorch CPU/CUDA、TensorFlow CPU/CUDA、VS Code Python、RStudio Tidyverse。地址与版本取自本地 Kubeflow Notebook 官方 `spawner_ui_config.yaml`，目前均为 `v1.11.1`，未访问仓库验证拉取。标签不是不可变 digest；生产建议使用经审核的 `@sha256:...` 引用，私仓账号通过集群 Secret 管理，不写在镜像地址里。

## 业务流转

1. 用户创建 Notebook 时提交 `imageId`，不提交实际地址、启动命令或任意 PodSpec。
2. API 从目录查找条目。未提供 `serverType` 时取条目类型；显式提供时必须匹配。
3. 经过现有权限、空间、额度和幂等检查后，禁用条目不允许新建。实例事务保存 `image_id`、实际 `image` 地址快照、`server_type` 和业务规格，生成待执行操作。
4. worker 使用数据库中已固定的地址和类型创建 CR，沿用现有类型 annotation、PVC 和生命周期逻辑。受理成功不代表镜像已拉取或环境已就绪。
5. 后续启停、更新和操作重试继续使用快照。修改目录地址、禁用或移除条目，不会隐式替换已有实例的地址，也不阻止它们启停/删除。已受理请求在保留原 ID/类型时可按原幂等键回放，禁用不影响回放。

对旧版尚未保存实际地址的实例，worker 首次解析要求目录条目存在、启用且类型一致，随后保存快照；不匹配时明确返回 `IMAGE_NOT_RESOLVED`。停止和删除不经过镜像解析。

发布后的 ID/类型应保持稳定，停用采用 `enabled: false`，不要移除后复用同一 ID 表示另一种环境。新版本优先新增 ID；移除条目会使基于旧 ID 的创建请求无法再次解析。当前提供只读镜像选项接口，不提供镜像增删改管理接口，未修改前端。

## 下拉列表查询接口

`GET /api/v1/groups/{gid}/notebook-images`

沿用业务认证入口，要求目标空间的 `notebook.create` 权限（平台管理员沿用现有授权规则）。空间不存在或已删除返回 404；目录未装配返回 503。读取配置快照，不访问 Kubernetes 或镜像仓库，不校验空间额度或证明当前可以创建成功。

可选参数 `category=jupyter|vscode|rstudio`，省略或空字符串返回全部已启用镜像；其他非空值返回 400。不分页，按 YAML 配置顺序返回。没有匹配条目或全部禁用时返回 `items: []`，不是 null。不能通过查询参数获取禁用条目或实际镜像地址。

例如 `GET /api/v1/groups/12/notebook-images?category=vscode`：

```json
{
  "data": {
    "items": [
      {
        "id": "vscode-python",
        "name": "VS Code · Python",
        "category": "vscode",
        "serverType": "group-one"
      }
    ]
  }
}
```

选项显示 `name`，选中值 `id` 作为创建请求的 `imageId` 提交；`serverType` 可以省略由后端从目录补齐，也可以按返回值提交。创建时仍会再次检查目录启用状态及类型，查询选项不构成授权凭据。

目录检查只证明配置语法和允许选择关系，不证明镜像实际可达、安全或内部软件兼容。镜像仍需符合既有 Notebook 约定（8888 端口、`/home/jovyan` 工作目录、非 root 和平台认证）；CUDA 条目也不会自动安装 GPU 驱动或调度组件。
