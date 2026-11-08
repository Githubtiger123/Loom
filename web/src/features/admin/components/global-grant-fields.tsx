import { Checkbox, Input } from '@atomus';
import type { FieldErrors } from 'react-hook-form';
import type { GlobalGrantsValues } from '../form-schema';
import type { Role } from '../types';
export function GlobalGrantFields({
  roles,
  value,
  onChange,
  errors,
}: {
  roles: Role[];
  value: GlobalGrantsValues['roles'];
  onChange: (v: GlobalGrantsValues['roles']) => void;
  errors?: FieldErrors<GlobalGrantsValues>['roles'];
}) {
  return (
    <div className="stack">
      {roles.map((role) => {
        const index = value.findIndex((v) => v.roleId === role.id);
        const grant = value[index];
        return (
          <div className="role-option stack compact" key={role.id}>
            <Checkbox
              label={`${role.name} · ${role.code}`}
              description={role.description}
              checked={!!grant}
              onChange={(e) =>
                onChange(
                  e.target.checked
                    ? [...value, { roleId: role.id, expiresAt: '' }]
                    : value.filter((v) => v.roleId !== role.id),
                )
              }
            />
            <details>
              <summary className="text-link small">查看 {role.permissions.length} 个权限点</summary>
              {role.permissions.map((p) => (
                <span className="config-chip mono" key={p}>
                  {p}
                </span>
              ))}
            </details>
            {grant && role.code !== 'platform_admin' && (
              <Input
                label={`${role.name} 有效期（本地时间）`}
                type="datetime-local"
                step="0.001"
                value={grant.expiresAt}
                error={errors?.[index]?.expiresAt?.message}
                hint="留空为长期；提交时转换为 UTC"
                onChange={(e) =>
                  onChange(
                    value.map((v) =>
                      v.roleId === role.id ? { ...v, expiresAt: e.target.value } : v,
                    ),
                  )
                }
              />
            )}
          </div>
        );
      })}
      {value.some((v) => !roles.some((r) => r.id === v.roleId)) && (
        <p className="field-error">旧角色不在可用目录内，请返回后重新读取，避免误覆盖。</p>
      )}
    </div>
  );
}
