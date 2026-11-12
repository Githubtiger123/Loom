# 模型发布联调验收

## 当前状态

本次补充了前后端源码和配置/迁移，尚未执行迁移、编译或启动新版本。遵循仓库“不新增/运行测试、不编译/运行项目”的既有约束，仅做源码检查、格式化与只读集群检查。dev 的 Rancher API 在本次只读 kubectl 检查中连接超时，因此以下真实验收未通过、也未创建任何真实推理服务。

已完成静态检查：新增模型模块、修改的路由与 Pipeline 产物面板通过 ESLint；Go 文件通过 gofmt 语法/格式处理，Git diff 空白检查通过。这些不能替代类型检查、编译或真实端到端验收。

## 升级前检查

- 显式迁移到 `000026_kserve_diagnostics.sql`（包含之前的 000025），同步升级 API、通用 worker、kserve-worker 和前端；应用更新的机器 RBAC（Runtime 只读、API 专属 services/proxy）。
- API、worker 配置一致的 artifact_store/model_store；只读产物凭据、模型写凭据、KServe 读凭据相互独立。不要把密钥填到前端或版本描述。
- `kserve.enabled=true`，kserve-worker 正常领取任务；Standard Runtime 支持所选格式和协议，Runtime/Storage Initializer 镜像可拉取。
- 先检查 `/kserve-runtimes` 与 `/kserve-preflight`；后者只验证元数据，`ready=true` 不是存储已读取或推理成功。配置可选 `kserve.storage_secret` 时，要求相同 namespace 的模型执行 SA 关联该 Secret。
- namespace 的 `kserve.service_account`、关联读取 Secret、endpoint、region、TLS、网络策略均正确。系统组件和任务运行 ServiceAccount 不等于模型读取凭据已就绪。
- worker 有最多 10 GiB 的临时压缩包空间和足够带宽；模型输出布局匹配 Runtime，例如 sklearn `model.joblib`、TensorFlow SavedModel 完整目录。

可以先做只读检查（替换 namespace/service，不输出 Secret 内容）：

```bash
kubectl --context dev get clusterservingruntimes.serving.kserve.io
kubectl --context dev get servingruntimes.serving.kserve.io -n <namespace>
kubectl --context dev get inferenceservices.serving.kserve.io -n <namespace>
kubectl --context dev get serviceaccount <configured-service-account> -n <namespace>
kubectl --context dev get pods -n kserve
```

## 正向链路

1. 在验收工作空间登记一个模型，创建 DRAFT 版本并登记事先核验可读的模型 URI。至少一个 ACTIVE 产物后才允许 READY。
2. 选择该产物创建服务，保存操作编号。确认操作排队 → CR 创建 → storage initializer 成功 → 模型容器加载成功 → generation 被观测、Ready=True。不要仅以 202 作为成功。
3. 服务“推理验收”填入该模型真实格式的 v1/v2 输入，检查预期预测值，不只是 HTTP 成功。例如 Iris 输入四个数值时检查分类结果是否与训练模型一致。
4. 用同一模型经过原生 Pipeline 训练产生 FILE 与 DIRECTORY 产物各一次；产物面板注册版本，确认来源 run/instance/artifact 不变、文件转换操作成功、目录结构正确、Registry URI 不指向 tar.gz。
5. 对导入版本标记 READY，发布并做同样真实推理。核对缓存命中来源也只能来自同 group/namespace UID。
6. 保存不同资源配置修订，确认线上未变化；显式发布新修订后查看 appliedRevision，再选择旧配置修订回滚。原模型归档后历史配置不能再次发布。

## 异常/权限链路

- 错误凭据/不可读前缀：storage initializer 报错；服务不得展示就绪或推理成功。修复存储权限后重新发布并验证。
- `/diagnostics` 展示当前发布中/失败 Pod 的 storage-initializer 与 kserve-container 状态及 UID 事件；按返回的 Pod UID、容器名和当前业务 revision 调 `/logs` 查看快照，不能读取 sidecar 或其他空间 Pod。
- 不兼容权重、错误文件名、错误框架版本：模型加载失败；应保留可定位的发布提示，不以“Registry READY”掩盖错误。
- 错误推理形状/协议：接口明确失败，不能返回伪预测；超时不自动重试。
- 同一意图重复点击复用幂等键；并发编辑返回 409 且不清空表单、不自动覆盖。
- 归档最后产物使 READY → DRAFT；归档版本/模型级联元数据，不删文件、不影响已运行服务。
- 导入中撤权/归档/编辑版本：最终不能注册或覆盖新版本；失败任务可观测，未完成对象前缀不被引用。
- Viewer 或撤权用户不能创建/发布/invoke；不能通过猜测跨组 run、artifact、service 或旧 operation ID 越权。
- 停止服务确认 CR 删除后才 STOPPED；重新发布会绑定新 UID，旧实例推理响应被丢弃。
- 发布/回滚期间旧 revision Ready 不得回填；保存配置草稿不影响已发布服务；Deployment observedGeneration 只与 Deployment generation 比较。

## 验收记录

联通后记录：验收 group/namespace、模型/版本/产物 ID、导入 operation、服务/配置 revision、CR UID、加载状态、预期预测与实际预测、权限用例结果；凭据和敏感推理内容不要写入仓库。
