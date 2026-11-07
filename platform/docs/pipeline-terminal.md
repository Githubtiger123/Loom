# Pipeline 在线终端

本次仅实现后端源码、权限迁移和接入契约；没有新增测试，没有测试、编译、运行项目或执行迁移，也没有修改实际集群 RBAC。不是已完成联调的功能。

## 业务流程

用户选择 Run 的 runtime 节点 → Loom 检查身份、组角色 `pipeline.exec`、`pipeline.read` 和运行对象权限 → 用已核实的 KFP taskId 解析 Pod → 检查 Pod → Workflow UID → KFP Run 归属、原 Profile/namespace 身份、正在运行的容器 → 保存准入审计 → 用 API 机器 ServiceAccount 调用 Kubernetes `pods/exec` → WebSocket 代理终端。

- 直接建立长连接，不经过 worker，不创建新 Run 或调试 Pod，不暂停原任务。
- 只能访问当前执行；不接受任意 namespace、Pod 名称、历史 instanceId、命令数组或集群地址。读取他人/无主 Run 仍需要 `pipeline.read.any`，有 exec 但无对象访问权不放行。
- exec 和 logs 权限独立：终端不要求 `pipeline.logs`；仅有日志权限不能执行命令。
- 空间必须 ACTIVE，运行 desiredState 必须 RUNNING，不能带 pendingSpec。实际 Pod 必须 Running、未在删除，目标必须是状态为 Running 且具有 containerID 的普通容器。不支持已退出、init 或临时容器，不创建 ephemeral container。
- 节点定位复用在线日志逻辑：KFP 有 podName 时使用它；缺失时按受管 launcher 的 run_id/execution_id 在本次 Run 的 Pod 中唯一匹配。缓存、逻辑节点、尚未启动或无法唯一定位的重试节点返回冲突，不能猜 Pod。
- 终端固定 instanceId、revision、Pod UID 和 containerID。打开前再次检查；持续约每 10 秒复核当前身份/权限、空间、执行版本、Pod/Workflow 归属和容器。撤权、停用、停止/编辑/删除、Pod 替换或容器重启后结束旧连接，不自动跳转另一 Pod。
- 本地认证重新校验原 Access JWT/会话，过期、注销、强制改密后断开。使用原 token 的会话不接受帧内刷新；客户端刷新后重新建连。
- Kubernetes exec URL 没有 Pod UID/containerID 前置条件，校验只能缩小检查与执行之间的竞态，不能提供原子 UID 绑定或瞬时撤权保证。已执行命令不会回滚。

## 权限及升级

先显式执行 `internal/migration/000019_pipeline_exec.sql` 和此前未应用迁移，再部署新版后端。迁移增加组内 `pipeline.exec`，授予内置 group_owner/group_editor，不自动升级自定义角色，不授予 group_viewer；平台管理员沿用既有授权规则。不新增终端表，不启动时 AutoMigrate。

Kubernetes API 机器身份需在目标 namespace 具备：

| 资源 | 动词 | 用途 |
|---|---|---|
| pods | get；兼容节点定位时另需 list | 核实 Pod、容器与节点关联 |
| pods/exec | get、create | WebSocket GET，旧传输兼容 POST |
| workflows.argoproj.io | get | 核实 ownerReference UID 和 pipeline/runid |
| namespaces | get | 原执行空间 UID 校验 |

这里的 Kubernetes RBAC 是机器调用权限，不能代替 Loom 组内权限。不能因为某用户被授予组内 exec，就把 Kubernetes 机器 token 发给浏览器。

**exec 是执行能力，不是只读诊断。** 用户拥有容器内既有 Unix 身份，能接触容器可访问的环境变量、挂载凭据、ServiceAccount token、文件和网络，并可能影响运行结果、结束进程、持久修改共享卷。未增加 root/sudo/特权能力，但也不是命令沙箱。部署方必须控制工作负载的 SA、Secret、挂载和网络边界。Argo wait 等普通 sidecar 若被选择，也按相同规则授予执行能力，应谨慎分配组角色。

只审计 `pipeline.exec.open` 的 ACCEPTED 和关闭原因；目标记录 business Run ID、instanceId、Pod UID、容器，不保存 Access JWT、键盘输入或终端输出。ACCEPTED 表示 Loom 准入，不代表上游 shell 已成功启动。准入审计失败不执行命令；关闭审计失败写服务错误日志。

## WebSocket 接口

```text
GET /api/v1/groups/{gid}/pipeline-runs/{rid}/tasks/{taskId}/terminal
    ?container=main&shell=%2Fbin%2Fsh&width=80&height=24
Sec-WebSocket-Protocol: loom.terminal.v1
Origin: <平台页面来源>
```

- container 默认 main；shell 默认 `/bin/sh`，也可显式选 `/bin/bash`。不会自动切换 shell，不把 shell 字符串拼进 `sh -c`。镜像必须实际包含所选 shell；distroless 镜像不能凭空进入终端。
- width/height 默认 80/24，各自限制 1–1000。所有参数拒绝未知项、重复或空值；URL 中禁止传 token，也不通过 WebSocket 子协议传凭据。
- 必须携带单个 Origin。认证启用时与 `auth.public_origin` 完全匹配；可信头模式要求与请求 Host 匹配且不是 cross-site。网关必须保留实际外部 Host 并按现有契约验证身份，不信任客户端自行提供的身份/转发头。

### 本地 Access JWT 模式

