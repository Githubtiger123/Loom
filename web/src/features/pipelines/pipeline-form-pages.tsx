import { useState } from 'react';
import { Link, useOutletContext, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Card, Input, Select } from '@atomus';
import type { Group } from '@/lib/types';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { pipelineApi, pipelineKeys, pipelinePath, runPath, noAccess, pollPipeline } from './api';
import {
  createPipelineSchema,
  importVersionSchema,
  parameterValues,
  pipelineName,
} from './form-schema';
import { canManageRun, usePipelineAccess } from './policy';
import type { Pipeline, Run, Version } from './types';
import { CommandForm } from './components/command-form';
import { QueryState } from './components/query-state';
import { CompatibilitySummary } from './components/version-summary';
import { PageControls } from './components/catalog-tables';
import { IRInput } from './components/ir-input';
import './pipelines.css';

export function CreatePipelinePage() {
  const { groupId = '' } = useParams();
  const group = useOutletContext<Group>();
  const access = usePipelineAccess(groupId);
  const [name, setName] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [description, setDescription] = useState('');
  if (access.isPending) return <Loading />;
  if (access.isError) return <ErrorState error={access.error} />;
  if (!access.can('pipeline.create')) return <ErrorState error={noAccess()} />;
  return (
    <div className="stack spacious pipeline-form">
      <Link className="text-link" to={`/workspaces/${groupId}/pipelines`}>
        ← 返回流水线
      </Link>
      <PageHeading
        title="新建流水线"
        description="创建定义后，导入由 KFP Python SDK 编译生成的版本。"
      />
      <Card>
        <CommandForm
          groupId={groupId}
          path={pipelinePath(groupId)}
          destination={`/workspaces/${groupId}/pipelines`}
          resultLink="pipeline"
          label="创建流水线"
          disabled={group.status !== 'ACTIVE'}
          payload={() => createPipelineSchema.parse({ name, displayName, description })}
        >
          <Input
            label="名称"
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={63}
            required
            hint="小写字母、数字和中划线，创建后不可更改。"
          />
          <Input
            label="显示名称"
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
            maxLength={128}
          />
          <FormTextarea
            label="描述"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            maxLength={1000}
            rows={4}
          />
        </CommandForm>
      </Card>
    </div>
  );
}

