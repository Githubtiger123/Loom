# Notebook 业务接口

当前源码已包含业务 API、权限、MySQL 持久操作与审计，以及 Notebook 执行器、GET 轮询观测、事件/日志/诊断/用量查询。worker 会消费 `notebook.create/start/stop/update/delete`，复用已有映射器和社区 notebook-controller，不新增 controller 部署。

这不代表集群已可用：命令行入口已从独立 `configs/images.yaml` 加载可信镜像目录并注入 API/worker，创建时固定实际地址和类型，详见 [镜像配置](notebook-images.md)。未验证仓库拉取、镜像运行兼容性或网关部署，不回退到用户镜像地址或任意 PodSpec。停止、删除不依赖镜像解析。没有可信 kernel 活动源，当前不自动 idle 回收。

本次仅编辑源码和文档，未测试、编译、运行项目或执行数据库迁移。

后续已增加 [认证与 Notebook 网关校验主线](authentication.md)：除“访问入口”按钮接口外，网关 `/verify` 可以针对实际 Notebook HTTP/WS 握手重新检查当前业务连接权限。实际网关策略仍未部署，不据此宣称原生访问或同源内容已完全隔离。

## 1. 接口清单

前缀 `/api/v1/groups/{gid}/notebooks`，`gid` 和 `nid` 均为数据库正整数 ID，不是空间名或 Kubernetes UID。所有接口经过现有认证入口。

| 方法 | 相对路径 | 用途 |
| --- | --- | --- |
| GET | `/` | 列表，默认我的实例 |
| POST | `/` | 受理创建 |
| GET | `/{nid}` | 详情，环境变量只返回名称和是否有值 |
| PATCH | `/{nid}` | 修改业务配置 |
| POST | `/{nid}/start` | 受理启动 |
| POST | `/{nid}/stop` | 受理停止 |
| DELETE | `/{nid}` | 受理删除，默认保留工作目录 |
| POST | `/{nid}/retry` | 重试该实例当前失败的操作 |
| GET | `/{nid}/status` | 期望、观测状态、版本、操作 ID 和时间 |
| GET | `/{nid}/access` | 检查访问资格及就绪后返回相对 URL |
| GET | `/{nid}/events` | 归属已验证对象的有界事件快照 |
| GET | `/{nid}/logs` | Notebook 主容器的有界日志快照 |
| GET | `/{nid}/diagnostics` | 实际状态、Pod 故障与当前负责人 |
| GET | `/{nid}/usage` | CPU/内存采样；不可用时明确说明 |
| POST | `/{nid}/owner-transfer` | 转移当前负责人，不改变历史创建人或数据负责人 |

URL 实际不带集合末尾 `/`。全部路由集中注册于 `internal/route/route.go`。

