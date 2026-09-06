import { bilingual, type BilingualText } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import type { CounselingItem } from '../api/counseling';
import type { CounselingMissingItem, CounselingSession } from '../api/gate';

/**
 * The words the physician's panel reads a checklist back in (CP57, §5.4).
 *
 * # Why this is not `counselingText.ts`
 *
 * `counselingText.ts` deliberately refuses to fall back: `itemText` answers `null` when the
 * reader's language is empty, because on an authoring screen the missing half is the thing
 * the author is there to find, and quietly showing the English would hide it.
 *
 * This screen is not that screen. A physician is standing in front of a patient with about a
 * minute, asking what they were told about injection sites. Drawing a blank where an item's
 * Bangla is missing would remove the item from a panel whose entire job is to say what was
 * covered — and the item *was* covered, by somebody who read it in the language it does
 * exist in.
 *
 * So this falls back, and says that it has. `translated: false` means the reader is looking
 * at the other language, the component marks it, and the fact stays visible instead of being
 * either hidden or turned into a hole. The two rules disagree on purpose, and each is right
 * where it lives.
 *
 * The item **code** is the last resort and is never silently dropped: it is what a tick
 * references and what an override's `missing_at_grant` records, so a physician who has to
 * ask a counsellor about a row can at least name it exactly.
 */

/*
 * `BilingualText` and `bilingual` moved to `@/lib/bilingual` at CP61 and are re-exported
 * here unchanged. They were never specific to counselling — the attribution component needs
 * exactly the same "reader's language, otherwise the other, and say which" rule — and two
 * implementations of a fallback are how two screens end up disagreeing about whether an
 * empty Bangla field is a blank or a fallback.
 */
export type { BilingualText };

/** What this item says, in the reader's language where it exists. */
export function itemLabel(item: CounselingItem, locale: Locale): BilingualText | null {
  return bilingual(item.text_en, item.text_bn, locale);
}

/**
 * What a missing item says.
 *
 * The gate carries both languages on every missing item — the contract requires `text_en`
 * and `text_bn` — which is why a blocked screen can name what is outstanding in words rather
 * than in codes. Same fallback, for the same reason: an item nobody translated is still an
 * item nobody covered.
 */
export function missingLabel(item: CounselingMissingItem, locale: Locale): BilingualText | null {
  return bilingual(item.text_en, item.text_bn, locale);
}

/**
 * What this checklist is called.
 *
 * The template's title is written in both languages when it is created and the server
 * refuses one missing either, so the ordinary fallback holds. The code is the last resort
 * because it is at least a thing a person can look up — and on a session opened against a
 * template that has since been renamed, it is the honest identifier.
 */
export function sessionTitle(session: CounselingSession, locale: Locale): string {
  const label = bilingual(session.title_en ?? '', session.title_bn ?? '', locale);
  return label?.text ?? session.template_code ?? session.template_id;
}

/**
 * What the checklist an outstanding item belongs to is called.
 *
 * The gate now carries the template's own title on every missing item, so a refusal reads as
 * "Diabetes counselling" rather than as `DIABETES`. The code is the last resort and is worth
 * keeping as one: it is what an assignment rule and an audit entry name, and it is what a
 * physician can quote to a counsellor when the title is a blank.
 */
export function missingChecklistTitle(item: CounselingMissingItem, locale: Locale): string {
  const label = bilingual(item.title_en ?? '', item.title_bn ?? '', locale);
  return label?.text ?? item.template_code;
}

/**
 * What the room an outstanding item belongs to is called.
 *
 * From the missing item itself. The room's own names arrive on it now, which is why nothing
 * on this panel fetches the room catalogue: a refusal that had to wait on a second request to
 * say "insulin corner" would say `INSULIN_CORNER` for as long as that request took, on the
 * one screen whose whole job is telling somebody which room to walk the patient to.
 */
export function missingRoomName(item: CounselingMissingItem, locale: Locale): string {
  const label = bilingual(item.room_en ?? '', item.room_bn ?? '', locale);
  return label?.text ?? item.room;
}

/*
 * `staffLabel` moved to `@/features/attribution` at CP61 and is re-exported here unchanged.
 *
 * It was written for this panel and it solved the general problem — name the person, keep
 * the role beside it, fall back honestly, never render a blank — which is exactly what CP61
 * asks every screen in the application to do about every clinical value. It now lives in the
 * feature that owns attribution; the re-export is so that nothing which already imported it
 * from here had to change, and so that `@/features/counseling` keeps the public surface its
 * own tests read.
 */
export { staffLabel, type StaffFields, type StaffLabel } from '@/features/attribution';
