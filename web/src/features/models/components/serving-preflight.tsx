import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Table } from '@atomus';
import { ErrorState, Loading, NoData } from '@/components/feedback';
import { registryApi, servingKeys } from '../api';
import type { ServingRequest } from '../types';

// The caller keys this component by form/snapshot identity. Editing cancels the
// old read and unmounts its result instead of showing a check for stale inputs.
export function ServingPreflight({
  g,
  identity,
  payload,
}: {
  g: string;
  identity: string;
  payload: () => ServingRequest;
}) {
  const [requested, setRequested] = useState(false);
  const query = useQuery({
    queryKey: servingKeys.query(g, 'preflight', identity),
    queryFn: ({ signal }) => registryApi.preflight(g, payload(), signal),
    enabled: false,
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  return (
    <Card title="发布前检查">
      <div className="stack">
        <p className="muted small">
          只检查 KServe API、执行身份、存储 Secret 关联和 Runtime
          能力，不创建资源，不读取模型文件，也不执行推理。修改配置后需重新检查；发布时后端仍会复核。
        </p>
        <div className="actions">
          <Button
            type="button"
            hierarchy="outline"
            loading={query.isFetching}
            onClick={() => {
              setRequested(true);
              void query.refetch();
            }}
          >
            {requested ? '重新检查' : '检查当前配置'}
          </Button>
        </div>
        {requested &&
          (query.isFetching ? (
            <Loading label="检查发布配置…" />
          ) : query.isError ? (
            <ErrorState error={query.error} />
          ) : query.isSuccess ? (
            <>
              <Alert
                color={query.data.ready ? 'success' : 'warning'}
                title={
                  query.data.ready
                    ? '元数据检查通过（不代表服务就绪）'
                    : '检查未通过，请先处理以下问题'
                }
              >
                存储访问：
                {query.data.storageAccess === 'NOT_VERIFIED' ? '未验证' : query.data.storageAccess}
                ；真实推理：{query.data.inferenceValidated ? '已验证' : '未验证'}。
              </Alert>
              <Table
                caption="配置检查结果"
                rows={query.data.checks}
                rowKey={(r) => r.code}
                empty={<NoData title="未返回检查项" />}
                columns={[
                  { key: 'code', header: '检查项', render: (r) => r.code },
                  { key: 'passed', header: '结果', render: (r) => (r.passed ? '通过' : '未通过') },
                  {
                    key: 'message',
                    header: '说明',
                    render: (r) => <span className="break-word">{r.message}</span>,
                  },
                ]}
              />
              <p className="muted small">
                匹配 Runtime：
                {query.data.runtimes
                  .map((r) => `${r.name}（${r.scope === 'NAMESPACE' ? '空间' : '集群'}）`)
                  .join('、') || '无／此类型无需 Runtime'}{' '}
                · {new Date(query.data.observedAt).toLocaleString()}
              </p>
            </>
          ) : null)}
      </div>
    </Card>
  );
}
