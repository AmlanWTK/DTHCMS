import type { components } from '@dthcms/api-client';

/**
 * Answering a correction request, as data (CP62, §4.3, [R-04]).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * A React Native component cannot be rendered in this container, so anything it decided would
 * be a decision nobody checks. What this feature decides is not layout. It decides **what one
 * press writes into a patient's clinical record** — a new height of 140 where 150 stood — and
 * it decides what an operator is told when somebody has said their work is wrong. Both of
 * those live in pure functions with tests beside them; the two `.tsx` files are arrangement
 * and hold no rule at all.
 *
 * # This is the 140/150 case from the other end
 *
 * §4.3's scenario is an anthropometry officer who typed 150 and a physician who is sure it is
 * 140. The server routes the request to **whoever typed the value**, not to a supervisor, so
 * this queue is the operator's own and the screen it draws is the one they meet the flag on.
 * Everything below follows from that one fact:
 *
 *   - **There is no permission check anywhere in this file.** Answering a request routed to
 *     you needs none — asking somebody to hold a permission to fix their own mistake is how
 *     mistakes stay — and a client-side gate would be a locked control in front of the one
 *     person entitled to press it. `corrections.test.ts` asserts the absence.
 *   - **Correcting is one act.** `toApply` takes a draft with a number, a unit and an optional
 *     note. There is no reason picker on the correcting side, no confirmation step and no
 *     second screen: the operator is standing at a station with a patient in front of them.
 *   - **Rejecting is one act and a reason.** `toReject` refuses an empty one before it can
 *     become a request, and the screen says *why* it is refusing — that sentence is the whole
 *     mechanism by which a physician keeps flagging.
 *   - **Nothing here produces a word that reads as blame.** There is no severity, no tone that
 *     escalates, no count of how many times this operator has been flagged. The plan's own
 *     risk note is that a metric which feels punitive makes staff hide errors instead of
 *     correcting them, and `transcription` on the reason — the flag CP63 counts — is
 *     deliberately **not** carried into anything this screen draws. It is a fact about a
 *     taxonomy, and rendering it beside a colleague's note would turn a question about a
 *     number into a mark against a person.
 *
 * # The queue's order is the server's and this file never changes it
 *
 * `/v1/corrections/mine` answers oldest first, and `queueOf` preserves that exactly. A queue
 * answered newest-first is a queue where the oldest request is never answered: the flag that
 * has been waiting since Tuesday is the one that matters, and any sort this file applied would
 * be a second, quieter opinion about which colleague gets ignored.
 *
 * # A closed request is never silently dropped
 *
 * An operator who answered on another device, or whose request a supervisor took, must be told
 * rather than shown an empty screen — otherwise the honest reading of the blank is "the thing
 * I was about to fix has vanished". `alreadyAnswered` recognises the server's own conflict
 * code, `outcomeOf` reads how it was answered, and the screen keeps the row.
 */

export type CorrectionRequest = components['schemas']['CorrectionRequest'];
export type CorrectionReason = components['schemas']['CorrectionReason'];
export type Observation = components['schemas']['Observation'];

export type Locale = 'en' | 'bn';

/** The two things that can be done to a request, plus the read that finds them. */
export const ACTS = ['read', 'apply', 'reject'] as const;
export type Act = (typeof ACTS)[number];

/** The contract's own limits. Refused here so the operator hears it while they can still act. */
export const NOTE_MAX = 500;
export const REASON_MAX = 500;

// --- the clinic's clock ---

/**
 * Dhaka, as a fixed offset.
 *
 * The third copy of this number in the application, and the duplication is deliberate rather
 * than tidy: `features/attribution` and `features/counseling` each hold their own, because a
 * feature reaching into another feature's internals for a constant is the coupling the lint
 * rule exists to stop, and the alternative — a shared clock module — is a change to two
 * finished checkpoints. `corrections.test.ts` checks this against `Asia/Dhaka` exactly as the
 * other two do, so the three statements of the clinic's clock cannot drift apart quietly.
 *
 * Bangladesh has kept a single offset with no daylight saving since 2009. If that ever
 * changes, every time on this screen is wrong by an hour for part of the year, and the fix is
 * a real zone conversion in all three places.
 */
export const CLINIC_UTC_OFFSET_MINUTES = 360;

