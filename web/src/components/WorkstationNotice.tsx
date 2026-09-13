'use client';

import { useEffect } from 'react';
import { useTranslations } from 'next-intl';

import { AlertBanner } from '@dthcms/ui';

import { useWorkstationNotice } from '@/features/auth';

/**
 * "This workstation was not recognised", carried through the redirect (CP82, ADR-0021).
 *
 * The sign-in screen raises the flag and is immediately replaced by the destination, so the
 * sentence has to be drawn by the frame rather than by the form. It sits at the top of every
 * signed-in screen and stays there — through navigation, through a reload — until the person
 * dismisses it or signs in again with a code that names a desk.
 *
 * `stale` rather than `critical`: nothing is broken and nothing was refused. What is true is
 * that this session has no device, so every clinical write from it will be refused with
 * DEVICE_REQUIRED, and the person needs to know that *before* a patient is standing in front
 * of them rather than from a 403.
 */
export function WorkstationNotice() {
  const t = useTranslations('login');
  const unrecognised = useWorkstationNotice((state) => state.unrecognised);
  const hydrate = useWorkstationNotice((state) => state.hydrate);
  const clear = useWorkstationNotice((state) => state.clear);

  // Read after mount: the server render has no storage, and disagreeing with it here is a
  // hydration error that blanks the screen the banner is supposed to sit on.
  useEffect(() => {
    hydrate();
  }, [hydrate]);

  if (!unrecognised) return null;

  return (
    <AlertBanner tone="stale" title={t('workstationUnknownTitle')} onDismiss={clear}>
      {t('workstationUnknownBody')}
    </AlertBanner>
  );
}
