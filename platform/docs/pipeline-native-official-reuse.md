# 原生 Pipeline：官方优先复用改造

2026-11-12 更新：以下是 v2 阶段记录。新提交已改走 `loom-native-kfp-v3`，七项扩展及 CEL 官方复用见 [v3 说明](pipeline-native-v3.md)；旧 v2 仍保留重放。本轮仍未编译/运行验证。

日期：2026-11-11。本轮仅修改源码/文档，未编译、未运行测试、未部署，也未验证真实流水线。

## 1. 本轮实际替换的位置

新的原生预检和新 attempt 暂存统一走 `loom-native-kfp-v2`：

1. 官方 `pipeline_spec.pb.go` + `protojson` 解析 IR，先检查字段、类型和 oneof；不再仅凭 Loom 手写 JSON 结构认定 IR 有效。
2. 使用抽取的官方 `compiler.AcceptSpec` / Visitor，解析组件引用与 executor，按子组件先于 DAG 的顺序访问。
3. Visitor 的组件结果交给 Loom 的有界 lowering 适配器，直接读取官方 protobuf 字段及输入来源 oneof，生成现有原生执行计划及 Argo Workflow。新路径不再调用旧手写 IR 解析器；端口限制仍复用 Loom 第一版策略检查。
4. 组件仍执行 SDK 编译出的原始命令；普通 Python 函数的加载、类型转换与调用仍由官方 `kfp.dsl.executor_main` 负责。
5. 参数文件读取和整数结果恢复改用官方本地执行工具中抽取的函数，并保留 Loom 边界检查。
6. Argo 文件传输、Informer 观测、Loom SQL 登记和权限体系不变。

源码出处、精确 revision、许可证和改动清单见
[`UPSTREAM.md`](../internal/thirdparty/kfp/UPSTREAM.md)。生成代码未手改；没有引入完整 KFP Go module 或第二套服务。

## 2. 哪些是直接复用，哪些仍是适配

| 部分 | 当前处理 |
| --- | --- |
| IR protobuf 类型及 oneof | 官方生成源码原样复制 |
| 组件引用/executor 遍历 | 官方 visitor，增加已解析 spec 的入口 |
| Python 用户函数执行 | 沿用 SDK 原命令，不另写 Python 函数执行器 |
| OutputPath 参数读取、整数恢复 | 官方函数抽取；去掉 protobuf Python 运行依赖，增加严格类型检查 |
| 支持范围、参数绑定计划、Argo 模板 | Loom 有界适配器，**不是官方完整 argocompiler** |
| 输入值解析、executor input 组装、文件摘要/bundle | 仍是 Loom launcher 适配，**不是官方完整 driver/launcher** |
| 存储授权、租户隔离、UID、操作事务、状态入库 | Loom 自有业务逻辑 |

这是一次明确的官方代码接入，不应描述为“已完整移植 KFP 执行内核”。后续应继续从官方可分离的解析/执行模块抽取能力，而不是另起一套语义。

## 3. 本轮修正的语义偏差

- `OutputPath` 布尔文件接受 `true/false` 和 `True/False`，与官方读取规则一致。
- 同名参数同时出现在 executor JSON 与 OutputPath 文件时，按官方规则由文件覆盖。
- protobuf number 导出的整数型结果恢复为 Python int；额外拒绝布尔值和小数被静默截断成整数。
- IR 中多个互斥输入来源/组件实现，由官方 protobuf oneof 拒绝。
- deploymentSpec 本来是 protobuf Struct；在调用 visitor 前另做严格 executor 解析，不沿用官方 `DiscardUnknown: true` 吞掉未知执行配置。
- integer literal 经 protobuf Value 的 double 表示会丢精度时，返回 `KFP_NUMBER_PRECISION_LOSS`，不静默改变常量或默认值。十进制/指数写法沿用 double 语义。
- 未使用组件、保留名 `root` 和非平铺组件在新协议中明确拒绝；不能借官方 visitor 的宽松行为默默放行。

## 4. 历史执行兼容

保留 `loom-native-static-v1` 和 `loom-native-python-v1` 的解析/launcher。恢复已有 attempt 时仍按 snapshot 中的 compilerVersion 编译与核验，不把旧计划切到新规则。

新预检、新 attempt 使用 `loom-native-kfp-v2`，Workflow annotation、计划摘要和 snapshot 中均记录该版本。新版本不会重写历史 Run、数据库结构或旧 KFP engine。

新旧 Python 协议复用现有输出 bundle 和数据库结构，不新增表。已冻结规格中的 launcher 文本不变，不能因重启而产生不同规格摘要。

## 5. 当前范围与剩余工作

第一版范围仍是普通 Python 组件、标量参数、单文件产物、基本平铺 DAG、日志和数值指标。接入官方 schema 并不等于自动支持 schema 中所有字段。

循环、条件、缓存、自动重试、目录产物、在线停止、自动日志归档均未因此完成；仍应在预检拒绝不支持的语义。不能调用官方完整 argocompiler 生成依赖 driver/MLMD 的模板，再删几个容器冒充可执行。

按项目约定，本轮只做格式化、源码对照和差异检查。依赖解析、类型编译、单元/集成测试及真实运行验收均未进行；开关默认保持关闭。部署启用前需要另行授权验证。