/** `YYYY-MM-DD` in clinic time, or '' when the timestamp cannot be read. */
export function clinicDay(iso: string): string {
  const parsed = Date.parse(text(iso));
  if (Number.isNaN(parsed)) return '';
  return new Date(parsed + CLINIC_UTC_OFFSET_MINUTES * 60_000).toISOString().slice(0, 10);
}

/**
 * `HH:MM` in clinic time, or '' when the timestamp cannot be read.
 *
 * An unparseable timestamp is emptied rather than rendered as `Invalid Date`: a row saying a
 * colleague questioned a value at Invalid Date is worse than one that leaves the time out,
 * because the first is read as data.
 */
export function clockTime(iso: string): string {
  const parsed = Date.parse(text(iso));
  if (Number.isNaN(parsed)) return '';
  const local = new Date(parsed + CLINIC_UTC_OFFSET_MINUTES * 60_000);
  const hours = String(local.getUTCHours()).padStart(2, '0');
  const minutes = String(local.getUTCMinutes()).padStart(2, '0');
  return `${hours}:${minutes}`;
}

export interface Moment {
  date: string;
  time: string;
  known: boolean;
}

/**
 * One timestamp, as a date and a clock time.
 *
 * Both, always — never "today's time" alone. A flag raised last Tuesday showing `11:02` and
 * nothing else is a flag an operator reads as this morning's, and this queue is precisely the
 * place where the old request is the one that has been waiting.
 */
export function momentOf(iso: string): Moment {
  const date = clinicDay(iso);
  if (date === '') return { date: '', time: '', known: false };
  return { date, time: clockTime(iso), known: true };
}

// --- the queue ---

export interface Queue {
  /** Still to answer, in the server's order: oldest first. */
  open: CorrectionRequest[];
  /** Answered — kept, never dropped, so nothing disappears out from under a reader. */
  answered: CorrectionRequest[];
  /**
   * The one request to open on arrival, or ''.
   *
   * Exactly one open request is the ordinary case, and making the operator tap a list of one
   * to reach the only thing on it is a tap that teaches nothing. Two or more stay closed:
   * opening several at once would put two clinical values on a screen with two keypads under
   * them, which is how the wrong one gets typed into.
   */
  focus: string;
}

export function queueOf(requests: readonly CorrectionRequest[] | undefined | null): Queue {
  const open: CorrectionRequest[] = [];
  const answered: CorrectionRequest[] = [];
  for (const request of requests ?? []) {
    if (request.status === 'OPEN') open.push(request);
    else answered.push(request);
  }
  return { open, answered, focus: open.length === 1 ? text(open[0]?.id) : '' };
}

export const OUTCOMES = ['open', 'applied', 'rejected', 'overridden'] as const;
export type Outcome = (typeof OUTCOMES)[number];

/**
 * How a request was answered.
 *
 * `OVERRIDDEN` keeps its own word rather than collapsing into "corrected", because it means
 * somebody else corrected the value — and an operator reading "you put this right" about a
 * supervisor's fix would be told something untrue about their own record.
 */
export function outcomeOf(request: CorrectionRequest): Outcome {
  switch (request.status) {
    case 'APPLIED':
      return 'applied';
    case 'REJECTED':
      return 'rejected';
    case 'OVERRIDDEN':
      return 'overridden';
    default:
      return 'open';
  }
}

export interface Recomputed {
  code: string;
  /** False for the contract's `:not-recomputed` marker: this one did not follow. */
  done: boolean;
}

/**
 * What else moved because this moved (criterion 3), as rows.
 *
 * The server appends `:not-recomputed` to a code whose other input has since been removed or
 * whose formula refused the new value. Shown as its own row rather than filtered out: an
 * operator told "height corrected" while a stale BMI sits beside it on somebody's screen is
 * exactly the disagreement the transaction exists to prevent, and the one case it cannot
 * prevent is the one worth naming.
 */
export function recomputedOf(request: CorrectionRequest): Recomputed[] {
  const out: Recomputed[] = [];
  for (const entry of request.recomputed ?? []) {
    const raw = text(entry);
    if (raw === '') continue;
    if (raw.endsWith(':not-recomputed')) {
      out.push({ code: raw.slice(0, -':not-recomputed'.length), done: false });
      continue;
    }
    out.push({ code: raw, done: true });
  }
  return out;
}

// --- what was flagged ---

