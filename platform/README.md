# Loom 后端

当前进度：已补充认证会话管理、空间生命周期、配额预算、独立工作目录、Notebook 执行与诊断，以及 Pipeline / Trainer / KServe 业务 API。Pipeline 已接官方 KFP v2 主线；KServe 已接内置 `kserve-worker`，结构化生成和管理 `InferenceService`，并用 Watch + 周期修复同步状态。仅交付后端源码，不包含前端、实际网关部署及 Trainer 实际适配器。
按用户要求不新增测试、不测试、不编译、不运行项目或迁移。当前改动仅做源码与文档级核对，不代表真实 MySQL、身份网关或 Kubernetes 集成已经验证。

Pipeline 后端新增 [DAG 与节点详情接口](docs/pipeline-dag.md)：运行 IR 快照 + KFP 状态 + MLMD 参数/产物元数据；不包含前端或产物可视化。

## 持久化与校验约定

业务路由统一放在 `internal/route/route.go` 一个文件中，按模块注释分组；`route.New` 统一装配认证。对应请求处理方法仍按模块放在 `internal/handler`，handler 不注册 URL。入口通过 `route.New(handler.New(...))` 完成组装，原有接口路径及响应约定保持不变。

- 后端统一使用 GORM v1.31.2 与 MySQL 驱动 v1.6.0；连接初始化、业务读写、授权事务、操作领取及结果回填均使用 `*gorm.DB`，不是用 GORM 包装原有 SQL 调用。
- `internal/model` 显式映射现有 `tb_*` 表。Operation 的自增 `id` 与业务 UUID `operation_id` 分开；角色权限集合不当作列，基础访问使用组/用户复合主键。不嵌入 `gorm.Model`，生命周期仍由业务状态控制。
- 事务通过 `repository.InTx` 使用 READ COMMITTED，闭包内沿用同一个 GORM 事务句柄。更新使用显式字段映射，包含 `false`、`0` 和 `NULL`；不使用可能意外插入的 `Save`。
- 保留版本化 SQL 迁移、checksum、dirty 标记和迁移锁；不启用 AutoMigrate，不修改已经发布的迁移 SQL。特殊 MySQL 会话锁函数与 DDL 保留必要的原生语句，通过 GORM 执行。
- MySQL 会话锁固定在同一条连接上，底层 `sql.Conn` 仅封装在 `infra/mysql` 管理生命周期，业务层不再持有 `*sql.DB`。不得把获取锁、检查锁、释放锁拆到不同池连接。
- 参考 cube 将通用纯格式校验集中到 `internal/utils/validator`：DNS-1123 名称、角色编码、字符长度、资源数量、标签、邮箱、幂等键、身份头、可信网段。DTO 的 Validate 只组合请求规则；权限、最后一个管理员/Owner、额度可用性等业务规则不搬到工具包。
- GORM 及其新增间接依赖的版本和 go.sum 校验和取自本机已有模块缓存；本轮未执行依赖下载或整理命令。

## 当前已经接入

