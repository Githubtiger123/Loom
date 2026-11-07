# Loom 认证与 Notebook 访问主线

本文描述当前后端认证、设备会话、强制改密及 Notebook 独立主机交接的实际源码契约，不包含登录页面或前端实现。其他执行层模块不属于本文验证范围；没有完成网关接入时不能宣称浏览器到 Notebook 已经联通。本次仅核对源码和更新文档，没有测试、编译、运行项目、安装网关或执行迁移。

## 1. 模式与职责

`auth.enabled=false` 保留原来的外部 IdP 模式：Loom 接受可信代理提供的 username + 稳定 subject，按配置 issuer 查找已登记身份；不校验本地密码。本地登录、刷新和 `/verify` 返回 503。

`auth.enabled=true` 启用本地认证：

- Loom API 只接受本模块签发的 Bearer JWT，不回退到客户端或代理身份头。
- 浏览器访问 Notebook 使用承载同一个 Access JWT 的 HttpOnly Cookie，由网关调用 `/verify`，不再签发第三种身份凭据。
- `tb_user` 继续保存业务身份和角色关系；密码散列与 Token 摘要在独立 MySQL 表，Redis 仅作可选缓存。
- Profile Owner 继续使用配置中的固定基础设施 admin，不取真实创建人或本地登录者。
- 登录不自动开户、不自动升权，不按相同邮箱/用户名合并或迁移外部身份。

身份发行方与业务授权分开。平台 SA 创建 CR 的权限与普通用户 RoleBinding 不是同一件事；Notebook HTTP 访问由网关校验与 Istio 策略共同控制，Kubernetes RBAC 不直接认证浏览器页面。

## 2. 凭据与存储

| 数据 | 保存位置 | 用途 |
| --- | --- | --- |
| Argon2id 密码散列 | MySQL `tb_local_credential` | 本地登录，不作为业务响应返回 |
| password_change_required | MySQL `tb_local_credential` | 首次配置/管理员重置后的强制改密门禁，每次从 SQL 检查 |
| session_version | MySQL `tb_user`，不返回前端 | 全设备撤销，独立于 authorization_version |
| Access JWT | 前端内存，同时写入 `__Host-loom-access` HttpOnly Cookie | API Bearer / Notebook 导航及 WebSocket 握手，共用同一个 JWT，默认 15 分钟 |
| Refresh | 平台主机的 `__Secure-loom-refresh` HttpOnly Cookie | 刷新，以及 Access 失效后注销当前设备，默认设备 Token 族最多 15 天 |
| Access / Refresh 摘要和状态 | MySQL `tb_auth_token` | 两类记录，保存有效期、撤销状态及刷新消费/替代关系 |
| 设备 family 状态与元数据 | MySQL `tb_auth_session` | 用户、认证域、登录版本、创建/刷新/绝对到期/撤销时间，不是第三种凭据 |
| Access 不可变摘要缓存 | Redis `loom:auth:access-hash:<认证域摘要>:<jti>` | 可选；故障或未命中读 MySQL，不缓存可授权的有效状态 |
| 共享限流桶 | MySQL `tb_auth_rate_bucket` | 登录/刷新/改密限流，不是第三类 Token 或会话凭据 |

Access 使用固定 HS256，强制检查算法、issuer、audience、type、exp、iat、nbf，包含 sub（业务用户 ID）、公开 sid（设备 Token 族 ID）、jti、session_version。每次签发都保存对应 jti 的 SQL 记录；每次验证同时检查记录存在、摘要一致、未撤销且未过期，family 在 SQL 中存在且版本/期限一致、未撤销，以及当前用户 ACTIVE、未删除、issuer 和 session_version 匹配。当前提供按设备/全设备撤销，不开放单个 jti 的管理接口。

Refresh 使用 `独立 UUID + 32 字节随机 secret`，每次轮换更换 UUID 和 secret。JWT 的公开 sid 不是 Refresh 凭据。MySQL 只保存 Access/Refresh 的 SHA-256 摘要，不保存可重放的 Token 原文；每条记录绑定 issuer/audience 的认证域摘要，避免共用数据库/Redis 时串用。family_id 关联同次登录的轮换记录和 `tb_auth_session` 元数据，不新增浏览器 Token 或独立会话 secret。

