# 平台管理前端接入契约

本阶段接入用户、角色、设备会话和审计页面，新增三个只读接口与两处可选并发字段。不修改数据库结构、权限字典、既有路由组织或认证模式。

## 新增读取接口

| 路由 | 权限与返回 |
| --- | --- |
| GET `/api/v1/users/{uid}` | 全局 user.read；User 字段加 localAccount 布尔值，不返回凭据 |
| GET `/api/v1/users/{uid}/roles` | 全局 role.manage；用户全部有效全局 Binding，含 id/version/roleId/roleCode/expiresAt |
| GET `/api/v1/users/{uid}/auth/sessions?limit=50&after={family}` | 本地 Bearer 认证和全局 user.manage；同认证域有效会话元数据，沿用 AuthSessionPage |

localAccount 由 `a.auth != nil && user.issuer == auth.Config.Issuer` 决定，仅用于 UI 提示，不代替 ResetPassword / UserStatus 的后端检查。角色读取复用 effective 条件：用户 ACTIVE、角色启用、绑定 ACTIVE 且未到期。失效历史绑定不混入有效授权编辑表单。

管理员会话读取复用现有 `tb_auth_session` 查询，以认证域、目标用户、最新 session_version、未撤销和未过期条件筛选，按 family ID 分页。请求经过 Authenticate + Wrap；事务授权锁内再次检查操作者当前账号、强制改密门禁、当前 family 状态以及 user.manage。目标必须属于当前本地认证域。

响应仅包含 familyId/current/createdAt/lastRefreshedAt/expiresAt 和 nextAfter；current 只在目标是操作者且 family 匹配时为 true。不返回 Access、Refresh、摘要、IP 或 User-Agent。after 必须是规范 UUID，limit 1–200；不能用通用数值 after 分页解析器解析 family 游标。

## 可选并发保护

### 用户状态

现有 enable/disable 请求可以携带读取到的 authorizationVersion：

```json
{
  "subject": "verified-stable-subject",
  "externalIdentityConfirmed": true,
  "expectedAuthorizationVersion": 7
}
```

UserStatus 在事务内锁定目标用户后比较版本，不匹配返回 409。既有外部身份确认、稳定主体不可迁移、最后管理员/Owner、活跃 ML 工作负载和资源清理规则不变。旧客户端省略新字段继续按原契约处理。

### 自定义角色定义

现有 PUT role 请求仍提交完整定义，可加 `expectedVersion`。服务端锁定 Role 后核对版本，不匹配返回 409；不默默覆盖他人的权限变更。编码和范围仍不可改，内置角色仍不可编辑。旧客户端省略该字段仍兼容。

### 全局角色绑定

沿用上阶段的 `expectedBindings: [{id,version}]`。GET user roles 提供固定编辑基线，PUT 完整替换该用户全局有效角色；空 roles 表示撤销。管理员角色不可设置有效期，最后一个有效平台管理员仍受事务保护。此操作不修改任何组内授权。

## 前端工作流

- `/admin/users`：分页用户目录，筛选仅覆盖已加载记录；`user.manage` 不隐含 `user.read`。
- `/admin/users/new`：本地账号由服务端生成稳定主体，创建后单独设置初始密码；外部身份默认未核验、可登记为 PENDING，不代建 IdP 账号或按邮箱合并。
- `/admin/users/{uid}`：稳定身份、业务状态、本地密码设置入口、目标用户有效会话。最近活动沿用 lastLoginAt；因 `/me` 会更新该字段，不将它描述成精确设备登录事件。
- `/admin/users/{uid}/roles`：显式确认用户 ID，整体替换全局角色与有效期；修改自己后返回空间列表。
- `/admin/roles` 及 new/edit：查看内置角色、管理自定义全局/空间角色，权限点按模块分组，不接受通配符。
- `/account/sessions`：自己的有效设备、单设备撤销、全部退出、改密入口；外部身份模式明确引导到身份提供方，不调用本地 session API。
- `/admin/audits`：按现有 ID 正序分页展示审计，不伪装成全库搜索。关联操作单使用原 Operation 接口重新授权，audit.read 不代表读取所有操作单。

密码重置复用现有同步接口，不创建新的凭据种类或密码操作表。浏览器不将密码放入查询、mutation variables 或业务幂等指纹；成功/取消清空表单，不持久化。网络失败可能是结果不确定，禁止自动重试。目标旧会话失效并在下次登录强制改密，不修改外部 SSO 密码。

## 边界

没有实现全库用户/审计筛选、审计导出、用户软删除、OIDC/MFA、自助找回、统一失败操作运维台或真实部署。这些不应由基础页面推断成已完成。

仅完成源码、格式化和文档核对；按用户要求没有测试、类型检查、编译、启动或迁移。
