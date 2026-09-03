'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useTranslations } from 'next-intl';

import { AlertBanner } from '@dthcms/ui';

import { needsEnrolment, useSessionStore } from '@/stores/session';

/**
 * "Set up your authenticator" — shown on every shelled screen to a person whose roles
 * require one and who has not done it yet (D-45). Not a wall: a physician can still read
 * the queue. But nothing privileged is possible until this is done, and the banner says
 * so every time, which is the point.
 */
export function SecondFactorNudge() {
  const t = useTranslations('secondFactor');
  const user = useSessionStore((state) => state.user);
  const pathname = usePathname();

  if (!needsEnrolment(user) || pathname === '/account/security') return null;

  return (
    <AlertBanner
      tone="high"
      title={t('nudgeTitle')}
      action={
        // A link styled as the primary button: the destination is a page, and a link is
        // what a page deserves — middle-click, copy address, keyboard semantics.
        <Link className="dthc-button dthc-button--primary dthc-button--sm" href="/account/security">
          <span className="dthc-button__label">{t('nudgeAction')}</span>
        </Link>
      }
    >
      {t('nudgeBody')}
    </AlertBanner>
  );
}