- MySQL 8.4 迁移：用户模块核心十张表、Notebook 表、迁移版本/校验和/dirty 状态。迁移由显式命令执行，API 启动不自动建表。
- 显式初始化首个管理员、内置角色和已实现的权限点。后续登录不自动升权、不按邮箱合并身份、不自动注册。
- 用户登记、待开通、确认身份后启用、本地禁用/恢复；最后一个有效平台管理员与空间 Owner 的事务保护。
- 后端权限校验：全局与空间范围分离，用户状态、角色启用状态、绑定状态及有效期均参与判断，暂不缓存权限。
- 空间的创建受理、列表、详情、业务信息更新；Profile 的固定基础设施 Owner 只取服务配置快照，不取前端输入。
- 多角色原子替换、撤销成员、Owner 转让、自定义角色维护和影响用户的访问目标重算。
- `none < view < edit` 聚合目标、待落实的 `tb_group_access`、持久 `access.reconcile` 请求。
- 写接口的请求幂等、操作进度查询、失败任务重试受理、审计查询及敏感拒绝记录。
- 健康接口区分必需 MySQL 与可选 Redis；本地认证 Token 与撤销状态以 MySQL 为准，Redis 故障降级直查，不阻断就绪。
- `worker` 持久任务领取、唯一领取令牌、续租、目标串行锁、退避重试和过时目标淘汰；领域状态与任务完成结果同事务提交。
- Profile 创建、同名对象拒绝接管、实例及 UID 校验、namespace/默认 SA/系统绑定/Owner 策略/配额的就绪观测。
- 聚合后的成员开通、降权与撤权，先清理旧 edit，再确认目标 RoleBinding 与 AuthorizationPolicy；部分成功由任务继续恢复，不删除整个空间回滚。
- `cron` 到期处理与周期访问对账调度；`sync` 按空间 GET 轮询，只更新观测、不反向授予业务角色。
- 单独的访问落实查询，可查看已移除/禁用用户的撤权进度；发现额外原生授权时显示 DRIFTED，不擅自删除。
- Notebook 业务接口、对象级权限、分页过滤、期望/观测状态分离、revision 并发保护、持久操作及审计。接口契约、请求示例和执行层边界见 [Notebook 业务接口](docs/notebook-api.md)。
- 可选本地认证：独立 Argon2id 凭据表、SQL 持久化 Access/Refresh 摘要、事务刷新轮换、登出/全部登出、改密/管理员重置及 Notebook `/verify`。Notebook Cookie 复用 Access JWT，不再签发第三种凭据。初始化与网关契约见 [认证与访问主线](docs/authentication.md)。启用后业务 API 只验 Bearer，不回退到身份头；不自动开户或合并外部身份。
- 设备会话列表与撤销、有效 Refresh 注销过期 Access 会话、初始/重置密码强制更换；可选独立 Notebook HTTPS 主机，通过 Bearer 写入同一 Access JWT 的 host-only Cookie，不复制 Refresh。
- 空间停用/恢复/安全删除、实际配额查看/调整、Notebook 并发预算占用、工作目录保留/复用/归属交接；用户禁用和移除成员生成 Notebook 停止任务并等待处置确认。详见 [生命周期与目录接口](docs/lifecycle-api.md)。
- Notebook worker 已领取创建/启停/修改/删除任务，核对 UID、revision、租约与实例身份；观测器采集真实状态。日志、事件、诊断与 metrics 查询受对象权限及 UID 链约束，不将 CR 已创建等同于实例就绪。
- Notebook 创建支持 `serverType=jupyter/group-one/group-two`，省略时取所选镜像类型、创建后固定；类型保存到 MySQL、查询返回和执行命令，并渲染对应 CR 类型、路径重写及 RStudio 请求头。升级需显式执行 `000014_notebook_server_type.sql`。
- 独立 `configs/images.yaml` 预置官方 Jupyter / VS Code / RStudio 七个镜像，主配置只引用路径；API/worker 启动严格加载，创建时校验类型并固定地址，目录禁用或修改不隐式换掉已有实例镜像。`GET /api/v1/groups/{gid}/notebook-images` 提供带创建权限校验的只读下拉选项与分类筛选。详见 [镜像配置](docs/notebook-images.md)。
- Pipeline 定义/可编辑版本/修订历史/运行、Trainer 作业、KServe 服务/修订/操作历史。Pipeline 通过独立 pipeline-worker 接官方 KFP；KServe 通过独立 kserve-worker 使用固定执行身份管理 `InferenceService`，支持 MODEL/CUSTOM、停止后重新发布、实时 Watch 和周期对账。详见 [KFP 主线](docs/pipeline-kfp.md)、[KServe 主线](docs/kserve.md)、[ML 业务接口](docs/ml-api.md) 与 [执行桥契约](internal/executor/README.md)。

## 尚未接入，不能当作已完成

