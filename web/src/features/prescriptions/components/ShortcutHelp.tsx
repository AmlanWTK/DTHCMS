'use client';

import { useTranslations } from 'next-intl';
import { useEffect, useRef } from 'react';

import { Button } from '@dthcms/ui';

import { PRESCRIPTION_SHORTCUTS } from './usePrescriptionShortcuts';

/**
 * The keyboard card, on screen (CP81).
 *
 * A shortcut nobody can discover is a shortcut for the person who wrote it, and in a clinic these
 * are learned from a printed card on a desk — so the overlay is the same list the documentation
 * carries, rendered from the same array the handler switches on. Two copies would agree until
 * somebody rebound a key.
 *
 * It takes focus when it opens and returns it when it closes, because a panel a keyboard user
 * cannot reach is a panel that is not there.
 */
export function ShortcutHelp({ onClose }: { onClose: () => void }) {
  const t = useTranslations('prescriptions');
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeRef.current?.focus();
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') onClose();
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  return (
    <div
      className="app-shortcuts"
      role="dialog"
      aria-modal="true"
      aria-label={t('shortcuts.title')}
    >
      <div className="app-shortcuts__panel">
        <h2>{t('shortcuts.title')}</h2>
        <dl className="app-shortcuts__list">
          {PRESCRIPTION_SHORTCUTS.map((shortcut) => (
            <div key={shortcut.keys}>
              <dt>
                <kbd>{shortcut.keys}</kbd>
              </dt>
              <dd>{t(`shortcuts.${shortcut.action}`)}</dd>
            </div>
          ))}
        </dl>
        <Button ref={closeRef} onClick={onClose}>
          {t('shortcuts.close')}
        </Button>
      </div>
    </div>
  );
}
