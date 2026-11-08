import { Link } from 'react-router-dom';
import { Button, Card } from '@atomus';
import { NoData } from '@/components/feedback';

export function WorkspaceReferences({
  ids,
  groupId,
  canReadNotebook,
  canDetach,
  onDetach,
}: {
  ids: number[];
  groupId: string;
  canReadNotebook: boolean;
  canDetach: boolean;
  onDetach: (id: number) => void;
}) {
  return (
    <Card>
      <div className="stack">
        <h2>关联与历史实例引用</h2>
        <p className="small muted">
          包括尚未安全解除的已删除实例。删除实例不自动解除数据引用，需逐个核对；实例详情与解除动作仍独立授权。
        </p>
        {ids.length ? (
          <ul className="stack">
            {ids.map((id) => (
              <li key={id}>
                <div className="actions">
                  {canReadNotebook ? (
                    <Link className="text-link" to={`/workspaces/${groupId}/notebooks/${id}`}>
                      开发环境 #{id}
                    </Link>
                  ) : (
                    <span>开发环境 #{id}</span>
                  )}
                  {canDetach && (
                    <Button hierarchy="outline" size="sm" onClick={() => onDetach(id)}>
                      核对并解除关联
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <NoData title="没有关联实例" description="可复用到符合条件的环境，或显式处置数据。" />
        )}
      </div>
    </Card>
  );
}