export interface Measurement {
  /** A key under the namespace named in `keyNamespace`, or null when this build has no word. */
  key: string | null;
  /** The units a station offers for this code, canonical first. */
  units: readonly string[];
}

/**
 * The codes this build already has words and units for.
 *
 * A copy of what `features/anthropometry` and `features/vitals` declare, for the same reason
 * the clock is copied: importing either barrel would pull a React Native screen into a module
 * that has to load under Node, and importing their internals is the boundary the lint rule
 * forbids. `corrections.test.ts` asserts this table covers every field those two stations
 * declare, with the same units and in the same order, so the copy cannot rot unnoticed.
 *
 * A code that is not here — a lab result, an examination finding, something a later checkpoint
 * adds — is drawn as its own code, and its unit selector collapses to the single unit the
 * value was entered in. `BODY_FAT_PCT` on a screen is ugly and readable; a blank where the
 * measurement's name belongs is a request an operator cannot answer.
 */
export const MEASUREMENTS: Readonly<Record<string, Measurement>> = Object.freeze({
  BODY_HEIGHT: { key: 'anthropometry.field.height', units: ['cm', 'in', '[ft_i]'] },
  BODY_WEIGHT: { key: 'anthropometry.field.weight', units: ['kg', '[lb_av]'] },
  WAIST_CIRC: { key: 'anthropometry.field.waist', units: ['cm', 'in'] },
  HIP_CIRC: { key: 'anthropometry.field.hip', units: ['cm', 'in'] },
  BODY_FAT_PCT: { key: 'anthropometry.field.bodyFat', units: ['%'] },
  MUSCLE_MASS: { key: 'anthropometry.field.muscle', units: ['kg', '[lb_av]'] },
  BP_SYSTOLIC: { key: 'vitals.field.systolic', units: ['mm[Hg]', 'kPa'] },
  BP_DIASTOLIC: { key: 'vitals.field.diastolic', units: ['mm[Hg]', 'kPa'] },
  HEART_RATE: { key: 'vitals.field.pulse', units: ['/min'] },
  SPO2: { key: 'vitals.field.spo2', units: ['%'] },
  BODY_TEMP: { key: 'vitals.field.temperature', units: ['Cel', '[degF]'] },
  RESP_RATE: { key: 'vitals.field.respiratory', units: ['/min'] },
});

export interface CodeReading {
  /** The code exactly as the request carried it. Never empty on a real request. */
  code: string;
  /** The message key for its name, or null when this build has none and the code stands in. */
  key: string | null;
}

export function measurementFor(code: string): CodeReading {
  const raw = text(code);
  return { code: raw, key: MEASUREMENTS[raw]?.key ?? null };
}

/**
 * The units to offer while retyping.
 *
 * The station's own list where this build knows the code, so a height typed in feet is
 * corrected in feet. Otherwise exactly one — the unit the value carries — which is what makes
 * the field draw the unit as a label instead of a selector: a control with one option teaches
 * people to tap without reading, and a *guessed* second option on an unknown code would be an
 * invitation to record a lab result in centimetres.
 */
export function unitsFor(code: string, entered: string): readonly string[] {
  const known = MEASUREMENTS[text(code)]?.units;
  if (known !== undefined && known.length > 0) return known;
  const unit = text(entered);
  return unit === '' ? [] : [unit];
}

// --- the value itself ---

/**
 * The observation fields this feature reads.
 *
 * Structural rather than the generated `Observation`, so a caller holding a narrower row can
 * pass what it has, and so this module never depends on fields it does not use.
 */
export interface FlaggedObservation {
  code?: string;
  category?: string;
  value_type?: string;
  value?: number;
  unit?: string;
  entered_value?: number;
  entered_unit?: string;
  value_text?: string;
  value_bool?: boolean;
  value_code?: string;
  formula?: string;
}

export const ANSWER_KINDS = ['number', 'words', 'yesno', 'derived', 'notHere', 'unknown'] as const;
export type AnswerKind = (typeof ANSWER_KINDS)[number];

