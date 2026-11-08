import { useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { Alert, Button, Input } from '@atomus';
import { ErrorState, Loading, PageHeading } from '@/components/feedback';
import { OperationDialog } from '@/components/operation-dialog';
import { adminApi, adminKeys } from './api';
import { GlobalCapability } from './components/global-capability';
import { AuditsTable } from './components/audits-table';
export function AuditsPage() {
  return (
    <GlobalCapability permission="audit.read">
      {(allowed) => <AuditsContent allowed={allowed} />}
    </GlobalCapability>
  );
}
function AuditsContent({ allowed }: { allowed: boolean }) {
  const [search, setSearch] = useState('');
  const [operation, setOperation] = useState<string>();
  const query = useInfiniteQuery({
    queryKey: adminKeys.audits,
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => adminApi.audits(pageParam, signal),
    getNextPageParam: (rows) => (rows.length === 50 ? rows.at(-1)?.id : undefined),
    enabled: allowed,
    gcTime: 0,
  });
  const rows = query.data?.pages.flat() ?? [];
  const filtered = rows.filter((a) =>
    `${a.actorName} ${a.action} ${a.targetType} ${a.targetId} ${a.outcome} ${a.operationId ?? ''}`
      .toLowerCase()
      .includes(search.trim().toLowerCase()),
  );
  return (
    <>
      <PageHeading
        eyebrow="PLATFORM AUDIT"
        title="审计记录"
        description="查看操作者、动作、目标、结果与关联操作单。不展示原始请求体、密码或 Token。"
      />
      <Alert color="info" title="当前接口按 ID 正序分页">
        筛选仅针对已加载记录，不是全库搜索。查看操作单会再次检查该对象的访问权限，拥有 audit.read
        不代表可以读取所有操作详情。
      </Alert>
      <div className="toolbar notebook-toolbar">
        <Input label="筛选已加载记录" value={search} onChange={(e) => setSearch(e.target.value)} />
        <Button
          hierarchy="outline"
          disabled={!allowed}
          loading={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
        >
          刷新记录
        </Button>
      </div>
      {!allowed ? (
        <p className="muted">权限尚未确认，暂不展示审计数据。</p>
      ) : query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorState
          error={query.error}
          retry={() => {
            void query.refetch();
          }}
        />
      ) : (
        <>
          <p className="small muted">
            已加载 {rows.length} 条，匹配 {filtered.length} 条。
          </p>
          <AuditsTable rows={filtered} onOperation={setOperation} />
          {query.hasNextPage && (
            <Button
              hierarchy="outline"
              loading={query.isFetchingNextPage}
              onClick={() => {
                void query.fetchNextPage();
              }}
            >
              加载后续记录
            </Button>
          )}
        </>
      )}
      {operation && (
        <OperationDialog id={operation} groupId="" onClose={() => setOperation(undefined)} />
      )}
    </>
  );
}
