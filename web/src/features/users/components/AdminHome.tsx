'use client';

import Link from 'next/link';
import { useTranslations } from 'next-intl';

import { Card, Icon, type IconName } from '@dthcms/ui';

import { usePermission } from '@/lib/use-permission';
import type { PermissionAction } from '@/lib/permissions';

/**
 * The console's areas, as cards. Configuration of stations and the clinic itself arrive
 * with their own checkpoints; until then the three areas that exist are the three drawn.
 *
 * Each card is gated on the permission its page is gated on, so a card is never an
 * invitation to a 403. The list mirrors the admin section of `lib/navigation.ts` — the
 * sidebar and this page are two ways into the same three pages, and an area that appears
 * in one and not the other is a page somebody can only reach by accident.
 */
const AREAS: {
  href: string;
  icon: IconName;
  key: 'users' | 'devices' | 'audit';
  permission: PermissionAction;
}[] = [
  { href: '/admin/users', icon: 'users', key: 'users', permission: 'admin.users.manage' },
  { href: '/admin/devices', icon: 'tablet', key: 'devices', permission: 'admin.devices.manage' },
  { href: '/admin/audit', icon: 'scroll-text', key: 'audit', permission: 'admin.audit.view' },
];

export function AdminHome() {
  const t = useTranslations('page.admin.areas');
  const can: Record<string, boolean> = {
    'admin.users.manage': usePermission('admin.users.manage'),
    'admin.devices.manage': usePermission('admin.devices.manage'),
    'admin.audit.view': usePermission('admin.audit.view'),
  };
  const allowed = AREAS.filter((area) => can[area.permission]);

  return (
    <div className="app-area-grid">
      {allowed.map((area) => (
        <Link key={area.key} href={area.href} className="app-area-card">
          <Card>
            <div className="app-area-card__body">
              <span className="app-area-card__icon" aria-hidden="true">
                <Icon name={area.icon} size={24} />
              </span>
              <div>
                <div className="app-area-card__title">{t(`${area.key}.title`)}</div>
                <p className="app-page__description">{t(`${area.key}.body`)}</p>
              </div>
            </div>
          </Card>
        </Link>
      ))}
    </div>
  );
}
