'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useTranslations } from 'next-intl';

import { Icon } from '@dthcms/ui';

import { groupForPath, itemForPath } from '@/lib/navigation';

/**
 * Breadcrumbs.
 *
 * Built from the navigation definition rather than from the path segments. A path-derived
 * trail on a clinical application produces "Patients / 0190a8f2-… / Visit / 3", which
 * tells a reader nothing and leaks an identifier into a place people screenshot. The
 * group and the screen are named; anything deeper is the screen's own to add once it
 * knows what the identifier means.
 */
export function Breadcrumbs() {
  const pathname = usePathname();
  const t = useTranslations();

  const group = groupForPath(pathname);
  const item = itemForPath(pathname);

  if (!group || !item) return null;

  return (
    <nav className="app-breadcrumbs" aria-label={t('nav.breadcrumb')}>
      <ol className="app-breadcrumbs__list">
        <li>
          <Link className="app-breadcrumbs__link" href="/dashboard">
            {t('nav.home')}
          </Link>
        </li>
        <li aria-hidden>
          <Icon name="chevron-right" size={16} />
        </li>
        <li>
          <span>{t(group.labelKey)}</span>
        </li>
        <li aria-hidden>
          <Icon name="chevron-right" size={16} />
        </li>
        <li>
          <span className="app-breadcrumbs__current" aria-current="page">
            {t(item.labelKey)}
          </span>
        </li>
      </ol>
    </nav>
  );
}
