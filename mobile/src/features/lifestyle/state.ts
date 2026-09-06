import {
  evaluatePlausibility,
  resolvePlausibilityRule,
  toCanonical,
  type PlausibilityRule,
  type PlausibilitySubject,
} from '@dthcms/clinical-calc';
import type { components } from '@dthcms/api-client';

/**
 * Station 3's lifestyle assessment, as data (CP58, blueprint §3 step 3, §12, ADR-0030).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * The same rule stations 2, 4 and 5 follow: a React Native component cannot be rendered
 * outside a device, so anything it decides is a decision nobody checks. What this station
 * decides is not layout. It decides what a client may send, what a client may never compute,
 * and what an operator is told when the server produces no score — and all three are in pure
 * functions with tests beside them.
 *
 * # Nothing in this file adds up an answer
 *
 * `core.instrument_total` is a function over the stored item rows and the composite is a CP42
 * derived observation; both belong to the server, and §12's cohorting reads them. A client
 * that could produce a total could produce **any** total, and a second implementation of the
 * arithmetic is how a phone shows one number while the research extract holds another.
 *
 * So: `toAssessment` sends the option **code** and never its points; there is no `reduce` in
 * this feature and no arithmetic anywhere on an option's `score`; and the only numbers this
 * file reports are ones the server sent. The catalogue's per-option points are carried in the
 * contract so a screen can show its working *after* the server has scored a response, which is
 * a different act from computing one.
 *
 * # An unusable instrument is a row, not a gap
 *
 * D-26 is unanswered: PSS-10, PHQ-9 and IPAQ are copyrighted or free under terms written for
 * academic research rather than a fee-charging clinic, and none of their wording is in the
 * database. The catalogue returns them anyway, with `usable: false` and a note saying what has
 * to be confirmed, and `catalogueRows` keeps every one of them. Filtering them out would make
 * a decision look like an oversight, and a clinician who went looking for PHQ-9 and found
 * nothing would report the app as broken rather than the licence as missing.
 *
 * `availabilityOf` is what the screen draws from, and `mayOpen` is false for all three, so
 * there is no arrangement of this module in which an unlicensed questionnaire can be opened
 * and answered.
 *
 * # `READINESS_1` is this clinic's own question and must never read as an instrument
 *
 * §3 step 3 asks for readiness-to-change and every validated alternative is behind D-26, so
 * the clinic wrote one question of its own. `isOwnWriting` reads the row's `provenance`, which
 * is a typed fact — it was briefly a comparison against the English words in
 * `copyright_holder`, which is to say the rule that matters most on this screen rested on a
 * seed nobody was stopping from being tidied.
 *
 * # A score is allowed to be absent, and the server says why
 *
 * Below `minimum` assessed domains the API answers `scoring.score: null`, because a "lifestyle
 * risk" computed from one answer is a number whose name promises far more than it holds. An
 * operator who finished a questionnaire and saw nothing would reasonably assume the save
 * failed, so the payload carries `assessed`, `missing` and `minimum` and the screen reads them
 * out — never a zero and never a dash, both of which are marks a reader compares with a real
 * score.
 *
 * The account is available from the **first render**: `GET /v1/patients/{id}/lifestyle-scoring`
 * computes it and stores nothing, so a screen can ask where a patient stands without appending a
 * derived observation every time somebody opens a tab. The writing endpoint is still called
 * after a save, and only there.
 *
 * **Nothing in this file knows which observation feeds which domain.** There was a copy of that
 * mapping here while the API said only `null`, and it was the one place this feature
 * re-implemented a server decision. `scorePanel` and `stillNeeded` read the server's own
 * account; a fifth domain added next year needs no change on this side.
 */

// --- what the contract gives us ---

export type Instrument = components['schemas']['Instrument'];
export type InstrumentItem = components['schemas']['InstrumentItem'];
export type InstrumentOption = components['schemas']['InstrumentOption'];
export type InstrumentResponse = components['schemas']['InstrumentResponse'];
export type InstrumentAnswerInput = components['schemas']['InstrumentAnswerInput'];
/**
 * A value as the record holds it.
 *
 * The composite is one of these — it is an ordinary CP42 derived observation, which is what
 * gives it the correction cascade, the timeline and the research extract for nothing — and so
 * are the four plain numbers and the pack-years derived from two of them.
 */
export type Observation = components['schemas']['Observation'];

export type Locale = 'en' | 'bn';

// --- the words on a row, in the reader's language ---