Cookie 均为 Secure、HttpOnly、host-only；Access Cookie Path=/，Refresh Path=/api/v1/auth。平台登录/刷新写出的两个 Cookie 为 SameSite=Lax；独立 Notebook 初始化只写该主机上的 Access Cookie，使用 SameSite=None，不写 Refresh。JavaScript 只从登录/刷新 JSON 获得 Access，不获得 Refresh，不写 localStorage。Cookie 不得晚于已验证 JWT、SQL Token 和 family 的到期时间；刷新不延长最初的 family 绝对期限。前端需 single-flight 刷新，页面重载可用 Refresh 恢复内存 Access。独立主机模式下，刷新平台凭据后还需重新初始化 Notebook Cookie；网关和 Notebook 不持有 Refresh，不能静默续签，也不能把 15 天当成 Access Cookie 有效期。

Redis 可以关闭；启动或运行中不可用时直查 MySQL，不因 Redis 故障阻断就绪或要求用户重新登录。缓存操作有短超时，失败不影响 SQL 提交；恢复后可重新填充。即使缓存命中，撤销/消费/有效期状态与用户登录版本仍查询 MySQL 写主库，不允许旧正缓存放行已退出用户。这不是完全脱离 SQL 的缓存认证，缓存当前仅减少摘要字段读取，不承诺减少查询次数。MySQL 故障时拒绝认证，不降级为“JWT 签名通过就放行”；数据库备份回滚仍需额外会话失效处置，不能把 Redis 的恢复安全性等同于数据库恢复安全性。

`tb_auth_token` 的 token_id 是 Access 的 jti 或 Refresh 的独立 ID，token_type 仅 ACCESS/REFRESH；family_id 关联设备，user_id/session_version 绑定用户，token_hash 存摘要，expires_at/family_expires_at 存期限，consumed_at/replaced_by 记录 Refresh 轮换，revoked_at 记录设备撤销。已消费 Refresh 不立即删除，maintenance 每轮分别最多清理 1000 条已过族绝对期限的 Token、1000 条已过期 family 元数据和 1000 条过期限流桶。

## 3. 接口清单

前缀 `/api/v1`，成功返回 `{"data": ...}`。认证接口不返回 token/Cookie/密码散列到审计、操作单或日志。密码接口只记录操作者、目标和结果，不存原始请求体。

| 接口 | 凭据与规则 |
| --- | --- |
| POST `/auth/login` | 公开入口；严格 Origin；用户名+密码 |
| POST `/auth/refresh` | refresh Cookie；严格 Origin；无请求体 |
| POST `/auth/logout` | 有效 Access Bearer/Access Cookie，或 Access 失效后使用有效 Refresh Cookie；严格 Origin；注销当前设备 |
| POST `/auth/logout-all` | 有效 access 或浏览器 Cookie；严格 Origin；注销所有设备 |
| PUT `/auth/password` | Access Bearer；严格 Origin；当前密码+不同的新密码；受限用户也可调用，成功后全部设备重新登录 |
| GET `/auth/sessions` | Access Bearer；只列当前用户当前版本的有效 family；不接受 Cookie 代替 Bearer；待改密用户拒绝 |
| DELETE `/auth/sessions/{family}` | Access Bearer；严格 Origin；撤销本人指定 family；待改密用户拒绝 |
| DELETE `/users/{uid}/auth/sessions/{family}` | Access Bearer + user.manage；严格 Origin；事务内重新核验权限并撤销指定用户的 family |
| POST `/auth/notebook-session` | 到达 NotebookOrigin 主机；仅 Access Bearer；严格 PublicOrigin；仅写同一 JWT 的 Notebook Access Cookie；待改密用户拒绝 |
| OPTIONS `/auth/notebook-session` | 同样核验主机和 PublicOrigin；无凭据 CORS 预检，只允许 POST 与 Authorization，不写 Cookie |
| POST `/local-users` | access + user.manage + Idempotency-Key；显式创建本地业务用户 |
| PUT `/users/{uid}/password` | access + user.manage；设置/重置本地用户密码，撤销其全部会话 |
| POST `/users/{uid}/disable` | 沿用既有管理员、Owner 保护及授权清理，同时递增登录版本 |
| `/verify/<原始路径>` | 仅可信网关内部调用；返回身份头，不返回 JWT |

