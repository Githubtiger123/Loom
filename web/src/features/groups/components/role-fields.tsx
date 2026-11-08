import { Checkbox, Input } from '@atomus';
import type { FieldErrors } from 'react-hook-form';
import { accessLabel } from '../policy';
import type { Role } from '../types';
import type { MemberValues } from '../form-schema';

export function RoleFields({
  roles,
  value,
  onChange,
  errors,
}: {
  roles: Role[];
  value: MemberValues['roles'];
  onChange: (v: MemberValues['roles']) => void;
  errors?: FieldErrors<MemberValues>['roles'];
}) {
  return (
    <fieldset className="form-section">
      <legend>空间角色与有效期</legend>
      {roles.map((role) => {
        const grant = value.find((v) => v.roleId === role.id);
        return (
          <div key={role.id} className="role-option stack compact">
            <Checkbox
              label={`${role.name} · ${role.code}`}
              description={`${role.description || '组内角色'} · 原生访问：${accessLabel(role.accessLevel)}`}
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
              <summary className="small text-link">
                查看平台权限（{role.permissions?.length ?? 0}）
              </summary>
              <div>
                {role.permissions?.map((p) => (
                  <span className="config-chip mono" key={p}>
                    {p}
                  </span>
                ))}
              </div>
            </details>
            {grant && role.code !== 'group_owner' && (
              <Input
                label={`${role.name} 到期时间（本地时间）`}
                type="datetime-local"
                step="0.001"
                value={grant.expiresAt}
                hint="留空为长期有效，保存时转换为 UTC"
                error={errors?.[value.findIndex((v) => v.roleId === role.id)]?.expiresAt?.message}
                onChange={(e) =>
                  onChange(
                    value.map((v) =>
                      v.roleId === role.id ? { ...v, expiresAt: e.target.value } : v,
                    ),
                  )
                }
              />
            )}
            {grant && role.code === 'group_owner' && (
              <p className="small muted">负责人必须长期有效；后端会保护最后一位负责人。</p>
            )}
          </div>
        );
      })}
      {value.some((v) => !roles.some((r) => r.id === v.roleId)) && (
        <p className="field-error" role="alert">
          已选角色不在当前目录中，请重新读取成员与角色。
        </p>
      )}
    </fieldset>
  );
}
