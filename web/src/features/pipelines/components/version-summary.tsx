import { useQuery } from '@tanstack/react-query';
import { Alert, Card, Table } from '@atomus';
import { pipelineApi, pipelineKeys } from '../api';
import type { Compatibility, Version } from '../types';
import { QueryState } from './query-state';

export function CompatibilitySummary({ report }: { report: Compatibility }) {
  return (
    <div className="stack">
      <Alert
        color={report.compilable && report.runnable ? 'info' : 'warning'}
        title={
          report.compilable
            ? report.runnable
              ? '原生引擎已配置，尚需实际运行验证'
              : 'IR 可编译，原生执行尚未就绪'
            : '此版本包含原生引擎暂不支持的内容'
        }
      >
        {report.executionReason} · {report.taskCount} 个任务 · {report.dependencyCount} 条依赖
      </Alert>
      {!!report.issues?.length && (
        <ul>
          {report.issues.map((issue, index) => (
            <li key={index}>
              <code>{issue.path}</code>：{issue.code}
            </li>
          ))}
        </ul>
      )}
      {report.issuesTruncated && <p className="muted">问题列表已截断。</p>}
    </div>
  );
}
export function VersionSummary({
  groupId,
  pipelineId,
  version,
}: {
  groupId: string;
  pipelineId: string;
  version: Version;
}) {
  const compatibility = useQuery({
    queryKey: pipelineKeys.query(
      groupId,
      'compatibility',
      pipelineId,
      version.id,
      version.revision,
    ),
    queryFn: ({ signal }) => pipelineApi.compatibility(groupId, pipelineId, version, signal),
    retry: false,
  });
  return (
    <>
      <Card title="版本包">
        <dl className="details-grid">
          <dt>修订</dt>
          <dd>r{version.revision}</dd>
          <dt>SHA256</dt>
          <dd className="mono break-word">{version.spec.packageDigest}</dd>
          <dt>引用</dt>
          <dd>{version.spec.packageRef}</dd>
        </dl>
      </Card>
      <Card title="原生引擎兼容性">
        <QueryState query={compatibility}>
          {(report) => <CompatibilitySummary report={report} />}
        </QueryState>
      </Card>
      <Table
        rows={version.spec.parameters ?? []}
        rowKey={(row) => row.name}
        caption="输入参数定义"
        columns={[
          { key: 'name', header: '参数名' },
          { key: 'type', header: '类型' },
          { key: 'required', header: '必填', render: (row) => (row.required ? '是' : '否') },
          {
            key: 'default',
            header: '默认值',
            render: (row) => (row.type === 'CREDENTIAL' ? '受管引用' : (row.default ?? '—')),
          },
        ]}
      />
    </>
  );
}