/**
 * One piece of an instrument's text, and which language it turned out to be in.
 *
 * The database refuses an item or an option whose wording is missing either language
 * (`instrument_item_bilingual`, `instrument_option_bilingual`), so a missing translation is an
 * edge case rather than a shape to design around. It is still worth one honest line: a
 * Bangla-reading operator handed English with no explanation is an operator who thinks the app
 * has switched languages on them, and who may then read a question aloud wrongly rather than
 * admit they could not read it.
 *
 * Deliberately its own copy rather than an import from the counselling feature. A feature that
 * reached into another's internals for a ten-line helper would couple two stations so that
 * neither could change its mind, and the boundary rule exists for exactly that.
 */
export interface Wording {
  text: string;
  /** The language the text actually is, or null when there is none in either. */
  language: Locale | null;
  ownLanguage: boolean;
}

export function wordingOf(english: string, bengali: string, locale: Locale): Wording {
  const own = (locale === 'bn' ? bengali : english).trim();
  if (own !== '') return { text: own, language: locale, ownLanguage: true };

  const other: Locale = locale === 'bn' ? 'en' : 'bn';
  const fallback = (locale === 'bn' ? english : bengali).trim();
  if (fallback !== '') return { text: fallback, language: other, ownLanguage: false };

  return { text: '', language: null, ownLanguage: false };
}

// --- the catalogue, including what may not be run ---

/**
 * Why an instrument can or cannot be opened, and there is no fourth answer.
 *
 * `unlicensed` is D-26 and is the whole reason the row exists. `unpublished` is the ordinary
 * gap: an instrument the clinic may run whose wording nobody has published a version of yet,
 * which the contract reports as `version_published: false`. They are separate because the
 * sentences a person needs are completely different — one is a licence somebody has to chase,
 * the other is a seed nobody has written — and a screen that collapsed them would send an
 * operator to the wrong person.
 */
export const AVAILABILITY = ['ready', 'unlicensed', 'unpublished'] as const;
export type Availability = (typeof AVAILABILITY)[number];

export function availabilityOf(instrument: Instrument): Availability {
  // The licence first, always. An unlicensed instrument with no published version is refused
  // for the licence, because that is the fact somebody has to act on — and because the day a
  // licence arrives its wording arrives with it.
  if (!instrument.usable) return 'unlicensed';
  if (!instrument.version_published) return 'unpublished';
  // A published version with no items is the same dead end from the other direction, and it
  // is worth the extra check: `names_only` responses carry no items either, and a screen that
  // opened one of those would show a questionnaire with no questions.
  if ((instrument.items ?? []).length === 0) return 'unpublished';
  return 'ready';
}

/** Whether the operator may open this one at all. */
export function mayOpen(instrument: Instrument): boolean {
  return availabilityOf(instrument) === 'ready';
}

/**
 * Whether this is published literature or something this clinic wrote.
 *
 * `provenance` is a typed fact on the row. It briefly was not: this read the English words in
 * `copyright_holder` and compared them against the literal string "This clinic", which held up
 * the one rule on this screen that must not bend — that the clinic's own readiness question can
 * never read as a validated instrument — on a seed nobody was stopping from being tidied.
 *
 * Read through a function rather than at each call site so there is exactly one place that
 * decides what the enum means, and so a third value added later is a compile error here.
 */
export function isOwnWriting(instrument: Instrument): boolean {
  return instrument.provenance === 'THIS_CLINIC';
}

/** One instrument as the screen draws it. Everything decided; nothing left for the component. */
export interface InstrumentRow {
  code: string;
  name: Wording;
  purpose: Wording;
  domain: Instrument['domain'];
  availability: Availability;
  /** True only for a row a thumb may open. Every other row is drawn and is not a control. */
  open: boolean;
  /**
   * The server's own sentence about the licence, in the reader's language.
   *
   * A bilingual pair like everything else on the row, and an invariant refuses an instrument
   * that says why it may or may not be used in only one language. It was English only for one
   * checkpoint, which meant a Bangla-reading counsellor met the D-26 explanation — the single
   * sentence on this screen that must not be misunderstood — in a language they may not read.
   */
  licenceNote: Wording;
  copyrightHolder: string;
  /** This clinic wrote it, and the screen must say so. */
  ownWriting: boolean;
  /** Whether this instrument's answers add up to a total worth showing. */
  totalMeansSomething: boolean;
  /** The published version, or null when there is none. */
  version: number | null;
  items: InstrumentItem[];
}

/**
 * The catalogue in the server's order, with **nothing removed**.
 *
 * `ordering` comes off the row and puts the three unusable ones where the clinic put them
 * rather than at the bottom where a screen would be tempted to hide them. Ties break on code so
 * two instruments with the same ordering do not swap places between renders.
 */
