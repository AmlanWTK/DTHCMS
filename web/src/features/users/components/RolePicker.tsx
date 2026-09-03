'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useId, useMemo } from 'react';

import { Badge } from '@dthcms/ui';

import { permissionsOf, type RoleDefinition } from '../api/users';

/**
 * Choosing roles, with the consequence shown beside the choice.
 *
 * An administrator ticking "Nutritionist" is granting a list of permissions they may not
 * have memorised, so the list is printed as they tick — the effective-permission preview
 * of CP21 criterion 2. Clinical roles come first because most staff hold one.
 *
 * Roles already held (on the account page) are shown ticked and locked: revoking is a
 * separate act with a reason, not an untick.
 */
export function RolePicker({
  catalogue,
  chosen,
  held = [],
  onChange,
  disabled,
}: {
  catalogue: RoleDefinition[];
  chosen: string[];
  held?: string[];
  onChange: (roles: string[]) => void;
  disabled?: boolean;
}) {
  const t = useTranslations('users.roles');
  const tRole = useTranslations('role');
  const locale = useLocale();
  const groupId = useId();

  const ordered = useMemo(
    () => [...catalogue].sort((a, b) => Number(b.is_clinical) - Number(a.is_clinical)),
    [catalogue],
  );
  const preview = useMemo(
    () => permissionsOf([...held, ...chosen], catalogue),
    [held, chosen, catalogue],
  );
  const added = useMemo(() => {
    const before = new Set(permissionsOf(held, catalogue));
    return preview.filter((p) => !before.has(p));
  }, [preview, held, catalogue]);

  function toggle(code: string) {
    onChange(chosen.includes(code) ? chosen.filter((c) => c !== code) : [...chosen, code]);
  }

  return (
    <div className="app-role-picker">
      <fieldset className="app-role-picker__roles" disabled={disabled}>
        <legend className="dthc-field__label">{t('label')}</legend>
        <ul className="app-role-picker__list" aria-describedby={groupId}>
          {ordered.map((role) => {
            const locked = held.includes(role.code);
            const ticked = locked || chosen.includes(role.code);
            const name = locale === 'bn' ? role.name_bn : role.name_en;
            return (
              <li key={role.code}>
                <label className="app-role-picker__role" data-locked={locked || undefined}>
                  <input
                    type="checkbox"
                    name="roles"
                    value={role.code}
                    checked={ticked}
                    disabled={locked || disabled}
                    onChange={() => toggle(role.code)}
                  />
                  <span className="app-role-picker__name">
                    {name || tRole(role.code as never)}
                    <span className="app-role-picker__meta">
                      {role.code} · {t('permissionCount', { count: role.permissions.length })}
                    </span>
                  </span>
                  {role.is_clinical && <Badge tone="info">{t('clinical')}</Badge>}
                </label>
              </li>
            );
          })}
        </ul>
      </fieldset>
      <div className="app-role-picker__preview" id={groupId} aria-live="polite">
        <div className="app-role-picker__preview-title">
          {t('previewTitle', { count: preview.length })}
        </div>
        {preview.length === 0 ? (
          <p className="app-table__secondary">{t('previewEmpty')}</p>
        ) : (
          <ul className="app-permission-list">
            {preview.map((permission) => (
              <li key={permission} data-added={added.includes(permission) || undefined}>
                <code>{permission}</code>
              </li>
            ))}
          </ul>
        )}
        {held.length > 0 && added.length > 0 && (
          <p className="app-table__secondary">{t('previewAdded', { count: added.length })}</p>
        )}
      </div>
    </div>
  );
}
