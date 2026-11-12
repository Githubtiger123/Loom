import type { ReactNode } from 'react';
import { useOutletContext, useParams } from 'react-router-dom';
import type { Group } from '@/lib/types';
import { ErrorState, Loading } from '@/components/feedback';
import { noAccess } from '../api';
import { useModelAccess } from '../policy';
import '../models.css';
export type Access = ReturnType<typeof useModelAccess>;
export function ModelBoundary({
  permission = 'model.read',
  children,
}: {
  permission?: string;
  children: (g: string, group: Group, access: Access) => ReactNode;
}) {
  const { groupId = '' } = useParams();
  const group = useOutletContext<Group>();
  const access = useModelAccess(groupId);
  if (access.isPending) return <Loading />;
  if (access.isError)
    return (
      <ErrorState
        error={access.error}
        retry={() => {
          void access.refetch();
        }}
      />
    );
  if (!access.can(permission)) return <ErrorState error={noAccess()} />;
  return <>{children(groupId, group, access)}</>;
}
