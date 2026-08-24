import type { ReactNode } from 'react';

import { AppShell } from '@/components/AppShell';

/*
 * One of nine near-identical group layouts. They exist separately rather than as one
 * shared parent because a route group is the unit an error boundary attaches to: a
 * failure in the research area should not blank the clinical screen a physician is
 * reading. See AppShell for what the frame does.
 */
export default function GroupLayout({ children }: { children: ReactNode }) {
  return <AppShell>{children}</AppShell>;
}