export function catalogueRows(
  instruments: readonly Instrument[] | undefined,
  locale: Locale,
): InstrumentRow[] {
  return [...(instruments ?? [])]
    .sort((a, b) => a.ordering - b.ordering || a.code.localeCompare(b.code))
    .map((instrument) => ({
      code: instrument.code,
      name: wordingOf(instrument.name_en, instrument.name_bn, locale),
      purpose: wordingOf(instrument.purpose_en ?? '', instrument.purpose_bn ?? '', locale),
      domain: instrument.domain,
      availability: availabilityOf(instrument),
      open: mayOpen(instrument),
      licenceNote: wordingOf(
        instrument.licence_note ?? '',
        instrument.licence_note_bn ?? '',
        locale,
      ),
      copyrightHolder: (instrument.copyright_holder ?? '').trim(),
      ownWriting: isOwnWriting(instrument),
      // Whether a total means anything for this instrument. `none` is a real scheme and
      // `READINESS_1`'s own seed note says its score means nothing on its own; a screen that
      // showed "Total 3" there would be inventing a finding out of an answer.
      //
      // Absent means there is no published version to report a scheme from, which is the same
      // set of rows `availabilityOf` already calls `unpublished` — so the default decides
      // nothing anybody can reach, and `sum` is the safe direction for the day one is
      // published without the field.
      totalMeansSomething: (instrument.scoring ?? 'sum') === 'sum',
      version: instrument.version ?? null,
      items: itemsInOrder(instrument),
    }));
}

/**
 * The catalogue a tablet is holding: the instruments and the version they came at.
 *
 * They travel together because the version is only useful beside the copy it describes. A
 * station tablet fetches this once at the start of a clinic session and works from it offline
 * for the rest of the morning (ADR-0004), which is right — and which means the copy in memory
 * can be behind a republished version, and the only signal of that used to be a 422 on an item
 * code at submit, after a patient had answered every question.
 *
 * Held in React state and nowhere else. There is no local database until CP64 and web storage
 * is refused outright (ADR-0010), so a clinic session is exactly as long as this lives.
 */
export interface Catalogue {
  instruments: Instrument[];
  /** When the catalogue last changed, as the server reports it. */
  version: string;
}

/**
 * Whether the catalogue this tablet is holding has been overtaken.
 *
 * A string comparison and deliberately not a date one: the value is the server's and its only
 * job is to differ. Parsing it into a `Date` here would invite a screen to decide which of two
 * versions is *newer*, which is a question a client holding a stale copy cannot answer.
 *
 * An empty latest is "the server did not say", and nothing is claimed from silence — a reload
 * prompt raised because a response was missing a field would train people to ignore it.
 */
export function movedOn(held: string, latest: string): boolean {
  const before = held.trim();
  const after = latest.trim();
  if (before === '' || after === '') return false;
  return before !== after;
}

/** One instrument out of the catalogue by code, or nothing. */
export function instrumentNamed(
  instruments: readonly Instrument[] | undefined,
  code: string,
): Instrument | null {
  const wanted = code.trim();
  if (wanted === '') return null;
  return (instruments ?? []).find((one) => one.code === wanted) ?? null;
}

/** The questions in the order the instrument asks them. */
export function itemsInOrder(instrument: Instrument): InstrumentItem[] {
  return [...(instrument.items ?? [])].sort(
    (a, b) => a.ordering - b.ordering || a.item_code.localeCompare(b.item_code),
  );
}

/** The answers in the order they are offered, which for a scale is the order that means something. */
export function optionsInOrder(item: InstrumentItem): InstrumentOption[] {
  return [...(item.options ?? [])].sort(
    (a, b) => a.ordering - b.ordering || a.option_code.localeCompare(b.option_code),
  );
}

// --- running one questionnaire ---

/**
 * One item, part-answered.
 *
 * A numeric item keeps its **text**, for the reason station 2's fields do: an operator typing
 * "7" on the way to "7.5" has typed a plausible number, and a field that parsed on every
 * keystroke could not hold "7." at all — the operator would watch their decimal point vanish.
 * The number is derived from the text, and text that does not parse contributes nothing.
 */
export interface ItemAnswer {
  option: string;
  text: string;
  yesNo: boolean | null;
}

export type RunState = Record<string, ItemAnswer>;

export function emptyRun(instrument: Instrument): RunState {
  const out: RunState = {};
  for (const item of itemsInOrder(instrument)) {
    out[item.item_code] = { option: '', text: '', yesNo: null };
  }
  return out;
}

/**
 * The three ways an answer is given, one per `answer_type`.
 *
 * Each returns a new state rather than mutating: the screen holds this in React state and a
 * mutation would not re-render. Each also clears the other two fields, because an item answered
 * two ways is an item the database's own trigger refuses — and a refusal that arrives after the
 * patient has left is a questionnaire that has to be run again.
 */
