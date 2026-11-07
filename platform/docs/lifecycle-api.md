# 空间、目录、额度与撤权生命周期

本文对应源码契约，未进行测试、编译、运行或迁移验证。路由统一由
`internal/route/route.go` 注册。除查询外，请求必须带可见 ASCII、1–64 字符的
`Idempotency-Key`；同操作者同键同请求返回原操作，同键不同请求冲突。
返回 `202`/`PENDING` 表示持久受理，不代表集群已经执行成功。

## 空间

| 请求（前缀 `/api/v1/groups/{gid}`） | 权限 | 请求体 / 行为 |
| --- | --- | --- |
| `POST /disable` | `group.disable` | `{"revision":2,"reason":"暂停使用"}` |
| `POST /restore` | `group.restore` | `{"revision":3,"reason":"恢复使用"}` |
| `DELETE /`（实际无尾斜杠） | `group.delete` | `{"revision":4,"confirmName":"space-name","reason":"回收空空间"}` |
| `GET /quota` | `group.quota.read` | 请求值、已观测值、实际 hard/used、未反映占用、剩余值 |
| `PUT /quota` | 全局 `group.quota.update` | `{"revision":2,"quota":{"cpu":"8","memory":"16Gi","gpu":1},"reason":"管理员批准"}` |

空间操作要求当前 revision、已确认 Profile/namespace UID，且没有未完成空间操作。
停用从 ACTIVE/DEGRADED 进入 DISABLING：立即阻断新增和连接，保留数据，持久提交
Notebook 停止任务、成员访问 none；执行器确认后才变 DISABLED。
恢复仅允许 DISABLED，先 RESTORING，确认有效成员访问后 ACTIVE；不自动启动 Notebook。

默认拒绝非空空间删除，包括未删除 Notebook、目录、历史未解除 PVC 引用、未释放预算、
Pipeline 定义/运行、训练、推理记录。历史版本、执行回执、审计不会被顺带删除。
Pipeline 运行须先确认终态并归档，再归档其定义；Trainer、KServe 须通过各自删除接口处置。
归档不删除外部 KFP 记录，也不能靠删 namespace 绕过。执行器还须完整检查原生资源，未知对象、PVC、运行任务、
身份/ownerReference 不匹配、无法完整列举均阻断。禁止强拆未知 finalizer。
按 UID 前置条件删除 Profile，namespace 确认消失后才写 DELETED/deleted_at。

`group.disable/restore/delete/quota.update` 的 target 为 `group/<groupId>`，
`target_version=revision`。payload 类型为 `service.GroupLifecycleCommand`：

```json
{"groupId":1,"instanceId":"group-uuid","cluster":"default","namespace":"space-name","profileUID":"uid","namespaceUID":"uid","revision":3,"desiredState":"DISABLED"}
```

恢复 desiredState=ACTIVE；删除 desiredState=DELETED 且 requireEmpty=true。
额度操作不含 desiredState，附 `quota`。执行前后核对身份和 revision；过期 SUPERSEDED。

## 额度与运行预算

CPU/内存额度必须为可精确转换的正值，GPU 非负；不接受清空 hard。
管理员调整先更新 quota_requested/revision，持久提交 `group.quota.update`，
实际 Profile/ResourceQuota hard 确认一致才完成。ACTIVE/DISABLED 可调整，不能降至
实际 used 加并发待执行占用以下。提额尚未实际生效时不能借用新增额度。

`QuotaReader.ReadGroupQuota` 在事务外读取已验证 namespace 的实际额度；不可在数据库锁内
调用集群，不接受旧缓存冒充实时观测。快照带 group revision、namespace UID、读取开始时间，
受理事务内再次核对，超过 30 秒拒绝。集群不可读时拒绝新的预算申请；幂等重放不依赖集群在线。

Notebook create/start 已接入事务内 `ReserveNotebookBudget`；start 使用 `notebook.start`，
不额外要求 create 权限。目录关联、实例、预算和操作单一起提交/回滚。预算按操作单幂等，
空间行锁串行申请；HELD 与快照期间刚 ACCOUNTED 的额度一起扣除，允许保守重复计入，不能超售。
ResourceQuota 仍是集群最终硬约束，不承诺跨系统原子分配。

执行器在实际 Pod 已计入 ResourceQuota used 后调用 `Store.AccountNotebookBudget(operationID)`；
不能仅凭 Notebook CR 创建完成确认。确认对应版本及更早 Pod 停止/消失后才调用
`Store.ReleaseNotebookBudget(notebookID, throughRevision)`，均在空间锁内。失败、超时或 TTL
不能自动释放。新版本规格全量占用，旧占用必须经过确认才能释放。

## 工作目录与负责人

