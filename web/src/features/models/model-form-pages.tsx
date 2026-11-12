import { useState, type ReactNode } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Input, Select } from '@atomus';
import { ErrorState, PageHeading } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { registryApi, modelKeys, modelPath, noAccess } from './api';
import { activeModel, activeVersion } from './policy';
import {
  artifactPayload,
  descriptionSchema,
  formats,
  modelSchema,
  nameSchema,
} from './form-schema';
import type { Model, Version } from './types';
import { ModelBoundary, type Access } from './components/boundary';
import { CommandForm } from './components/command-form';
import { resultID } from './operation-result';
import { QueryState } from './components/query-state';

export function CreateModelPage() {
  return (
    <ModelBoundary permission="model.create">
      {(g, group) => <ModelMetadataForm key={g} g={g} disabled={group.status !== 'ACTIVE'} />}
    </ModelBoundary>
  );
}
export function EditModelPage() {
  return (
    <RegistryContext permission="model.update">
      {(g, model) => <ModelMetadataForm key={`${g}/${model.id}`} g={g} row={model} />}
    </RegistryContext>
  );
}
export function CreateModelVersionPage() {
  return (
    <RegistryContext permission="model.version.create">
      {(g, model) => <VersionForm key={`${g}/${model.id}`} g={g} model={model} />}
    </RegistryContext>
  );
}
export function EditModelVersionPage() {
  return (
    <RegistryContext permission="model.version.update" version>
      {(g, model, row) =>
        row && <VersionForm key={`${g}/${row.id}`} g={g} model={model} row={row} />
      }
    </RegistryContext>
  );
}
export function CreateModelArtifactPage() {
  return (
    <RegistryContext permission="model.artifact.create" version>
      {(g, model, row) =>
        row && <ArtifactForm key={`${g}/${row.id}`} g={g} model={model} version={row} />
      }
    </RegistryContext>
  );
}
function RegistryContext({
  permission,
  version,
  children,
}: {
  permission: string;
  version?: boolean;
  children: (g: string, model: Model, row?: Version) => ReactNode;
}) {
  const { modelId = '', versionId = '' } = useParams();
  return (
    <ModelBoundary permission={permission}>
      {(g, group, access) =>
        group.status === 'ACTIVE' ? (
          <ContextRows
            key={`${g}/${modelId}/${versionId}`}
            g={g}
            m={modelId}
            v={version ? versionId : undefined}
            access={access}
            permission={permission}
          >
            {children}
          </ContextRows>
        ) : (
          <ErrorState error={new Error('空间不可写')} />
        )
      }
    </ModelBoundary>
  );
}
function ContextRows({
  g,
  m,
  v,
  access,
  permission,
  children,
}: {
  g: string;
  m: string;
  v?: string;
  access: Access;
  permission: string;
  children: (g: string, model: Model, row?: Version) => ReactNode;
}) {
  const model = useQuery({
    queryKey: modelKeys.query(g, 'model', m),
    queryFn: ({ signal }) => registryApi.model(g, m, signal),
    retry: false,
    gcTime: 0,
  });
  const version = useQuery({
    queryKey: modelKeys.query(g, 'version', m, v),
    queryFn: ({ signal }) => registryApi.version(g, m, v!, signal),
    enabled: !!v && model.isSuccess && access.allows(permission, model.data),
    retry: false,
    gcTime: 0,
  });
  return (
    <QueryState query={model}>
      {(parent) =>
        !access.allows(permission, parent) ? (
          <ErrorState error={noAccess()} />
        ) : !activeModel(parent) ? (
          <ErrorState error={new Error('模型已归档')} />
        ) : v ? (
          <QueryState query={version}>
            {(row) =>
              activeVersion(row) ? (
                children(g, parent, row)
              ) : (
                <ErrorState error={new Error('版本已归档')} />
              )
            }
          </QueryState>
        ) : (
          children(g, parent)
        )
      }
    </QueryState>
  );
}
function ModelMetadataForm({ g, row, disabled }: { g: string; row?: Model; disabled?: boolean }) {
  const [snapshot] = useState(row);
  const [name, setName] = useState(row?.name ?? ''),
    [displayName, setDisplay] = useState(row?.displayName ?? ''),
    [description, setDescription] = useState(row?.description ?? '');
  return (
    <div className="stack spacious">
      <PageHeading
        title={row ? '编辑模型信息' : '登记模型'}
        description="管理模型展示信息；权重文件由模型版本的产物记录关联。"
      />
      <CommandForm
        groupId={g}
        path={modelPath(g, row?.id)}
        method={row ? 'PATCH' : 'POST'}
        label={row ? '保存信息' : '登记模型'}
        disabled={disabled}
        payload={() => {
          const data = modelSchema.parse({ name, displayName, description });
          if (!snapshot) return data;
          if (displayName === snapshot.displayName && description === snapshot.description)
            throw new Error('没有修改内容');
          return {
            revision: snapshot.revision,
            ...(displayName !== snapshot.displayName ? { displayName } : {}),
            ...(description !== snapshot.description ? { description } : {}),
          };
        }}
        destination={(operation) => {
          const id = row?.id ?? resultID(operation, 'id');
          return `/workspaces/${g}/models${id ? `/${id}` : ''}`;
        }}
      >
        <Input
          label="模型标识"
          value={name}
          onChange={(e) => setName(e.target.value)}
          disabled={!!row}
          required
          maxLength={63}
        />
        <Input
          label="显示名称"
          value={displayName}
          onChange={(e) => setDisplay(e.target.value)}
          maxLength={128}
        />
        <FormTextarea
          label="描述"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          maxLength={2000}
          rows={4}
        />
      </CommandForm>
    </div>
  );
}
function VersionForm({ g, model, row }: { g: string; model: Model; row?: Version }) {
  const [revision] = useState(row?.revision ?? model.revision),
    [name, setName] = useState(row?.name ?? ''),
    [description, setDescription] = useState(row?.description ?? '');
  return (
    <div className="stack spacious">
      <PageHeading
        title={row ? '编辑版本描述' : '创建模型版本'}
        description={model.displayName || model.name}
      />
      <CommandForm
        groupId={g}
        path={row ? modelPath(g, model.id, row.id) : `${modelPath(g, model.id)}/versions`}
        method={row ? 'PATCH' : 'POST'}
        label={row ? '保存描述' : '创建草稿版本'}
        payload={() => {
          descriptionSchema.parse(description);
          if (row) {
            if (description === row.description) throw new Error('没有修改内容');
            return { revision, description };
          }
          return { revision, name: nameSchema.parse(name), description };
        }}
        destination={(operation) => {
          const id = row?.id ?? resultID(operation, 'id', 'version');
          return `/workspaces/${g}/models/${model.id}${id ? `/versions/${id}` : ''}`;
        }}
      >
        <Input
          label="版本标识"
          value={name}
          onChange={(e) => setName(e.target.value)}
          disabled={!!row}
          maxLength={63}
          required
        />
        <FormTextarea
          label="版本描述"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={4}
          maxLength={2000}
        />
        <p className="muted">版本先以 DRAFT 创建，登记产物后可显式标记 READY。</p>
      </CommandForm>
    </div>
  );
}
function ArtifactForm({ g, model, version }: { g: string; model: Model; version: Version }) {
  const [revision] = useState(version.revision);
  const [value, setValue] = useState({
    name: '',
    uri: '',
    storageType: 'S3',
    modelFormat: 'sklearn',
    modelFormatVersion: '',
    digest: '',
    sizeBytes: '',
  });
  const field = (key: keyof typeof value, label: string, hint?: string) => (
    <Input
      label={label}
      hint={hint}
      value={value[key]}
      onChange={(e) => setValue({ ...value, [key]: e.target.value })}
    />
  );
  return (
    <div className="stack spacious">
      <PageHeading title="登记文件地址" description={`${model.name} / ${version.name}`} />
      <Alert color="warning" title="这里登记的是模型文件，不是上传文件">
        请确认地址对 KServe ServiceAccount 可读，并符合 Runtime 的文件布局。不要登记 Pipeline
        的压缩传输包；请使用运行页的“注册模型版本”。
      </Alert>
      <CommandForm
        groupId={g}
        path={`${modelPath(g, model.id, version.id)}/artifacts`}
        label="登记产物"
        payload={() => ({ revision, ...artifactPayload(value) })}
        destination={`/workspaces/${g}/models/${model.id}/versions/${version.id}`}
      >
        {field('name', '产物名称')}
        <Select
          label="存储类型"
          value={value.storageType}
          onChange={(v) => setValue({ ...value, storageType: v })}
          options={['S3', 'GS', 'PVC', 'OCI'].map((v) => ({ value: v, label: v }))}
        />
        {field(
          'uri',
          '模型存储 URI',
          '例如 s3://models/team/model/version/，不能含访问密钥或签名参数',
        )}
        <Select
          label="模型格式"
          value={value.modelFormat}
          onChange={(v) => setValue({ ...value, modelFormat: v })}
          options={formats.map((v) => ({ value: v, label: v }))}
        />
        {field('modelFormatVersion', '格式版本（可选）')}
        {field('digest', 'SHA256（可选）')}
        {field('sizeBytes', '文件大小／字节（可选）')}
      </CommandForm>
      <Link className="text-link" to={`/workspaces/${g}/models/${model.id}/versions/${version.id}`}>
        返回版本
      </Link>
    </div>
  );
}