export function chooseOption(run: RunState, itemCode: string, optionCode: string): RunState {
  return { ...run, [itemCode]: { option: optionCode, text: '', yesNo: null } };
}

export function typeNumber(run: RunState, itemCode: string, text: string): RunState {
  return { ...run, [itemCode]: { option: '', text, yesNo: null } };
}

export function answerYesNo(run: RunState, itemCode: string, value: boolean): RunState {
  return { ...run, [itemCode]: { option: '', text: '', yesNo: value } };
}

/** What is currently held for an item, whether or not the run has a row for it. */
export function answerFor(run: RunState, itemCode: string): ItemAnswer {
  return run[itemCode] ?? { option: '', text: '', yesNo: null };
}

/** The number a numeric item currently holds, or null for empty, half-typed or unparseable. */
export function parsedAnswer(answer: ItemAnswer): number | null {
  const text = answer.text.trim();
  if (text === '') return null;
  const value = Number(text);
  if (!Number.isFinite(value)) return null;
  return value;
}

/**
 * Whether an item has an answer worth sending.
 *
 * Zero counts. A questionnaire item asking how many days a week the patient walks has a real
 * answer of none, and a station form that treated it as unanswered would refuse to save a true
 * record — which is the opposite of the mistake station 2 guards against, where a weight of
 * zero is a refusal wearing a measurement's clothes.
 */
export function answered(run: RunState, item: InstrumentItem): boolean {
  const answer = answerFor(run, item.item_code);
  switch (item.answer_type) {
    case 'coded':
      return answer.option !== '';
    case 'numeric':
      return parsedAnswer(answer) !== null;
    default:
      return answer.yesNo !== null;
  }
}

/**
 * Two integers, and there is no percentage anywhere in this feature.
 *
 * "Two of three" is what an operator says out loud with a patient in front of them. A
 * percentage rounds away the difference between finished and nearly finished, which on a
 * three-item questionnaire is most of the information.
 */
export interface Progress {
  answered: number;
  total: number;
}

export function progressOf(instrument: Instrument, run: RunState): Progress {
  const items = itemsInOrder(instrument);
  return {
    answered: items.filter((item) => answered(run, item)).length,
    total: items.length,
  };
}

/** The required items still blank, in the order they are asked. */
export function outstandingOf(instrument: Instrument, run: RunState): string[] {
  return itemsInOrder(instrument)
    .filter((item) => item.required && !answered(run, item))
    .map((item) => item.item_code);
}

/**
 * What is wrong with a number an operator has typed into a questionnaire item.
 *
 * The item carries its own `min_value` / `max_value` and the server refuses anything outside
 * them with a 422. Checking here as well is not a second implementation of a clinical rule —
 * it is the same two numbers, sent by the server in the catalogue — and it is what puts the
 * refusal in front of the operator while the patient is still in the chair.
 */
export const NUMBER_PROBLEMS = ['not_a_number', 'below', 'above'] as const;
export type NumberProblem = (typeof NUMBER_PROBLEMS)[number];

export function numberProblem(item: InstrumentItem, answer: ItemAnswer): NumberProblem | null {
  if (item.answer_type !== 'numeric') return null;
  const text = answer.text.trim();
  // An empty field is not a mistake. Whether it is allowed is `required`'s question, and it is
  // answered by `outstandingOf` with a different sentence.
  if (text === '') return null;
  const value = parsedAnswer(answer);
  if (value === null) return 'not_a_number';
  // `typeof` rather than `!== undefined`, because a bound the server sends as JSON null would
  // otherwise be compared against as zero — and every legitimate answer would read as below the
  // minimum. A bound that is not a number is a bound this item does not have.
  if (typeof item.min_value === 'number' && value < item.min_value) return 'below';
  if (typeof item.max_value === 'number' && value > item.max_value) return 'above';
  return null;
}

/** Every numeric item currently holding something the server would refuse. */
export function numberProblems(
  instrument: Instrument,
  run: RunState,
): Partial<Record<string, NumberProblem>> {
  const out: Partial<Record<string, NumberProblem>> = {};
  for (const item of itemsInOrder(instrument)) {
    const problem = numberProblem(item, answerFor(run, item.item_code));
    if (problem !== null) out[item.item_code] = problem;
  }
  return out;
}

export interface AssessmentBody {
  event_id: string;
  patient_id: string;
  visit_id?: string;
  instrument_code: string;
  answers: InstrumentAnswerInput[];
}

