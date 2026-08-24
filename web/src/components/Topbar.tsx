'use client';

import { useTranslations } from 'next-intl';

import { Button } from '@dthcms/ui';

import { useSessionStore } from '@/stores/session';
import { useUiStore } from '@/stores/ui';
import { LanguageToggle } from '@/components/LanguageToggle';
import { RoleSwitcher } from '@/components/RoleSwitcher';

/**
 * The top bar.
 *
 * It carries the clinic's identity, the language switch and who is signed in — and
 * nothing else. Everything a clinic dashboard is tempted to put here (counts, alerts,
 * a search box) belongs to a screen, where it can be wrong in a way somebody notices.
 */
export function Topbar() {
  const t = useTranslations();
  const toggleSidebar = useUiStore((state) => state.toggleSidebar);
  const user = useSessionStore((state) => state.user);
  const activeRole = useSessionStore((state) => state.activeRole);

  return (
    <header className="app-topbar">
      <Button
        className="app-menu-button"
        size="md"
        variant="quiet"
        iconStart="menu"
        aria-label={t('nav.menu')}
        onClick={toggleSidebar}
      />

      <div className="app-topbar__brand">
        <span className="app-topbar__name">{t('app.name')}</span>
        <span className="app-topbar__clinic">{t('app.fullName')}</span>
      </div>

      <div className="app-topbar__spacer" />

      {/* Scaffolding until CP16. See RoleSwitcher. */}
      <RoleSwitcher />

      <LanguageToggle />

      {user && activeRole && (
        <div className="app-topbar__identity">
          <span>{t('shell.signedInAs', { name: user.displayName })}</span>
          <span>{t(`shell.role.${activeRole}`)}</span>
        </div>
      )}
    </header>
  );
}
