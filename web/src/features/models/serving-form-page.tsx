import { useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Input, Select } from '@atomus';
import { ErrorState, PageHeading } from '@/components/feedback';
import { registryApi, modelKeys, servingKeys, servingPath, noAccess } from './api';
import { formats, servingPayload } from './form-schema';
import { activeModel, activeVersion, publishable } from './policy';
import type { Artifact, ServingService } from './types';
import { ModelBoundary, type Access } from './components/boundary';
import { CommandForm } from './components/command-form';
import { resultID } from './operation-result';
import { QueryState } from './components/query-state';

export function PublishModelPage() {
  const [search] = useSearchParams();
  const m = search.get('modelId') ?? '',
    v = search.get('versionId') ?? '',
    a = search.get('artifactId') ?? '';
  return (
    <ModelBoundary permission="kserve.create">
      {(g, group, access) =>
        group.status !== 'ACTIVE' || !access.can('model.read') ? (
          <ErrorState error={noAccess()} />
        ) : !m || !v || !a ? (
          <div className="stack">
            <PageHeading
              title="选择要发布的模型产物"
              description="进入 READY 模型版本，选择其中一项有效产物发布。"
            />
            <Link className="text-link" to={`/workspaces/${g}/models`}>
              前往模型管理
            </Link>
          </div>
        ) : (
          <PublishContext key={`${g}/${m}/${v}/${a}`} g={g} m={m} v={v} a={a} access={access} />
        )
      }
    </ModelBoundary>
  );
}
function PublishContext({
  g,
  m,
  v,
  a,
  access,
}: {
  g: string;
  m: string;
  v: string;
  a: string;
  access: Access;
}) {
  const model = useQuery({
    queryKey: modelKeys.query(g, 'model', m),
    queryFn: ({ signal }) => registryApi.model(g, m, signal),
    retry: false,
    gcTime: 0,
  });
  const version = useQuery({
    queryKey: modelKeys.query(g, 'version', m, v),
    queryFn: ({ signal }) => registryApi.version(g, m, v, signal),
    enabled: model.isSuccess && access.allows('model.read', model.data),
    retry: false,
    gcTime: 0,
  });
  const artifact = useQuery({
    queryKey: modelKeys.query(g, 'artifact', m, v, a),
    queryFn: ({ signal }) => registryApi.artifact(g, m, v, a, signal),
    enabled: version.isSuccess && model.isSuccess && access.allows('model.read', model.data),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={model}>
      {(parent) =>
        !access.allows('model.read', parent) ? (
          <ErrorState error={noAccess()} />
        ) : (
          <QueryState query={version}>
            {(row) => (
              <QueryState query={artifact}>
                {(file) =>
                  publishable(parent, row, file) && formats.includes(file.modelFormat) ? (
                    <ServingForm g={g} artifact={file} />
                  ) : (
                    <ErrorState
                      error={new Error('需要有效模型、READY 版本和 KServe 支持的有效产物')}
                    />
                  )
                }
              </QueryState>
            )}
          </QueryState>
        )
      }
    </QueryState>
  );
}
export function EditServingPage() {
  const { serviceId = '' } = useParams();
  return (
    <ModelBoundary permission="kserve.update">
      {(g, group, access) =>
        group.status === 'ACTIVE' ? (
          <EditContext key={`${g}/${serviceId}`} g={g} id={serviceId} access={access} />
        ) : (
          <ErrorState error={new Error('空间不可写')} />
        )
      }
    </ModelBoundary>
  );
}
function EditContext({ g, id, access }: { g: string; id: string; access: Access }) {
  const query = useQuery({
    queryKey: servingKeys.query(g, 'service', id),
    queryFn: ({ signal }) => registryApi.service(g, id, signal),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={query}>
      {(row) =>
        access.allows('kserve.update', row, 'kserve') && !row.deletedAt ? (
          row.spec.deploymentType === 'MODEL' && row.spec.model?.artifactId ? (
            <ExistingArtifactContext g={g} row={row} access={access} />
          ) : (
            <ErrorState error={new Error('此入口只编辑已关联模型注册产物的 MODEL 服务')} />
          )
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}
function ExistingArtifactContext({
  g,
  row,
  access,
}: {
  g: string;
  row: ServingService;
  access: Access;
}) {
  const m = String(row.spec.model?.registeredModelId ?? ''),
    v = String(row.spec.model?.modelVersionId ?? ''),
    a = String(row.spec.model?.artifactId ?? '');
  const model = useQuery({
    queryKey: modelKeys.query(g, 'model', m),
    queryFn: ({ signal }) => registryApi.model(g, m, signal),
    retry: false,
    gcTime: 0,
  });
  const version = useQuery({
    queryKey: modelKeys.query(g, 'version', m, v),
    queryFn: ({ signal }) => registryApi.version(g, m, v, signal),
    enabled: model.isSuccess && access.allows('model.read', model.data),
    retry: false,
    gcTime: 0,
  });
  const artifact = useQuery({
    queryKey: modelKeys.query(g, 'artifact', m, v, a),
    queryFn: ({ signal }) => registryApi.artifact(g, m, v, a, signal),
    enabled: model.isSuccess && version.isSuccess && access.allows('model.read', model.data),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={model}>
      {(parent) =>
        !access.allows('model.read', parent) ? (
          <ErrorState error={noAccess()} />
        ) : !activeModel(parent) ? (
          <ErrorState error={new Error('原模型已归档，不能保存或重新部署此产物')} />
        ) : (
          <QueryState query={version}>
            {(version) => (
              <QueryState query={artifact}>
                {(artifact) =>
                  activeVersion(version) && publishable(parent, version, artifact) ? (
                    <ServingForm g={g} artifact={artifact} row={row} />
                  ) : (
                    <ErrorState error={new Error('原产物或版本已归档／不再 READY')} />
                  )
                }
              </QueryState>
            )}
          </QueryState>
        )
      }
    </QueryState>
  );
}
function ServingForm({
  g,
  artifact,
  row,
}: {
  g: string;
  artifact: Artifact;
  row?: ServingService;
}) {
  const [snapshot] = useState(row);
  const [value, setValue] = useState({
    name: row?.name ?? '',
    artifactId: artifact.id,
    runtime: row?.spec.model?.runtime ?? '',
    protocol: row?.spec.protocol ?? 'v1',
    minReplicas: String(row?.spec.minReplicas ?? 1),
    maxReplicas: String(row?.spec.maxReplicas ?? 1),
    cpu: row?.spec.resources.cpu ?? '1',
    memory: row?.spec.resources.memory ?? '2Gi',
    gpu: String(row?.spec.resources.gpu ?? 0),
  });
  const input = (
    key: 'name' | 'runtime' | 'minReplicas' | 'maxReplicas' | 'cpu' | 'memory' | 'gpu',
    label: string,
    hint?: string,
  ) => (
    <Input
      label={label}
      hint={hint}
      value={value[key]}
      onChange={(e) => setValue({ ...value, [key]: e.target.value })}
      disabled={key === 'name' && !!row}
    />
  );
  return (
    <div className="stack spacious">
      <PageHeading
        title={row ? '保存服务配置修订' : '发布模型服务'}
        description={`模型 #${artifact.modelId} / 版本 #${artifact.versionId} / 产物 ${artifact.name}`}
      />
      <Alert
        color="warning"
        title={row ? '保存不会影响当前线上配置' : '发布将创建 KServe 服务并消耗集群资源'}
      >
        存储凭据由平台配置的 ServiceAccount 提供，不在表单传递。请确认对应 namespace 中的存储
        Secret、Runtime 和镜像可用。
      </Alert>
      <dl className="details-grid">
        <dt>产物格式</dt>
        <dd>
          {artifact.modelFormat} {artifact.modelFormatVersion}
        </dd>
        <dt>文件目录</dt>
        <dd className="mono break-word">{artifact.uri}</dd>
      </dl>
      <CommandForm
        groupId={g}
        path={servingPath(g, row?.id)}
        method={row ? 'PATCH' : 'POST'}
        label={row ? '保存配置（不发布）' : '创建并发布'}
        payload={() => {
          const body = servingPayload(value);
          if (snapshot) {
            const spec = {
              deploymentType: body.deploymentType,
              protocol: body.protocol,
              minReplicas: body.minReplicas,
              maxReplicas: body.maxReplicas,
              resources: body.resources,
              model: body.model,
            };
            return { revision: snapshot.revision, ...spec };
          }
          return body;
        }}
        destination={(operation) => {
          const id = row?.id ?? resultID(operation, 'resourceId');
          return `/workspaces/${g}/kserve-services${id ? `/${id}` : ''}`;
        }}
      >
        {input('name', '服务名称')}
        <Select
          label="推理协议"
          value={value.protocol}
          onChange={(protocol) => setValue({ ...value, protocol: protocol as 'v1' | 'v2' })}
          options={[
            { value: 'v1', label: 'KServe v1' },
            { value: 'v2', label: 'Open Inference v2' },
          ]}
        />
        {input(
          'runtime',
          'Runtime（可选）',
          '留空由 KServe 按模型格式自动匹配；指定时必须是集群已有的 Runtime。',
        )}
        <div className="model-fields">
          {input('minReplicas', '最小副本')}
          {input('maxReplicas', '最大副本')}
          {input('cpu', '每副本 CPU')}
          {input('memory', '每副本内存')}
          {input('gpu', '每副本 GPU 数量')}
        </div>
      </CommandForm>
    </div>
  );
}
