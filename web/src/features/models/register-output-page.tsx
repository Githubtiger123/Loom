import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Input, Select, Table } from '@atomus';
import { ErrorState, NoData, PageHeading } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { pipelineApi, pipelineKeys } from '@/features/pipelines/api';
import type { Run } from '@/features/pipelines/types';
import { registryApi, modelKeys, modelPath, noAccess } from './api';
import { activeModel } from './policy';
import { descriptionSchema, formats, nameSchema } from './form-schema';
import type { Model } from './types';
import { ModelBoundary, type Access } from './components/boundary';
import { CommandForm } from './components/command-form';
import { resultID } from './operation-result';
import { PageControls, QueryState } from './components/query-state';

export function RegisterOutputPage() {
  const [params] = useSearchParams();
  const runId = params.get('runId') ?? '',
    instance = params.get('instanceId') ?? '',
    artifactId = params.get('artifactId') ?? '';
  return (
    <ModelBoundary permission="model.version.create">
      {(g, group, access) =>
        access.can('pipeline.read') &&
        access.can('model.artifact.create') &&
        group.status === 'ACTIVE' ? (
          <OutputContext
            key={`${g}/${runId}/${instance}/${artifactId}`}
            g={g}
            runId={runId}
            instance={instance}
            artifactId={artifactId}
            access={access}
          />
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </ModelBoundary>
  );
}
function OutputContext({
  g,
  runId,
  instance,
  artifactId,
  access,
}: {
  g: string;
  runId: string;
  instance: string;
  artifactId: string;
  access: Access;
}) {
  const run = useQuery({
    queryKey: pipelineKeys.query(g, 'run', runId),
    queryFn: ({ signal }) => pipelineApi.run(g, runId, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={run}>
      {(row) =>
        !access.allows('pipeline.read', row, 'pipeline') ? (
          <ErrorState error={noAccess()} />
        ) : row.instanceId !== instance || row.engine !== 'NATIVE_ARGO' ? (
          <ErrorState error={new Error('运行实例已变化或不是原生引擎，请从最新产物面板重新进入')} />
        ) : (
          <OutputArtifact g={g} run={row} artifactId={artifactId} access={access} />
        )
      }
    </QueryState>
  );
}
function OutputArtifact({
  g,
  run,
  artifactId,
  access,
}: {
  g: string;
  run: Run;
  artifactId: string;
  access: Access;
}) {
  const query = useQuery({
    queryKey: pipelineKeys.query(g, 'visualizations', run.id, run.instanceId, run.revision),
    queryFn: ({ signal }) => pipelineApi.visualizations(g, run, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={query}>
      {(data) => {
        const artifact = data.items.find((a) => a.artifactId === artifactId);
        if (!artifact || artifact.contentStatus !== 'ON_DEMAND')
          return <ErrorState error={new Error('产物不存在、不可读或尚未发布')} />;
        return (
          <div className="stack spacious">
            <PageHeading
              title="从训练产物注册模型版本"
              description="后端解析授权输出并转换文件布局，不需要手动复制存储地址。"
            />
            <Card title={artifact.name}>
              <p>
                运行 #{run.id} · 产物 #{artifact.artifactId} · {artifact.type}
              </p>
              <p className="mono break-word">执行实例：{run.instanceId}</p>
            </Card>
            <Alert color="warning" title="后台转换，不会立即就绪">
              支持原生归档的单文件与目录（最多 10 GiB、10000 项）。单文件需要提供 Runtime
              要求的文件名；目录保留内部布局。导入完成后再标记 READY。
            </Alert>
            <ModelPicker g={g} run={run} artifactId={artifactId} access={access} />
          </div>
        );
      }}
    </QueryState>
  );
}
function ModelPicker({
  g,
  run,
  artifactId,
  access,
}: {
  g: string;
  run: Run;
  artifactId: string;
  access: Access;
}) {
  const [after, setAfter] = useState(0),
    [selected, setSelected] = useState<Model>(),
    [scope, setScope] = useState('mine');
  const actualScope = access.can('model.read.any') ? scope : 'mine';
  const query = useQuery({
    queryKey: modelKeys.query(g, 'list', actualScope, false, after),
    queryFn: ({ signal }) => registryApi.models(g, actualScope, false, after, signal),
    retry: false,
    gcTime: 0,
  });
  if (selected)
    return access.allows('model.version.create', selected) ? (
      <div className="stack">
        <Button hierarchy="outline" onClick={() => setSelected(undefined)}>
          重新选择模型
        </Button>
        <ImportForm key={selected.id} g={g} model={selected} run={run} artifactId={artifactId} />
      </div>
    ) : (
      <ErrorState error={noAccess()} />
    );
  return (
    <div className="stack">
      <h2>选择目标模型</h2>
      {access.can('model.read.any') && (
        <Select
          label="模型范围"
          value={actualScope}
          onChange={(value) => {
            setScope(value);
            setAfter(0);
          }}
          options={[
            { value: 'mine', label: '我创建的' },
            { value: 'all', label: '全部可见' },
          ]}
        />
      )}
      <QueryState query={query}>
        {(page) => (
          <>
            <Table
              caption="目标模型"
              rows={page.items.filter(
                (r) => activeModel(r) && access.allows('model.version.create', r),
              )}
              rowKey={(r) => String(r.id)}
              empty={<NoData title="没有可注册的模型" />}
              columns={[
                { key: 'name', header: '模型', render: (r) => r.displayName || r.name },
                {
                  key: 'select',
                  header: '操作',
                  render: (r) => (
                    <Button hierarchy="outline" onClick={() => setSelected(r)}>
                      选择
                    </Button>
                  ),
                },
              ]}
            />
            <PageControls after={after} next={page.nextAfter} onChange={setAfter} />
          </>
        )}
      </QueryState>
      {access.can('model.create') && (
        <Link className="text-link" to={`/workspaces/${g}/models/new`}>
          还没有模型？先登记模型后返回本页
        </Link>
      )}
    </div>
  );
}
function ImportForm({
  g,
  model,
  run,
  artifactId,
}: {
  g: string;
  model: Model;
  run: Run;
  artifactId: string;
}) {
  const [revision] = useState(model.revision),
    [name, setName] = useState(''),
    [description, setDescription] = useState(''),
    [artifactName, setArtifactName] = useState('weights'),
    [fileName, setFileName] = useState('model.joblib'),
    [format, setFormat] = useState('sklearn'),
    [formatVersion, setFormatVersion] = useState('');
  return (
    <CommandForm
      groupId={g}
      path={`${modelPath(g, model.id)}/import-pipeline-artifact`}
      label="注册版本并转换文件"
      payload={() => ({
        revision,
        name: nameSchema.parse(name),
        description: descriptionSchema.parse(description),
        runId: run.id,
        instanceId: run.instanceId,
        artifactId,
        artifactName: nameSchema.parse(artifactName),
        fileName: nameSchema.parse(fileName),
        modelFormat: format,
        modelFormatVersion: formatVersion,
      })}
      destination={(operation) => {
        const id = resultID(operation, 'id', 'version');
        return `/workspaces/${g}/models/${model.id}${id ? `/versions/${id}` : ''}`;
      }}
    >
      <p>
        目标模型：{model.displayName || model.name} · r{revision}
      </p>
      <Input
        label="版本标识"
        value={name}
        onChange={(e) => setName(e.target.value)}
        maxLength={63}
        required
      />
      <FormTextarea
        label="版本描述"
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        rows={3}
        maxLength={2000}
      />
      <Input
        label="产物登记名称"
        value={artifactName}
        onChange={(e) => setArtifactName(e.target.value)}
        required
      />
      <Select
        label="模型格式"
        value={format}
        onChange={setFormat}
        options={formats.map((value) => ({ value, label: value }))}
      />
      <Input
        label="格式版本（可选）"
        value={formatVersion}
        onChange={(e) => setFormatVersion(e.target.value)}
        maxLength={64}
      />
      <Input
        label="单文件发布名称"
        hint="例如 sklearn 用 model.joblib、ONNX 用 model.onnx。目录产物忽略此名称并保留内部布局。"
        value={fileName}
        onChange={(e) => setFileName(e.target.value)}
        required
        maxLength={63}
      />
    </CommandForm>
  );
}