/**
 * The run as one request, or null when it is not one yet.
 *
 * Null — rather than a body the server will refuse — for four reasons, and each of them is a
 * refusal the operator would otherwise meet after the patient stood up:
 *
 *  - the instrument may not be run here (D-26). A body for an unlicensed questionnaire is a
 *    request that exists only to be refused, and building one would mean the only thing
 *    stopping an unlicensed response was a disabled button;
 *  - a required item is blank;
 *  - a number is outside the range the item itself carries;
 *  - there is no patient.
 *
 * **There is no score on any answer and no total on the body.** The client sends the option
 * chosen; the points come from `core.instrument_option` at write time. A client that could
 * send a score could send any total it liked, and the total is what §12's cohorting reads.
 */
export function toAssessment(
  instrument: Instrument,
  run: RunState,
  ids: { event: string; patient: string; visit?: string },
): AssessmentBody | null {
  if (!mayOpen(instrument)) return null;
  const patient = ids.patient.trim();
  if (patient === '' || ids.event.trim() === '') return null;
  if (outstandingOf(instrument, run).length > 0) return null;
  if (Object.keys(numberProblems(instrument, run)).length > 0) return null;

  const answers: InstrumentAnswerInput[] = [];
  for (const item of itemsInOrder(instrument)) {
    if (!answered(run, item)) continue;
    const answer = answerFor(run, item.item_code);
    switch (item.answer_type) {
      case 'coded':
        answers.push({ item_code: item.item_code, option_code: answer.option });
        break;
      case 'numeric':
        answers.push({ item_code: item.item_code, value_num: parsedAnswer(answer) as number });
        break;
      default:
        answers.push({ item_code: item.item_code, value_bool: answer.yesNo as boolean });
    }
  }
  // A response with no answers is a questionnaire nobody filled in, stored as though somebody
  // had — which `core.assert_every_response_kept_its_items` refuses at the other end.
  if (answers.length === 0) return null;

  const body: AssessmentBody = {
    event_id: ids.event.trim(),
    patient_id: patient,
    instrument_code: instrument.code,
    answers,
  };
  const visit = (ids.visit ?? '').trim();
  if (visit !== '') body.visit_id = visit;
  return body;
}

// --- §3 step 3's four plain numbers ---

/**
 * The four values that are not a questionnaire.
 *
 * They are CP42 observations rather than instrument items, and that is the honest shape: a
 * count of cigarettes is a value about a patient at a moment, not an answer to a published
 * question. It also means they inherit the correction cascade, the plausibility bands and the
 * research extract without a line of new code.
 *
 * **The two counts are dimensionless** and are entered in `1`, the ratio dimension's canonical
 * unit — "twenty a day" is a pure number and the period is in the code's name, not in a unit
 * anybody could convert.
 *
 * **The two durations are stored in canonical minutes and may be entered in either**, which is
 * CP42's whole argument applied where it would have been easiest to skip. The first unit listed
 * is the one the field opens on, and they differ on purpose: nobody says their patient slept
 * four hundred and twenty minutes, and nobody reports a week's activity in hours. The unit
 * travels with the value to the server, which is what stops the operator who thinks in hours
 * and the one who thinks in minutes recording the same night's sleep as two different numbers.
 */
export interface LifestyleFieldSpec {
  key: LifestyleFieldKey;
  code: string;
  /** The units the selector offers. The first is the one the field opens on. */
  units: string[];
}

export type LifestyleFieldKey = 'cigarettes' | 'smokingYears' | 'sleep' | 'activity';

export const LIFESTYLE_FIELDS: readonly LifestyleFieldSpec[] = Object.freeze([
  { key: 'cigarettes', code: 'CIGARETTES_PER_DAY', units: ['1'] },
  { key: 'smokingYears', code: 'SMOKING_YEARS', units: ['1'] },
  { key: 'sleep', code: 'SLEEP_MINUTES', units: ['h', 'min'] },
  { key: 'activity', code: 'ACTIVE_MINUTES_WEEK', units: ['min', 'h'] },
]);

export interface NumberField {
  text: string;
  unit: string;
}

export type NumbersForm = Record<LifestyleFieldKey, NumberField>;

export function emptyNumbers(): NumbersForm {
  const out = {} as NumbersForm;
  for (const field of LIFESTYLE_FIELDS) out[field.key] = { text: '', unit: field.units[0]! };
  return out;
}

/**
 * The number a field currently holds, or null.
 *
 * Zero is a value here and not a refusal, which is the opposite of station 2's rule and
 * deliberately so: nought cigarettes a day and nought minutes of activity a week are the two
 * most clinically interesting answers this station collects, and a form that discarded them
 * would record the non-smoker as unasked.
 */
export function parsedNumber(field: NumberField): number | null {
  const text = field.text.trim();
  if (text === '') return null;
  const value = Number(text);
  if (!Number.isFinite(value) || value < 0) return null;
  return value;
}