工作目录是独立资产，实例删除默认保留目录。容量是 PVC capacity；usedBytes 未接入可信文件系统
观测时为 null，不用 capacity 冒充实际已用量。列表只显示本人目录，持有 workspace.manage.any
可跨对象管理。所有写接口仍需对应动作权限。

| 请求（前缀 `/api/v1/groups/{gid}/workspaces`） | 权限 | 请求体 |
| --- | --- | --- |
| `GET /`、`GET /{wid}` | `workspace.read` | 列表支持 after/limit（1–200） |
| `POST /{wid}/attach` | `workspace.attach` | `{"revision":1,"notebookId":2,"notebookRevision":3}` |
| `POST /{wid}/detach` | `workspace.attach` | 同上 |
| `POST /{wid}/owner-transfer` | `workspace.transfer` | `{"revision":1,"userId":4,"reason":"数据交接"}` |
| `DELETE /{wid}` | `workspace.delete` | `{"revision":2,"confirmName":"pvc-name","releaseRetention":true,"reason":"已确认备份"}` |

关联/解除要求实例已确认停止（解除也可为已确认删除），且没有待执行实例操作。
关联验证空间、对象权限、ACTIVE 有效成员、容量、独占归属和 PVC UID，禁止隐式替换已有目录。
返回 runtimeApply=ON_NEXT_START：只改变下次启动配置，不宣称已改原生挂载。
创建 Notebook 可附 `workspaceId` 与 `workspaceRevision`，必须同时提供，事务内复用目录；
仍须提供有效 workspaceSize。目录不能被其他实例引用，包括尚未安全解除的历史引用。

Notebook 负责人转移入口为
`POST /api/v1/groups/{gid}/notebooks/{nid}/owner-transfer`，请求为
`{"revision":<ownerRevision>,"userId":4,"reason":"实例交接"}`；需要 notebook.transfer，
非本人对象另需 notebook.transfer.any。仅停止后的实例可转移，目标为本空间 ACTIVE 有效成员。
createdBy 保留历史，ownerUserId/ownerRevision 表示当前负责人；mine 列表与对象权限按当前负责人。
不移动空间或 PVC、不自动交接数据。数据负责人未交接时，新负责人不能启动读取旧目录；
关联实例需先转给同一目标且已停止，或安全解除关联，才能转目录负责人。

删除目录要求独占身份已确认、无关联/历史引用、名称二次确认，显式解除 retained 保护。
Notebook deleteWorkspace=true 不能绕过登记目录的保留保护。
受理后 DELETING，action=`workspace.delete`，target=`workspace/<workspaceId>`，
target_version=目录 revision，payload=`service.WorkspaceDeleteCommand`：

```json
{"workspaceId":1,"groupId":1,"instanceId":"native-pvc-instance-id","groupInstanceId":"group-uuid","cluster":"default","namespace":"space-name","namespaceUID":"uid","pvcName":"pvc-name","pvcUID":"uid","revision":3}
```

执行前重新核对 DB 引用、原生 Notebook/Pod 挂载、管理标记、namespace/PVC UID；
只按 UID 删除该 PVC，不能删 namespace/PV。确认 PVC 消失才写 DELETED/deleted_at。
登记 InstanceID 来自 PVC 原 annotation（新目录初始为原 Notebook InstanceID），不随机替换。

## 用户禁用、成员移除与 ML

用户禁用保留绑定历史，立即使业务权限失效并增加授权/会话版本；成员移除撤销该空间全部角色。
最后有效管理员/Owner 保护仍生效。两者不擅自转移资源、不删除数据，持久停止当前负责人名下
Notebook，并等待访问 none 落实。恢复用户/重加成员不自动启动 Notebook。
外部 IdP 账号不由本接口自动处置，result.identityProviderChanged=false。

当前策略对 live ML **阻断而非自动停止**：空间停用检查全空间，用户禁用检查该用户所有空间，
成员移除检查该用户当前空间；有运行中、排队中、未完成操作、停止未确认或观测不明的
PipelineRun/TrainerJob/KServeService 时整个事务拒绝。先调用各模块 stop 接口并确认后重试。
训练/运行可保留已确认成功/失败终态；推理失败不代表已停止。查询失败/缺表不会按零资源放行。

## 撤权父操作领取与聚合契约

协调者仅需把以下 action 加入常规可调度状态筛选，再调用
`Runner.resourcePolicy(ctx, claim, guard)`；不要领取 SUCCEEDED 的角色替换：

| action | target_type / target_key | cluster | 产生条件 |
| --- | --- | --- | --- |
| `user.DISABLED` | `user` / `<userId>` | Service.Cluster；空值回退 default | 用户禁用，PENDING |
| `member.roles.replace` | `user` / `<groupId>/<userId>` | 所属 Group.Cluster；空值回退 Service.Cluster/default | 空间角色集合替换为空，PENDING |