export function ImportPipelineVersionPage() {
  const { groupId = '', pipelineId = '' } = useParams();
  const access = usePipelineAccess(groupId);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'definition', pipelineId),
    queryFn: ({ signal }) => pipelineApi.detail(groupId, pipelineId, signal),
    enabled: access.can('pipeline.read'),
    retry: false,
  });
  if (access.isPending) return <Loading />;
  if (access.isError) return <ErrorState error={access.error} />;
  if (!access.can('pipeline.read') || !access.can('pipeline.version.create'))
    return <ErrorState error={noAccess()} />;
  return (
    <QueryState query={query}>
      {(row) =>
        access.allows('pipeline.version.create', row) ? (
          <ImportForm key={`${groupId}/${pipelineId}`} groupId={groupId} row={row} />
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}

function ImportForm({ groupId, row }: { groupId: string; row: Pipeline }) {
  const group = useOutletContext<Group>();
  const [revision] = useState(row.revision);
  const [name, setName] = useState('');
  const [packageYaml, setPackageYaml] = useState('');
  const [blocked, setBlocked] = useState(false);
  return (
    <div className="stack spacious pipeline-form">
      <Link className="text-link" to={`/workspaces/${groupId}/pipelines/${row.id}`}>
        ← 返回流水线
      </Link>
      <PageHeading
        title="导入流水线版本"
        description={`${row.displayName || row.name} · 基于定义 r${revision} 导入。浏览器不执行 Python，只接收单文档 IR YAML。`}
      />
      <Card>
        <CommandForm
          groupId={groupId}
          path={`${pipelinePath(groupId, row.id)}/versions`}
          destination={`/workspaces/${groupId}/pipelines/${row.id}`}
          label="导入版本"
          disabled={
            blocked || group.status !== 'ACTIVE' || !!row.deletedAt || row.revision !== revision
          }
          payload={() => ({ ...importVersionSchema.parse({ name, packageYaml }), revision })}
        >
          <Input
            label="版本名称"
            value={name}
            onChange={(event) => setName(event.target.value)}
            required
            maxLength={63}
          />
          <IRInput value={packageYaml} onChange={setPackageYaml} onBlocked={setBlocked} />
          {row.revision !== revision && (
            <Alert color="warning" title="定义修订已变化">
              输入已保留，请核对后重新打开导入页面。
            </Alert>
          )}
        </CommandForm>
      </Card>
    </div>
  );
}

export function SubmitPipelineRunPage() {
  const { groupId = '', pipelineId = '' } = useParams();
  const access = usePipelineAccess(groupId);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'definition', pipelineId),
    queryFn: ({ signal }) => pipelineApi.detail(groupId, pipelineId, signal),
    enabled: access.can('pipeline.read'),
    retry: false,
  });
  if (access.isPending) return <Loading />;
  if (access.isError) return <ErrorState error={access.error} />;
  if (!access.can('pipeline.read') || !access.can('pipeline.run'))
    return <ErrorState error={noAccess()} />;
  return (
    <QueryState query={query}>
      {(row) =>
        access.allows('pipeline.run', row) ? (
          <RunVersionPicker key={`${groupId}/${pipelineId}`} groupId={groupId} row={row} />
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}

export function EditPipelineRunPage() {
  const { groupId = '', runId = '' } = useParams();
  const access = usePipelineAccess(groupId);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'run', runId),
    queryFn: ({ signal }) => pipelineApi.run(groupId, runId, signal),
    enabled: access.can('pipeline.read'),
    retry: false,
    refetchInterval: pollPipeline,
  });
  if (access.isPending) return <Loading />;
  if (access.isError) return <ErrorState error={access.error} />;
  if (!access.can('pipeline.read')) return <ErrorState error={noAccess()} />;
  return (
    <QueryState query={query}>
      {(run) =>
        ['pipeline.read', 'pipeline.update', 'pipeline.stop', 'pipeline.delete'].every((code) =>
          access.allows(code, run),
        ) ? (
          <EditRunParent key={`${groupId}/${run.id}`} groupId={groupId} run={run} />
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}

function EditRunParent({ groupId, run }: { groupId: string; run: Run }) {
  const access = usePipelineAccess(groupId);
  const query = useQuery({
    queryKey: pipelineKeys.query(groupId, 'definition', String(run.pipelineId)),
    queryFn: ({ signal }) => pipelineApi.detail(groupId, String(run.pipelineId), signal),
    retry: false,
  });
  return (
    <QueryState query={query}>
      {(row) =>
        access.allows('pipeline.read', row) ? (
          <RunVersionPicker key={`${groupId}/${run.id}`} groupId={groupId} row={row} run={run} />
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}

function RunVersionPicker({ groupId, row, run }: { groupId: string; row: Pipeline; run?: Run }) {
  const [search, setSearch] = useSearchParams();
  const [after, setAfter] = useState(0);
  const [initialVersionId] = useState(run ? String(run.versionId) : '');
  const selected = search.get('version') ?? initialVersionId;
  const versions = useQuery({
    queryKey: pipelineKeys.query(groupId, 'versions', String(row.id), after),
    queryFn: ({ signal }) => pipelineApi.versions(groupId, String(row.id), after, signal),
    retry: false,
  });
  const version = useQuery({
    queryKey: pipelineKeys.query(groupId, 'version', String(row.id), selected),
    queryFn: ({ signal }) => pipelineApi.version(groupId, String(row.id), selected, signal),
    enabled: /^\d+$/.test(selected),
    retry: false,
  });
  return (
    <div className="stack spacious pipeline-form">
      <Link
        className="text-link"
        to={
          run
            ? `/workspaces/${groupId}/pipeline-runs/${run.id}`
            : `/workspaces/${groupId}/pipelines/${row.id}`
        }
      >
        ← 返回{run ? '运行' : '流水线'}
      </Link>
      <PageHeading
        title={run ? '修改运行配置' : '提交运行'}
        description={
          run
            ? '保存将请求停止并清理旧实例，再应用新快照；完成后需在运行详情单独确认启动。不会修改流水线版本。'
            : '选择已保存版本；每次运行固定版本修订与参数快照。'
        }
      />
      <QueryState query={versions}>
        {(page) => (
          <>
            {page.items?.length ? (
              <Select
                label="选择版本"
                value={selected}
                onChange={(value) => setSearch({ version: value })}
                options={page.items.map((v) => ({
                  value: String(v.id),
                  label: `${v.name} · r${v.revision}`,
                }))}
              />
            ) : (
              <NoData title="还没有版本" />
            )}
            <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
          </>
        )}
      </QueryState>
      {selected ? (
        <QueryState query={version}>
          {(value) => (
            <RunForm
              key={`${row.id}/${value.id}`}
              groupId={groupId}
              row={row}
              version={value}
              run={run}
            />
          )}
        </QueryState>
      ) : (
        <p className="muted">请选择一个版本。</p>
      )}
    </div>
  );
}

function RunForm({
  groupId,
  row,
  version,
  run,
}: {
  groupId: string;
  row: Pipeline;
  version: Version;
  run?: Run;
}) {
  const group = useOutletContext<Group>();
  const [frozen] = useState(version);
  const [runRevision] = useState(run?.revision);
  const [name, setName] = useState(run?.name ?? '');
  const [engine, setEngine] = useState(run?.engine ?? 'NATIVE_ARGO');
  // Do not silently carry parameters across a version/schema change. Credential
  // references always require deliberate re-entry; no secret is prefilled.
  const initialValues =
    run &&
    run.versionId === version.id &&
    (run.versionRevision ?? run.spec.versionRevision) === version.revision
      ? (run.spec.values ?? []).filter((v) => v.value !== undefined && !v.credentialRef)
      : [];
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(initialValues.map((v) => [v.name, v.value!])),
  );
  const [supplied, setSupplied] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(initialValues.map((v) => [v.name, true])),
  );
  const report = useQuery({
    queryKey: pipelineKeys.query(
      groupId,
      'compatibility',
      String(row.id),
      frozen.id,
      frozen.revision,
    ),
    queryFn: ({ signal }) => pipelineApi.compatibility(groupId, String(row.id), frozen, signal),
    retry: false,
  });
  const nativeReady =
    report.isSuccess &&
    report.data.runnable &&
    report.data.compilable &&
    report.data.revision === frozen.revision;
  return (
    <Card title={`${frozen.name} · r${frozen.revision}`}>
      <CommandForm
        groupId={groupId}
        path={run ? runPath(groupId, run.id) : `${pipelinePath(groupId, row.id)}/runs`}
        method={run ? 'PUT' : 'POST'}
        destination={
          run
            ? `/workspaces/${groupId}/pipeline-runs/${run.id}`
            : `/workspaces/${groupId}/pipelines/${row.id}`
        }
        resultLink={run ? undefined : 'run'}
        label={run ? '保存配置并请求清理旧实例' : '提交运行'}
        disabled={
          !!row.deletedAt ||
          group.status !== 'ACTIVE' ||
          frozen.revision !== version.revision ||
          (!!run && (run.revision !== runRevision || !canManageRun(run) || !!run.pendingSpec)) ||
          (engine === 'NATIVE_ARGO' && !nativeReady)
        }
        payload={() => ({
          ...(run ? { revision: runRevision } : { name: pipelineName.parse(name), engine }),
          versionId: frozen.id,
          versionRevision: frozen.revision,
          parameters: parameterValues(frozen.spec.parameters ?? [], values, supplied),
        })}
      >
        <Input
          label="运行名称"
          disabled={!!run}
          required
          value={name}
          maxLength={63}
          onChange={(event) => setName(event.target.value)}
        />
        <Select
          label="执行引擎"
          disabled={!!run}
          value={engine}
          onChange={setEngine}
          options={[
            { value: 'NATIVE_ARGO', label: '原生 Argo（统一后端）' },
            { value: 'KFP', label: '外部 KFP（需部署并配置）' },
          ]}
        />
        {run && (
          <Alert color="warning" title="保存不会自动启动">
            名称与引擎保持不变。保存可能中断当前任务并清理旧实例，请确认后提交；已有产物不会删除。不同版本或新修订的参数不自动沿用，凭据引用需重新填写。
          </Alert>
        )}
        {run && run.revision !== runRevision && (
          <Alert color="warning" title="运行修订已变化">
            输入已保留，请复制草稿并核对运行详情后重新打开编辑页面。
          </Alert>
        )}
        {engine === 'NATIVE_ARGO' ? (
          <QueryState query={report}>
            {(value) => <CompatibilitySummary report={value} />}
          </QueryState>
        ) : (
          <Alert color="warning" title="外部 KFP 引擎">
            需要独立配置的 KFP 服务；不会自动回退到原生引擎。
          </Alert>
        )}
        {frozen.revision !== version.revision && (
          <Alert color="warning" title="版本已变化">
            当前输入已保留。请核对新版本后重新打开提交页面。
          </Alert>
        )}
        {(frozen.spec.parameters ?? []).map((parameter) => (
          <div className="stack" key={parameter.name}>
            <Select
              label={`${parameter.name} · ${parameter.type}${parameter.required ? ' · 必填' : ''}`}
              value={supplied[parameter.name] ? 'custom' : 'default'}
              onChange={(value) =>
                setSupplied((current) => ({ ...current, [parameter.name]: value === 'custom' }))
              }
              options={[
                {
                  value: 'default',
                  label:
                    parameter.default !== undefined ? `使用默认值：${parameter.default}` : '不提供',
                },
                {
                  value: 'custom',
                  label: parameter.type === 'CREDENTIAL' ? '填写受管凭据引用' : '自定义值',
                },
              ]}
            />
            {supplied[parameter.name] && (
              <FormTextarea
                label={`${parameter.name} 的值`}
                rows={2}
                value={values[parameter.name] ?? ''}
                onChange={(event) =>
                  setValues((current) => ({ ...current, [parameter.name]: event.target.value }))
                }
                hint={
                  parameter.type === 'CREDENTIAL'
                    ? '仅填写受管引用 ID，禁止粘贴密码或 Token。'
                    : parameter.type === 'STRING'
                      ? '字符串原文，可以显式传入空字符串。'
                      : '使用对应类型的 JSON 值，如 3、true、[1,2]、{"key":"value"}。'
                }
              />
            )}
          </div>
        ))}
      </CommandForm>
    </Card>
  );
}