/** The same number in the unit the record stores, for the plausibility bands to read. */
export function canonicalNumber(field: NumberField): number | null {
  const value = parsedNumber(field);
  if (value === null) return null;
  return toCanonical(value, field.unit);
}

/** True when there is nothing on the form worth saving. */
export function numbersEmpty(form: NumbersForm): boolean {
  return LIFESTYLE_FIELDS.every((field) => parsedNumber(form[field.key]) === null);
}

export interface FieldWarning {
  severity: 'stop' | 'warn';
  kind: 'low' | 'high' | 'rose' | 'fell';
  limit: number;
  previous?: number;
  note_en?: string;
  note_bn?: string;
}

export type NumberWarnings = Partial<Record<LifestyleFieldKey, FieldWarning>>;
export type Confirmations = Partial<Record<LifestyleFieldKey, boolean>>;

/**
 * The plausibility verdicts for the form as it stands (CP46).
 *
 * The bands are the server's, fetched once per clinic session and evaluated here so that the
 * warning arrives while the patient can still be asked again — "how many hours do you actually
 * sleep" is a question that can only be re-asked while they are in the room. The bands are read
 * in **canonical** units, which is why the hours field is converted first: a rule written in
 * minutes compared against a value in hours is the unit bug CP42's framework exists to prevent.
 *
 * There are no delta rules for these four codes, and their absence is a decision recorded in
 * the migration: years smoked goes up by one a year and a patient who quits does not lose them,
 * so "changed a lot since last time" is not a signal here the way it is for a weight.
 */
export function warningsForNumbers(
  form: NumbersForm,
  rules: readonly PlausibilityRule[],
  subject: PlausibilitySubject,
  confirmed: Confirmations = {},
): NumberWarnings {
  const out: NumberWarnings = {};
  for (const field of LIFESTYLE_FIELDS) {
    const verdict = evaluatePlausibility(
      resolvePlausibilityRule(rules, field.code, subject),
      canonicalNumber(form[field.key]),
      { confirmed: confirmed[field.key] === true },
    );
    if (verdict !== null) out[field.key] = verdict;
  }
  return out;
}

/** True when something on the form cannot be saved by anybody. */
export function hasBlockingNumber(warnings: NumberWarnings): boolean {
  return Object.values(warnings).some((warning) => warning?.severity === 'stop');
}

/**
 * The derivation this save asks the server for, and the condition under which it asks.
 *
 * `PACK_YEARS` has been declared since CP43 and refusing to compute ever since, because
 * nothing recorded a smoking history. These two fields are what make it reachable — and
 * because CP62's cascade reads the derivation registry, a correction to either number moves
 * the pack-years and the composite with it.
 *
 * Asked for whenever **either** smoking number is in this save rather than only when both are:
 * the server derives from the patient's record, not from this request, so an operator adding
 * the years to a count taken at a previous visit is completing the derivation and should see
 * it. Asked for never when neither is: a derivation nobody's typing changed is a value the
 * record does not need written again.
 *
 * A literal tuple rather than `string[]`, so a name this server does not know is a compile
 * error here rather than a 422 in a corridor.
 */
export const LIFESTYLE_DERIVATIONS = ['PACK_YEARS'] as const;
export type LifestyleDerivation = (typeof LIFESTYLE_DERIVATIONS)[number];

export function derivationsFor(form: NumbersForm): LifestyleDerivation[] {
  const smoking =
    parsedNumber(form.cigarettes) !== null || parsedNumber(form.smokingYears) !== null;
  return smoking ? [...LIFESTYLE_DERIVATIONS] : [];
}

export interface NumbersBatch {
  event_id: string;
  patient_id: string;
  visit_id?: string;
  observations: {
    event_id: string;
    code: string;
    value: number;
    unit: string;
    confirmed?: boolean;
  }[];
  derive: LifestyleDerivation[];
}

/**
 * The four numbers as one request.
 *
 * The values go **as typed**, with the unit **as selected** — never a number this file
 * converted. `core.to_canonical` is what decides what is stored (CP42), and posting a converted
 * number would quietly make a phone authoritative about a clinical value. The canonical
 * conversion above exists only so the plausibility band can be checked before the save.
 */