/**
 * What answering this request actually consists of.
 *
 * Read off the observation rather than off the request, because the request carries a code and
 * not a value, and a screen that guessed a keypad from a code would show one for a foot
 * examination.
 *
 * Two of the six are refusals with reasons, and both matter:
 *
 *   - `derived` — a BMI, an eGFR. The number was computed; retyping it would put a hand-typed
 *     value where a formula's answer belongs and leave the inputs it disagrees with untouched.
 *     Correcting the height is what changes the BMI, and the server does exactly that in the
 *     same transaction. The server does not currently refuse a flag on a derived value, so
 *     this screen is where an operator finds out what to do instead.
 *   - `notHere` — a coded or structured value. Choosing a concept is the terminology picker's
 *     act, at the station that owns the finding, and a free-text box here would let somebody
 *     write a diagnosis into a coded field.
 *
 * `unknown` is the value not having arrived yet, which is not the same as there being nothing
 * to answer, and the screen says so rather than drawing an empty form.
 */
export function answerKindOf(row: FlaggedObservation | null | undefined): AnswerKind {
  if (row === null || row === undefined) return 'unknown';
  if (text(row.category).toUpperCase() === 'DERIVED' || text(row.formula) !== '') return 'derived';
  switch (shapeOf(row)) {
    case 'number':
      return 'number';
    case 'words':
      return 'words';
    case 'yesno':
      return 'yesno';
    case 'coded':
      return 'notHere';
    default:
      return 'unknown';
  }
}

export const VALUE_SHAPES = ['number', 'words', 'yesno', 'coded', 'unknown'] as const;
export type ValueShape = (typeof VALUE_SHAPES)[number];

/**
 * What the record holds, as opposed to what can be done about it.
 *
 * The two are not the same and collapsing them loses a number. A BMI is `derived` — there is
 * nothing to retype — and it is still a *number*, and a screen that showed "not recorded" where
 * a flagged BMI of 22.4 should be would leave an operator with no idea what anybody is talking
 * about. So the shape says what to draw and `answerKindOf` says what to offer.
 */
export function shapeOf(row: FlaggedObservation | null | undefined): ValueShape {
  switch (text(row?.value_type).toLowerCase()) {
    case 'numeric':
      return 'number';
    case 'text':
      return 'words';
    case 'boolean':
      return 'yesno';
    case 'coded':
    case 'structured':
      return 'coded';
    default:
      return 'unknown';
  }
}

export interface FlaggedValue {
  /** What can be done about it. */
  kind: AnswerKind;
  /** What the record holds. A derived value is not answerable and is still a number. */
  shape: ValueShape;
  /** As typed, in the unit it was typed in — never the canonical round trip. */
  value: number | null;
  unit: string;
  words: string;
  bool: boolean | null;
  /** For a coded value: the concept code, so the screen can show what stands rather than a gap. */
  concept: string;
}

/**
 * The value that was questioned, in the shape the operator entered it.
 *
 * `entered_value` and `entered_unit` come first and `value`/`unit` are the fallback. That
 * order is the whole of criterion 3's "in the unit they typed it in": an operator who recorded
 * 154 lb must be shown 154 lb, not 69.9 kg, or the correction they type is a correction to a
 * number they never entered. The fallback exists because a value written on the web carries no
 * entered pair, and showing the canonical figure is better than showing none.
 *
 * Only the fields that belong to the record's own shape are filled. A row that arrived carrying
 * both a number and some text — a mis-projected value, an older writer, a shape a later
 * checkpoint changes — must not be able to present a number for a value the record says is
 * words: this screen would then offer a keypad seeded with a figure nobody entered, and one
 * press would write it into a patient's record.
 */
export function flaggedValueOf(row: FlaggedObservation | null | undefined): FlaggedValue {
  const shape = shapeOf(row);
  const enteredValue = row?.entered_value;
  const canonical = row?.value;
  const numeric =
    typeof enteredValue === 'number' && Number.isFinite(enteredValue)
      ? enteredValue
      : typeof canonical === 'number' && Number.isFinite(canonical)
        ? canonical
        : null;
  const unit = text(row?.entered_unit) !== '' ? text(row?.entered_unit) : text(row?.unit);
  return {
    kind: answerKindOf(row),
    shape,
    value: shape === 'number' ? numeric : null,
    unit: shape === 'number' ? unit : '',
    words: shape === 'words' ? text(row?.value_text) : '',
    bool: shape === 'yesno' && typeof row?.value_bool === 'boolean' ? row.value_bool : null,
    concept: shape === 'coded' ? text(row?.value_code) : '',
  };
}

