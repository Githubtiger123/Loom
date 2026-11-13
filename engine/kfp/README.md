# Loom 官方 KFP 内核边界

该模块只包装官方执行内核，不提供另一套 IR lowering 或 Python runtime。

## 当前入口

- `Inspect(IR)`：官方 protobuf 解码与规模/递归引用保护，不宣称全部语义可运行。
- `Compile(ctx, Request)`：真实调用官方 `argocompiler.Compile`，传入固定 digest 的 driver/launcher 镜像、参数和对象存储根路径；补充 Loom 身份、SA、并发限制，输出暂停的 typed Argo Workflow 与摘要。只读，不提交集群。
- `ExecuteComponent(...)`：调用官方 `component.NewLauncherV2(...).Execute(ctx)`，通过官方 `ClientManagerInterface` 注入 Kubernetes、metadata、cache 客户端。没有默认 MLMD 客户端，也没有伪造元数据的成功 stub。
- `Backend`：业务 operation 到执行系统的接口；当前没有注册实现。`Ready` 必须实际核验适配和部署依赖，而不是检查配置开关。

调用路径：官方 Python SDK → IR → Loom 版本 API → 本模块官方编译 → **待接：受控提交、官方 driver/launcher、Loom Runtime API、同一业务数据库**。

## 当前明确不能做

不能提交、恢复、停止或清理原生运行，不能因为编译成功就宣称支持循环/缓存/产物闭环。官方生成的 driver 参数仍包含官方运行时协议；只换镜像名称不会自动改成 Loom API。`ExecuteComponent` 是 Go 调用入口，不是已交付 launcher CLI 或镜像。

driver 多处依赖具体 `*metadata.Client`，不能像 launcher 一样直接替换接口。下一阶段应在固定上游版本上做可追踪的最小接口抽取，并让 driver/launcher 使用受运行级授权的 Runtime API 客户端。Runtime API 负责幂等元数据、输入解析、结果发布和缓存持久化，统一经平台 GORM/事务访问数据库；不能靠 Informer 事后补写替代。

## 发布前工作

1. 完成官方依赖与工具链锁定（版本/来源见 UPSTREAM.md），保留真实 go.sum，不跳过校验。
2. 抽取 driver metadata 接口及必要构造器；不重写调度、循环或表达式逻辑。
3. 接入受限 Runtime API、Pod 身份认证、元数据/缓存适配和存储授权。
4. 实现 Backend 的提交/UID 绑定/恢复/停止/清理与 Informer 节点投影；绑定冻结的 engine release。
5. 提供符合官方命令/参数契约的 driver/launcher CLI 与镜像、安装清单，再开放原生入口。
6. 另行授权真实闭环验证；当前按项目约定没有编译、测试或运行项目。

模块不依赖工作区旁边的 pipelines checkout；平台通过仓库内相对 replace 引用本模块，Docker/Compose 包含 engine/kfp。Python SDK 仍在用户编译端，不在浏览器/后端执行任意上传的 Python。
