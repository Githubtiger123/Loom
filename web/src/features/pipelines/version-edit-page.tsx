import { useState } from 'react';
import { Link, useOutletContext, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Alert, Card } from '@atomus';
import type { Group } from '@/lib/types';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { pipelineApi, pipelineKeys, pipelinePath, noAccess } from './api';
import { usePipelineAccess } from './policy';
import { importVersionSchema } from './form-schema';
import type { Pipeline, Version } from './types';
import { CommandForm } from './components/command-form';
import { QueryState } from './components/query-state';
import { IRInput } from './components/ir-input';
import './pipelines.css';

export function EditPipelineVersionPage() {
  const { groupId = '', pipelineId = '', versionId = '' } = useParams();
  const access = usePipelineAccess(groupId);
  const permitted = access.can('pipeline.read') && access.can('pipeline.version.update');
  const parent = useQuery({
    queryKey: pipelineKeys.query(groupId, 'definition', pipelineId),
    queryFn: ({ signal }) => pipelineApi.detail(groupId, pipelineId, signal),
    enabled: permitted,
    retry: false,
  });
  const version = useQuery({
    queryKey: pipelineKeys.query(groupId, 'version', pipelineId, versionId),
    queryFn: ({ signal }) => pipelineApi.version(groupId, pipelineId, versionId, signal),
    enabled: parent.isSuccess && access.allows('pipeline.version.update', parent.data),
    retry: false,
  });
  if (access.isPending) return <Loading />;
  if (access.isError) return <ErrorState error={access.error} />;
  if (!permitted) return <ErrorState error={noAccess()} />;
  return (
    <QueryState query={parent}>
      {(row) =>
        access.allows('pipeline.version.update', row) ? (
          <QueryState query={version}>
            {(value) => (
              <VersionEditForm
                key={`${groupId}/${row.id}/${value.id}`}
                groupId={groupId}
                row={row}
                version={value}
              />
            )}
          </QueryState>
        ) : (
          <ErrorState error={noAccess()} />
        )
      }
    </QueryState>
  );
}

function VersionEditForm({
  groupId,
  row,
  version,
}: {
  groupId: string;
  row: Pipeline;
  version: Version;
}) {
  const group = useOutletContext<Group>();
  const [frozen] = useState({ parent: row.revision, version: version.revision });
  const [packageYaml, setPackageYaml] = useState(() =>
    version.spec.pipelineSpec ? JSON.stringify(version.spec.pipelineSpec, null, 2) : '',
  );
  const [initial] = useState(packageYaml);
  const [blocked, setBlocked] = useState(false);
  const changed = frozen.parent !== row.revision || frozen.version !== version.revision;
  const destination = `/workspaces/${groupId}/pipelines/${row.id}/versions/${version.id}`;
  return (
    <div className="stack spacious pipeline-form">
      <Link className="text-link" to={destination}>
        ← 返回版本
      </Link>
      <PageHeading
        title={`编辑版本 · ${version.name}`}
        description={`基于版本 r${frozen.version} / 定义 r${frozen.parent}。保存后生成新修订，不改变已提交运行的快照。版本名称保持不变。`}
      />
      <Card>
        <CommandForm
          groupId={groupId}
          path={`${pipelinePath(groupId, row.id)}/versions/${version.id}`}
          method="PUT"
          destination={destination}
          label="保存新修订"
          disabled={
            blocked ||
            changed ||
            !!row.deletedAt ||
            group.status !== 'ACTIVE' ||
            packageYaml === initial
          }
          payload={() => ({
            revision: frozen.version,
            parentRevision: frozen.parent,
            packageYaml: importVersionSchema.parse({ name: version.name, packageYaml }).packageYaml,
          })}
        >
          <Alert color="warning" title="替换版本 IR">
            输入官方 SDK 编译后的完整 IR。后端重新计算参数定义与摘要；不会执行
            Python，也不会修改正在运行的任务。
          </Alert>
          <IRInput value={packageYaml} onChange={setPackageYaml} onBlocked={setBlocked} />
          {changed && (
            <Alert color="warning" title="版本或定义已变化">
              当前输入已保留。请先复制草稿并核对最新修订，再重新打开页面，不会自动改用新 revision
              覆盖。
            </Alert>
          )}
        </CommandForm>
      </Card>
    </div>
  );
}
