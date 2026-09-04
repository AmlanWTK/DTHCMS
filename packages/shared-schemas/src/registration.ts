/**
 * Registration's rules, shared by the web desk and the station app (CP32, CP33).
 *
 * It lives here, in a package both surfaces depend on, because CP33 asks for something
 * stronger than "the two forms behave the same": it asks for that to be *provable*. Two
 * copies of a validation rule are two rules, and the day they disagree is the day a patient
 * registered on a phone is refused at the desk — or worse, accepted with a date the web
 * would have caught.
 *
 * The date of birth is the reason this file exists. [R-06] makes exact age clinically
 * load-bearing — pediatric percentiles are computed from it — and the failure mode is not a
 * refused form but an accepted one: an operator types 1085 for 1985, the record is created,
 * and every percentile that patient ever gets is wrong in a way nothing downstream can
 * detect. So the form is designed against that error rather than merely validating for it:
 * three separate fields, and an age echoed back in words as the year is typed. "941 years"
 * is obvious in a way that "1085" is not.
 */

export interface DateParts {
  day: string;
  month: string;
  year: string;
}

export type Precision = 'day' | 'month' | 'year';

/**
 * What the three fields amount to.
 *
 * A patient who knows only their birth year is ordinary here, so a year on its own is a
 * complete answer — recorded as 1 January with `precision: 'year'`, which is honest, rather
 * than an invented day a percentile calculation would treat as exact.
 */
export function readDate(parts: DateParts): { iso: string; precision: Precision } | null {
  const year = Number(parts.year);
  if (!/^\d{4}$/.test(parts.year) || !Number.isFinite(year)) return null;

  const month = parts.month ? Number(parts.month) : null;
  const day = parts.day ? Number(parts.day) : null;

  if (month !== null && (!Number.isInteger(month) || month < 1 || month > 12)) return null;
  if (day !== null && (!Number.isInteger(day) || day < 1 || day > 31)) return null;
  // A day without a month says nothing.
  if (day !== null && month === null) return null;

  const precision: Precision = day !== null ? 'day' : month !== null ? 'month' : 'year';
  const iso = `${pad(year, 4)}-${pad(month ?? 1, 2)}-${pad(day ?? 1, 2)}`;

  // 31 February is a typing error, not a date. Checked by round-tripping rather than by a
  // table of month lengths, which is one leap-year rule away from being wrong.
  if (precision === 'day') {
    const parsed = new Date(`${iso}T00:00:00Z`);
    if (
      Number.isNaN(parsed.getTime()) ||
      parsed.getUTCDate() !== day ||
      parsed.getUTCMonth() + 1 !== month
    ) {
      return null;
    }
  }
  return { iso, precision };
}

function pad(value: number, width: number): string {
  return String(value).padStart(width, '0');
}

export interface Age {
  years: number;
  months: number;
  /** True when the date is not exact, so a screen can say "about". */
  approximate: boolean;
}

/**
 * The age echo. Years and months, because months are what make a wrong year obvious on a
 * child and years alone are what make it invisible.
 */
export function ageOn(iso: string, precision: Precision, today: Date): Age | null {
  const born = new Date(`${iso}T00:00:00Z`);
  if (Number.isNaN(born.getTime())) return null;

  let years = today.getUTCFullYear() - born.getUTCFullYear();
  let months = today.getUTCMonth() - born.getUTCMonth();
  if (today.getUTCDate() < born.getUTCDate()) months -= 1;
  if (months < 0) {
    years -= 1;
    months += 12;
  }
  return { years, months, approximate: precision !== 'day' };
}

/** Whether a date of birth is one the server will accept, so the form can say so first. */
export function birthDateProblem(iso: string, today: Date): 'future' | 'implausible' | null {
  const born = new Date(`${iso}T00:00:00Z`);
  if (born.getTime() > today.getTime()) return 'future';
  if (today.getUTCFullYear() - born.getUTCFullYear() > 130) return 'implausible';
  return null;
}

/**
 * A Bangladeshi mobile, as the desk types it: 01712345678, +8801712345678, 8801712345678,
 * with or without spaces and hyphens.
 *
 * Mirrored from the server so the form can refuse before a round trip — the server still has
 * the last word, because a rule enforced only in a browser is not a rule.
 */
export function normalisePhone(raw: string): string | null {
  const digits = raw.trim().replace(/[^0-9]/g, '');
  let local = digits;
  if (local.startsWith('880')) local = `0${local.slice(3)}`;
  else if (local.length === 10 && local.startsWith('1')) local = `0${local}`;
  return /^01[3-9][0-9]{8}$/.test(local) ? `+880${local.slice(1)}` : null;
}

/**
 * The clinical minimum, confirmed with the clinical lead: an English name, a sex, a date of
 * birth with its precision and source, one mobile, and the consent record. Everything else
 * is prompted and skippable so a record can be finished while the patient walks to the next
 * station.
 */
export interface RequiredState {
  nameEN: boolean;
  sex: boolean;
  birthDate: boolean;
  phone: boolean;
  consent: boolean;
}

export function requiredState(form: {
  nameEN: string;
  sex: string;
  date: DateParts;
  dobSource: string;
  phone: string;
  consentReference: string;
}): RequiredState {
  return {
    nameEN: form.nameEN.trim().length >= 2,
    sex: form.sex !== '',
    birthDate: readDate(form.date) !== null && form.dobSource !== '',
    phone: normalisePhone(form.phone) !== null,
    consent: form.consentReference.trim() !== '',
  };
}

export function isComplete(state: RequiredState): boolean {
  return Object.values(state).every(Boolean);
}

/**
 * A document carries an exact date, so "birth certificate, year only" is almost always a
 * transcription error. Caught at the desk, where it costs a question, rather than in a
 * growth chart two years later.
 */
const DOCUMENT_SOURCES = ['birth_certificate', 'national_id', 'passport', 'immunisation_card'];

export function documentNeedsExactDate(source: string, precision: Precision | null): boolean {
  return precision !== null && precision !== 'day' && DOCUMENT_SOURCES.includes(source);
}
