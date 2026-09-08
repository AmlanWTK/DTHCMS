'use client';

import { useTranslations } from 'next-intl';
import { useEffect, useRef } from 'react';

import { Button } from '@dthcms/ui';

import { DASHBOARD_SHORTCUTS } from './useDashboardShortcuts';

/**
 * The keyboard shortcuts, on screen (CP73).
 *
 * # Why this exists rather than a line in the documentation
 *
 * A shortcut nobody can discover is a shortcut for the person who wrote it. In this clinic
 * they will be learned from somebody looking over a shoulder and from a card taped to a desk,
 * and both of those start with somebody pressing `?` once and reading the list.
 *
 * # Focus, because the shortcuts are what opened it
 *
 * Focus moves into the panel when it opens and returns to whatever had it when it closes.
 * Without the return, a physician who opened this with `?` and closed it with Escape would
 * find their next Tab starting at the top of the document — which is the accessibility bug
 * that makes people stop using keyboard navigation altogether.
 *
 * It is not a modal. Nothing behind it is disabled, there is no scrim, and Escape closes it:
 * it is a reference somebody glances at while the screen behind stays readable, and trapping
 * focus in a list of shortcuts would be trapping a physician in the help.
 */

export interface ShortcutHelpProps {
  onClose: () => void;
}

export function ShortcutHelp({ onClose }: ShortcutHelpProps) {
  const t = useTranslations('dashboard.shortcuts');
  const panelRef = useRef<HTMLDivElement>(null);
  const returnTo = useRef<HTMLElement | null>(null);

  useEffect(() => {
    returnTo.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    panelRef.current?.focus();
    return () => {
      // Guarded: the element that had focus may have unmounted while the panel was open — a
      // realtime refresh replacing a card, most likely — and calling `focus` on a detached
      // node silently does nothing, which would leave focus at the document root.
      if (returnTo.current?.isConnected) returnTo.current.focus();
    };
  }, []);

  return (
    <div
      className="dash-shortcuts"
      role="region"
      aria-label={t('title')}
      tabIndex={-1}
      ref={panelRef}
      data-testid="shortcut-help"
    >
      <div className="dash-shortcuts__head">
        <h2>{t('title')}</h2>
        <Button variant="quiet" size="sm" onClick={onClose} data-testid="close-shortcuts">
          {t('close')}
        </Button>
      </div>
      <dl className="dash-shortcuts__list">
        {DASHBOARD_SHORTCUTS.map((shortcut) => (
          <div key={shortcut.action} className="dash-shortcuts__row">
            <dt>
              {shortcut.keys.map((key) => (
                <kbd key={key}>{key}</kbd>
              ))}
            </dt>
            <dd>{t(`action.${shortcut.action}`)}</dd>
          </div>
        ))}
      </dl>
      <p className="dash-shortcuts__note">{t('note')}</p>
    </div>
  );
}
