'use client';

import { useCallback, useEffect, useState } from 'react';

import { useSessionStore } from '@/stores/session';

/**
 * Which panels are open, remembered per person (CP73's open decision).
 *
 * # The decision, and whose it is
 *
 * The plan lists panel priority and default state as an open decision — a clinical
 * preference. What ships is: **left and centre open, right open but collapsible, and the
 * state remembered per user.** That is a proposal, not an answer, and `docs/progress.md`
 * records it as awaiting Dr. Nahid's confirmation. He is the only person who can say what he
 * wants to see first.
 *
 * The reasoning behind the proposal, so there is something to disagree with: the left panel is
 * the record and the centre is the summary of it, and those two are what a consultation opens
 * with. The right panel is where the physician *acts* on the AI, which happens later in the
 * consultation and not at all on many of them — so it is the one that can be out of the way
 * without the screen losing its purpose.
 *
 * # Why `localStorage` and not the server
 *
 * A collapsed panel is a preference about a screen, not a fact about the clinic. Putting it on
 * the server would mean a table, a migration, a permission and a write on every toggle — for a
 * boolean whose worst failure mode is a panel opening when somebody expected it closed.
 *
 * It is keyed by user id, and that is the part that matters: this clinic's browsers are
 * **shared**. Three physicians and a junior doctor sign in and out of the same machine in a
 * morning, and an unkeyed preference would mean each of them inheriting the last one's layout
 * — which reads as the software forgetting, and is worse than no memory at all.
 *
 * # Why every access is wrapped
 *
 * `localStorage` throws rather than returning null in a private window and under some
 * enterprise policies, and the throw happens on *access to the property*, not on the read. A
 * dashboard that failed to render because a browser refused to store a boolean would be a
 * clinical screen lost to a preference.
 */

export interface DashboardLayout {
  rightOpen: boolean;
  toggleRight: () => void;
}

const KEY_PREFIX = 'dthcms.dashboard.layout';

export function useDashboardLayout(): DashboardLayout {
  const userId = useSessionStore((store) => store.user?.id ?? null);
  const [rightOpen, setRightOpen] = useState(true);

  // Read after mount rather than in the initial state, because the server render has no
  // storage and a mismatch between the two is a hydration error that blanks the screen. The
  // default is "open", so the flash — if any — is towards showing more rather than less.
  useEffect(() => {
    if (userId === null) return;
    setRightOpen(read(userId));
  }, [userId]);

  const toggleRight = useCallback(() => {
    setRightOpen((current) => {
      const next = !current;
      if (userId !== null) write(userId, next);
      return next;
    });
  }, [userId]);

  return { rightOpen, toggleRight };
}

function read(userId: string): boolean {
  try {
    /*
     * ADR-0010 forbids a session credential in web storage, and the rule's own message says
     * to disable it on the line and name what this is. This is the string "open" or "closed":
     * whether the physician last had the assistant column showing. It is not a credential, it
     * is not clinical, and it is keyed by user id because this clinic's browsers are shared.
     */
    // eslint-disable-next-line no-restricted-properties
    const stored = window.localStorage.getItem(`${KEY_PREFIX}.${userId}`);
    // Absent means "never chosen", which takes the default. Only an explicit "closed" closes
    // it: a parse that treated anything-but-"open" as closed would hide the panel on the
    // first load after the key's format ever changed.
    return stored !== 'closed';
  } catch {
    return true;
  }
}

function write(userId: string, open: boolean): void {
  try {
    /*
     * The other half of `read` above: the word "open" or "closed" against a user id. Nothing
     * here identifies a patient and nothing here would let anybody in.
     */
    // eslint-disable-next-line no-restricted-properties
    window.localStorage.setItem(`${KEY_PREFIX}.${userId}`, open ? 'open' : 'closed');
  } catch {
    // A browser that will not store a preference still shows the panel the person asked for
    // until they navigate. Losing it on the next load is the whole cost.
  }
}
