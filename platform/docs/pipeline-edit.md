# Pipeline 版本原地编辑与运行重建

本文是源码契约，未测试、编译、运行或执行迁移。部署需先显式应用 `000017_pipeline_edit.sql`（及此前迁移），停止旧 Pipeline 写入者，API 与 pipeline-worker 同步升级。前端不在此次范围。

## 版本编辑

`PUT /api/v1/groups/{gid}/pipelines/{pid}/versions/{vid}`，需要 `pipeline.version.update`、对象权限及 `Idempotency-Key`：

```json
{
  "revision": 1,
  "parentRevision": 2,
  "packageYaml": "<SDK 编译的完整单文档 KFP v2 IR>"
}
```

也可用 `pipelineSpec` JSON 替代 packageYaml。`revision` 是版本自身修订号，`parentRevision` 是 Pipeline 定义当前修订号；二者均校验并递增。版本 ID、名称保持不变，IR 内流程/节点/镜像/资源/参数定义一起替换，参数 schema 仍从 IR 提取。通过一个 GORM 事务更新当前版本并在 `tb_pipeline_version_revision` 保留每次内容。版本修改是同步元数据操作，不操作现有 KFP Run，不隐式编辑所有引用该版本的运行。

提交运行时可额外带 `versionRevision`，防止提交到刚被并发编辑的版本；省略则使用事务中读取的当前版本。Run 保存 versionId、versionRevision 和完整独立快照，后续修改版本不改变已提交实例。

`GET /api/v1/groups/{gid}/pipelines/{pid}/versions/{vid}/revisions?after=0&limit=20` 查看修订历史。返回 data 数组，after 是版本 revision，下一页使用本页最后一条 revision。

## 编辑运行：先清理旧实例，再保存为待启动

`PUT /api/v1/groups/{gid}/pipeline-runs/{rid}`，要求 `pipeline.update`、`pipeline.stop`、`pipeline.delete` 和运行对象权限：

```json
{
  "revision": 1,
  "versionId": 8,
  "versionRevision": 2,
  "parameters": [{"name":"epochs","value":"20"}]
}
```

选定版本必须属于该运行的 Pipeline。可额外提供 `packageYaml` 或 `pipelineSpec` 来修改本次运行的流程、节点、镜像、资源和参数定义；此时不改共享版本，只生成该运行的新配置快照。参数按新 schema 校验和补默认值。完整替换配置，不是 JSON Patch。版本字段用于明确来源，内联修改后的权威内容以运行快照/摘要为准。

1. API 校验配置，在事务中暂存 `pendingSpec`，递增运行 revision，将 desiredState 设为 STOPPED，排队 `pipeline.run.edit`，替代旧提交/停止/编辑操作。返回 202 仅代表受理。
2. worker 继续使用旧 `requestedSpec`、旧 instanceId 和提交日志核对旧 KFP Run。若提交回执不确定，按原确定性 ID 恢复，不允许误删其他运行。
3. 活动 Run 先 TerminateRun，等待终态，然后 DeleteRun，并确认 KFP Run 不存在。从未尝试提交的情况可直接确认没有外部实例。
4. 在同一个回填事务中，将旧执行快照保存到 `tb_pipeline_run_attempt`，应用 pendingSpec，清空 pendingSpec，设置 STOPPED 和 `executionCleaned=true`，关闭旧实例的独立状态同步。**不填写 deleted_at，不软删除业务记录，不自动启动。**

删除的是 KFP Run；Workflow/Pod 的最终回收仍由 KFP/Argo 完成，本实现不把 KFP 404 夸大为已经独立核实所有 Pod/PVC 消失。不是无损暂停，也不保证恢复进程内存或从某个节点续跑。

清理超时/失败时不应用 pendingSpec，也不能手动启动。可查询操作单并走已有操作重试；也可提交新 revision 的编辑，或显式停止/删除来替代编辑（取消未应用的新配置）。已经清理完成的业务运行可继续编辑保存，不重新创建旧实例。执行清理可在空间冻结/原用户撤权后继续收敛；这不授予新执行权限。启动仍必须重新检查当前授权及 ACTIVE 空间。

## 手动启动

`POST /api/v1/groups/{gid}/pipeline-runs/{rid}/start`，需要 `pipeline.run` 和当前运行/父定义对象权限：

```json
{"revision": 2}
```

只接受已完成编辑清理、无 pendingSpec、无活动操作且状态 STOPPED 的业务运行。普通 stop 不删除 KFP Run，因此不能直接通过此接口复用旧实例，需先完成编辑清理。

API 保留业务 rid 和名称，生成全新的 instanceId，清空旧外部 ID、观测/同步字段，递增 revision 并排队 `pipeline.run.submit`。worker 用新 instanceId 作为新的 KFP run_id 创建实例，旧 ID 永不复用。使用此前编辑保存的快照，不暗中切换为后来再次修改的共享版本。202 后继续查询操作单和状态。

`GET /api/v1/groups/{gid}/pipeline-runs/{rid}/attempts?after=0&limit=20` 返回旧执行快照 data 数组，after 是清理归档的业务 revision，下一页使用最后一条 revision。当前尚未清理的执行通过 Run/runtime 查看；历史操作和状态变化继续使用 history。待启动时 runtime 返回明确冲突，不再读取已经清理的旧 KFP Run。

## 安全边界

旧实例日志可用 attempts 的 instanceId 请求 [MinIO 离线日志接口](log-archives.md)，不依赖已删除的 KFP Run/Pod，不新增数据库日志索引。前提是采集端在清理前已上传日志和 manifest；当前编辑清理不等待归档完成，旧 attempts 仅保留业务身份和规格，不代表日志已归档。

执行命令与独立观测沿用实例/revision/租约/目标锁隔离。新实例启动后，旧实例的晚到回填不能覆盖新配置。编辑新配置以 pendingSpec 暂存，清理仍依据旧快照；不会用新配置摘要去匹配旧 KFP Run。只有确认外部删除的 STOPPED 观测才能完成编辑，普通终态/停止回执不足以完成。

新增权限通过显式迁移授予内置 group_owner/group_editor；不自动修改自定义角色，平台管理员仍走原有授权逻辑。