1. 人工确认后的历史对象迁移，不能自动接管同名 Profile、Notebook 或 PVC。
2. Notebook 镜像拉取及实际运行兼容性、路由真实可达验证、可信 kernel 活动采集和安全空闲回收尚未验证或实现。配置目录只提供可信选项及地址映射，不替代这些集成。
3. 用户软删除、完整 OIDC/MFA/自助找回与外部身份提供方适配。本地认证可显式开通本地用户，但不代建 Dex/LDAP/SSO 账号，不自动迁移外部账号。
4. 完整的 LIST/WATCH 同步游标；当前空间同步是 GET 轮询。
5. 两模块的前端页面与真实环境部署配置。
6. Trainer 实际适配器与受管代码/数据引用解析；KFP Experiment 管理、定时调度、产物下载等完整能力。KFP/KServe 内置主线仍需真实环境联调；未启用相应执行端时操作等待执行。

**重要：POST 空间返回 `202/PENDING`。运行 worker 后才会创建 Profile 并落实访问；只运行 API 不会执行集群操作。成员业务变更成功不代表集群访问已经生效，必须查看访问状态和操作记录。此版本尚未经真实 MySQL/集群集成验证，不宣称可直接生产使用。**

## 初始化顺序

从 `platform` 目录运行。先由部署方创建空业务数据库并提供 `LOOM_DB_DSN`，账号需要建表权限；运行 API 的账号建议另用较低权限。

```powershell
# 环境变量中的连接串由部署方配置，此处不写真实凭据。
# 1. 执行版本迁移；不会自动初始化管理员或修改集群。
go run ./cmd/platform -cmd=migrate

# 2. 外部身份模式：显式初始化第一个管理员，使用已验证 issuer/subject。
# 本地模式改用 auth.issuer 与本地永久主体，并按认证文档另外执行 set-password。
go run ./cmd/platform -cmd=bootstrap -issuer="https://sso.example.com" -subject="actual-stable-subject" -username="admin@example.com"

# 3. 配置下方身份参数后启动 API。
go run ./cmd/platform -cmd=api

# 4. 下面三个角色各自常驻运行（分别使用终端或独立工作负载）。
go run ./cmd/platform -cmd=worker -worker-concurrency=4
go run ./cmd/platform -cmd=cron
go run ./cmd/platform -cmd=sync

# 5. 仅在配置 pipeline.enabled=true 并挂载 KFP token 后，另起独立角色。
go run ./cmd/platform -cmd=pipeline-worker -worker-concurrency=4

# 6. 仅在配置 kserve.enabled=true、CRD/RBAC 已就绪后，另起独立角色。
go run ./cmd/platform -cmd=kserve-worker -worker-concurrency=4
```

重复 bootstrap 只允许相同的有效管理员，不可借此新增其他管理员或绕过已禁用状态。
迁移执行期间有数据库命名锁；某条 DDL 失败会保留 dirty 标记并停止。MySQL DDL 不能整体事务回滚，需管理员核对实际表结构、修复后再处理迁移标记，不能盲目删除版本记录重跑。

执行器阶段新增 `000003_operation_execution.sql`；Notebook 业务阶段另新增 `000004_notebook_business.sql`。已有库也必须由部署方先显式执行新增迁移，再启动新版 API 和后台角色；本次没有执行迁移。启动时只校验已执行迁移的版本、校验和及 dirty 标记，不自动迁移。

本地认证使用 `000005_authentication.sql`（凭据及 session_version）和 `000006_auth_tokens.sql`（Access/Refresh 摘要、轮换/撤销状态及共享限流桶）。须显式迁移后部署，升级不接续旧 Redis 会话，用户需重新登录。首个本地密码通过 `-cmd=set-password -user-id=<实际ID>` 的隐藏终端输入配置，不支持匿名设置密码或将明文放在进程参数中。

新增迁移包括 `000007_auth_sessions.sql`、`000009_workspace_lifecycle.sql` 至 `000020_kserve.sql`（编号 000008 未使用）。迁移按实际文件名排序执行，不要求连续。切换内置 KFP/KServe 前须停止对应旧外部适配器并核查未完操作；`000020` 增加 KServe CR 身份、地址、就绪状态和同步租约投影。本轮未执行任何迁移。

## Profile 与授权执行说明