登录、刷新、登出和密码变更不使用通用操作单幂等缓存，避免存储可重放凭据。密码重置不是幂等回放操作：重复提交会再次变更散列和登录版本。普通业务写接口（包括创建本地用户）继续要求 Idempotency-Key。

错误区分：401 凭据无效/会话失效，403 来源、权限或必须改密拒绝，404 指定用户/认证域下没有该设备 family，409 并发刷新/凭据 family 不一致/业务冲突，429 限流，503 认证依赖不可用或未配置 NotebookOrigin。不要把 503 当成用户退出，也不要自动无限重试刷新。

## 4. 初始化与本地用户生命周期

认证表依次使用 `000005_authentication.sql`（独立 session_version 与密码凭据）、`000006_auth_tokens.sql`（两类 Token 和共享限流桶）、`000007_auth_sessions.sql`（family 元数据及强制改密列）；其他模块的迁移仍按项目版本顺序显式安装。保留旧迁移校验和，不 AutoMigrate。000007 从既有 SQL Token 回填 family 创建/刷新/期限/撤销状态，并把已有本地凭据保守标记为必须改密。迁移前停止旧认证写入者，迁移后不得混跑不维护 family/改密标记的旧版本。更早的 Redis 会话不导入；旧 `__Host-loom-session` 不再认证，登录/成功退出时清除，转发 Notebook 时也会剥离。下面是部署方后续操作说明，本次未执行这些命令。

1. 配置真实 HTTPS 来源、唯一的本地 issuer、随机签名密钥和 MySQL 写主库，Redis 可选。`identity.issuer` 留空或与 auth.issuer 完全一致。
2. 部署方显式运行迁移，初始化管理员时 `-issuer` 必须等于 auth.issuer，`-subject` 使用本地管理员稳定且永久不复用的标识。不得将已有外部账号只改 issuer 后重新认领。
3. 使用显式运维命令为既有、ACTIVE、同一本地 issuer 的管理员配置首个密码：

   ```text
   platform -cmd=set-password -user-id=<实际用户ID>
   ```

   终端交互隐藏输入并二次确认；也支持标准输入。密码不放 flag、命令行参数或环境变量，不回显。命令不新建管理员、不自动激活账号，也不调用 Kubernetes 或 Redis；MySQL 中的版本变更即使 Redis 暂不可用也能使旧会话失效。
4. 管理员首次登录后先通过 PUT `/auth/password` 验证临时密码并设置不同的新密码，再重新登录，之后才能调用 POST `/local-users`：

   ```json
   {"username":"alice","displayName":"Alice","email":"alice@example.com"}
   ```

   issuer 从服务端配置取，subject 使用新 UUID。记录为 ACTIVE 并授予既有 platform_user 默认角色，但没有密码不能登录，没有空间绑定不能访问任何空间。响应包含 user ID 与 passwordSetupRequired=true。
5. 管理员通过 PUT `/users/{uid}/password` 设置临时密码，使用安全线下渠道通知本人。此操作要求本人改密，并撤销目标用户所有设备；不提供匿名重置、找回或公开注册。
6. 禁用沿用现有接口。重新启用本地 issuer 下已有稳定 subject 的账号可以提交 `{}`，不需要假装确认外部 IdP；不会更换 subject、自动设密码或恢复旧会话。外部账号继续要求原有 externalIdentityConfirmed 流程。

新密码要求 12–1024 字节有效文本。密码散列参数固定为 Argon2id v19，64MiB、3 次、并行度 2、16 字节随机 salt、32 字节结果，不接受客户端控制开销。

