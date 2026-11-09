# Pipeline 普通可视化接口

Loom 的普通可视化沿用 KFP v2 的产物约定。指标元数据来自 MLMD，文件型产物按 MLMD 事件关联后从固定 MinIO 读取；接口不会接受客户端提交的对象存储地址、对象 Key 或访问凭据。

## 接口

运行级产物：

```text
GET /api/v1/groups/{gid}/pipeline-runs/{rid}/visualizations?instanceId={instanceId}
```

节点级产物：

```text
GET /api/v1/groups/{gid}/pipeline-runs/{rid}/nodes/{nodeId}/visualizations?instanceId={instanceId}
```

返回 `items` 中每个产物的 `artifactId`、类型、关联节点和 `views`。`system.Metrics` 返回标量；`system.ClassificationMetrics` 返回 ROC 与混淆矩阵；`system.SlicedClassificationMetrics` 返回带切片名称的同样视图。MLMD 尚未就绪时返回明确的 `metadata.status`，不会伪造空指标。

列表使用统一的 `{ "data": ... }` 包装，示例：

```json
{
  "data": {
    "instanceId": "与 DAG 相同的执行实例 UUID",
    "revision": 3,
    "readAt": "2026-11-08T08:00:00Z",
    "metadata": { "status": "AVAILABLE" },
    "items": [{
      "artifactId": "128",
      "name": "evaluation",
      "type": "system.Metrics",
      "state": "LIVE",
      "nodeIds": ["execution-42"],
      "contentStatus": "NO_URI",
      "views": [{
        "kind": "SCALAR",
        "status": "AVAILABLE",
        "data": [{"name": "accuracy", "value": 0.93}]
      }]
    }]
  }
}
```

ROC 的 `data` 是保留官方字段的点数组：`confidenceThreshold`、`falsePositiveRate`、`recall`。混淆矩阵返回 `labels`、`rows` 和 `axes`，行表示真实类别，列表示预测类别。切片指标在视图上携带 `slice`。`contentStatus` 只描述文件读取能力，不影响已从 MLMD 获得的指标。

节点选择应传 DAG `instances[].id`（通常为 `execution-<MLMD ID>`）。静态 `definitions[].id` 不能唯一选择循环或重试的某个执行，接口返回空产物列表，不会任意挑选一个实例。

文件型产物需要使用列表返回的 `artifactId`：

```text
GET /api/v1/groups/{gid}/pipeline-runs/{rid}/artifacts/{artifactId}/preview?instanceId={instanceId}
GET /api/v1/groups/{gid}/pipeline-runs/{rid}/artifacts/{artifactId}/download?instanceId={instanceId}
```

节点级路径也支持同样的 `nodeId`。目前支持 HTML、Markdown、PNG/JPEG/GIF/WebP、JSON、CSV/TSV 和 UTF-8 文本。HTML 返回内容供前端放入无同源 iframe；后端不把 HTML 插入平台页面。Dataset/Model 只有在 `_kfp_preview` 声明符合 KFP v1/v2 固定格式时才读取预览文件。

预览返回 `{data: {instanceId, revision, artifactId, preview}}`：`preview` 包含 `kind`、`name`、`etag`、`mediaType`，文本在 `text`、结构化内容在 `data`；图片在 `text` 中返回 base64 并携带 `encoding: "base64"`。下载返回二进制附件，不使用 JSON 包装，也不直接返回 MinIO 凭据或预签名链接。

CSV/TSV 首行作为列名，最多显示 200 行、50 列，每格最多 4096 字符；有更多行时返回 `truncated: true`。一般文件预览上限 2 MiB，原文件下载上限 32 MiB，KFP Dataset/Model 预览上限 64 KiB。标量最多 1000 项；每条 ROC 最多 5000 点；混淆矩阵最多 100 类；分类切片最多 20 个，超限返回 `INVALID` 视图。