Pipeline 支持版本原地编辑和运行“清理后编辑、手动重建”。显式应用新增 `000017_pipeline_edit.sql` 后，同步升级 API/worker；业务运行 ID 保留，重建换新 instanceId/KFP Run ID，旧执行快照保留。详见 [编辑接口](docs/pipeline-edit.md)。本轮未执行迁移或真实联调。

Pipeline 在线日志通过公共 `internal/k8s/podlog` Reader 直读 Kubernetes，支持快照和 follow；Notebook 现有日志快照也复用该组件。需显式应用 `000018_pipeline_logs.sql`，并为 API 机器身份配置只读 pods/log 与执行归属核验权限，详见 [日志接口](docs/pod-logs.md)。[离线日志](docs/log-archives.md) 使用独立 `log_archive` 配置从 MinIO 按需读取清单和日志，不新增日志表，支持历史/软删除运行；采集端需提前上传约定格式，不安装采集器，不自动适配任意 Argo 归档桶。

[Pipeline 在线终端](docs/pipeline-terminal.md) 使用公共 `internal/k8s/podexec` 直连 Kubernetes pods/exec，支持 WebSocket 输入、TTY 输出和窗口缩放。组内 `pipeline.exec` 与日志权限独立；需显式应用 `000019_pipeline_exec.sql`，机器身份需 pods/exec 的 get/create 权限。仅支持当前运行中的普通容器及镜像自带 shell，首帧复用现有 Access JWT，不新增凭据或终端表，不经过 worker；本次没有实现前端或独立调试 Pod。

Pipeline 独立观测另需显式执行 `000016_pipeline_sync.sql`（本轮未执行），随 pipeline-worker 常驻运行。默认活动 Run 约 5 秒、终态约 5 分钟查询一次；`sync_concurrency=2`、`sync_qps=10` 限制每进程观测开销。操作结束/撤权不停止观测，已逻辑删除的 Run 停止对账。运行查询提供同步时间与异常原因，详情见 [KFP 状态同步](docs/pipeline-kfp.md)。

- `worker` 每约 2 秒扫描待执行任务；默认并发 4，支持 1–16；数据库最大连接数至少为并发数 + 2（建议沿用 50）。
- 每轮外部执行最多 40 秒，单个 Kubernetes 请求最多 10 秒；租约 60 秒、约每 10 秒续租。等待派生资源时 5 秒后重新检查，不持续占用线程。
- MySQL `SKIP LOCKED` 负责领取不同任务，独立连接命名锁负责同一空间用户跨进程串行；部署需要同一个 MySQL 写主库，不能把会话锁请求随意路由到不同主节点。
- 瞬时失败最多记录 8 次失败尝试，指数退避并加随机抖动；正常等待不计失败尝试，但单次受理后的正常等待上限是 30 分钟。人工重试会重置等待期限与失败计数。
- 创建超时先读取实际 Profile 核对本次实例；不会盲目删除重建。已记录 UID 的 Profile 消失或变成同名不同 UID，停止自动接管。
- 普通业务 Owner 和 editor 映射 `kubeflow-edit`，viewer 映射 `kubeflow-view`，不向普通用户授予 `kubeflow-admin`。
- 执行前、每次外部写入前、最终回填前核对最新业务目标；过时任务进入 SUPERSEDED，并推进最新目标。MySQL 与 Kubernetes 不存在原子事务，网络分区时仍可能出现短暂迟到写入，由周期对账收敛。
- `cron` 约每 15 秒处理到期绑定并扫描需观测的访问目标；正常 READY 目标约 60 秒后可再次调度。必须常驻运行，否则业务权限到期后原生访问不会自行清理。
- `sync` 约每轮 30 秒间隔观测已有空间，缺失对象只记异常，不自动重建，也不从外部对象产生业务授权。
- 成员对象带空间实例、group/user ID 和访问版本信息。对旧 helper/kfam 创建、缺少本次实例标识的对象不自动迁移；冲突需要管理员核验。
- 只处置 Loom 管理的访问对象。额外直接用户绑定、广泛主体绑定或可疑访问策略会显示 DRIFTED，关联任务 FAILED，保留警告与对象清单。排查修复后由平台管理员重试对应 access.reconcile 操作。
- 固定基础设施 Owner 的系统权限永远保留；若该身份同时为业务成员，会显示系统权限保留的 DRIFTED 提示，不会声称撤销其全部访问。这不阻止其作为业务 Owner 完成空间初始化。
- 漂移探测不是完整的 Kubernetes/Istio 有效权限证明，不能覆盖任意 OIDC 身份组、网格全局策略、应用会话及已经建立的连接。