首次运维配置与管理员设置/重置都会写入 `password_change_required=true`。登录和刷新仍签发原有 Access/Refresh，但响应携带 `passwordChangeRequired=true`，不产生第三种“改密 Token”。普通业务中间件只对 GET `/api/v1/me` 与 PUT `/api/v1/auth/password` 豁免改密门禁；登录、刷新、当前/全部退出在各自认证入口处理。设备管理、Notebook 初始化和 Notebook 网关校验拒绝待改密用户。`RequirePasswordChanged(ctx, actor)` 每次从 SQL 重新检查用户版本及凭据标记，不把响应布尔值或 JWT 声明当授权。本人用正确当前密码改成不同的新密码，才会清除标记，并原子提升版本、撤销全部 family/Token、写审计；随后重新登录。仍未提供邀请链接。

## 5. 登录、刷新、退出和改密

登录示例：

```json
{"username":"alice","password":"<用户输入>"}
```

流程：校验 Origin/请求体 → SQL 共享限流 → 按 issuer/username 查用户和凭据 → Argon2id → 生成 Access/Refresh → 事务内重新核对用户状态、登录版本和密码散列 → 一起保存 family 元数据、双 Token 摘要、登录时间和审计并提交 → 尽力写摘要缓存 → 写入平台两个 Cookie，JSON 返回 accessToken、tokenType、expiresIn、userId、passwordChangeRequired。SQL 提交失败不发放凭据；Redis 故障不回滚登录。

未知用户也执行一次固定开销的密码校验，失败统一响应。进程最多并发 4 个密码运算，防止无限内存分配。MySQL 默认限流为直接连接 IP 每分钟 60 次、用户名每 15 分钟 10 次（成功尝试也计数）、有效 Refresh 所属设备每分钟 20 次、改密每用户每 15 分钟 10 次。计数全程使用 SQL，避免 Redis 中断/恢复导致额度重置；不信任客户端 X-Forwarded-For。反向代理后同一出口共享该 IP 桶，生产网关需补充自己的真实客户端限流与容量规划，不能把此默认值当完整防暴力破解方案。

刷新不需要 Access，使用 HttpOnly Refresh Cookie，要求 Origin 与 PublicOrigin 严格匹配。先验证 Refresh 摘要再占限流桶，事务内与密码/禁用/退出串行，再锁定核对旧 Refresh、family、用户状态和版本。原子条件消费旧 Refresh、记录 replaced_by、插入新的 Access/Refresh 两条记录并更新 family 的 last_refreshed_at，族绝对期限不变，新 Access 仍关联同一 sid。待改密用户允许续签，但响应标记与后端门禁仍然生效。Redis 不参与消费判定，健康与故障时使用同一套事务。前端需要 single-flight 刷新，多标签页协调，不应定时高频刷新。

- 旧 refresh 在消费后 10 秒内再次使用：409，不下发凭据，不撤销会话，给并发刷新留出失败容忍窗口。
- 10 秒后再次使用已消费 Refresh：在 SQL 内撤销该 sid 全部 Access/Refresh 并记录重放审计，提交后返回 401，不注销其他设备。
- 摘要不匹配、未知 ID 等随机错误凭据：401，不允许仅凭公开 ID 撤销他人会话。
- 已消费记录保留到原会话绝对到期；单会话最多 4096 次轮换，达到上限要求重新登录，避免无界增长。
- 请求已完成但响应丢失时，不保存可恢复的新 token 原文；必要时重新登录，不悄悄重放凭据。
- 所有刷新错误均不写清理 Cookie，避免无效凭据或较晚返回的失败响应覆盖并发刷新成功的新 Cookie；即使重放已经撤销 family，保留的 Cookie 也不能通过 SQL 校验。

当前设备退出始终要求唯一、严格匹配 PublicOrigin 的 Origin，且拒绝 Sec-Fetch-Site=cross-site。先验证 Bearer（存在时优先）或 Access Cookie；仅在 Access 缺失/无效/过期的 401 情况下，允许凭 Refresh Cookie 独立退出，数据库故障等错误不降级。Refresh 必须摘要匹配、未撤销、未消费、未过期，用户及 family 版本仍有效；不旋转、不发放新凭据。若 Access 有效且同时提供 Refresh，两者必须属于同一用户/版本/family；此时同 family 已消费 Refresh 也可由有效 Access 授权撤销。重复 Cookie、错误 secret、不同 family 或依赖失败均不清 Cookie。事务提交 family 与全部 Token 的撤销及审计后才清理平台 Cookie。全部退出仍需有效 Access，不支持仅 Refresh。

