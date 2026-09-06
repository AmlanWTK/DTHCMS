import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

/**
 * Who did something, as every screen in this application names a person (CP57, promoted at
 * CP61).
 *
 * # Why this moved out of the counselling panel
 *
 * It was written for CP57's per-item attribution and it solved the general problem: name the
 * person, keep the role beside it, fall back honestly, and never render a blank. CP61 asks
 * every screen to answer the same question about every clinical value, and a second
 * implementation of "what do we call this person" is how one screen ends up saying
 * "Clinical counselor" where another says "Rina Akter" for the same tick. So it lives here,
 * in the feature that owns attribution, and `counseling/components/panelText.ts` re-exports
 * it so that nothing which already imported it had to change.
 *
 * # Why the name leads and the role stays beside it
 *
 * The role tells a counsellor from a nutritionist and does not tell two counsellors apart.
 * §4.2 is a question about a *person* — the reviewer is looking at a value and wants to know
 * who entered it — so the name leads. The role is the other half of the answer and is never
 * dropped: it says which hat that person was wearing at the time, which is a property of the
 * value rather than of the person, and it is the only half that survives when the staff
 * record cannot be read at all.
 *
 * # Why an empty name is a sentence rather than a blank
 *
 * Names are resolved from the staff record rather than copied onto each value — which is
 * right, so somebody who changes their name reads correctly on last year's work — and that
 * lookup can come back empty: a person from another facility, a record removed, or a
 * directory request that failed. A blank there reads as a rendering fault, so this answers
 * `null` and the caller says "a member of staff whose record could not be read", which is
 * what actually happened.
 */
export interface StaffFields {
  code?: string;
  /**
   * A name already chosen for the reader's language.
   *
   * The directory lookup has picked one before this is called; a record that carries both
   * languages on the row itself — a counselling tick does — passes `name_en`/`name_bn`
   * instead and lets the fallback below choose. One of the two, never both, and this one
   * wins when it is set.
   */
  name?: string;
  name_en?: string;
  name_bn?: string;
  role?: string;
  /** The uuid, which is always there. Offered only when there is no staff code. */
  id?: string;
}

export interface StaffLabel {
  /** The person's name in the reader's language where there is one, otherwise the other. */
  name: string | null;
  /** The staff code — `E010` — or `null` when the record could not be read. */
  code: string | null;
  /** The role code, for the caller to translate. Never translated here. */
  role: string | null;
  /** The uuid, offered only when there is no code. Never both: one identifier, not two. */
  id: string | null;
}

export function staffLabel(who: StaffFields, locale: Locale): StaffLabel {
  const chosen = (who.name ?? '').trim();
  const name = chosen === '' ? bilingual(who.name_en ?? '', who.name_bn ?? '', locale) : null;
  const code = (who.code ?? '').trim();
  const role = (who.role ?? '').trim();
  const id = (who.id ?? '').trim();

  return {
    name: chosen === '' ? (name?.text ?? null) : chosen,
    code: code === '' ? null : code,
    role: role === '' ? null : role,
    id: code === '' && id !== '' ? id : null,
  };
}
