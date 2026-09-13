import { create } from 'zustand';

/**
 * The notice that this browser signed in naming a desk the clinic does not have (CP82,
 * ADR-0021).
 *
 * # Why this is not a piece of the sign-in form's state
 *
 * ADR-0021 decides that an unrecognised workstation code **still signs you in** — refusing
 * would let an unauthenticated caller enumerate the clinic's desks, and would take a
 * registration desk down over a typo. That decision is only defensible because the screen
 * says so at sign-in.
 *
 * It did not. The session store marks the session authenticated synchronously inside
 * `signIn()`, so the sign-in screen's redirect effect fires in the same commit that would
 * have painted the banner: the notice was never visible, not for a single frame, and the
 * desk found out at the first patient registration, from a 403 DEVICE_REQUIRED, with a
 * patient in front of them.
 *
 * A banner that lives in the form cannot survive the navigation the form triggers. So the
 * fact lives here — outside the component tree — and the shell draws it on whatever screen
 * the person lands on, until they acknowledge it or sign in again with a code that names a
 * desk.
 *
 * # Why the flag is mirrored into `sessionStorage`
 *
 * A reload must not quietly clear it: the state it describes — a session with no device —
 * survives a reload, and a notice that does not is a notice that lies by omission. It is in
 * `sessionStorage` rather than `localStorage` because it is a fact about *this session*, and
 * it is a boolean rather than the code itself, so nothing that was typed and rejected is
 * written to disk. That last part matters: ADR-0021 §4 remembers a *recognised* code only,
 * and `rememberWorkstation` is what does it.
 */

const KEY = 'dthcms.workstation-unrecognised';

function persist(raised: boolean): void {
  try {
    // eslint-disable-next-line no-restricted-properties
    if (raised) window.sessionStorage.setItem(KEY, '1');
    // eslint-disable-next-line no-restricted-properties
    else window.sessionStorage.removeItem(KEY);
  } catch {
    // A private window, or a policy that refuses storage. The banner still shows for as
    // long as the tab lives, which is the case this exists for.
  }
}

function stored(): boolean {
  try {
    // eslint-disable-next-line no-restricted-properties
    return window.sessionStorage.getItem(KEY) === '1';
  } catch {
    return false;
  }
}

interface WorkstationNoticeState {
  /** Whether the last sign-in named a desk this clinic does not have. */
  unrecognised: boolean;
  /** The server did not recognise the code. Raised by the sign-in form. */
  raise: () => void;
  /** A good code, a sign-out, or the person acknowledging the banner. */
  clear: () => void;
  /** Read the flag back after a reload. Safe to call on every mount. */
  hydrate: () => void;
}

export const useWorkstationNotice = create<WorkstationNoticeState>((set) => ({
  unrecognised: false,

  raise: () => {
    persist(true);
    set({ unrecognised: true });
  },

  clear: () => {
    persist(false);
    set({ unrecognised: false });
  },

  hydrate: () => set({ unrecognised: stored() }),
}));
