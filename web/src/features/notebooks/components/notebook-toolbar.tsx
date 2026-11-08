import { Button, Input, Select } from '@atomus';
import { RefreshCw } from 'lucide-react';
import type { NotebookListFilters } from '../types';

export function NotebookToolbar({
  filters,
  onChange,
  canReadAny,
  refreshing,
  onRefresh,
}: {
  filters: NotebookListFilters;
  onChange: (next: NotebookListFilters) => void;
  canReadAny: boolean;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  return (
    <div className="toolbar notebook-toolbar">
      <Input
        label="搜索环境"
        size="sm"
        type="search"
        value={filters.search}
        onChange={(event) => onChange({ ...filters, search: event.target.value })}
        placeholder="名称或展示名称"
      />
      <Select
        label="资源范围"
        size="sm"
        value={filters.scope}
        onChange={(scope) => onChange({ ...filters, scope })}
        options={[
          { value: 'mine', label: '我的环境' },
          ...(canReadAny
            ? [
                { value: 'all', label: '空间全部环境' },
                { value: 'unowned', label: '未分配负责人' },
              ]
            : []),
        ]}
      />
      <Select
        label="运行状态"
        size="sm"
        value={filters.status}
        onChange={(status) => onChange({ ...filters, status })}
        options={[
          { value: '', label: '全部状态' },
          { value: 'RUNNING', label: '运行中' },
          { value: 'STOPPED', label: '已停止' },
          { value: 'CREATING', label: '创建中' },
          { value: 'STARTING', label: '启动中' },
          { value: 'STOPPING', label: '停止中' },
          { value: 'UPDATING', label: '更新中' },
          { value: 'ERROR', label: '异常' },
          { value: 'DELETING', label: '删除中' },
        ]}
      />
      <Select
        label="环境类型"
        size="sm"
        value={filters.serverType}
        onChange={(serverType) => onChange({ ...filters, serverType })}
        options={[
          { value: '', label: '全部类型' },
          { value: 'jupyter', label: 'Jupyter' },
          { value: 'group-one', label: 'VS Code / Group One' },
          { value: 'group-two', label: 'RStudio / Group Two' },
        ]}
      />
      <Button
        hierarchy="outline"
        size="sm"
        loading={refreshing}
        iconLeading={<RefreshCw size={15} />}
        onClick={onRefresh}
      >
        刷新
      </Button>
    </div>
  );
}
