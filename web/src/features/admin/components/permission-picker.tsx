import { Checkbox } from '@atomus';
import type { Permission } from '../types';
export function PermissionPicker({
  dictionary,
  value,
  onChange,
}: {
  dictionary: Permission[];
  value: string[];
  onChange: (value: string[]) => void;
}) {
  const modules = new Map<string, Permission[]>();
  for (const p of dictionary) modules.set(p.module, [...(modules.get(p.module) ?? []), p]);
  return (
    <fieldset className="form-section">
      <legend>权限点（显式选择，不支持通配符）</legend>
      {[...modules].map(([module, permissions]) => (
        <div key={module} className="role-option stack compact">
          <h3>{module}</h3>
          {permissions.map((p) => (
            <Checkbox
              key={p.code}
              label={p.name}
              description={p.code}
              checked={value.includes(p.code)}
              onChange={(e) =>
                onChange(e.target.checked ? [...value, p.code] : value.filter((v) => v !== p.code))
              }
            />
          ))}
        </div>
      ))}
    </fieldset>
  );
}
