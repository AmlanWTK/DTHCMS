/**
 * Impossible inputs, caught while the operator is still holding the tape measure (CP46).
 *
 * # Why the client has this at all
 *
 * The server is authoritative and always checks. But a refusal that arrives after the save
 * button is a refusal that arrives after the operator has moved on to the next field, and
 * often after the patient has stood up. The point of the checkpoint is to catch a typing
 * error *while it can still be re-measured*, and that means the warning has to appear as the
 * number is typed, on a tablet that may have no signal.
 *
 * # Why this cannot drift from the server
 *
 * The rules come from `GET /v1/observations/plausibility`, **already ordered most specific
 * first** — in exactly the order `core.plausibility_for` resolves them. So the rule that
 * applies is "the first one in the list whose predicate matches", and this file never ranks
 * anything. A client reimplementing the specificity ranking is a client that one day shows an
 * operator one band and is refused by another.
 */

export interface PlausibilityRule {
  code: string;
  sex?: 'male' | 'female';
  min_age_years?: number;
  max_age_years?: number;
  absolute_min?: number;
  absolute_max?: number;
  plausible_min?: number;
  plausible_max?: number;
  max_increase?: number;
  max_decrease?: number;
  max_increase_per_day?: number;
  max_decrease_per_day?: number;
  note_en?: string;
  note_bn?: string;
  /** Whether a clinician has signed off on these numbers. The seeded bands are proposals. */
  approved: boolean;
}

export interface PlausibilitySubject {
  sex: 'male' | 'female' | 'other';
  ageYears: number;
}

/**
 * What a rule said about a value.
 *
 * `stop` cannot be saved by anybody. `warn` can, with an explicit confirmation — and the
 * difference matters more than it looks: a system that refused every unusual value would be
 * a system that cannot record the tallest patient in Faridpur, and staff who discover that
 * work around the system, which costs more than the typing errors did.
 */
export interface PlausibilityVerdict {
  severity: 'stop' | 'warn';
  /** `low`, `high`, `rose` or `fell`. */
  kind: 'low' | 'high' | 'rose' | 'fell';
  /** The band edge or change limit that was crossed, in canonical units. */
  limit: number;
  /** The previous value, for a delta verdict. */
  previous?: number;
  note_en?: string;
  note_bn?: string;
}

/** The rule that applies: the first match in the server's own order. */
export function resolveRule(
  rules: readonly PlausibilityRule[],
  code: string,
  subject: PlausibilitySubject,
): PlausibilityRule | null {
  for (const rule of rules) {
    if (rule.code !== code) continue;
    if (rule.sex !== undefined && rule.sex !== subject.sex) continue;
    if (rule.min_age_years !== undefined && subject.ageYears < rule.min_age_years) continue;
    if (rule.max_age_years !== undefined && subject.ageYears >= rule.max_age_years) continue;
    return rule;
  }
  return null;
}

export interface PreviousMeasurement {
  /** Canonical value. */
  value: number;
  /** When it was taken, so a per-day rate means something. */
  at: Date;
}

/**
 * Evaluate one canonical value against its rule.
 *
 * Ordered so the first thing an operator sees is the most actionable: impossible before
 * implausible, and the value before the delta — because a value that is itself wrong makes
 * its delta wrong too, and telling somebody their weight changed by 300 kg when they typed
 * 372 instead of 72 sends them to the wrong question.
 */
export function evaluate(
  rule: PlausibilityRule | null,
  canonical: number | null,
  options?: { previous?: PreviousMeasurement; now?: Date; confirmed?: boolean },
): PlausibilityVerdict | null {
  if (rule === null || canonical === null || !Number.isFinite(canonical)) return null;

  const note = { note_en: rule.note_en, note_bn: rule.note_bn };

  if (rule.absolute_min !== undefined && canonical < rule.absolute_min) {
    return { severity: 'stop', kind: 'low', limit: rule.absolute_min, ...note };
  }
  if (rule.absolute_max !== undefined && canonical > rule.absolute_max) {
    return { severity: 'stop', kind: 'high', limit: rule.absolute_max, ...note };
  }

  // Everything below is confirmable — but only after the absolute band above, which no
  // confirmation passes.
  if (options?.confirmed === true) return null;

  if (rule.plausible_min !== undefined && canonical < rule.plausible_min) {
    return { severity: 'warn', kind: 'low', limit: rule.plausible_min, ...note };
  }
  if (rule.plausible_max !== undefined && canonical > rule.plausible_max) {
    return { severity: 'warn', kind: 'high', limit: rule.plausible_max, ...note };
  }

  const previous = options?.previous;
  if (previous === undefined) return null;

  const change = canonical - previous.value;
  const days = ((options?.now ?? new Date()).getTime() - previous.at.getTime()) / 86_400_000;

  if (change > 0 && rule.max_increase !== undefined && change > rule.max_increase) {
    return {
      severity: 'warn',
      kind: 'rose',
      limit: rule.max_increase,
      previous: previous.value,
      ...note,
    };
  }
  if (change < 0 && rule.max_decrease !== undefined && -change > rule.max_decrease) {
    return {
      severity: 'warn',
      kind: 'fell',
      limit: rule.max_decrease,
      previous: previous.value,
      ...note,
    };
  }

  // A rate needs a gap to be a rate. Under a day, two measurements are the same visit and
  // their difference is a re-measurement, not a trend.
  if (days < 1) return null;

  if (
    change > 0 &&
    rule.max_increase_per_day !== undefined &&
    change / days > rule.max_increase_per_day
  ) {
    return {
      severity: 'warn',
      kind: 'rose',
      limit: rule.max_increase_per_day * days,
      previous: previous.value,
      ...note,
    };
  }
  if (
    change < 0 &&
    rule.max_decrease_per_day !== undefined &&
    -change / days > rule.max_decrease_per_day
  ) {
    return {
      severity: 'warn',
      kind: 'fell',
      limit: rule.max_decrease_per_day * days,
      previous: previous.value,
      ...note,
    };
  }
  return null;
}
