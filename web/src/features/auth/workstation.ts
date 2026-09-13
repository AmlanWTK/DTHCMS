/**
 * The workstation code this browser last signed in with (CP82, ADR-0021).
 *
 * # Why this is in `localStorage`, and why that does not touch ADR-0010
 *
 * ADR-0010 forbids **session credentials** in web storage, for a reason that is specific and
 * worth restating rather than generalising: a token in `localStorage` turns one cross-site
 * scripting hole into stolen credentials that keep working from the attacker's own machine.
 * The value stored here is not one of those and could not become one.
 *
 * A workstation code is a label. It is printed on a sticker and stuck to the monitor this
 * browser is running on, in a room patients walk through. It authenticates nothing, grants
 * nothing, and opens nothing: the server resolves it to a desk *after* the password has
 * already been verified, and a wrong one does not even refuse the sign-in. An attacker who
 * reads it out of storage learns the same thing they would learn by looking at the screen.
 *
 * So this is not an exception to ADR-0010 and must not be "fixed" into one. Deleting this
 * would not make anything safer; it would make the registration desk type `FRD-REG-1` at
 * every sign-in, which is how a code ends up written on a different sticker, in a different
 * hand, saying something else.
 *
 * The decision is ADR-0021 §4, in those words: *the browser stores the code — not a token —
 * so the desk types it once. A code in web storage is a label, not a credential; ADR-0010's
 * rule is unaffected.*
 *
 * # Why it is not keyed by user
 *
 * The dashboard's remembered layout is keyed by user id, because it is a preference and this
 * clinic's browsers are shared. This is the opposite kind of thing: it is a fact about the
 * *machine*, and it is the same fact for everybody who sits down at it. Keying it by user
 * would mean the third person of the morning typing it again, which is the one thing this
 * exists to avoid.
 */

const KEY = 'dthcms.workstation';

/**
 * The shape the server will accept — `FRD-REG-1` — checked here so that a stored value from
 * an older format, or one a person edited by hand, is ignored rather than sent.
 */
const SHAPE = /^[A-Z]{2,4}(-[A-Z0-9]{1,6}){1,3}$/;

/** Upper case, no spaces: what somebody copying a sticker is forgiven. Mirrors the server. */
export function normaliseWorkstation(code: string): string {
  return code.trim().toUpperCase().replace(/\s+/g, '').replace(/_/g, '-');
}

/** The code this browser last used, or the empty string. Never throws. */
export function readWorkstation(): string {
  try {
    /*
     * ADR-0010 forbids a session credential in web storage, and the rule's own message says
     * to disable it on the line and name what this is. This is a workstation code: the label
     * printed on the sticker on this monitor, such as FRD-REG-1. It is not a credential — it
     * is not verified, it grants nothing, and a wrong one does not refuse a sign-in. ADR-0021
     * §4 decides that it is kept here so the desk types it once. Please do not "fix" this.
     */
    // eslint-disable-next-line no-restricted-properties
    const stored = window.localStorage.getItem(KEY);
    if (!stored) return '';
    const normalised = normaliseWorkstation(stored);
    return SHAPE.test(normalised) ? normalised : '';
  } catch {
    // A private window, or an enterprise policy that refuses storage. The person types the
    // code; nothing else about the screen changes.
    return '';
  }
}

/**
 * Remember the code, or forget it when it is empty.
 *
 * Called only after a sign-in the server recognised the code for. A code that named no desk
 * is deliberately not remembered: keeping it would make every subsequent sign-in at this
 * machine fail the same way, silently, which is how a typo becomes a fortnight of records
 * with no device on them.
 */
export function rememberWorkstation(code: string): void {
  try {
    // eslint-disable-next-line no-restricted-properties
    if (!code) window.localStorage.removeItem(KEY);
    // eslint-disable-next-line no-restricted-properties
    else window.localStorage.setItem(KEY, normaliseWorkstation(code));
  } catch {
    // A browser that will not remember a label still signed the person in.
  }
}