全部退出、改密、管理员重置同时递增 MySQL session_version 并显式撤销该用户全部 family/Token；即使旧记录尚未清理也不再有效。禁用/启用也递增该版本，因此重新启用不能让旧 Cookie/JWT 复活。授权变更只改变业务权限/authorization_version，不要求整个平台重新登录。旧 Access 在正常刷新后可继续用到原到期点；设备退出、重放撤销或登录版本变化会使它提前失效。

### 5.1 设备 family 列表与撤销

一次登录对应一个设备 family，不做硬件指纹识别；同一浏览器重新登录会创建新 family。GET `/auth/sessions?limit=50&after=<family UUID>` 只返回当前用户、当前认证域/登录版本下未撤销且未到期的 family。`limit` 为 1–200，默认 50，按 family UUID 字典序分页，不按最近活动排序。响应 `data.items` 含 familyId、current、createdAt、lastRefreshedAt、expiresAt；有后续页时 `data.nextAfter` 是本页末项 UUID。lastRefreshedAt 初始为登录时间，之后仅刷新更新，不代表最后业务访问或设备在线。

本人 DELETE `/auth/sessions/{family}` 与管理员 DELETE `/users/{uid}/auth/sessions/{family}` 都在 SQL 授权锁事务内重新验证操作者状态/版本/已改密状态，管理员另检查全局 user.manage，再按认证域+用户+family 定位，原子撤销元数据和全部 Token、写设备审计。不能猜 family 跨用户或认证域撤销；记录不存在返回404，已撤销但尚未清理的记录可重复撤销。本人响应为 `{"data":{"revoked":true,"loginRequired":false}}`，撤销当前 family 时 loginRequired=true；管理员响应为 `{"data":{"revoked":true}}`。这些接口不清 Cookie，避免误删其他身份的浏览器凭据；被撤销 Token 的后续认证由 SQL 拒绝。

## 6. Notebook 网关校验

网关使用 HTTP ext_authz 的 `pathPrefix: /verify`，原始路径 `/notebook/team-a/analysis-01/api/...` 对应内部校验路径 `/verify/notebook/team-a/analysis-01/api/...`。必须保留原始 method、Host/authority、Cookie、Origin、Sec-Fetch-Site/Mode/Dest 和 WebSocket Upgrade；check 的 Host 必须是浏览器原始 Notebook Host，不是内部 Loom 服务地址。

校验顺序：

1. 连接方必须落在最小可信网关网段，不能凭 X-Forwarded-For 声明自己可信。
2. 验证 Cookie 中的 Access JWT 签名/claims，并检查 SQL 对应摘要、撤销状态、Access 与族绝对期限。
3. 查询 MySQL，检查 family 有效、用户 ACTIVE、未删除、正确 issuer、session_version 匹配且已完成强制改密。
4. 根据路径中的 namespace/name 查本集群 ACTIVE 空间和未删除业务实例，不相信客户端提交的 groupId 或身份头。
5. 检查当前 notebook.connect、实例归属；访问他人/无归属对象还需 notebook.read.any。停止/删除中的实例拒绝新连接。
6. NotebookOrigin 已配置时，r.Host 必须与其 Host（包括显式端口）大小写无关匹配，不信任 X-Forwarded-Host。非安全方法、WebSocket 握手要求唯一 Origin 严格等于 NotebookOrigin；只读请求允许缺少 Origin，但若携带仍必须匹配。Sec-Fetch-Site=cross-site 默认拒绝，唯一例外是非 WebSocket 的 GET/HEAD 顶层文档导航（Sec-Fetch-Mode=navigate、Sec-Fetch-Dest=document），以支持平台跳转。未配置 NotebookOrigin 时沿用 PublicOrigin 来源规则。拒绝可能在代理与应用产生不同解释的编码分隔符、二次编码和父级路径段。
7. 从当前数据库身份生成 userid（包含配置 prefix）和 subject 响应头；网关必须覆盖原始同名头，不能追加。
8. 将清理后的 Cookie 和无凭据的 Authorization 标记作为 check 响应头，由网关覆盖发往 Notebook 的相应请求头。保留 Jupyter 自己的 Cookie，但去掉两个 Loom Cookie；没有其他 Cookie 时用固定无敏感标记，避免空值被代理忽略而保留原 Cookie。
9. 下游 Istio AuthorizationPolicy 继续核对用户身份和实际网关 workload principal，路由到 Notebook Service。