执行器需要的身份及权限示例见 `../install/platform-worker-rbac.yaml`。仅生成清单，程序不会自动安装或提权。清单包含集群级 Profile 创建、空间角色绑定和访问策略写权限，部署方必须审查，不能给普通用户或不可信工作负载使用。若集群启用了 Istio ambient/waypoint 等不同策略模型，需要单独适配，不能直接认定当前网关规则已生效。

API/cron 的集群权限可单独收紧；不要将 worker 的写权限当作所有角色共同的默认权限。当前 `/readyz` 中 Kubernetes 项只表示客户端已构造，实际 API 连通和权限错误会在执行器/观测器中记录；不将该项当作完整集群预检。

## 认证与配置

认证有两个显式模式：默认外部 IdP；`auth.enabled=true` 为本地密码/JWT/会话模式。后者的完整配置、接口与部署前提见 [认证与访问主线](docs/authentication.md)。下面身份头说明主要针对外部模式，本地模式只在内部 `/verify` 输出可信身份头。

覆盖顺序为：默认值 → 指定配置文件 → 同目录 `config.local.yaml` → 环境变量。
`-config=""` 不读任何文件。CLI 的 issuer/subject/username 仅用于显式 bootstrap，不覆盖运行时身份配置。

推荐本地覆盖内容（不要提交真实身份信息或凭据）：

```yaml
k8s:
  cluster: default
  infrastructure_owner: admin@example.com
identity:
  issuer: https://sso.example.com
  subject_header: oidc-sub
  userid_header: kubeflow-userid
  userid_prefix: ""
  trusted_proxy_cidrs: ["127.0.0.1/32", "::1/128"]
```

未启用本地认证且 `identity.issuer` 为空时，业务接口返回 `503 IDENTITY_NOT_CONFIGURED`，不会降级成匿名或默认 admin。本地模式使用 auth.issuer，不因缺少 identity.issuer 回退到匿名。
生产环境必须将可信网段改为网关的最小实际来源范围，并通过 NetworkPolicy 阻止其他工作负载直连 API。
网关必须先清除客户端提供的同名头，再注入自己已经验证的 subject 和登录名，并只接纳所配置 issuer 的身份。
这里只以直接连接地址判断可信来源，不信任 `X-Forwarded-For`。仅配置一个网段不等于完成网关认证或网络隔离。

环境变量支持 `LOOM_IDENTITY_ISSUER`、`LOOM_IDENTITY_SUBJECT_HEADER`、`LOOM_IDENTITY_TRUSTED_PROXY_CIDRS`（逗号分隔）、`LOOM_K8S_CLUSTER`、`LOOM_K8S_INFRASTRUCTURE_OWNER`。
Profile Owner 与 bootstrap 管理员可以相同，但两者的业务授权不能互相推导。
MySQL 驱动统一开启时间解析，连接和会话按 UTC 处理；前端展示时再转换时区。

## HTTP 契约

当前前缀统一为 `/api/v1`。成功返回 `{"data": ...}`；失败返回 `{"error":{"code":"...","message":"..."}}`，不返回数据库或身份凭据细节。
分页使用 `after=<上一页最后一个ID>&limit=50`，limit 范围 1–200；暂不返回总数。
普通业务写接口要求唯一 `Idempotency-Key` 请求头（1–64 个可见 ASCII 字符），有 JSON 请求体时必须为 `application/json`。登录、刷新、登出和密码变更不使用通用幂等操作单缓存，避免持久化可重放凭据；详情见认证文档。
同一操作者相同幂等键与内容返回原操作；换内容复用键返回冲突。当前权限仍会重新检查。