HTML 前端接入须遵循 `sandbox="allow-scripts"` 且不得加 `allow-same-origin`；不得使用平台页面的 `innerHTML`。Markdown 须在前端禁用原始 HTML 或经过可靠消毒。此轮后端返回报告内容，尚未加入实际 iframe 或渲染器。

## 读取与授权流程

1. 校验当前用户的组内 `pipeline.read` 和运行对象访问权；读取业务运行快照和 KFP 运行身份。
2. 查找经过运行上下文验证的 MLMD Execution、Event、Artifact；运行级按每批 100 个执行读取事件，节点级只读对应执行。上限为 2000 个执行、10000 个事件、4000 个产物及 16 MiB MLMD RPC 总预算，超限或依赖异常会明确标记元数据不可读。
3. 指标直接解析 MLMD 属性，兼容 KFP launcher 的 `list`/`struct` 包装，不解析 Pod stdout。
4. 文件读取必须已关联该 Run/节点，且 URI 为固定桶下的 `<artifact_store.prefix>/<namespace>/...`。同组缓存复用的历史产物可读；跨组路径不会因为缓存引用而获得权限。
5. 文件读取后复核业务权限、运行版本和执行实例，HTTP 层再复核会话。重建过程中的旧 `instanceId` 返回 409，客户端需刷新列表。

没有新增产物内容表、指标表或后台全桶扫描。运行级列表读取该 Run 的元数据；内容和下载按需读取单个对象。API 每进程最多 4 个可视化读取响应，直到内容发送结束才释放名额。

KFP 数据集/模型预览只支持生产者明确声明的固定位置：v1 `{version: 1, path: "_kfp_preview.json"}` 读取 `<uri>/_kfp_preview.json`；v2 `{version: 2, storage: "sibling"}` 读取 `<uri>.kfp-preview.json`。预览文件内容仍为 v1 dataset/model schema，分别提供采样行列、模型框架/超参数/结构摘要。后端不会加载模型、执行 Python 或猜测目录中的第一个文件。

## MinIO 配置

产物使用独立的 `minio-artifact-reader` 只读账号，权限限定为 `mlpipeline/loom/pipelines/*`，与 Argo 日志账号、KFP 写入账号和 root 账号分离。API 注入 `LOOM_ARTIFACT_STORE_*` 环境变量，配置样例见 `install/examples/artifact-store-api-pod-spec.yaml`。普通指标只依赖 MLMD；没有启用产物存储时仍可读取指标。

`artifact_store` 配置在 `platform/configs/config.yaml`，默认关闭。启用 `pipeline`、配置好 `metadata_endpoint`，再按部署样例为 API 打开 `artifact_store.enabled` 并注入凭据。修改桶或 pipeline root 时，需同步配置 `artifact_store.bucket/prefix` 和 `install/manifests/minio/bootstrap/artifacts-policy.json` 的权限范围。

## 边界

本接口暂不实现 TensorBoard、旧版 v1 UI metadata 生成器、TFDV/TFMA 渲染服务或前端图表。大对象、未声明的目录产物、被清理的产物和不在 Loom pipeline root 下的 URI 会返回受限或不可用状态。

文件型类型优先识别 `system.HTML`、`system.Markdown`，其他普通文件按 URI 扩展名识别。目录没有原文件下载打包能力；CSV 表格为有界样本预览，非完整数据检索。只支持配置好的 MinIO/S3，不支持任意 HTTP/GCS/PVC 来源代理。当前只读当前执行，未增加历史 attempt 的可视化恢复；执行已清理时不会绕过业务状态直接查全局 MLMD ID。

参考本工作区 `pipelines/sdk/python/kfp/dsl/types/artifact_types.py`、`frontend/src/components/viewers/MetricsVisualizations.tsx` 及 `frontend/src/lib/v2/ArtifactContentPreview.ts` 的数据约定。源码已实现，按项目要求未运行测试、编译、安装或实际集群联调。