// --- the correction, as a draft ---

export interface Draft {
  /** The number as typed, or the words. Text, so a half-typed "14." survives a keystroke. */
  text: string;
  unit: string;
  /** For a yes/no finding. Null until the operator chooses, which is not the same as "no". */
  bool: boolean | null;
  /** Optional, and never seeded from the original: this is the corrector's own sentence. */
  note: string;
}

/**
 * The form, opened on the value that is being questioned.
 *
 * Seeded rather than blank, and that is the decision worth arguing with. A blank field is one
 * fewer number on screen to copy by mistake; a seeded one is the number the physician and the
 * operator are actually discussing, one digit away from right. §4.3's case is a transposition
 * — 150 for 140 — so the correction is nearly always an edit of what is there, and an operator
 * who has to retype a whole reading from memory in front of a patient will sometimes retype it
 * wrong.
 *
 * The note starts empty on purpose. The original's note belongs to the original; carrying it
 * forward would put words in the corrector's mouth and re-file an old remark as new.
 */
export function draftFor(row: FlaggedObservation | null | undefined): Draft {
  const flagged = flaggedValueOf(row);
  return {
    text:
      flagged.kind === 'words'
        ? flagged.words
        : flagged.kind === 'number' && flagged.value !== null
          ? String(flagged.value)
          : '',
    unit: flagged.unit,
    bool: flagged.kind === 'yesno' ? flagged.bool : null,
    note: '',
  };
}

export const PROBLEMS = [
  'empty',
  'notANumber',
  'negative',
  'unchanged',
  'noteTooLong',
  'needsReason',
  'reasonTooLong',
  'cannotAnswerHere',
] as const;
export type Problem = (typeof PROBLEMS)[number];

/**
 * Why this draft is not yet a correction, or null.
 *
 * Every one of these is a sentence the operator reads while they can still fix it, and none of
 * them is the enforcement — the server has its own validation and the record has its own
 * constraints. What this buys is that an operator with a patient in front of them finds out
 * about an empty field before a round trip rather than after one.
 *
 * **`unchanged` is the interesting one.** Retyping the value that is already there would write
 * a new observation identical to the old one and mark the original CORRECTED — a correction
 * that corrects nothing, on somebody's record, for ever. If the number is right, the act is to
 * say it stands and say why, and that is what the screen points at.
 *
 * Zero is allowed, unlike on the anthropometry form, and deliberately: this screen answers a
 * request about *any* code, and a lab result of zero is a result. What is refused is a
 * negative measurement and anything that does not parse.
 */
export function applyProblem(draft: Draft, flagged: FlaggedValue): Problem | null {
  if (text(draft.note).length > NOTE_MAX) return 'noteTooLong';

  switch (flagged.kind) {
    case 'number': {
      const typed = text(draft.text);
      if (typed === '') return 'empty';
      const value = Number(typed);
      if (!Number.isFinite(value)) return 'notANumber';
      if (value < 0) return 'negative';
      if (flagged.value !== null && value === flagged.value && text(draft.unit) === flagged.unit) {
        return 'unchanged';
      }
      return null;
    }
    case 'words': {
      const typed = text(draft.text);
      if (typed === '') return 'empty';
      if (typed === flagged.words) return 'unchanged';
      return null;
    }
    case 'yesno': {
      if (draft.bool === null) return 'empty';
      if (draft.bool === flagged.bool) return 'unchanged';
      return null;
    }
    default:
      // Derived, coded, structured, or a value that has not arrived. There is nothing here
      // this screen may write, and the sentence beside it says where the act belongs instead.
      return 'cannotAnswerHere';
  }
}

export interface ApplyRequest {
  event_id: string;
  value?: number;
  unit?: string;
  value_text?: string;
  value_bool?: boolean;
  note?: string;
}

/**
 * The body, or null when the draft is not yet an answer.
 *
 * One field of value per kind and never two: a body carrying both `value` and `value_text`
 * would leave the server to decide which the operator meant, and the one it picked would be
 * written into a patient's record either way.
 *
 * The unit travels with the number, exactly as it does from the capture stations, so the
 * server converts once and the record keeps both figures. Nothing is converted here.
 */
