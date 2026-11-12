# 模型与推理服务

- `/workspaces/:groupId/models`：模型、版本、文件地址登记、READY 与归档。
- Pipeline 运行产物面板 → `models/register-output`：只传 run/instance/artifact 标识，后端负责来源鉴权和归档转换；无需复制 URI。
- READY 版本产物 → `kserve-services/new`：绑定不可变 artifactId；不传 Secret 或任意 PodSpec。
- `kserve-services/:serviceId`：后台观测、配置修订发布/回滚、历史、停止/删除、v1/v2 真实推理。

## KServe 发布与诊断

- 创建／编辑表单自动发现空间和集群 ServingRuntime，按产物格式、版本、协议显示可用选项；同名空间 Runtime 遮蔽集群 Runtime，禁用／多模型／多节点不可选。原选择失效时保留并告警，不自动替换；自动选择的最终决策仍由官方控制器执行。
- 创建、编辑及历史修订提供手动发布前检查。检查结果逐项展示，修改表单即清除旧结果；需要 `kserve.read` + `kserve.create`，Registry 产物还需模型读取权限。检查不发布资源，也不验证存储读取、模型加载或真实推理；不替代后端发布时复核。
- 服务详情 → **诊断／日志**：按需展示 predictor Pod、模型存储初始化／加载状态、退出码、重启次数、受控资源事件。仅可见时每 10 秒刷新诊断，失败停止自动刷新；服务无需 Ready 即可诊断，但需 ACTIVE 空间和已绑定 CR UID。
- 日志需显式选择 Pod／容器，只读取 `storage-initializer` 或 `kserve-container` 的有界快照。支持当前／上一次退出、末尾 200／500／1000 行、最近 5／15／60 分钟，单次最多 256 KiB；上一次日志缺失不退回当前日志。Pod 消失不会自动切换副本，容器重启／修订或身份变化会清除旧快照。
- 诊断、事件和日志需要 `kserve.logs` 和对象范围权限；请先执行后端迁移 `000026_kserve_diagnostics.sql`。查询包含空间、身份、实例、修订、Pod UID／容器标识，不持久化这些内容，失败或撤权不继续展示缓存结果。

模块遵循空间权限、对象归属、CAS revision、用户意图幂等键、失败关闭陈旧数据和可见面板按需查询。Artifact 元数据不可变，替换采用新建＋归档；READY 只表示 Registry 可发布，不证明模型可加载。

安装配置、迁移和真实验收清单见 `platform/docs/model-registry.md` 与 `platform/docs/model-serving-acceptance.md`。本次仅源码实现与静态检查，不能把未执行的集群验收宣称已通过。