export function toNumbersBatch(
  form: NumbersForm,
  ids: {
    batch: string;
    patient: string;
    visit?: string;
    perField: (key: LifestyleFieldKey) => string;
    /** Which fields the operator confirmed after a plausibility warning (CP46). */
    confirmed?: (key: LifestyleFieldKey) => boolean;
  },
): NumbersBatch | null {
  const patient = ids.patient.trim();
  if (patient === '' || ids.batch.trim() === '') return null;

  const observations: NumbersBatch['observations'] = [];
  for (const field of LIFESTYLE_FIELDS) {
    const state = form[field.key];
    const value = parsedNumber(state);
    if (value === null) continue;
    const entry: NumbersBatch['observations'][number] = {
      event_id: ids.perField(field.key),
      code: field.code,
      value,
      unit: state.unit,
    };
    if (ids.confirmed?.(field.key) === true) entry.confirmed = true;
    observations.push(entry);
  }
  if (observations.length === 0) return null;

  const body: NumbersBatch = {
    event_id: ids.batch.trim(),
    patient_id: patient,
    observations,
    derive: derivationsFor(form),
  };
  const visit = (ids.visit ?? '').trim();
  if (visit !== '') body.visit_id = visit;
  return body;
}

// --- the composite, read honestly ---

/**
 * The composite, or the reason there is not one, exactly as the server sends it.
 *
 * `assessed` and `missing` name the domains and `minimum` is the floor, and **every one of those
 * is the server's**. There was briefly a copy of the mapping in this file — pack-years for
 * smoking, the live AUDIT-C for alcohol, and so on — because the API said only `null` and an
 * operator who has just finished a questionnaire has to be told what is still wanted. It was a
 * second implementation of a decision `assessment.derive` owns, and it would have been silently
 * wrong the day a fifth domain arrived. There is nothing of it left: no code in this feature
 * knows which observation feeds which domain.
 */
export type Scoring = components['schemas']['LifestyleScoring'];

/** The four domains, as the server names them. */
export type LifestyleDomain = Scoring['assessed'][number];

/**
 * A formula version that no clinician has agreed to.
 *
 * `0.1.0-proposed` is the composite's version today and the suffix is load-bearing: a score
 * computed now must be identifiable forever as one computed before anybody agreed to the
 * arithmetic (D-26). Recognised as "any semver pre-release" rather than by matching the literal
 * word, because a version that became `0.2.0-draft` would otherwise go quietly unlabelled — and
 * the label going quiet is precisely the failure the suffix exists to prevent.
 */
export function isProposal(version: string): boolean {
  return /^[^+]*-/.test(version.trim());
}

/** The composite as the screen states it. Every number in it is the server's. */
export interface ScoreReading {
  value: number;
  unit: string;
  formula: string;
  version: string;
  /** True while no clinician has approved the arithmetic. */
  proposed: boolean;
  /** How many domains went into it — the server's own count, off the value. */
  domains: number | null;
  recordedAt: string;
}

/**
 * The score, or null when there is not one.
 *
 * Null is a legitimate answer from the API and is not an error: below `minimum` assessed domains
 * there is nothing honest to compute. The screen must not render that as a zero or as a dash —
 * both are marks a reader compares with a real score — so this returns null and the `Scoring`
 * object beside it supplies the sentence.
 */
export function readScore(observation: Observation | null | undefined): ScoreReading | null {
  if (observation === null || observation === undefined) return null;
  if (observation.value === undefined || !Number.isFinite(observation.value)) return null;

  const version = (observation.formula_version ?? '').trim();
  const domains = (observation.inputs ?? {})['domains'];
  return {
    value: observation.value,
    unit: observation.unit ?? '1',
    formula: (observation.formula ?? '').trim(),
    version,
    proposed: isProposal(version),
    // The count the server put on the value. A score from three domains is a different number
    // from one from four, and a reader holding only the figure cannot tell.
    domains: typeof domains === 'number' ? domains : null,
    recordedAt: observation.recorded_at,
  };
}

/**
 * The composite the record already holds, if any.
 *
 * Read off the patient's current values so an operator who sits down at station 3 sees the score
 * somebody else's assessment produced this morning instead of an empty card. `LIFESTYLE_RISK` is
 * an ordinary derived observation and the observations endpoint returns the live one.
 */
export function scoreOnRecord(
  observations: readonly Observation[] | undefined,
): Observation | null {
  return (observations ?? []).find((row) => row.code === 'LIFESTYLE_RISK') ?? null;
}

/**
 * The pack-years the server has computed, if any.
 *
 * Shown beside the two counts it comes from, and never worked out here: a client that could
 * produce a derived value would be a client asserting a clinical number nobody measured.
 */
export function packYearsOnRecord(
  observations: readonly Observation[] | undefined,
): Observation | null {
  return (observations ?? []).find((row) => row.code === 'PACK_YEARS') ?? null;
}