export function toApply(draft: Draft, flagged: FlaggedValue, eventId: string): ApplyRequest | null {
  if (applyProblem(draft, flagged) !== null) return null;
  const note = text(draft.note);
  const base: ApplyRequest = { event_id: text(eventId) };
  if (note !== '') base.note = note;

  switch (flagged.kind) {
    case 'number':
      return { ...base, value: Number(text(draft.text)), unit: text(draft.unit) };
    case 'words':
      return { ...base, value_text: text(draft.text) };
    case 'yesno':
      return { ...base, value_bool: draft.bool === true };
    default:
      return null;
  }
}

export interface RejectRequest {
  event_id: string;
  reason: string;
}

/** Why this refusal cannot be sent yet, or null. */
export function rejectProblem(reason: string): Problem | null {
  const given = text(reason);
  if (given === '') return 'needsReason';
  if (given.length > REASON_MAX) return 'reasonTooLong';
  return null;
}

/**
 * "The value stands", with the reason that makes it worth anything.
 *
 * The reason is required by the contract and by the record, and it is refused here as well —
 * not as the enforcement but as the moment a person can still do something about it. The
 * sentence the screen draws beside this field is the actual mechanism: a physician told only
 * "no" cannot tell a disagreement from an oversight, learns nothing either way, and stops
 * flagging. A clinic where nobody flags anything has an audit trail and no quality signal.
 */
export function toReject(reason: string, eventId: string): RejectRequest | null {
  if (rejectProblem(reason) !== null) return null;
  return { event_id: text(eventId), reason: text(reason) };
}

// --- who asked, and why ---

/**
 * Who says this looks wrong, as one string in the reader's language.
 *
 * The name first, because the question a colleague asks six months later is *which* physician
 * — and because the point of §4.3's routing is that this is one person asking another, not a
 * system raising a ticket. The staff code before the role, since a code identifies a person
 * and a role identifies a hat. Empty where the request carries none of the three, and the
 * screen supplies the honest word for it: a placeholder invented here would be this system's
 * one attribution naming somebody who did not do the thing.
 */
export function flaggedBy(request: CorrectionRequest, locale: Locale): string {
  const named = wordingOf(
    request.requested_by_name_en ?? '',
    request.requested_by_name_bn ?? '',
    locale,
  );
  if (named !== '') return named;
  const code = text(request.requested_by_code);
  if (code !== '') return code;
  return text(request.requested_role);
}

/**
 * The reason, in the reader's language, from the vocabulary the server publishes.
 *
 * A reason is a code **and** free text — the code so it can be counted, the note so it can say
 * "the tape was against the wall, not the patient". This resolves the code half. The
 * vocabulary is rows rather than an enum precisely because it is expected to change, so a code
 * this phone's copy has never seen is shown as itself: a blank where the reason belongs would
 * turn "somebody says this is a transcription error" into "somebody says nothing".
 */
export function reasonDisplay(
  code: string,
  reasons: readonly CorrectionReason[] | undefined | null,
  locale: Locale,
): string {
  const raw = text(code);
  for (const reason of reasons ?? []) {
    if (text(reason.code) !== raw) continue;
    const words = wordingOf(reason.display_en, reason.display_bn, locale);
    return words === '' ? raw : words;
  }
  return raw;
}

/**
 * The role codes this build has a word for.
 *
 * A copy of the keys under `role.codes` in the message files, and `corrections.test.ts`
 * asserts it is exactly that set in both languages. It exists so this module can decide
 * *here* whether a role has a sentence, instead of the screen asking `use-intl` for a key that
 * may not exist and drawing the literal string `role.codes.SOMETHING` at an operator who is
 * trying to find out who questioned their measurement.
 *
 * A role this build has never heard of is shown as its own code. `RX_EDUCATOR` is ugly and it
 * is readable; a blank where the role belongs turns "a physician is asking" into "somebody is
 * asking", which is a different and less answerable sentence.
 */
export const KNOWN_ROLE_CODES: readonly string[] = [
  'REGISTRATION',
  'ANTHROPOMETRY',
  'COUNSELOR',
  'HISTORY',
  'CLINICAL_ASSISTANT',
  'JUNIOR_DOCTOR',
  'RECORDS',
  'NUTRITIONIST',
  'EXERCISE',
  'PHYSICIAN',
  'QA',
  'RX_EDUCATOR',
  'PHARMACIST',
  'CRM',
  'RESEARCHER',
  'HR',
  'ADMIN',
  'FIELD_WORKER',
];