`/verify` 不读取 X-Forwarded-Uri 来决定目标，不接受 JWT query 参数，不把原始 Cookie 转发给 Notebook。它不回填 RUNNING，也不证明 Notebook 实时可用；原有 `/access` 接口的“就绪提示”与此处的“访问资格”各司其职。

API 路径的 `/verify` 分支使用 access Bearer，不用 Cookie 替代；API handler 会再次检查 Bearer 和业务权限。登录/刷新/退出/改密路径不经此网关 CUSTOM 检查，由各自入口检查；`/auth/password` 本身仍必须通过 API Bearer 中间件。

`/verify` 只支持 Loom API 与 Notebook。KFP/KServe 原生页面、执行器机器认证和跨源 iframe 嵌入不走本校验，未知路径默认拒绝，不能靠加入 Cookie 自动放行。ML 执行器使用第8节的独立 mTLS listener，不接受用户凭据。

### 6.1 独立 Notebook 主机的交接顺序

1. 配置 PublicOrigin（如 https://loom.example.com）和可选 NotebookOrigin（如 https://notebooks.example.com）。两者必须是无路径/查询/片段的 HTTPS origin，配置校验同时拒绝尾随空 `?` 与原始 `#`。NotebookOrigin 的 hostname 必须与 PublicOrigin 不同；仅端口不同不能隔离 host-only Cookie。
2. 用户在平台登录并完成必要的强制改密；Access 保留在平台页面内存，Refresh 仅在平台主机。
3. 平台页面向 `NotebookOrigin + /api/v1/auth/notebook-session` 发起携带 Authorization: Bearer 的跨源 POST，使用 credentials=include，不提交 JSON/body 或其他自定义请求头。OPTIONS 预检只声明 POST 与 Authorization。服务端严格验证原始 r.Host、唯一 Origin=PublicOrigin、Access 的 JWT/SQL/family/用户版本和已改密状态；Cookie 不能替代 Bearer。
4. 响应只为 Notebook 主机设置 host-only `__Host-loom-access`，值为同一 JWT，不重新签发或旋转，Expires 取已验证 JWT/SQL Token/family 的最早期限，HttpOnly、Secure、Path=/、SameSite=None。CORS 只允许 PublicOrigin、凭据、POST/OPTIONS 和 Authorization；预检204、不写 Cookie，未配置 NotebookOrigin 返回503。
5. 平台调用 GET `/groups/{gid}/notebooks/{nid}/access`。服务层只在观测、版本及空间原生授权满足就绪条件时产生经 DNS 标签校验的 `/notebook/<namespace>/<name>/`；handler 仅用配置的 NotebookOrigin 前缀生成绝对 HTTPS 地址，不接受客户端 Host/回跳地址，不带 JWT/query secret。未配置独立主机时仍返回相对路径。这个接口不负责初始化 Cookie，也不保证实时可达。
6. 浏览器顶层打开返回地址；网关先走上述 `/verify` 再路由到 Notebook，剥离 Loom 凭据。Access 到期后，由平台页面 single-flight 刷新，再重复 Notebook 初始化并重试/打开；Notebook 页面不能获取平台 Refresh。平台注销无法跨主机删除 Notebook Cookie，但 family/用户版本撤销会阻止后续请求；已建立连接不主动断开。

