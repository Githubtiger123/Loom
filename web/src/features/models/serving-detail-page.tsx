import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Table, Tabs } from '@atomus';
import { ErrorState, NoData, PageHeading, StatusBadge } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { OperationDialog } from '@/components/operation-dialog';
import { useCommandKey } from '@/lib/use-command-key';
import { useOperationNavigation } from '@/lib/use-operation-navigation';
import { registryApi, servingKeys, servingPath, noAccess } from './api';
import type { ServingService } from './types';
import { ModelBoundary, type Access } from './components/boundary';
import { PageControls, QueryState } from './components/query-state';
import { RegistryActionDialog, type RegistryAction } from './components/registry-action';

export function ServingDetailPage() {
  const { serviceId = '' } = useParams();
  return (
    <ModelBoundary permission="kserve.read">
      {(g, group, access) => (
        <ServiceDetail
          key={`${g}/${serviceId}`}
          g={g}
          id={serviceId}
          access={access}
          writable={group.status === 'ACTIVE'}
        />
      )}
    </ModelBoundary>
  );
}
function ServiceDetail({
  g,
  id,
  access,
  writable,
}: {
  g: string;
  id: string;
  access: Access;
  writable: boolean;
}) {
  const query = useQuery({
    queryKey: servingKeys.query(g, 'service', id),
    queryFn: ({ signal }) => registryApi.service(g, id, signal),
    retry: false,
    gcTime: 0,
    refetchInterval: (q) => (q.state.error ? false : 5000),
  });
  const [tab, setTab] = useState('overview');
  const { operation, setOperation } = useOperationNavigation();
  return (
    <QueryState query={query}>
      {(row) =>
        !access.allows('kserve.read', row, 'kserve') ? (
          <ErrorState error={noAccess()} />
        ) : (
          <div className="stack spacious">
            <ServiceHeading g={g} row={row} access={access} writable={writable} />
            <Tabs
              value={tab}
              onChange={setTab}
              items={[
                { value: 'overview', label: '发布状态' },
                { value: 'revisions', label: '配置修订' },
                { value: 'history', label: '发布历史' },
                { value: 'predict', label: '推理验收' },
              ]}
            />
            {tab === 'overview' && <ServiceOverview row={row} g={g} />}
            {tab === 'revisions' && (
              <RevisionPanel g={g} row={row} access={access} writable={writable} />
            )}
            {tab === 'history' && <HistoryPanel g={g} row={row} />}
            {tab === 'predict' &&
              (access.allows('kserve.invoke', row, 'kserve') && writable ? (
                <PredictPanel
                  key={`${g}/${row.instanceId}/${row.revision}/${access.data?.userId}`}
                  g={g}
                  row={row}
                />
              ) : (
                <ErrorState error={noAccess()} />
              ))}
            {operation && (
              <OperationDialog id={operation} groupId={g} onClose={() => setOperation(undefined)} />
            )}
          </div>
        )
      }
    </QueryState>
  );
}
function ServiceHeading({
  g,
  row,
  access,
  writable,
}: {
  g: string;
  row: ServingService;
  access: Access;
  writable: boolean;
}) {
  const [action, setAction] = useState<RegistryAction>();
  const base = `/workspaces/${g}/kserve-services/${row.id}`;
  return (
    <>
      <PageHeading
        eyebrow="KSERVE"
        title={row.name}
        description="查看 KServe 观测状态、发布配置与真实推理结果。"
        actions={
          <div className="actions">
            <Link className="text-link" to={`/workspaces/${g}/kserve-services`}>
              返回服务
            </Link>
            {!row.deletedAt && writable && access.allows('kserve.update', row, 'kserve') && (
              <Link className="text-link" to={`${base}/edit`}>
                保存配置修订
              </Link>
            )}
            {!row.deletedAt &&
              row.desiredState !== 'STOPPED' &&
              access.allows('kserve.stop', row, 'kserve') && (
                <Button
                  hierarchy="outline"
                  onClick={() =>
                    setAction({
                      title: '停止服务',
                      description: '将删除集群中的 InferenceService，保留业务记录和配置修订。',
                      path: `${servingPath(g, row.id)}/stop`,
                      body: { revision: row.revision },
                      destination: base,
                      destructive: true,
                    })
                  }
                >
                  停止
                </Button>
              )}
            {!row.deletedAt && access.allows('kserve.delete', row, 'kserve') && (
              <Button
                hierarchy="outline"
                onClick={() =>
                  setAction({
                    title: '删除服务',
                    description: '确认集群资源删除后再归档服务业务记录。模型文件不会删除。',
                    path: servingPath(g, row.id),
                    method: 'DELETE',
                    body: { revision: row.revision },
                    destination: base,
                    destructive: true,
                  })
                }
              >
                删除
              </Button>
            )}
          </div>
        }
      />
      {action &&
        access.allows(
          action.method === 'DELETE' ? 'kserve.delete' : 'kserve.stop',
          row,
          'kserve',
        ) && (
          <RegistryActionDialog groupId={g} action={action} close={() => setAction(undefined)} />
        )}
    </>
  );
}
function ServiceOverview({ row, g }: { row: ServingService; g: string }) {
  return (
    <div className="stack">
      <Card title="发布状态">
        <dl className="details-grid">
          <dt>业务状态</dt>
          <dd>
            <StatusBadge status={row.status} />
          </dd>
          <dt>期望 / 观测</dt>
          <dd>
            {row.desiredState} / {row.observedState}
          </dd>
          <dt>KServe Ready</dt>
          <dd>
            {row.kserveReady ? 'True' : 'False'} · generation {row.kserveObservedGeneration}/
            {row.kserveGeneration}
          </dd>
          <dt>业务修订 / 已应用</dt>
          <dd>
            r{row.revision} / r{row.appliedRevision}
          </dd>
          <dt>当前发布配置</dt>
          <dd>#{row.configRevisionId}</dd>
          <dt>最近成功同步</dt>
          <dd>{row.kserveSyncedAt ? new Date(row.kserveSyncedAt).toLocaleString() : '尚未同步'}</dd>
          <dt>最近同步尝试</dt>
          <dd>
            {row.kserveSyncAttemptedAt ? new Date(row.kserveSyncAttemptedAt).toLocaleString() : '—'}
          </dd>
          <dt>Namespace / CR UID</dt>
          <dd className="mono break-word">
            {row.namespace} / {row.externalId || '尚未创建'}
          </dd>
          <dt>服务地址</dt>
          <dd className="mono break-word">{row.kserveUrl || row.kserveAddress || '尚未分配'}</dd>
        </dl>
      </Card>
      {(row.kserveReason || row.statusReason) && (
        <Alert color="warning" title="发布／同步提示">
          {row.kserveReason || row.statusReason}
        </Alert>
      )}
      <p className="muted small">
        Ready=True 表示 KServe 已观测到当前资源就绪，不代替真实推理。若存储初始化失败，请核对
        ServiceAccount 关联的 S3 Secret、endpoint 和模型文件布局；若镜像下载失败，请核对
        ServingRuntime 镜像。
      </p>
      <Card title="当前已发布配置">
        <pre className="model-code">{JSON.stringify(row.spec, null, 2)}</pre>
        {row.spec.model?.registeredModelId && row.spec.model?.modelVersionId && (
          <Link
            className="text-link"
            to={`/workspaces/${g}/models/${row.spec.model.registeredModelId}/versions/${row.spec.model.modelVersionId}`}
          >
            查看来源模型版本
          </Link>
        )}
      </Card>
    </div>
  );
}
function RevisionPanel({
  g,
  row,
  access,
  writable,
}: {
  g: string;
  row: ServingService;
  access: Access;
  writable: boolean;
}) {
  const [after, setAfter] = useState(0),
    [action, setAction] = useState<RegistryAction>();
  const query = useQuery({
    queryKey: servingKeys.query(g, 'revisions', row.id, row.revision, after),
    queryFn: ({ signal }) => registryApi.revisions(g, String(row.id), after, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            caption="配置修订（保存不等于发布）"
            rows={page.items}
            rowKey={(r) => String(r.id)}
            empty={<NoData title="暂无修订" />}
            columns={[
              {
                key: 'revision',
                header: '修订',
                render: (r) =>
                  `r${r.revision} · #${r.id}${r.id === row.configRevisionId ? '（当前发布）' : ''}`,
              },
              {
                key: 'spec',
                header: '配置',
                render: (r) => (
                  <details>
                    <summary>查看不可变快照</summary>
                    <pre className="model-code">{JSON.stringify(r.spec, null, 2)}</pre>
                  </details>
                ),
              },
              {
                key: 'actions',
                header: '操作',
                render: (r) =>
                  !row.deletedAt &&
                  writable &&
                  access.allows('kserve.deploy', row, 'kserve') && (
                    <Button
                      hierarchy="outline"
                      onClick={() =>
                        setAction({
                          title: '发布此修订',
                          description: `使用配置 #${r.id} 发布，后端将重新验证来源产物是否有效和 READY。`,
                          path: `${servingPath(g, row.id)}/deploy`,
                          body: { revision: row.revision, revisionId: r.id },
                          destination: `/workspaces/${g}/kserve-services/${row.id}`,
                        })
                      }
                    >
                      发布／重新发布
                    </Button>
                  ),
              },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
          {action && access.allows('kserve.deploy', row, 'kserve') && (
            <RegistryActionDialog groupId={g} action={action} close={() => setAction(undefined)} />
          )}
        </div>
      )}
    </QueryState>
  );
}
function HistoryPanel({ g, row }: { g: string; row: ServingService }) {
  const [after, setAfter] = useState(0),
    [operation, setOperation] = useState<string>();
  const query = useQuery({
    queryKey: servingKeys.query(g, 'history', row.id, row.instanceId, row.revision, after),
    queryFn: ({ signal }) => registryApi.history(g, String(row.id), after, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={query}>
      {(page) => (
        <div className="stack">
          <Table
            caption="发布历史"
            rows={page.items}
            rowKey={(r) => String(r.id)}
            empty={<NoData title="暂无历史" />}
            columns={[
              { key: 'event', header: '事件', render: (r) => r.event },
              { key: 'revision', header: '修订', render: (r) => `r${r.revision}` },
              { key: 'status', header: '状态', render: (r) => <StatusBadge status={r.status} /> },
              {
                key: 'createdAt',
                header: '时间',
                render: (r) => new Date(r.createdAt).toLocaleString(),
              },
              {
                key: 'operation',
                header: '操作',
                render: (r) => (
                  <Button hierarchy="link" onClick={() => setOperation(r.operationId)}>
                    查看进度
                  </Button>
                ),
              },
            ]}
          />
          <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
          {operation && (
            <OperationDialog id={operation} groupId={g} onClose={() => setOperation(undefined)} />
          )}
        </div>
      )}
    </QueryState>
  );
}
function PredictPanel({ g, row }: { g: string; row: ServingService }) {
  const [input, setInput] = useState(
    row.spec.protocol === 'v2'
      ? '{"inputs":[{"name":"input","shape":[1,4],"datatype":"FP32","data":[5.1,3.5,1.4,0.2]}]}'
      : '{"instances":[[5.1,3.5,1.4,0.2]]}',
  );
  const [validation, setValidation] = useState<unknown>();
  const key = useCommandKey();
  const mutation = useMutation({
    retry: false,
    mutationFn: (payload: unknown) =>
      registryApi.predict(
        g,
        row,
        payload,
        key({ id: row.id, instanceId: row.instanceId, revision: row.revision, payload }),
      ),
  });
  const ready =
    row.spec.deploymentType === 'MODEL' &&
    row.kserveReady &&
    row.desiredState === 'SERVING' &&
    !row.deletedAt;
  return (
    <div className="stack">
      <Alert color="warning" title="向真实模型发送请求">
        这是实际推理，不是模拟结果。示例为 Iris 四特征输入，必须按你的模型修改；请求不持久化，最多
        256 KiB，响应最多 1 MiB，超时 20 秒。
      </Alert>
      {!ready && (
        <Alert color="warning" title="模型服务尚未就绪">
          请先等待发布完成并检查存储初始化与模型加载。
        </Alert>
      )}
      <form
        className="stack"
        onSubmit={(event) => {
          event.preventDefault();
          if (!ready || mutation.isPending) return;
          setValidation(undefined);
          mutation.reset();
          try {
            if (new TextEncoder().encode(input).length > 256 * 1024)
              throw new Error('输入超过 256 KiB');
            mutation.mutate(JSON.parse(input) as unknown);
          } catch (error) {
            setValidation(error);
          }
        }}
      >
        <FormTextarea
          label={`推理 JSON · ${row.spec.protocol}`}
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
            mutation.reset();
          }}
          rows={9}
          disabled={mutation.isPending}
        />
        <Button type="submit" hierarchy="primary" loading={mutation.isPending} disabled={!ready}>
          发送真实推理请求
        </Button>
      </form>
      {validation !== undefined && <ErrorState error={validation} />}
      {mutation.isError && <ErrorState error={mutation.error} />}
      {mutation.isSuccess &&
        ready &&
        mutation.data.instanceId === row.instanceId &&
        mutation.data.revision === row.revision && (
          <Card title={`推理成功 · ${mutation.data.durationMs} ms`}>
            <pre className="model-code">{JSON.stringify(mutation.data.response, null, 2)}</pre>
          </Card>
        )}
    </div>
  );
}