/** `role.codes.X` for a role this build can name, or null when the code has to stand in. */
export function roleKeyOf(raw: string): string | null {
  const code = text(raw);
  return code !== '' && KNOWN_ROLE_CODES.includes(code) ? `role.codes.${code}` : null;
}

export interface RequestReading {
  id: string;
  open: boolean;
  outcome: Outcome;
  /** What was flagged. */
  measurement: CodeReading;
  /** Who flagged it, or '' when the request names nobody. */
  who: string;
  /** The role behind the name, as the request carried it. Context, never the whole answer. */
  role: string;
  /** That role's message key, or null when the bare code has to stand in for it. */
  roleKey: string | null;
  /** Why, from the vocabulary — the code itself when this build has never seen it. */
  reason: string;
  /** What they actually saw. Empty when they wrote nothing. */
  note: string;
  when: Moment;
  /** Set only once the request has been answered. */
  answeredAt: Moment | null;
  /** The note left with the answer — the corrector's sentence, or the reason it stands. */
  answerNote: string;
  recomputed: Recomputed[];
}

/**
 * One request, as everything the card draws.
 *
 * One function rather than six accessors called from JSX, so that "what a request says" is a
 * single value a test can pin — and so that a screen cannot quietly start rendering the
 * requester's uuid, or the `transcription` flag, by reaching past it.
 */
export function requestReadingOf(
  request: CorrectionRequest,
  reasons: readonly CorrectionReason[] | undefined | null,
  locale: Locale,
): RequestReading {
  const outcome = outcomeOf(request);
  const answered = text(request.resolved_at);
  return {
    id: text(request.id),
    open: outcome === 'open',
    outcome,
    measurement: measurementFor(request.code),
    who: flaggedBy(request, locale),
    role: text(request.requested_role),
    roleKey: roleKeyOf(request.requested_role ?? ''),
    reason: reasonDisplay(request.reason_code, reasons, locale),
    note: text(request.note),
    when: momentOf(request.requested_at),
    answeredAt: outcome === 'open' || answered === '' ? null : momentOf(answered),
    answerNote: text(request.resolution_note),
    recomputed: recomputedOf(request),
  };
}

// --- what went wrong ---

export interface Trouble {
  kind: 'refused' | 'unreachable' | 'failed';
  /** Which act this belongs to. Context, not a branch — see `ACTS`. */
  act: Act;
  /** The HTTP status, or 0 when there was no answer at all. */
  status: number;
  /**
   * The server's own code for the refusal, or empty.
   *
   * The only part of an error a client may branch on. A message is written for a person: it
   * gets translated, shortened and improved, and a screen that matched on its words would
   * change behaviour the day somebody rewrote a sentence.
   */
  code: string;
  /** The field the server named on a refusal, or empty. */
  field: string;
  /** The server's own words, in the reader's language where there are any. */
  message: string;
}

/**
 * The one conflict code this screen changes its behaviour for.
 *
 * It means the request was answered while this phone was holding it — from another device, or
 * by a supervisor. Nothing was written twice and nothing the operator did was lost, and the
 * screen has to say both of those things: an operator who pressed a button and then watched a
 * row vanish will assume they broke something.
 */
export const CODE_ALREADY_ANSWERED = 'CORRECTION_ALREADY_ANSWERED';

export function alreadyAnswered(trouble: Trouble): boolean {
  return trouble.code === CODE_ALREADY_ANSWERED;
}

/**
 * A request that is not this person's to answer.
 *
 * It cannot arrive from `/mine`, which returns only the caller's own, and it is recognised
 * anyway: the queue on this phone can be a minute old, and a refusal an operator cannot read
 * is a refusal they take to whoever is standing nearest.
 */
export function notYours(trouble: Trouble): boolean {
  return trouble.act !== 'read' && trouble.status === 403;
}

/**
 * A read refused because of the hat, rather than because of the request.
 *
 * `/v1/corrections/mine` is gated on `observation.read.values`, which is a *role's* permission
 * while the queue itself belongs to a *person*. So an operator wearing a hat that does not
 * hold it — registration, say — gets a 403 for requests that are genuinely theirs, and the
 * screen has to name the hat rather than say the queue is empty. Switching roles is the fix
 * and it is one tap away in the switcher above.
 */
