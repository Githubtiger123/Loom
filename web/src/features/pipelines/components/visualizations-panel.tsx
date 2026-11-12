import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Table } from '@atomus';
import { ErrorState, NoData } from '@/components/feedback';
import { useMounted } from '@/lib/use-mounted';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { pipelineApi, pipelineKeys } from '../api';
import type { Preview, Run, View } from '../types';
import { QueryState } from './query-state';

export function VisualizationsPanel({ groupId, run }: { groupId: string; run: Run }) {
  const permissions = useGroupPermissions(groupId);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'visualizations', run.id, run.instanceId, run.revision),
    queryFn: ({ signal }) => pipelineApi.visualizations(groupId, run, signal),
    gcTime: 0,
    retry: false,
  });
  return (
    <div className="stack">
      <div className="actions">
        <h2>指标与产物</h2>
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新
        </Button>
      </div>
      {run.engine === 'NATIVE_ARGO' && (
        <p className="muted small">
          原生引擎当前支持数值 Metrics 与文件／目录产物。分类指标、HTML
          等预览仅在后端实际返回该格式时展示，不代表原生编译器支持这些产物类型。
        </p>
      )}
      <QueryState query={query}>
        {(data) =>
          data.items?.length ? (
            data.items.map((artifact) => (
              <Card
                key={artifact.artifactId}
                title={artifact.name || `产物 ${artifact.artifactId}`}
                supportingText={`${artifact.type} · ${artifact.state} · 内容：${artifact.contentStatus}`}
              >
                <div className="stack">
                  {(artifact.views ?? []).map((view, index) => (
                    <MetricView key={index} view={view} />
                  ))}
                  <ArtifactAccess
                    groupId={groupId}
                    run={run}
                    artifact={artifact.artifactId}
                    enabled={artifact.contentStatus === 'ON_DEMAND'}
                  />
                  {run.engine === 'NATIVE_ARGO' &&
                    artifact.contentStatus === 'ON_DEMAND' &&
                    permissions.can('model.read') &&
                    permissions.can('model.version.create') &&
                    permissions.can('model.artifact.create') && (
                      <Link
                        className="text-link"
                        to={`/workspaces/${groupId}/models/register-output?runId=${run.id}&instanceId=${encodeURIComponent(run.instanceId)}&artifactId=${encodeURIComponent(artifact.artifactId)}`}
                      >
                        注册模型版本
                      </Link>
                    )}
                </div>
              </Card>
            ))
          ) : (
            <NoData
              title="暂无指标或产物"
              description="节点成功写出并完成元数据同步后会出现在这里。"
            />
          )
        }
      </QueryState>
    </div>
  );
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}
function MetricView({ view }: { view: View }) {
  if (view.status === 'ON_DEMAND') return null;
  if (view.status !== 'AVAILABLE')
    return (
      <Alert color="warning" title={`${view.kind} · ${view.status}`}>
        {view.message || '没有可显示的数据。'}
      </Alert>
    );
  if (view.kind === 'SCALAR' && Array.isArray(view.data)) {
    const metrics = view.data
      .map(record)
      .filter(
        (value): value is Record<string, unknown> =>
          !!value &&
          typeof value.name === 'string' &&
          typeof value.value === 'number' &&
          Number.isFinite(value.value),
      );
    return metrics.length ? (
      <div className="pipeline-metrics">
        {metrics.map((metric, index) => (
          <div className="pipeline-metric" key={index}>
            <span className="muted break-word">{String(metric.name)}</span>
            <strong>
              {(metric.value as number).toLocaleString(undefined, { maximumSignificantDigits: 8 })}
            </strong>
          </div>
        ))}
      </div>
    ) : (
      <p className="muted">尚无数值指标。</p>
    );
  }
  if (view.kind === 'ROC' && Array.isArray(view.data)) {
    const points = view.data
      .map(record)
      .filter(
        (point): point is Record<string, unknown> =>
          !!point &&
          typeof point.falsePositiveRate === 'number' &&
          typeof point.recall === 'number' &&
          Number.isFinite(point.falsePositiveRate) &&
          Number.isFinite(point.recall) &&
          point.falsePositiveRate >= 0 &&
          point.falsePositiveRate <= 1 &&
          point.recall >= 0 &&
          point.recall <= 1,
      );
    if (points.length)
      return (
        <figure className="pipeline-chart">
          <figcaption>
            ROC{view.slice ? ` · ${view.slice}` : ''}（横轴 FPR，纵轴 Recall，0–1）
          </figcaption>
          <svg viewBox="0 0 420 280" role="img" aria-label="ROC 曲线">
            <path className="pipeline-axis" d="M40,20 V240 H400" />
            <path className="pipeline-axis pipeline-diagonal" d="M40,240 L400,20" />
            <polyline
              className="pipeline-curve"
              points={points
                .map(
                  (p) =>
                    `${40 + Number(p.falsePositiveRate) * 360},${240 - Number(p.recall) * 220}`,
                )
                .join(' ')}
            />
            <text x="25" y="260">
              0
            </text>
            <text x="390" y="260">
              1
            </text>
            <text x="20" y="25">
              1
            </text>
          </svg>
          <details>
            <summary>查看原始点</summary>
            <pre className="pipeline-code">{JSON.stringify(points, null, 2)}</pre>
          </details>
        </figure>
      );
  }
  if (view.kind === 'CONFUSION_MATRIX') {
    const matrix = record(view.data);
    const labels = matrix?.labels;
    const rows = matrix?.rows;
    if (
      Array.isArray(labels) &&
      labels.every((label) => typeof label === 'string') &&
      Array.isArray(rows) &&
      rows.every(
        (row) =>
          Array.isArray(row) &&
          row.length === labels.length &&
          row.every((v) => typeof v === 'number' && Number.isFinite(v)),
      )
    ) {
      return (
        <div className="pipeline-table-scroll">
          <table className="pipeline-matrix">
            <caption>
              混淆矩阵{view.slice ? ` · ${view.slice}` : ''} · 行：真实类别，列：预测类别
            </caption>
            <thead>
              <tr>
                <th scope="col">类别</th>
                {labels.map((label, index) => (
                  <th key={index} scope="col">
                    {label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i}>
                  <th scope="row">{labels[i] ?? i}</th>
                  {(row as number[]).map((value, j) => (
                    <td key={j}>{value}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    }
  }
  return (
    <details>
      <summary>
        {view.kind}
        {view.slice ? ` · ${view.slice}` : ''} 原始数据
      </summary>
      <pre className="pipeline-code">{JSON.stringify(view.data, null, 2)}</pre>
    </details>
  );
}

export function ArtifactAccess({
  groupId,
  run,
  artifact,
  enabled,
}: {
  groupId: string;
  run: Run;
  artifact: string;
  enabled: boolean;
}) {
  const [preview, setPreview] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState<unknown>();
  const mounted = useMounted();
  const controller = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => controller.current?.abort(), []);
  const query = useQuery({
    queryKey: pipelineKeys.query(
      groupId,
      'artifact-preview',
      run.id,
      run.instanceId,
      run.revision,
      artifact,
    ),
    queryFn: ({ signal }) => pipelineApi.preview(groupId, run, artifact, signal),
    enabled: enabled && preview,
    gcTime: 0,
    retry: false,
  });
  async function download() {
    if (controller.current || !enabled) return;
    const current = new AbortController();
    controller.current = current;
    setDownloading(true);
    setError(undefined);
    try {
      const file = await pipelineApi.download(groupId, run, artifact, current.signal);
      if (!mounted.current || current.signal.aborted) return;
      const url = URL.createObjectURL(file.blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = file.name;
      anchor.rel = 'noopener';
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (reason) {
      if (mounted.current && !current.signal.aborted) {
        setPreview(false);
        setError(reason);
      }
    } finally {
      controller.current = undefined;
      if (mounted.current) setDownloading(false);
    }
  }
  return (
    <div className="stack">
      <div className="actions">
        <Button
          hierarchy="outline"
          size="sm"
          disabled={!enabled}
          onClick={() => setPreview((value) => !value)}
        >
          {preview ? '收起预览' : '读取预览'}
        </Button>
        <Button
          hierarchy="outline"
          size="sm"
          disabled={!enabled}
          loading={downloading}
          onClick={() => {
            void download();
          }}
        >
          下载产物
        </Button>
        <span className="small muted">在线读取最多 32 MiB；目录按归档文件下载，不能直接预览。</span>
      </div>
      {error !== undefined && <ErrorState error={error} />}
      {enabled && preview && (
        <QueryState query={query}>
          {(result) =>
            result.preview ? (
              <ArtifactPreview preview={result.preview} />
            ) : (
              <NoData title="无预览内容" />
            )
          }
        </QueryState>
      )}
    </div>
  );
}

function ArtifactPreview({ preview }: { preview: Preview }) {
  if (preview.kind === 'HTML' && typeof preview.text === 'string')
    return (
      <div className="stack">
        <p className="muted small">隔离预览：不允许脚本、表单或外部资源。</p>
        <iframe
          className="pipeline-html"
          title={preview.name}
          sandbox=""
          referrerPolicy="no-referrer"
          srcDoc={`<!doctype html><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'none'; base-uri 'none'">${preview.text}`}
        />
      </div>
    );
  if (
    preview.kind === 'IMAGE' &&
    preview.encoding === 'base64' &&
    ['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(preview.mediaType) &&
    /^[A-Za-z0-9+/=\s]*$/.test(preview.text ?? '')
  )
    return (
      <img
        className="pipeline-image"
        alt={preview.name}
        src={`data:${preview.mediaType};base64,${preview.text}`}
      />
    );
  if (preview.kind === 'TABLE') {
    const table = record(preview.data);
    if (
      table &&
      Array.isArray(table.columns) &&
      table.columns.every((v) => typeof v === 'string') &&
      Array.isArray(table.rows) &&
      table.rows.every((r) => Array.isArray(r) && r.every((v) => typeof v === 'string'))
    ) {
      const columns = table.columns as string[];
      const rows = (table.rows as string[][]).map((cells, index) => ({ index, cells }));
      return (
        <div className="stack">
          <Table<{ index: number; cells: string[] }>
            rows={rows}
            rowKey={(row) => String(row.index)}
            caption={preview.name}
            columns={columns.map((name, index) => ({
              key: String(index),
              header: name,
              render: (row) => row.cells[index] ?? '',
            }))}
          />
          {table.truncated === true && <p className="muted">表格预览已截断，可下载完整内容。</p>}
        </div>
      );
    }
  }
  return (
    <div className="stack">
      {preview.kind === 'MARKDOWN' && <p className="muted small">Markdown 以安全源码方式显示。</p>}
      <pre className="pipeline-code" tabIndex={0}>
        {preview.text ?? JSON.stringify(preview.data, null, 2)}
      </pre>
    </div>
  );
}
