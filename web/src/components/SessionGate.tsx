'use client';

import { usePathname, useRouter } from 'next/navigation';
import { useTranslations } from 'next-intl';
import { useEffect, type ReactNode } from 'react';

import { Skeleton } from '@dthcms/ui';

import { useSessionStore } from '@/stores/session';

/**
 * Nothing behind the shell renders until the server has said who is looking at it.
 *
 * Three states, three outcomes. Before the first answer, a skeleton — not the sign-in page,
 * because most of the time the answer is "you", and a clinic tablet that flashed a login
 * form on every reload would teach its operators to distrust it. Once the answer is "nobody",
 * the sign-in page, remembering where they were trying to go. Once it is a person, the
 * screen.
 *
 * This is a courtesy, not a control. A person who bypasses it sees a shell whose every
 * request the server refuses.
 */
export function SessionGate({ children }: { children: ReactNode }) {
  const t = useTranslations();
  const router = useRouter();
  const pathname = usePathname();
  const status = useSessionStore((state) => state.status);
  const hydrate = useSessionStore((state) => state.hydrate);

  useEffect(() => {
    if (status === 'unknown') void hydrate();
  }, [status, hydrate]);

  useEffect(() => {
    if (status === 'anonymous') {
      router.replace(`/login?next=${encodeURIComponent(pathname)}`);
    }
  }, [status, pathname, router]);

  if (status !== 'authenticated') {
    return (
      <div className="app-centred" aria-busy="true">
        <Skeleton lines={3} className="app-centred__panel" label={t('session.checking')} />
      </div>
    );
  }

  return <>{children}</>;
}
