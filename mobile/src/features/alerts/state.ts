import type { components } from '@dthcms/api-client';

/**
 * Critical values, as the phone in an operator's hand understands them (CP50).
 *
 * # What is here and what is not
 *
 * Every decision lives in this file and nothing lives in the screen: which alert is drawn
 * first, what the operator is told to do, whether they are being asked to walk down the
 * corridor, and whether the alarm may stop. That is not tidiness — it is the only way any of
 * it is testable, because a React Native component cannot be rendered outside a device.
 *
 * # Why the phone evaluates thresholds at all
 *
 * It does not decide whether an alert exists: the server raises it, inside the transaction
 * that stores the value, and the write's own response carries it back. The rules are fetched
 * so the screen can *warn while the operator is still typing* — a red field before they press
 * save is worth more than a modal after it, and on a phone with no signal it is the only
 * warning there is.
 *
 * The list arrives already ordered most specific first, and this takes the first match. It
 * never ranks anything itself: a phone that ranked rules could sound an alarm the server did
 * not raise, or — far worse — stay quiet when the server did.
 */

export type CriticalRule = components['schemas']['CriticalValueRule'];
export type CriticalAlert = components['schemas']['CriticalAlert'];

export interface Subject {
  sex: 'male' | 'female' | 'other';
  ageYears: number;
}

/** The rule that applies to this patient and code, or none. First match wins. */
export function ruleFor(
  rules: readonly CriticalRule[],
  code: string,
  subject: Subject,
): CriticalRule | null {
  for (const rule of rules) {
    if (rule.code !== code) continue;
    if (rule.sex !== undefined && rule.sex !== subject.sex) continue;
    if (rule.min_age_years !== undefined && subject.ageYears < rule.min_age_years) continue;
    if (rule.max_age_years !== undefined && subject.ageYears >= rule.max_age_years) continue;
    return rule;
  }
  return null;
}

export type Breach = { breached: 'low' | 'high'; threshold: number; rule: CriticalRule };

/**
 * Whether a canonical value is critical, and which end it breached.
 *
 * Strict on both sides, exactly as the server is: 92 is not below 92. A phone that used `<=`
 * would turn a perfectly ordinary saturation into an alarm, and the third time that happened
 * the operator would stop reading the alarms.
 */
export function breachOf(value: number, rule: CriticalRule | null): Breach | null {
  if (rule === null) return null;
  if (rule.low !== undefined && value < rule.low) {
    return { breached: 'low', threshold: rule.low, rule };
  }
  if (rule.high !== undefined && value > rule.high) {
    return { breached: 'high', threshold: rule.high, rule };
  }
  return null;
}

/**
 * The alerts a write raised, in the order the operator should read them.
 *
 * Low breaches first. Not alphabetical and not the order the form happened to send them in:
 * a hypoglycaemia and a hypertension in the same entry are both urgent and only one of them
 * is treated in the next thirty seconds with something from a drawer.
 */
export function ordered(alerts: readonly CriticalAlert[]): CriticalAlert[] {
  return [...alerts].sort((a, b) => {
    if (a.breached !== b.breached) return a.breached === 'low' ? -1 : 1;
    return a.code.localeCompare(b.code);
  });
}

/**
 * What the operator is being asked to do about delivery (criterion 4).
 *
 *  - `delivered` — a clinician's screen has it; carry on.
 *  - `walk` — nobody was watching. Go and find somebody. This is the case the whole
 *    fail-safe exists for, and it must be unmissable rather than a footnote.
 */
export type DeliveryState = 'delivered' | 'walk';

export function deliveryOf(alerts: readonly CriticalAlert[]): DeliveryState {
  return alerts.every((alert) => alert.delivered) ? 'delivered' : 'walk';
}

/**
 * Whether the alarm may stop.
 *
 * Not on a timer, and not on the modal appearing: only when the operator has explicitly said
 * they have seen it. An alarm that stops by itself is an alarm somebody can be in the next
 * room for.
 *
 * Note what this is *not*: it is not the clinical acknowledgement. The officer at the station
 * cannot answer an alert — that is a clinician's act, and they do not hold the permission.
 * All they can do is confirm they have read it, and then either carry on or go and find
 * somebody. Conflating the two would let a clinic close its own alerts.
 */
export function alarmShouldSound(alerts: readonly CriticalAlert[], seen: boolean): boolean {
  return alerts.length > 0 && !seen;
}

/** The message key for the sentence under the number. */
export function actionTextOf(alert: CriticalAlert, language: 'en' | 'bn'): string {
  const text = language === 'bn' ? alert.action_bn : alert.action_en;
  return (text ?? '').trim();
}

/**
 * A short, unambiguous rendering of what breached.
 *
 * "88% — below 92%" rather than "SPO2 88". The operator has just typed the number; what they
 * have not got in their head is the threshold it crossed, and a screen that omits it asks
 * them to remember one under pressure.
 */
export function breachLine(alert: CriticalAlert): { value: string; limit: string } {
  const unit = alert.unit === '1' ? '' : (alert.unit ?? '');
  const number = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(1));
  // A space before the unit, except for the two that are written closed up. "88%" is right
  // and "204mmHg" is not — and in Bangla, where the unit is a word, running them together
  // makes the number end in a consonant cluster nobody can read at a glance.
  const closed = unit === '%' || unit.startsWith('°');
  const join = (value: number) => `${number(value)}${closed || unit === '' ? '' : ' '}${unit}`;
  return { value: join(alert.value), limit: join(alert.threshold) };
}

/**
 * The code's name, in the reader's language, as the server joined it from the registry.
 *
 * A fallback to the code itself rather than an empty label: a screen that showed a blank
 * where "Oxygen saturation" belongs is worse than one showing SPO2, and the fallback only
 * fires for a code added between the server's deploy and the phone's.
 */
export function nameOf(alert: CriticalAlert, language: 'en' | 'bn'): string {
  const name = language === 'bn' ? alert.display_bn : alert.display_en;
  return (name ?? '').trim() === '' ? alert.code : (name as string);
}