/**
 * Where the figure on the card came from, and there is no fourth answer.
 *
 * `write` is a score an assessment or a recompute has just produced; `record` is the
 * `LIFESTYLE_RISK` the patient's chart already held; `none` is a patient with no composite
 * anywhere. It matters because the first is a statement about what the operator just did and
 * the second is not, and a screen that ran them together would tell somebody they had just
 * scored a patient they had not.
 *
 * What none of the three decides any more is **whether the domains can be named**. That comes
 * from a `Scoring` object, and one is available from the first render: `GET
 * /v1/patients/{id}/lifestyle-scoring` computes the account without storing anything, which is
 * what makes it safe to call on arrival. The writing endpoint is still the writing endpoint and
 * is still only called after a save.
 */
export const SCORE_SOURCES = ['write', 'record', 'none'] as const;
export type ScoreSource = (typeof SCORE_SOURCES)[number];

export interface ScorePanel {
  /** The figure, or null when there is not one. */
  reading: ScoreReading | null;
  /** The server's account of the domains, or null when this tablet could not read one. */
  scoring: Scoring | null;
  from: ScoreSource;
}

/**
 * What the card draws.
 *
 * `account` is the domain account from whichever endpoint answered last — the read on arrival,
 * or a write since. It supplies `assessed`, `missing` and `minimum` and never a figure: the
 * read-only endpoint's `score` is null by construction, because it does not derive.
 *
 * `fromWrite` is the score a write produced this session: `undefined` when there has been no
 * write, and **null when a write produced no score**. The distinction is the whole reason this
 * takes three arguments. A null a write has just sent is a fact about the assessment just
 * recorded, and filling that space from the chart would put an older score under a questionnaire
 * that did not produce it.
 */
export function scorePanel(
  account: Scoring | null | undefined,
  fromWrite: Observation | null | undefined,
  onRecord: Observation | null | undefined,
): ScorePanel {
  const scoring = account ?? null;
  if (fromWrite !== undefined) {
    return { reading: readScore(fromWrite), scoring, from: 'write' };
  }
  const reading = readScore(onRecord);
  return { reading, scoring, from: reading === null ? 'none' : 'record' };
}

/** How many more domains before the server will compute one. Its floor, never a copy of one. */
export function stillNeeded(scoring: Scoring): number {
  const short = scoring.minimum - scoring.assessed.length;
  return short > 0 ? short : 0;
}

// --- what went wrong, and what the operator should do about it ---

/** Which call a trouble belongs to. Context for the sentence, not a branch. */
export const ATTEMPTS = ['catalogue', 'assessment', 'score', 'numbers'] as const;
export type Attempt = (typeof ATTEMPTS)[number];

/**
 * A refusal, an unreachable server, and a server that answered with something else.
 *
 * The same three shapes stations 3 and 4 already use, with the status and the server's own code
 * kept: what an operator should do next depends on which refusal it was, and a message alone
 * cannot say.
 */
export interface Trouble {
  kind: 'refused' | 'unreachable' | 'failed';
  attempt: Attempt;
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
  field: string;
  message: string;
}

/**
 * The one refusal this station changes its behaviour for.
 *
 * `POST /v1/assessments` answers 422 `INSTRUMENT_NOT_LICENSED` for a questionnaire this clinic
 * has not licensed, and it is a 422 rather than a 403 because nothing is wrong with who is
 * asking. The screen must say the same thing: an operator who read this as a permissions
 * problem would go and ask for a role change, and would be given one, and would still not be
 * able to run PHQ-9.
 */
export const CODE_NOT_LICENSED = 'INSTRUMENT_NOT_LICENSED';

export function notLicensed(trouble: Trouble): boolean {
  return trouble.code === CODE_NOT_LICENSED;
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

  // A questionnaire this clinic may not run. Nothing on this screen fixes that and nothing on
  // it should try — the fix is a licence, recorded on `core.instrument` — but the catalogue
  // this phone is holding says the opposite, so reading it again is the one useful act.
  if (notLicensed(trouble)) return 'reload';

  // The catalogue this phone fetched at the start of the morning has moved on: a version
  // republished, or an item that is no longer asked. Reading it again is what fixes it, and
  // pressing save again would send the same refused answers.
  if (trouble.status === 404) return 'reload';
  if (trouble.status === 409) return 'reload';

  // The hat being worn does not hold `observation.write.lifestyle`. A different hat or a
  // different person; nothing on this screen.
  if (trouble.status === 403) return 'none';

  // Any other refusal is about what was answered, and the server has said which field.
  // Retrying an unchanged request would fail identically.
  if (trouble.status === 422) return 'none';

  return 'retry';
}

/** The sentence above the server's own words. */
export function troubleKey(trouble: Trouble): string {
  // Its own sentence, because the generic one — "the server would not accept that" — reads as
  // the operator having done something wrong, and a licence nobody has bought is not that.
  if (notLicensed(trouble)) return 'trouble.notLicensed';
  return `trouble.${trouble.kind}`;
}