浏览器原生 `new WebSocket()` 不能自定义 Authorization 头，因此此路由专门处理首帧认证，不经过普通 HTTP Bearer 头中间件：

1. 同源 WebSocket 升级完成；HTTP 101 **不代表业务认证或 shell 已启动**。
2. 客户端在 5 秒内发送 JSON 文本帧：

```json
{"type":"auth","token":"<现有 Access JWT>"}
```

3. 服务端校验 Access、SQL 设备会话、用户状态和强制改密，再执行组/对象授权和目标解析；此之前绝不调用 pods/exec。
4. 收到 ready 后才能发送输入或 resize。认证失败返回 error 帧并关闭；不是新增 ticket 或第三类凭据，也不使用 Refresh/Notebook Cookie。

凭据只放在首帧，通过 WSS 传输；生产必须 HTTPS/WSS。网关/APM 不得采集 WebSocket 帧正文。连接期间保留原 Access 仅用于重复校验，不落库、不审计。

### 可信认证代理模式

仍经过现有 `Authenticate` 的可信代理 CIDR、Issuer/Subject/用户头校验；浏览器握手身份由网关注入。该模式不发送 auth 首帧，收到 ready 后发送终端消息；不能借选择此协议绕过代理身份检查。

### 消息协议

客户端 → 服务端均为 JSON 文本帧：

```json
{"type":"stdin","data":"ls -la\r"}
{"type":"resize","width":120,"height":36}
{"type":"close"}
```

stdin 的 data 可以包含 Ctrl-C 等控制字符，不能是空字符串；大粘贴需拆分。单消息最多 16 KiB（包括 JSON 转义开销）。不接受未知字段/类型、二进制输入或重复 auth。

服务端 → 客户端：

| 帧 | 内容 |
|---|---|
| JSON 文本 ready | type、pod、podUID、container、shell、maxSeconds；表示已准入，随后上游启动仍可能失败 |
| 二进制输出 | 首字节 `1`，后续是原始 TTY 字节；单帧输出最多 16 KiB |
| JSON 文本 exit | `{"type":"exit","exitCode":0}`，shell 正常结束或返回非零退出码 |
| JSON 文本 error | type、平台 code、message；随后关闭；不暴露上游响应体、API 地址或凭据 |
| ping/pong/close 控制帧 | 保活/关闭；浏览器自动响应协议 ping，不需实现自定义 pong 消息 |

TTY 模式合并 stdout/stderr。浏览器对输出剥离首字节后使用流式 UTF-8 解码，不能逐帧独立解码，否则汉字可能乱码。前端终端模拟器需自行控制危险的终端转义序列、超链接等，避免将输出当 HTML 渲染。本次不做前端。

HTTP 升级前的输入/Origin/握手限流错误返回普通平台 JSON；升级后均用上述协议，不向 WebSocket 连接追加 HTTP JSON。浏览器若异常断连未收到 exit，不能据此判断任务成功。

## 生命周期与连接限制

- 每进程最多 64 个正在升级/认证的连接；首帧最多等待 5 秒，认证查询最多 5 秒。
- 已认证终端每进程最多 128、每用户 4 个（独立于日志额度）。复用现有限流组件；多 API 副本的总限额由网关控制。
- 会话总长最多 15 分钟（或 Access 更早过期）；连续 5 分钟无 stdin 输入断开，输出、resize、pong 不延长空闲时间。
- 建立上游 exec 流限时 15 秒；优先 Kubernetes WebSocket v5，只在握手升级/HTTPS 代理错误时兼容回退 SPDY POST，不对已经开始的命令或非零退出码重试。拒绝跟随上游重定向。
- 每约 10 秒核对授权，单次检查最多 5 秒；每轮发协议 ping，45 秒未读到 pong 结束。网络、慢写、并发和检查延迟会影响实际断连时刻，不是瞬时撤权保证。
- 单次会话 stdin 最多 4 MiB、输出最多 64 MiB，普通写入限时 10 秒；输入平均最多 100 条/秒、突发 200，超限断开。窗口尺寸队列只保留最近值，stdin 使用有界管道，不在内存堆积全量会话。
- 浏览器关闭、超时、撤权、上游结束或 API 基础 context 取消时，取消 exec、关闭输入管道和 WebSocket。HTTP Server 的 Shutdown 不自动管理已升级 socket，源码显式关联 API 生命周期。
- 断开仅关闭 exec 连接，**不保证终止用户已启动的后台进程或其副作用**。不会为了“关闭终端”删除 Pod，也不会调用 KFP stop。普通终端关闭不修改 Pipeline 运行状态。

## 代码边界

- `internal/k8s/podexec`：公共 UID/containerID 校验、shell 和 Kubernetes remotecommand 传输，不识别 Pipeline/用户/数据库。
- `internal/k8s/pipeline_terminal.go`：绑定公共执行组件。
- `internal/service/pipeline_terminal.go`：组角色/对象/空间身份/节点归属检查、审计及复核闭包。
- `internal/handler/pipeline_terminal.go`：首帧认证、WebSocket 协议、流量和生命周期。
- 路由仍统一在 `internal/route/route.go`，没有新增第二套路由目录或注册文件。

未包含独立 Debug Pod、单节点 KFP Run、镜像 shell 注入、文件上传/下载、Notebook/Trainer/KServe 的终端业务接口。公共组件可供后续模块复用；不能直接暴露成任意 Pod exec 入口。
