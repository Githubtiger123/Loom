# 模型与推理服务

- `/workspaces/:groupId/models`：模型、版本、文件地址登记、READY 与归档。
- Pipeline 运行产物面板 → `models/register-output`：只传 run/instance/artifact 标识，后端负责来源鉴权和归档转换；无需复制 URI。
- READY 版本产物 → `kserve-services/new`：绑定不可变 artifactId；不传 Secret 或任意 PodSpec。
- `kserve-services/:serviceId`：后台观测、配置修订发布/回滚、历史、停止/删除、v1/v2 真实推理。

模块遵循空间权限、对象归属、CAS revision、用户意图幂等键、失败关闭陈旧数据和可见面板按需查询。Artifact 元数据不可变，替换采用新建＋归档；READY 只表示 Registry 可发布，不证明模型可加载。

安装配置、迁移和真实验收清单见 `platform/docs/model-registry.md` 与 `platform/docs/model-serving-acceptance.md`。本次仅源码实现与静态检查，不能把未执行的集群验收宣称已通过。