创建选择器另提供 `GET /api/v1/groups/{gid}/notebook-images`，要求 `notebook.create` 权限，只返回已启用的镜像 ID、名称、分类和类型；可选 `category=jupyter|vscode|rstudio` 筛选。返回示例见 [镜像列表接口](notebook-images.md#下拉列表查询接口)。

成功沿用 `{"data": ...}`，失败沿用 `{"error":{"code":"...","message":"..."}}`。需要执行的写请求返回 HTTP 202 和操作单；已确认状态下重复启停可以返回 HTTP 200、`changed=false`。HTTP 状态表示受理结果，不证明集群实时状态。

## 2. 权限与归属

不比较 editor/owner 角色名，按有效权限点授权；平台管理员沿用现有权限检查器。

- 创建：`notebook.create`，空间必须为 ACTIVE。creator、cluster、namespace 从登录身份和空间记录生成，不接受客户端覆盖。
- 我的列表和详情：`notebook.read`；按当前负责人判断，旧记录未登记负责人时回退到创建人。
- 全部/无归属列表，以及其他人的详情：还需 `notebook.read.any`。
- 修改、启停、删除和连接：对应 `notebook.update/start/stop/delete/connect`；操作其他人或无归属实例还需 `notebook.read.any`。
- 重试：`notebook.read` 加原失败操作对应的权限，并满足对象可见性。不授予额外的集群权限。
- Notebook 操作单查询也重新验证当前 `notebook.read` 和对象可见性，不因曾经发起操作而跳过撤权。
- 事件、日志和诊断另需 `notebook.connect`，消息可能包含用户内容；用量只要求台账读取权限及对象可见性。查询结束前再次授权，并核对实例、负责人版本及工作目录身份。

当前负责人为空代表无归属；`created_by` 保留历史含义，`ownerUserId/ownerRevision` 表示当前归属及其独立版本。不把已禁用负责人的对象自动改成无归属。负责人转移要求实例已确认停止；不因此自动转移工作目录数据所有权。

这是业务接口边界，不是共享 namespace 中的资源安全隔离。拿到 namespace 原生访问的用户仍可能手工访问其他实例；访问 URL 接口不是反向代理，也不会撤销已建立的连接。

## 3. 列表与详情

列表参数：

- `scope=mine|all|unowned`，默认 mine。
- `after=0&limit=50`，limit 为 1–200，按 ID 正序；返回 `items` 和 `nextAfter`，后者为 null 表示当前没有下一页。
- `search`：名称/显示名的字面子串，最长 128 字，`%`、`_` 不当通配符。
- `status`：按业务状态精确筛选，例如 RUNNING、STOPPED、FAILED。
- `serverType=jupyter|group-one|group-two`：可选，按环境访问类型精确筛选。
- `includeDeleted=false`：默认不含已确认删除记录，true 可查看本人/有权查看的历史。

详情保留已删除历史的只读查询。列表与详情响应包含 `serverType`、资源整数值（毫核/字节）、镜像 ID、目录归属、期望与观测状态及时间。不返回原始 `requested_spec/observed_spec` 或环境变量明文；Env 返回 `name/hasValue` 等安全字段。

## 4. 创建

所有写请求带唯一 `Idempotency-Key`；请求体用 `application/json`。示例：

```json
{
  "name": "analysis-01",
  "displayName": "分析开发环境",
  "description": "训练前数据分析",
  "imageId": "jupyter-scipy",
  "serverType": "jupyter",
  "cpu": "2",
  "memory": "8Gi",
  "gpu": 0,
  "workspaceSize": "50Gi",
  "idleStopMinutes": 0,
  "env": [{"name":"PROJECT_MODE","value":"research"}],
  "labels": {"team":"research"}
}
```

- name 为 DNS-1123 label，最长 63；同 cluster/namespace 的未删除实例唯一。
- `serverType` 只接受 `jupyter`、`group-one`、`group-two`，不传或空字符串取所选镜像配置的类型；显式提供必须匹配。创建后固定，PATCH 不接受修改类型；启停、重试与重新渲染均沿用原类型，不根据镜像名字猜测。
- 请求只接受目录中已启用的 `imageId`，不接受镜像完整地址。API 从可信配置取得地址与类型，随实例事务保存快照。已有实例不因目录地址修改或禁用而自动换镜像；worker 对旧版无地址记录才首次查目录并固定，不存在/禁用/类型不匹配时返回 `IMAGE_NOT_RESOLVED`。
- CPU 精确存毫核，内存和目录容量精确存字节；必须为正数、符合存储精度且不溢出。不使用浮点数换算。
- GPU 支持 0/1/2/4；空闲停止配置支持 0/60/240/480 分钟。没有可信 kernel 活动源，仅保存配置；页面访问、最后打开时间和陈旧 annotation 都不能证明 kernel 空闲。
- 最多 64 个环境变量和 32 个标签。拒绝重复/非法变量名和平台保留标签；单变量值最多 16KiB。
- Secret 引用的格式规则保留，但当前接口拒绝使用：没有 Secret 业务授权目录时，不能允许请求人任意引用同空间凭据。
- 不接受 namespace、creator、serviceAccount、挂载、调度、特权、command 等字段。

同一个事务内写入实例 `CREATING / desired=RUNNING / observed=UNKNOWN / revision=1`、待执行操作、额度预留和审计。新目录由执行器分配 PVC 名并确认 UID，Notebook UID 也以集群返回结果为准，不把请求受理当作已创建。

创建时可同时提供 `workspaceId` 和 `workspaceRevision` 复用已登记目录，二者必须同时存在；`workspaceSize` 仍须合法且不超过目录容量。目录必须同空间、同集群、同 namespace、独占、UID 已知、负责人匹配且无其他实例引用。业务事务写入 attachment；执行器使用资产原有 `InstanceID` 核对 PVC annotation，不改写原 Notebook 标签，也不因目录丢失自动新建同名数据卷。

### 类型与 CR 访问适配

类型写入 MySQL `tb_notebook.server_type`、`requested_spec.serverType` 和操作命令。worker 在执行前交叉核对，再将其传给 CR 映射器；类型不一致时拒绝执行旧命令，不静默切换环境。

| serverType | 常见环境 | CR annotation |
| --- | --- | --- |
| `jupyter` | JupyterLab 或支持 NB_PREFIX 的环境 | `notebooks.kubeflow.org/server-type: jupyter`，不设置路径重写或额外请求头 |
| `group-one` | VS Code / code-server 类 | 类型为 group-one，同时 `notebooks.kubeflow.org/http-rewrite-uri: /` |
| `group-two` | RStudio 类 | 类型为 group-two，重写为 `/`，并通过 `notebooks.kubeflow.org/http-headers-request-set` 注入 `X-RStudio-Root-Path` |

RStudio 请求头值由后端生成，例如 `{"X-RStudio-Root-Path":"/notebook/team-a/dev-01/"}`。客户端不能提交任意重写地址、请求头或 annotation。controller 根据 CR 创建/维护实际 VirtualService；Loom 不另建一套路由。重新渲染时同时删除不属于当前类型的受管重写和请求头，避免保留错误配置。运行观测会检查 CR 的类型与访问适配是否仍符合业务记录，但不据此声称网关实际可达已经验证。

三种类型的外部入口均为 `/notebook/{namespace}/{name}/`，`GET /{nid}/access` 同时返回 `serverType`。配置独立 Notebook origin 时仍由 handler 添加该域名，不使用容器根路径 `/` 作为外部入口。

类型只描述 HTTP 适配，不自动安装 IDE、覆盖镜像启动命令或验证镜像内的软件。镜像目录保存并校验分类、类型及地址，创建时拒绝类型不匹配；这不证明镜像内部软件实际兼容。镜像应符合既有约定（默认 8888 端口、`/home/jovyan` 持久目录、非 root 运行及平台认证），没有扩大为任意镜像支持。

升级需显式执行 `000014_notebook_server_type.sql`。迁移给旧记录填 `jupyter` 并补缺失的 requested_spec 字段，不修改历史操作 payload/hash 或集群对象。旧命令缺少 serverType 仅按 Jupyter 解释；旧 Jupyter CR 无类型 annotation 且无重写/请求头时兼容原规则，下次显式启停后启动/更新渲染时补齐。迁移本轮未执行。

## 5. 修改、启停与并发

修改、启停、删除和重试必须提供详情/状态接口返回的 `revision`。不匹配返回 409，客户端刷新后再决定是否重发，不自动覆盖最新意图。

PATCH 示例：

```json
{"revision":1,"displayName":"新名称","description":"新的业务说明","idleStopMinutes":60}
```

可修改 displayName、description、idleStopMinutes、cpu、memory、gpu、env、labels。PATCH 未传字段保留；env、labels 一旦提供就是整体替换，`[]`/`{}` 用于清空，null 视为未提供。环境变量值不回显，整体替换时需重新提供所需值。名称、空间、创建人、镜像、serverType 和目录容量不通过此接口修改。

资源、环境变量、标签修改要求当前期望/观测均为 STOPPED、状态 STOPPED、应用版本与业务版本相等。每次有效更新递增 revision，状态变为 UPDATING，并排队执行；不能把资源更新直接当成集群已应用。

启动与停止请求均为：

```json
{"revision":2}
```

启动写入 `desired=RUNNING / status=STARTING`；停止写入 `desired=STOPPED / status=STOPPING`。二者都递增 revision，保留旧观测状态和应用版本。停止不会删除工作目录，也不声称 Service/路由消失。实际 Kubeflow Notebook 由 StatefulSet 承载，不是 Deployment。

同一实例只接受一个未完成写操作（PENDING/RUNNING/RETRYING）。有未完成操作时新的修改/启停/删除返回 409；相同幂等键的原请求重放仍返回原操作。失败操作可以重试，非删除失败也可用最新 revision 提交新的业务意图。删除中的实例禁止重新启停或修改。

创建/修改/启动及其重试要求空间 ACTIVE；停止/删除及其重试允许 ACTIVE、DEGRADED、DISABLING 或 DISABLED，以便冻结后继续安全清理。该例外不放宽更新或启动，也不跳过对象权限、版本、UID、目录保留和删除确认检查。空间正在删除或已删除时不接受这些新请求。

## 6. 删除与数据安全

默认请求：

```json
{"revision":3,"deleteWorkspace":false}
```

申请同时删除数据：

```json
{"revision":3,"deleteWorkspace":true,"confirmName":"analysis-01"}
```

后者必须完全匹配实例名，且目录是 EXCLUSIVE、PVC 名/UID 已知、数据负责人匹配、未受保留保护，没有其他实例（含历史记录）引用。执行器登记的新目录默认 `retained=true`；通常应保留数据删除实例，再通过工作目录接口显式解除保留并删除。共享/未知归属直接拒绝。执行前再次核对 PVC 原生管理标记、资产 instanceID、UID、Pod 和 Notebook CR 引用，不接管或删除同名替换对象。

受理删除只写 `desired=DELETED / status=DELETING`，不立即设置 deleted_at。实际删除确认之前名称继续被占用，记录不从默认列表消失。执行成功后才由执行层写 DELETED、deleted_at 并释放名称；重建产生新行和新 instanceId，不覆盖旧行。

## 7. 重试与幂等

```json
{"revision":3,"operationId":"00000000-0000-4000-8000-000000000001"}
```

operationId 必须指向本实例当前最后一张失败操作单，目标版本和 instanceId 等必须仍匹配。已经成功、正在执行、已被新请求替代或已删除实例的旧命令不能重试。

重试恢复原业务状态、清除错误/租约/等待期限并重新排队，revision 不变，返回真正待执行的原操作单；另保留一条已完成的申请重试审计操作。平台管理员从通用 `/operations/{oid}/retry` 入口重试 Notebook，也执行相同的过期保护。

幂等键在同一操作者范围内唯一，计算包括动作、空间和实例目标以及请求内容。相同键换内容、换空间或换对象都冲突。幂等重放仍重新验证当前权限和对象可见性，但不再以请求里的旧 revision 拒绝原请求。

## 8. 状态和访问

状态包含 desiredState、observedState、status、revision、appliedRevision、operationId、observedAt 等。数据库保存执行层观测投影；`sync` 的 `RunObserver` 包含 Notebook GET 轮询，周期性维护检查观测时效，不增加 watch 依赖。对象丢失、UID/实例冲突、运行漂移会明确回填；网络失败不冒充对象丢失，也不推进成功观测时间。超过两分钟标记 `STALE / UNKNOWN`；失败操作的 FAILED 投影不会仅因资源恢复而被观测器掩盖。

访问接口需要 notebook.connect，且空间 ACTIVE、实例期望/观测/状态均 RUNNING、revision 已应用、有 UID 和最近两分钟内观测。普通用户还需原生空间授权 READY、期望/观测等级一致、访问版本已应用且观测未过期；固定基础设施 Owner 使用其原生空间权限，但仍需业务权限。否则返回 `ready=false` 与原因，不伪造 URL。

满足条件返回相对路径 `/notebook/<namespace>/<name>/`，不接受客户端 Host、外部跳转地址，不代发身份凭据。两分钟新鲜度是保守业务门槛，不是 Notebook 实时在线或路由可达性保证。

## 9. 实际执行器与注入契约

沿用显式版本迁移，不启用 AutoMigrate。Notebook 业务字段来自 `000004_notebook_business.sql`；工作目录、attachment、当前负责人和额度预留来自 `000009_workspace_lifecycle.sql`。本次没有执行迁移，也未为 Notebook 执行额外新建 `000008` 表。

`NotebookCommand` 只含身份、版本、期望状态和目录删除选择，不含明文环境变量。目录字段包含 `workspacePVC/workspaceUID/workspaceId/workspaceInstanceId`；后两者来自资产关联，不是可以由客户端随意指定的原生身份。执行器已经实现：

1. 仅领取自己支持的 notebook.create/update/start/stop/delete，保留持久租约、超时、重试、目标串行化与 fencing。
2. 每次外部写入及回填前检查最新 revision、lastOperationId、instanceId 和已记录 UID；禁止接管同名旧资源。
3. 从 requested_spec 读取并校验受限业务规格，使用已固定的可信镜像快照（旧版无地址记录才查目录），Secret 引用仍拒绝；复用 `BuildNotebook` 生成受控模板，不接受用户 PodSpec。
4. 超时先核对实际对象，避免重复创建。停止确认工作负载已停止，删除确认实际对象消失；只有达到目标才更新 appliedRevision、观测和操作结果。
5. 目录删除再次验证独占性、资源 UID 和其他引用，默认保留数据。不得误删共享或重建后的同名 PVC。
6. 领域状态、操作结果和完成审计经租约 fence 同事务提交；观测摘要不保存原始 PodSpec 或环境变量。

不提供可让普通调用者直接回填 RUNNING/UID/deletedAt 的公共接口。

运行目标的创建/更新/启动不仅提交 CR：须看到目标版本 StatefulSet、Pod Ready 和归属匹配的 Service 才完成。停止设置 `kubeflow-resource-stopped`，等待 StatefulSet 缩到 0（或尚未创建）、Pod 消失；CR/工作目录保留，不宣称 Service 或路由被删除。停止态更新则确认新模板已落实且仍停止。删除使用 UID/resourceVersion 前置条件和前台垃圾回收，等 CR、StatefulSet、Pod、Service、VirtualService 消失后才写 DELETED。

诊断依赖由 `Service.SetNotebookReader(k8sClient)` 注入，`newBusinessService` 已有接线；未装配时返回 `NOTEBOOK_READER_UNAVAILABLE`。这只说明源码接线，不说明集群访问权限、controller、存储或入口已经部署可用。

## 10. 诊断查询

- `events?limit=50`：limit 为 1–100，只取目标 Notebook 及其归属已验证的 StatefulSet/Pod 的 UID 事件；消息有界、按时间排序，超出范围返回 `truncated=true`。不是整个 namespace 的事件列表，也不保证截断时覆盖全部最新事件。
- `logs`：默认最近 3600 秒、200 行、256KiB；`tailLines` 最大 1000，`limitBytes` 最大 1MiB，`sinceSeconds` 最大 3600。支持 `previous=true`；指定 Pod 须同时给 `pod` 和 `podUID`。只读 Notebook 主容器，不支持 follow 或任意 sidecar。验证 Notebook → StatefulSet → Pod 的 controller UID 链，读取后再次核对 UID，避免同名替换泄漏日志。
- `diagnostics`：返回真实状态/原因、Pod phase/Ready/重启和失败摘要、当前 `ownerUserId`，以及不能自动 idle 回收的原因。复用目录使用资产 `WorkspaceInstanceID`，不把 PVC 原 Notebook 标签不同误判为接管。
- `usage`：通过已有 dynamic client 读 `metrics.k8s.io/v1beta1`，不新增 metrics 依赖。需样本 Pod UID 匹配、时间窗口有效且容器用量完整；未安装、无权限、过期或 UID 不可验证时返回 `available=false` 和 `METRICS_UNAVAILABLE` 原因，不返回假零。GPU 实际使用量不由该 API 提供，明确不可用。

这些接口查询当前资源，不替代持久操作单，也不返回环境变量明文或任意原生资源对象。

## 11. 配额与工作目录资产

`GET /api/v1/groups/{gid}/quota` 返回经过空间身份校验的实际 hard/used、未被快照反映的预留及剩余量。创建和启动在事务外读取新鲜 quota 快照，再在空间锁内核对身份/版本、当前数据负责人及并发占用，并在同一业务事务创建 HELD 预留。Kubernetes ResourceQuota 仍是最终原生准入边界。

执行器不会凭 CR 创建或 RUNNING 字样释放 HELD：需验证实际 Pod 和 ResourceQuota 用量覆盖后才转 ACCOUNTED 并记录 AccountedAt；证据不足继续 HELD，观测器可后续核算。确认停止或删除后，释放该实例不高于当前 revision 的预留；失败、超时、页面关闭和 TTL 都不作为释放依据。

工作目录前缀 `/api/v1/groups/{gid}/workspaces`：GET 集合/`/{wid}`，POST `/{wid}/attach`、`/{wid}/detach`、`/{wid}/owner-transfer`，DELETE `/{wid}`。权限分别为 `workspace.read/attach/transfer/delete`，非本人目录还需 `workspace.manage.any`，并重新校验关联实例权限。关联请求包含目录 `revision`、`notebookId` 和 `notebookRevision`；关联/解除要求实例已确认停止，删除历史实例解除还需确认已消失。关联变更不递增 Notebook runtime revision，返回 `runtimeApply=ON_NEXT_START`，下一次启动才更新模板挂载。

目录登记同时写 `tb_workspace` 与 `tb_workspace_attachment`，`Workspace.InstanceID` 沿用 PVC 的 `loom.ml/instance-id`，不是新随机 UUID。默认删除 Notebook 保留数据资产及历史 PVC 引用；复用前仍须显式解除旧引用。目录容量不代表实际文件系统已用量，`usedBytes` 没有可信来源时保持 null。

独立删除需目录版本、处置 `reason`、精确 PVC 名 `confirmName`，以及受保留保护时显式 `releaseRetention=true`；必须无关联/历史引用，执行时再核对 PVC UID、原生实例标记和实际 Pod 挂载。实例负责人和数据负责人分开转移，不能凭实例交接删除或读取其他负责人的数据。
