import type { Locale } from '@/lib/i18n/config';

import type { CounselingItem, CounselingRoom, CounselingTemplate } from '../api/counseling';

/**
 * The words a checklist is read in (CP55).
 *
 * Separated from the components for the same reason `historyText.ts` and `allergyText.ts`
 * are: these are decisions about what a thing is *called*, four screens ask them, and a
 * second copy is how the preview and the editor come to disagree about what the author is
 * looking at — which, on this screen, is the one comparison that matters.
 *
 * # Two different fallback rules, on purpose
 *
 * Everywhere else in this application the fallback goes one way: the reader's language if
 * there is one, English otherwise, the code last — because an English word is more use to a
 * reader of Bangla than a blank space.
 *
 * That rule is right for a *room name*, which is reference data the server always sends in
 * both languages, and it is `roomName` below.
 *
 * It is wrong for an **item's own text**, and `itemText` deliberately does not do it. A
 * half-written item is the normal state of a draft, and this whole checkpoint turns on
 * criterion 4: every item must read in both languages before it goes to the floor. An
 * editor that quietly showed the English where the Bengali is missing would hide exactly
 * the gap the author is there to close, and the preview — the screen whose entire job is
 * "what will a counsellor see on their phone" — would show a checklist that reads
 * completely and then be refused at publish for a reason nothing on screen displayed. So
 * `itemText` answers `null` when that language is empty, and the callers draw the absence.
 */

/** What this room is called. Reference data, always bilingual, so the usual fallback holds. */
export function roomName(room: CounselingRoom | undefined, code: string, locale: Locale): string {
  if (!room) return code;
  if (locale === 'bn' && room.display_bn) return room.display_bn;
  return room.display_en || room.display_bn || code;
}

/**
 * The item's text in one language, or `null` when it has not been written yet.
 *
 * `null` rather than a fallback. See the note above: the missing half is the thing the
 * author is here to find.
 */
export function itemText(item: CounselingItem, locale: Locale): string | null {
  const text = locale === 'bn' ? item.text_bn : item.text_en;
  return text.trim() === '' ? null : text;
}

/**
 * What to actually say, where the item has it. §5.4 is the reason it exists: the physician
 * spot-questions the patient afterwards, and two counsellors who covered "injection sites"
 * differently make that check useless.
 *
 * Guidance is genuinely optional, so an absent one is nothing rather than a gap to report.
 */
export function itemGuidance(item: CounselingItem, locale: Locale): string | null {
  const text = (locale === 'bn' ? item.guidance_bn : item.guidance_en) ?? '';
  return text.trim() === '' ? null : text;
}

/**
 * What this checklist is called.
 *
 * The template's title is written in both languages when it is created — the server refuses
 * a template missing either — so the ordinary fallback is safe here, and the code is the
 * last resort because it is at least something a person can look up.
 */
export function templateTitle(template: CounselingTemplate, locale: Locale): string {
  if (locale === 'bn' && template.title_bn) return template.title_bn;
  return template.title_en || template.title_bn || template.code;
}
