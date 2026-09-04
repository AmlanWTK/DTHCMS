import { deleteSecureItem, getSecureItem, setSecureItem } from '@/lib/secure-storage';

/**
 * A registration in progress (CP33, criterion 3).
 *
 * On a phone an interruption is not an edge case: a call comes in, the screen locks, the app
 * is swapped out for the camera, Android reclaims memory. A registration that loses eight
 * fields to any of those is a registration the operator does on paper instead.
 *
 * Three decisions:
 *
 *	**In the Keystore.** A draft holds a name, a telephone number and a date of birth — the
 *	same data the finished record holds. AsyncStorage is plain files a device backup carries
 *	off, and that is the disclosure the finished record is protected from.
 *
 *	**One draft.** Not a queue. A queue of half-registered people is a queue nobody
 *	reconciles, and the resume prompt has to be able to name one person to be answerable.
 *
 *	**It expires.** A draft older than the clinic day is stale: the patient has left. Kept
 *	longer it becomes a name in the Keystore of a phone in a drawer, which is the kind of
 *	residue nobody thinks to look for.
 */

/** How long a draft is worth resuming. One clinic day; after that the patient has gone. */
export const DRAFT_TTL_MS = 12 * 60 * 60 * 1000;

export interface Draft<T> {
  /** The event id, minted once so a resumed registration is still one registration. */
  eventID: string;
  /** How far through the operator got, so resuming lands where they left off. */
  step: number;
  savedAt: number;
  values: T;
}

export async function saveDraft<T>(draft: Draft<T>): Promise<void> {
  await setSecureItem('registrationDraft', JSON.stringify(draft));
}

/**
 * The draft, if there is one worth resuming.
 *
 * A stale or unreadable draft is discarded rather than surfaced. Offering to resume
 * something that then fails to load is worse than offering nothing: the operator has already
 * decided not to start again.
 */
export async function loadDraft<T>(now: number = Date.now()): Promise<Draft<T> | null> {
  const raw = await getSecureItem('registrationDraft');
  if (!raw) return null;
  try {
    const draft = JSON.parse(raw) as Draft<T>;
    if (!draft.eventID || typeof draft.savedAt !== 'number') throw new Error('shape');
    if (now - draft.savedAt > DRAFT_TTL_MS) {
      await clearDraft();
      return null;
    }
    return draft;
  } catch {
    await clearDraft();
    return null;
  }
}

export async function clearDraft(): Promise<void> {
  await deleteSecureItem('registrationDraft');
}

/** Whether a draft is stale, for the tests and for a screen that wants to say so. */
export function isStale(draft: { savedAt: number }, now: number = Date.now()): boolean {
  return now - draft.savedAt > DRAFT_TTL_MS;
}
