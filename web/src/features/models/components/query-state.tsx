import type { ReactNode } from 'react';
import type { UseQueryResult } from '@tanstack/react-query';
import { Button } from '@atomus';
import { ErrorState, Loading } from '@/components/feedback';
export function QueryState<T>({
  query,
  children,
}: {
  query: UseQueryResult<T>;
  children: (data: T) => ReactNode;
}) {
  if (query.isError)
    return (
      <ErrorState
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  if (!query.isSuccess) return <Loading />;
  return <>{children(query.data)}</>;
}
export function PageControls({
  after,
  next,
  onChange,
}: {
  after: number;
  next: number | null;
  onChange: (n: number) => void;
}) {
  return (
    <div className="actions end">
      <Button hierarchy="outline" disabled={!after} onClick={() => onChange(0)}>
        回到首页
      </Button>
      <Button
        hierarchy="outline"
        disabled={!next}
        onClick={() => {
          if (next) onChange(next);
        }}
      >
        下一页
      </Button>
    </div>
  );
}