| 接口 | 说明 |
|---|---|
| GET `/me` | 当前账号、全局角色和权限 |
| GET/POST `/users` | 用户列表 / 登记外部身份 |
| POST `/local-users` | 本地模式显式创建业务账号，密码另行设置 |
| PUT `/users/{uid}/password` | 本地模式管理员设置/重置密码，撤销目标的所有会话 |
| POST `/auth/login`、`/auth/refresh` | 本地登录 / Cookie 刷新，只在响应 JSON 返回短期 access |
| POST `/auth/logout`、`/auth/logout-all` | 注销当前设备 / 所有设备 |
| PUT `/auth/password` | 当前用户改密，随后重新登录 |
| POST `/users/{uid}/enable` | 确认外部身份可用后本地启用 |
| POST `/users/{uid}/disable` | 本地禁用，并持久化访问清理目标 |
| PUT `/users/{uid}/roles` | 替换全局角色集合 |
| GET/POST `/roles` | 查询 / 新建自定义角色 |
| PUT `/roles/{rid}` | 完整替换自定义角色定义，内置角色不可修改 |
| GET `/permissions` | 代码已经实现的权限字典 |
| GET/POST `/groups` | 空间列表 / 受理空间创建 |
| GET/PATCH `/groups/{gid}` | 空间详情 / 修改业务展示信息 |
| GET `/groups/{gid}/roles` | 空间管理员可选择的已批准空间角色 |
| GET `/groups/{gid}/members` | 成员业务角色与基础访问落实状态 |
| GET `/groups/{gid}/access` | 包含已移除/禁用用户的原生访问落实记录，支持 after/limit |
| PUT `/groups/{gid}/members/{uid}/roles` | 原子替换空间角色集合 |
| DELETE `/groups/{gid}/members/{uid}` | 撤销该用户全部空间角色 |
| POST `/groups/{gid}/owner-transfer` | 交接业务 Owner，不改 Profile Owner |
| GET/POST `/groups/{gid}/notebooks` | Notebook 列表 / 创建受理 |
| GET/PATCH/DELETE `/groups/{gid}/notebooks/{nid}` | 详情 / 修改 / 删除受理 |
| POST `/groups/{gid}/notebooks/{nid}/start`、`/stop` | 启停受理 |
| POST `/groups/{gid}/notebooks/{nid}/retry` | 有权限用户重试本实例当前失败操作 |
| GET `/groups/{gid}/notebooks/{nid}/status`、`/access` | 状态投影 / 访问资格和就绪 URL |
| GET `/operations/{oid}` | 有权查看的操作进度 |
| POST `/operations/{oid}/retry` | 管理员重新受理失败操作 |
| GET `/audits` | 经全局权限检查的审计记录 |

创建用户的 `externalIdentityConfirmed=false` 时记录为 PENDING，不宣称外部账号已经开通；为 true 时要求提供稳定 subject，由有管理权限的操作者负责线下确认。
外部用户启用接口要求 `externalIdentityConfirmed=true`；首次启用 PENDING 账号必须提供 subject，既有 subject 不允许通过此接口变更。本地模式同一 auth.issuer 下已经有稳定 subject 的账号恢复可提交 `{}`，不自动设置密码，也不恢复旧会话。

替换角色请求：`{"roles":[{"roleId":5,"expiresAt":"2027-01-01T00:00:00Z"}]}`；空数组表示移除该范围全部角色。Owner/平台管理员不允许到期授权。
成员由有效空间绑定派生，移除后不再出现在成员列表，但操作与历史绑定保留。
转让请求：`{"userId":12,"keepCurrent":false}`；false 只撤销发起人的 Owner 角色，不会抹去其余有效角色。

## 下一阶段的闭环顺序

本轮交付范围、接口、升级顺序和外部依赖见 [后端补全说明](docs/backend-completion.md)。Notebook 原生执行、镜像配置、内置 KFP 与 KServe 主线代码已接入；Trainer 实际适配器、集群权限/网关部署及真实联调仍由后续集成完成。前端未改动。
不能把数据库存在一行、HTTP 返回 202 或 helper 调用成功当作资源已经就绪。
