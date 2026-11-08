# 当前用户空间权限快照

`GET /api/v1/groups/{gid}/me/permissions` 为前端导航与操作入口提供当前用户的组内权限，不需要成员管理权限，也不能查询其他用户。

```json
{
  "data": {
    "userId": 12,
    "groupId": 3,
    "permissions": ["group.read", "notebook.read", "notebook.connect"],
    "evaluatedAt": "2026-11-08T10:00:00Z"
  }
}
```

调用者必须拥有该空间 group.read，空间必须存在。普通用户通过 GORM 聚合有效角色绑定，复用 HasPermission 的用户 ACTIVE、绑定 ACTIVE/有效期、角色 enabled 以及权限/角色/绑定 scope 一致条件。有效平台管理员沿用既有 Checker 的管理员行为，返回所有已定义的 group 权限。全局权限不混入该列表。

该结果不是授权凭证，客户端不能提交它替代认证。它不表达对象负责人、空间是否可启动、原生访问是否就绪、配额或目标 revision；所有业务接口仍各自校验。权限修改或到期后旧页面可能暂存旧快照，但不能凭快照执行已被撤销的命令。返回响应沿用认证中间件的 Cache-Control: no-store，不新增权限缓存或数据库表。

本接口只做源码级核对，未运行、编译或测试。