非空替换以及全局角色替换仍是 SUCCEEDED，不是资源策略父操作。
首次 step=WAIT_RESOURCE_POLICY；后续可以为 WAIT_POLICY_NOTEBOOK_STOPS 或
WAIT_POLICY_ACCESS_REVOKED，领取时不能只限定首次 step。领取仍须 status 为 PENDING/RETRYING
且到调度时间，或 RUNNING 且租约过期。cluster 是父协调执行器归属，不限定子操作 cluster；
用户跨集群停止由各子操作的 cluster 执行，父仅查询持久结果，不调用集群。

payload=`service.ResourcePolicyCommand`，schemaVersion=1：

```json
{"schemaVersion":1,"userId":2,"userVersion":7,"groupId":1,"stopOperationIds":["stop-operation-uuid"],"accessTargets":[{"groupId":1,"desiredVersion":5}],"dataPolicy":"RETAIN"}
```

用户禁用不含 groupId，改为 groupIds（与 accessTargets 空间集合完全一致）。
用户禁用的 userVersion 是 SessionVersion，成员移除的是 AuthorizationVersion，
父 target_version 必须等于 userVersion。queueResourcePolicy 同事务记录这些 fence，
同时涵盖用户历史遗留访问记录。旧无版本 payload 不猜测补全，明确 FAILED 后重新受理。

聚合器只读取指定 StopOperationIDs，核对 notebook.stop 的版本化身份和 deleteWorkspace=false。
仅 SUCCEEDED 算停止回执；PENDING/RUNNING/RETRYING 等待，FAILED 返回可退避重试错误，
SUPERSEDED 让父 SUPERSEDED，不能当作成功。不会自动重试子任务、生成 start 或释放预算。

撤权要求当前访问集合和 desiredVersion 与 payload 一致，desiredLevel=none、
appliedVersion=desiredVersion、observedLevel=none、status=READY 且存在观测。
FAILED/DRIFTED/conflict 默认阻断，不能假称完全撤权。固定基础设施 Owner 有一个窄例外：
当前用户 username 必须等于该空间 InfrastructureOwner，访问行必须 DRIFTED、desiredLevel=none、
observedLevel=conflict、appliedVersion=desiredVersion 且有观测，并且 last_error 为固定 Owner 保留提示。
同版本最新 `access.reconcile` 操作还必须 FAILED/EXTERNAL_ACCESS_REMAINS、有完成时间，
其结果与当前空间/用户/等级一致，并明确包含
`warningCode:"INFRASTRUCTURE_OWNER_ACCESS_RETAINED"` 和 `infrastructureOwnerOnly:true`。
只有此情形可完成父操作，返回 warnings、fullyRevoked=false、infrastructureOwnerAccessRetained=true，
步骤为 RESOURCE_POLICY_CONFIRMED_WITH_WARNING；警告同时保留在操作及审计原因中。
父操作不修改 FAILED 的 access 操作或 DRIFTED 行，不把系统权限保留冒充完全撤权。

**观测生产者契约：** 适配器必须区分预期固定 Owner 系统对象与其他 RB/AP/CRB 授权，
仅在完整观测确认不存在其他额外授权时产生上述结构化标记。不能用“username 是 Owner”
替代核验，也不能在发现额外授权后用固定 Owner 文案覆盖证据。旧版仅有覆盖后 warning 文本
的回执缺少证明，仍阻断；不能靠字符串相同自动兼容放行。worker/access.go 将可信观测标记
原样持久化到操作 result，父聚合只读持久回执、不在数据库锁内查询集群。

Owner AuthorizationPolicy 按原 dashboard Profile Controller 的四类完整规则核验：
Owner 身份、同 namespace 互访、固定探针路径，以及默认 notebook-controller 的 kernel 查询规则。
策略中每条规则都必须匹配预期完整结构；不能仅因其中一条匹配 Owner 就忽略其他额外 ALLOW。
自定义 notebook-controller principal 暂不自动兼容，保守报告 DRIFTED 并阻断父操作；
需显式核对部署配置并更新受信规则后重新观测，不能靠忽略漂移完成撤权。

子操作/访问失败先修复并重试相应操作；父可使用现有操作重试接口恢复检查。
普通错误按主 Runner 有界重试，等待超过 deadline 则 FAILED，不无限挂起。

父在授权锁与租约 fence 的短事务中重读用户、访问版本和子结果，并原子写成功与审计。
用户恢复/再次禁用、成员重加/再次移除、相关版本或目标集合变化均 SUPERSEDED，绝不把旧策略
完成写成新策略已落实。版本检查采用保守策略：无关授权变化也可能让成员父操作过期，需重新受理。
父成功只证明这些持久停止回执和当前撤权投影；不证明外部 IdP 已禁用或所有既有连接已终止。
