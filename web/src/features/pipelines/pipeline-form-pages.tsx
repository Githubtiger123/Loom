import { useState, useRef } from 'react';
import { Link, useOutletContext, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Card, Input, Select } from '@atomus';
import type { Group } from '@/lib/types';
import { ErrorState, Loading, NoData, PageHeading } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { pipelineApi, pipelineKeys, pipelinePath, noAccess } from './api';
import {
  createPipelineSchema,
  importVersionSchema,
  parameterValues,
  pipelineName,
} from './form-schema';
import { usePipelineAccess } from './policy';
import type { Pipeline, Version } from './types';
import { CommandForm } from './components/command-form';
import { QueryState } from './components/query-state';
import { CompatibilitySummary } from './components/version-summary';
import { PageControls } from './components/catalog-tables';
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
  const [fileError, setFileError] = useState<unknown>();
  const [reading, setReading] = useState(false);
  const readGeneration = useRef(0);
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
            reading ||
            fileError !== undefined ||
            group.status !== 'ACTIVE' ||
            !!row.deletedAt ||
            row.revision !== revision
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
          <Input
            label="选择 IR 文件（最多 512 KiB）"
            type="file"
            accept=".yaml,.yml,.json"
            onChange={(event) => {
              const file = event.target.files?.[0];
              const generation = ++readGeneration.current;
              setFileError(undefined);
              if (!file) {
                setReading(false);
                return;
              }
              if (file.size > 512 * 1024) {
                setReading(false);
                setFileError(new Error('文件不能超过 512 KiB'));
                return;
              }
              setReading(true);
              void file
                .text()
                .then((text) => {
                  if (generation === readGeneration.current) setPackageYaml(text);
                })
                .catch((error: unknown) => {
                  if (generation === readGeneration.current) setFileError(error);
                })
                .finally(() => {
                  if (generation === readGeneration.current) setReading(false);
                });
            }}
          />
          <FormTextarea
            label="Pipeline IR YAML"
            rows={20}
            value={packageYaml}
            onChange={(event) => {
              readGeneration.current++;
              setReading(false);
              setFileError(undefined);
              setPackageYaml(event.target.value);
            }}
            required
            spellCheck={false}
            hint="SDK 编译结果；不是 Python 源码，也不是 Argo Workflow YAML。"
          />
          {fileError !== undefined && <ErrorState error={fileError} />}
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

function RunVersionPicker({ groupId, row }: { groupId: string; row: Pipeline }) {
  const [search, setSearch] = useSearchParams();
  const [after, setAfter] = useState(0);
  const selected = search.get('version') ?? '';
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
      <Link className="text-link" to={`/workspaces/${groupId}/pipelines/${row.id}`}>
        ← 返回流水线
      </Link>
      <PageHeading
        title="提交运行"
        description="选择已保存版本；每次运行固定版本修订与参数快照。"
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
            <RunForm key={`${row.id}/${value.id}`} groupId={groupId} row={row} version={value} />
          )}
        </QueryState>
      ) : (
        <p className="muted">请选择一个版本。</p>
      )}
    </div>
  );
}

function RunForm({ groupId, row, version }: { groupId: string; row: Pipeline; version: Version }) {
  const group = useOutletContext<Group>();
  const [frozen] = useState(version);
  const [name, setName] = useState('');
  const [engine, setEngine] = useState('NATIVE_ARGO');
  const [values, setValues] = useState<Record<string, string>>({});
  const [supplied, setSupplied] = useState<Record<string, boolean>>({});
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
        path={`${pipelinePath(groupId, row.id)}/runs`}
        destination={`/workspaces/${groupId}/pipelines/${row.id}`}
        resultLink="run"
        label="提交运行"
        disabled={
          !!row.deletedAt ||
          group.status !== 'ACTIVE' ||
          frozen.revision !== version.revision ||
          (engine === 'NATIVE_ARGO' && !nativeReady)
        }
        payload={() => ({
          name: pipelineName.parse(name),
          engine,
          versionId: frozen.id,
          versionRevision: frozen.revision,
          parameters: parameterValues(frozen.spec.parameters ?? [], values, supplied),
        })}
      >
        <Input
          label="运行名称"
          required
          value={name}
          maxLength={63}
          onChange={(event) => setName(event.target.value)}
        />
        <Select
          label="执行引擎"
          value={engine}
          onChange={setEngine}
          options={[
            { value: 'NATIVE_ARGO', label: '原生 Argo（统一后端）' },
            { value: 'KFP', label: '外部 KFP（需部署并配置）' },
          ]}
        />
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
