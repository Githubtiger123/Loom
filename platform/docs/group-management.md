# 空间管理与成员编辑契约

本阶段为前端空间管理接入现有接口，并添加两处可选的并发保护。所有写接口仍要求认证、当前有效权限和 Idempotency-Key；后端保留最后负责人、空间状态、集群身份、工作负载及数据资产校验。没有新表、迁移或 AutoMigrate。

## 元数据 PATCH

`PATCH /api/v1/groups/{gid}` 要求 `group.update`，返回 Operation。displayName 与 description 改为可选字段，至少提供一个。省略不修改，空字符串表示清空。旧客户端同时提交两个字符串的用法继续接受。

新客户端提交读取时的完整 expected 元数据，并只提交改变字段。例如仅修改展示名：

```json
{
  "displayName": "研发团队",
  "expected": { "displayName": "旧名称", "description": "现有描述" }
}
```

事务内锁住 Group，比较数据库当前名称和描述；不匹配返回 409，不强制覆盖。比较成功仅更新提交字段，仍应用名称 128 字、描述 1000 字及删除状态校验。不递增用于集群执行器的 group.revision，避免展示字段变更淘汰集群任务。expected 可选以兼容已有调用；不携带的旧客户端没有这一并发保护。

## 完整替换成员角色

`PUT /api/v1/groups/{gid}/members/{uid}/roles` 要求 `group.member.manage`，仍然完整替换该用户的组内角色，不是增量合并。

成员有效 roles 中每个 Binding 额外返回数据库已有的 `version`。新客户端带读取时的完整有效绑定集合：

```json
{
  "roles": [{ "roleId": 3, "expiresAt": "2027-01-01T00:00:00Z" }],
  "expectedBindings": [{ "id": 42, "version": 5 }]
}
```

事务内持有用户/空间锁后，重新查询有效绑定，按 ID 和 version 校验集合相等，不受数组顺序影响；重复 ID、数量不同或版本不一致均返回 409。绑定行在撤销/重新授予时可能复用原 ID，因此不能仅比较 ID。

- 添加未授权用户：`expectedBindings: []`，仅在当前没有有效角色时接受。
- 编辑已有成员：提交完整旧绑定快照与新的完整 roles。
- 移除成员：同一 PUT 提交 `roles: []` 和旧快照，复用原来的最后负责人保护、活跃 ML 检查、Notebook 停止、数据保留与访问撤销流程。
- 旧客户端省略 expectedBindings 继续工作；原 DELETE 成员接口保持不变，不自动拥有新并发保护。
- 同一幂等请求的重放沿用既有 mutate 的请求摘要核对和当前权限检查，不重复授权；网络不确定失败不能改键盲目重发。

快照只包含当前有效绑定；角色失效、到期或用户停用都可能使快照变化。Owner 不允许设有效期，普通角色到期时间必须在未来。业务角色变更与原生 access 落实是两个阶段，不能把成员保存成功理解成 Kubernetes 授权已生效。

## 页面接入与权限

| 前端入口 | 后端能力 / 权限 |
| --- | --- |
| `/spaces/new` | POST groups；全局 group.create |
| `/g/{gid}/settings` | GET/PATCH group；group.read / group.update |
| 空间生命周期确认 | disable / restore / DELETE；对应 group 权限、revision 与 reason |
| `/g/{gid}/members` | GET members；group.read |
| 添加/编辑/移除成员 | GET roles、PUT roles；group.member.manage |
| 授权落实页签 | GET access，按 userId 分页；group.member.manage |
| `/g/{gid}/members/transfer` | POST owner-transfer；group.owner.transfer |
| `/g/{gid}/quota` | GET quota；group.quota.read |
| `/g/{gid}/quota/edit` | PUT quota；全局 group.quota.update，revision 与 reason |

负责人交接默认保留当前操作者的 group_owner，仅在明确取消时撤销操作者自己的该角色；不修改其他角色、不转移 Notebook 归属、不修改 Profile.spec.owner。创建和交接均不引入第二套身份系统。

配额界面使用可信集群 snapshot 的 hard/used，加上业务 reserved/remaining，不把它们命名为实时利用率。集群无法读取时明确报错，不降级成零用量。成员列表与独立 access 记录分开，已移除用户的撤权状态仍能分页查询。

以上仅完成源码修改、格式化与文档核对；按用户约束，未运行测试、类型检查、编译、迁移或项目。