跨站 Cookie 仍受浏览器第三方 Cookie 策略限制，SameSite=None 不保证能够写入。没有前端实现或实际网关/浏览器核验，不能宣称自动续期、跨源嵌入或完整访问链已经可用。

## 7. 网关对接契约（未安装）

仅实现服务端校验并不足以替代网关部署；需部署方按实际 Istio/Envoy 版本接入，不要直接覆盖全局 mesh 配置。示意 provider：

```yaml
extensionProviders:
  - name: loom-auth
    envoyExtAuthzHttp:
      service: <实际Loom服务FQDN>
      port: 8080
      pathPrefix: /verify
      timeout: 5s
      failOpen: false
      includeRequestHeadersInCheck:
        - host
        - authorization
        - cookie
        - origin
        - sec-fetch-site
        - sec-fetch-mode
        - sec-fetch-dest
        - upgrade
      headersToUpstreamOnAllow:
        - kubeflow-userid
        - oidc-sub
        - cookie
        - authorization
      headersToDownstreamOnDeny:
        - content-type
```

实际身份头名应与 Loom/profile-controller 配置一致。必须确认 check 中 Host/authority 保留原始 Notebook 主机，不能被 provider 内部地址替换；HTTP/2 authority 到 r.Host 的映射也需核对。Sec-Fetch-Mode/Dest 若被省略，独立站点顶层导航的例外无法成立。必须确认 headersToUpstreamOnAllow 在实际代理上执行覆盖，而不是追加或忽略；否则不能上线。check 响应头只用于改写 upstream 请求，不要作为浏览器可读响应透传。认证请求不需要发送登录密码/body，网关也不要记录 Cookie、Authorization 或密码字段。

网关 CUSTOM 策略应明确覆盖 `/notebook/*` 和需要保护的 `/api/v1/*`。`/verify` 的通用 API 分支拒绝 `/api/v1/auth/*`，因此必须精确排除登录、刷新、登出、全部登出、密码、设备列表/撤销和 Notebook 初始化/预检这些已登记的认证入口，让各 handler 执行自身校验；管理员设备撤销位于 `/users/...`，仍经过普通 API 校验。不能仅根据 Authorization 头决定是否排除，也不能把全部未知 auth 路径公开。独立 Notebook 主机只应向 Loom 路由精确的初始化 POST/OPTIONS，其余 `/api` 不应整段暴露；`/notebook/*` 走 Notebook 后端。平台与 Notebook 的浏览器侧响应不得覆盖这些 CORS/Set-Cookie 安全属性。登录页面静态资源需单独明确路由和策略。

内部 `/verify` 不应配置为公开 VirtualService 路由，也不能再次进入同一个 CUSTOM 流程。Loom 所在服务/Pod 的 NetworkPolicy、mTLS/来源控制必须保证只有预期网关可调用内部校验；仅写一个可信 CIDR 不构成来源证明。普通业务 API 可沿现有网关路径访问。

切换时必须成套审查现有 oauth2-proxy CUSTOM、require-jwt DENY、Dex RequestAuthentication 等策略：保留旧 require-jwt 会把新的会话请求拒绝；直接全局删除会影响其他组件和机器身份。此轮没有修改社区发行版配置、没有安装策略或扩大集群权限。

## 8. 配置示例与已知边界

默认不启用，启用时配置：

```yaml
auth:
  enabled: true
  issuer: https://loom.example.com/local
  audience: loom-api
  public_origin: https://loom.example.com
  notebook_origin: https://notebooks.example.com # 可选，不带末尾斜杠，且 hostname 必须不同
  access_ttl: 15m
  session_ttl: 360h
identity:
  issuer: ""
  userid_header: kubeflow-userid
  userid_prefix: ""
  subject_header: oidc-sub
  trusted_proxy_cidrs: ["<实际可信网关最小网段>"]
```

密钥单独由 `LOOM_AUTH_SIGNING_KEY` 注入：至少 32 字节密码学随机值的标准 Base64，多实例必须一致。不要提交真实值，不使用用户名/口令代替随机密钥。支持 `LOOM_AUTH_ENABLED/ISSUER/AUDIENCE/PUBLIC_ORIGIN/NOTEBOOK_ORIGIN/ACCESS_TTL/SESSION_TTL` 覆盖；身份头/网段沿用原配置。原 browser_ttl / LOOM_AUTH_BROWSER_TTL 已移除，应删除部署配置中的旧项，浏览器使用 Access 的期限。

