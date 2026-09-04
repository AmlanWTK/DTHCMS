import { unitLabel } from '@/features/observations';
import type { Locale } from '@/lib/i18n/config';
import { formatMeasurement } from '@/lib/formatters';

import { hasEscalated, type CriticalAlert } from '../api/alerts';

/**
 * The words an alert is read in, decided once for both surfaces (CP50).
 *
 * The board and the patient strip say the same things about the same alert, and they must
 * not drift: a physician who reads "Low" on the ward screen and "Below the limit" on the
 * patient record has to work out whether those are the same finding, at a moment when
 * working anything out is the wrong use of their attention.
 *
 * Two rules are enforced here rather than left to each caller.
 *
 * **The name and the action are the server's, in the reader's language.** `display_bn` and
 * `action_bn` exist so that a Bangla-reading clinician is not handed an English instruction
 * to translate before acting. Falling back to English when a translation is missing is
 * deliberate: an English sentence is worth more than a blank space beside a panic value.
 *
 * **Severity is a word, never a colour.** These functions return message keys, so a caller
 * physically cannot render the tint without also rendering the label — which is what makes
 * the screen survive deuteranopia, a tablet in direct sun, and a photograph of the screen
 * sent to somebody in a group chat.
 */

/** The code's name — "Oxygen saturation", not "SPO2". The moment it matters is no time for a lookup. */
export function displayName(alert: CriticalAlert, locale: Locale): string {
  if (locale === 'bn' && alert.display_bn) return alert.display_bn;
  return alert.display_en ?? alert.code;
}

/** What to do about it. Absent on a rule nobody has written an action for yet. */
export function actionText(alert: CriticalAlert, locale: Locale): string | null {
  if (locale === 'bn' && alert.action_bn) return alert.action_bn;
  return alert.action_en ?? alert.action_bn ?? null;
}

/**
 * The severity word. `escalated` when the chain has moved on, because those are not the
 * same fact: one says a value is dangerous, the other says a value is dangerous *and
 * nobody has answered*.
 */
export function severityKey(alert: CriticalAlert): 'flag.critical' | 'flag.escalated' {
  return hasEscalated(alert) ? 'flag.escalated' : 'flag.critical';
}

/** Which end was crossed. Both clinically meaningful and opposite to each other. */
export function limitKey(alert: CriticalAlert): 'belowLimit' | 'aboveLimit' {
  return alert.breached === 'low' ? 'belowLimit' : 'aboveLimit';
}

/**
 * The status pill's name, which carries an arrow and a word as well as a hue.
 *
 * 3.0 mmol/L is as urgent as 25.0 and the two mean opposite things, so the direction is
 * kept as its own signal rather than folded into a single "critical".
 */
export function breachStatus(alert: CriticalAlert): 'low' | 'high' {
  return alert.breached === 'low' ? 'low' : 'high';
}

/**
 * A measurement, with the decimals the number itself asks for.
 *
 * A saturation of 88 must not read as "88.0" — the trailing zero is precision the pulse
 * oximeter never claimed — and a potassium of 3.1 must not read as "3".
 */
export function formatValue(value: number, locale: Locale): string {
  return formatMeasurement(value, locale, { decimals: Number.isInteger(value) ? 0 : 1 });
}

/** Whole minutes since the alert was raised, floored, never negative. */
export function minutesSince(iso: string, now: number): number {
  const raised = Date.parse(iso);
  if (Number.isNaN(raised)) return 0;
  return Math.max(0, Math.floor((now - raised) / 60_000));
}

/**
 * The unit as a clinician writes it, not as UCUM spells it.
 *
 * `mm[Hg]` is the code the record stores and the wrong thing to put in front of somebody who
 * is deciding whether to walk. The registry's display labels are the same ones the observation
 * screens use, so the number on this board reads exactly as it does everywhere else.
 */
export function unitText(unit: string | undefined, locale: Locale): string {
  const code = (unit ?? '').trim();
  if (code === '' || code === '1') return '';
  return unitLabel(code, locale.startsWith('bn') ? 'bn' : 'en');
}