export function notThisRole(trouble: Trouble): boolean {
  return trouble.act === 'read' && trouble.status === 403;
}

/**
 * What the operator can actually do about it.
 *
 * Three answers and no fourth, because a screen that offered "try again" for every failure
 * would train people to press it at the one failure where pressing it cannot help.
 */
export const ADVICE = ['reload', 'retry', 'none'] as const;
export type Advice = (typeof ADVICE)[number];

export function adviceFor(trouble: Trouble): Advice {
  if (trouble.kind === 'unreachable') return 'retry';
  // Answered already, or gone. Both mean this phone's copy is behind what the clinic's record
  // says, and pressing again would send the same refused request for the same stale reason.
  if (trouble.status === 409 || trouble.status === 404) return 'reload';
  // The server fell over. Nothing about the request is wrong, so the act is to send it again.
  if (trouble.status >= 500) return 'retry';
  return 'none';
}

// --- one attempt, kept across a bad connection ---

/**
 * The event ids of attempts nobody knows the fate of, by act and request.
 *
 * Held by the screen, decided here. An empty record is the ordinary state: an id is kept only
 * between a write whose answer never arrived and the press that retries it.
 *
 * The same mechanism `features/counseling` uses, written again rather than imported, because
 * its key builder is typed to that station's four acts and a shared one would be a third
 * feature's worth of coupling for ten lines. What it buys here: the id is also the
 * `Idempotency-Key`, so a retry after a lost reply is answered with the stored response
 * instead of a `CORRECTION_ALREADY_ANSWERED` conflict against this phone's own write —
 * which, on a screen whose whole subject is a colleague's mistake, is the worst possible
 * sentence to show the wrong person.
 */
export type HeldEvents = Readonly<Record<string, string>>;

export function attemptKey(act: Act, requestId: string): string {
  return `${act}:${text(requestId)}`;
}

export function eventFor(held: HeldEvents, key: string, minted: string): string {
  const kept = text(held[key]);
  return kept === '' ? text(minted) : kept;
}

export function holdEvent(held: HeldEvents, key: string, event: string): HeldEvents {
  return { ...held, [key]: event };
}

export function releaseEvent(held: HeldEvents, key: string): HeldEvents {
  const next = { ...held };
  delete next[key];
  return next;
}

/**
 * Whether a failed attempt keeps the id it was made with.
 *
 * Only when nobody knows whether it landed. A **refusal** forgets its id: nothing was written,
 * the operator is being told why, and whatever they do next is a new act — correcting a value
 * after being told the draft was unchanged is a genuinely different request, and reusing the
 * old id would have the server hand back the answer to a request nobody is making any more.
 */
export function keepsItsEvent(trouble: Trouble): boolean {
  return trouble.kind !== 'refused';
}

// --- being told, on the device that typed the value (criterion 4) ---

/** The message the gateway publishes when somebody flags a value. */
export const CORRECTION_REQUESTED = 'correction.requested';

/** The operator's own channel, or '' when the session has not answered yet. */
export function myTopic(me: string): string {
  const id = text(me);
  return id === '' ? '' : `user:${id}`;
}

/**
 * Whether this message means the reader's own correction queue has changed.
 *
 * Both halves are checked. The kind alone would react to a flag raised against somebody else
 * if a future gateway ever fanned one out more widely; the topic alone would react to every
 * message addressed to this person, which today includes their critical-value alerts.
 *
 * **The message is a notification and never a value.** The summary carries the request id, the
 * observation id, the code and the reason code — deliberately not the number — so there is
 * nothing here to write into a cache even if this application were willing to, which it is
 * not: a value shown from a socket is a value no endpoint returned. What this function
 * produces is the answer to "should the queue be read again", and nothing else.
 */
export function notifiesMe(message: { kind?: string; topic?: string }, me: string): boolean {
  const topic = myTopic(me);
  if (topic === '') return false;
  return text(message.kind) === CORRECTION_REQUESTED && text(message.topic) === topic;
}

// --- small shared helpers ---

function text(value: string | null | undefined): string {
  return (value ?? '').trim();
}

/** A pair of translations, in the reader's language, falling back to the other. */
function wordingOf(en: string, bn: string, locale: Locale): string {
  const own = text(locale === 'bn' ? bn : en);
  if (own !== '') return own;
  return text(locale === 'bn' ? en : bn);
}