HTTPS 是必要前提，不提供关闭 Secure Cookie 的开发后门。issuer 标识本地认证域，不表示已经实现 OIDC discovery、JWKS 或可直接让 Kubernetes API 信任此 JWT。密钥轮换当前会使旧 access 失效；仍有效的 refresh 可以获取新 access。滚动切换需要部署方协调，本阶段不实现多 key/kid 宽限。

### 8.1 独立执行器 mTLS listener（与浏览器认证分离）

`executor.enabled=true` 时，API 进程除普通 `server.addr` HTTP listener 外，还在 `executor.addr` 创建独立 TLS listener，仅挂载 `/internal/v1/ml-executions/...` 的 claim/renew/report/查询接口。它使用 `route.NewExecutor` 和证书认证，不挂到普通业务入口，不接受用户 Access、Refresh、身份头或代理提供的证书头。普通浏览器 HTTPS 仍由网关提供，不因启用执行器而自动变为 TLS。

配置键为 `executor.enabled/addr/server_cert/server_key/client_ca/client_cert_sha256`，对应环境变量 `LOOM_EXECUTOR_ENABLED/ADDR/SERVER_CERT/SERVER_KEY/CLIENT_CA/CLIENT_CERT_SHA256`。最后一项环境变量为逗号分隔 pin 列表，pin 是整个 DER 叶证书的 SHA-256（不是 SPKI），允许带冒号十六进制，多项用于轮换。配置校验要求地址非空且与 server.addr 字符串不同、证书/私钥/CA/非空 pin；实际证书文件和 pin 格式由启动时 NewTLSConfig 校验，失败不启动 listener。等价地址未做规范化排重，实际端口冲突由监听失败返回。

TLS 最低1.3、RequireAndVerifyClientCert、配置 CA 验链及叶证书 pin 双重限制；HTTP 中间件每次还重新验链/pin，每个机器 handler 再要求完成 TLS 握手、VerifiedChains 和认证上下文。必须端到端保留此 TLS 连接，不可由普通 HTTP 网关终止后伪造证书头。该端口还需 NetworkPolicy/防火墙保持私有。

当前 main 为两台 server 设置共享可取消的 BaseContext，在任一 listener 报错或收到停止信号时先取消请求上下文，再对两台 server 执行 Shutdown，共享一个新建后台关闭期限，超时则 Close，返回前等待监听 goroutine 退出。子上下文取消并不等于强行终止不响应取消的外部调用。本文未修改 main/route，未进行 mTLS 握手或关停验证。

### 8.2 源码审查交接边界

- 网关示例是对接契约，不是已安装策略。原始 Host/authority、Sec-Fetch-*、凭据剥离/CORS/Set-Cookie、浏览器 Cookie 策略均未运行核验。

安全边界必须说明：

- 新 HTTP 请求与 WebSocket 握手会检查撤权；已经建立的 WebSocket/终端连接不会被自动断开。
- 同一 namespace 仍是协作互信边界，default-editor 和其他原生权限可以绕过 Loom 页面过滤；只有网关访问链经过 `/verify`。
- 认证 Cookie 不进入 Notebook upstream，只是避免服务端直接读到凭据。未配置独立 NotebookOrigin 时仍与平台同 origin，不适合承载不可信 Notebook 内容。独立 hostname + host-only Cookie + 严格来源门禁用于隔离平台刷新与业务身份，但不等于各 Notebook 实例互相隔离：它们仍共享 Notebook origin/访问 Cookie，不能宣称实现实例级浏览器安全域或防止全部 XSS/CSRF 风险。
- 已实现设备 family 管理、强制首次/重置后改密及独立 Notebook Cookie 初始化的后端；仍没有前端、MFA、自助找回、邀请链接、全链路吊销已建立连接或完整 OIDC 服务。
