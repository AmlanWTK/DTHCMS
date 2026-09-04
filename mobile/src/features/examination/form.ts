/**
 * Station 5's structured examination, as data (CP51, §3 step 5).
 *
 * # Why every decision is in here and none of it is in the screen
 *
 * The screen cannot be rendered outside a device, so anything it decides is a decision
 * nobody checks. What this examination decides is not layout: it is whether a foot the
 * examiner half-finished gets written as a whole one, whether "left" and "right" stay
 * attached to the findings they belong to, and whether a form nobody touched posts a batch
 * full of normal answers. Each of those is a wrong entry in somebody's record, so each lives
 * in a pure function with a test beside it.
 *
 * # The order is the order the examination is performed in
 *
 * Both feet before the neuropathy score, the score before the eyes, the eyes before the
 * chest — and inside a foot: the monofilament first (while the patient's eyes are still
 * shut), then vibration, then the reflex, then the two pulses, and only then the three
 * findings the examiner makes by looking. Criterion 1 gives the whole examination two
 * minutes, and two minutes is only possible if the screen runs in the same direction as the
 * examiner's hands. A form that made them put the tuning fork down and pick it up again
 * costs more than any amount of tapping.
 *
 * # Nothing is pre-selected, not even the normal answer
 *
 * `is_normal` exists on every answer and the API's own description suggests a screen may
 * pre-select it. This one does not. A pre-selected normal answer means an examiner who
 * runs out of time records "pulse present" on a foot nobody palpated — a finding invented
 * by a default, indistinguishable afterwards from one somebody made. The normal answer is
 * drawn **first** instead, which buys the same speed and invents nothing; what is still
 * blank stays blank and `stillBlank` says so.
 *
 * # Laterality is in the code
 *
 * `DP_PULSE_LEFT` and `DP_PULSE_RIGHT` are two codes, exactly as the record stores them,
 * and this form is keyed by those codes rather than by a field name plus a side. There is
 * then no place where a side can be attached to the wrong finding, because there is no
 * moment where the two are apart.
 */

// --- the two sides, and the ten sites ---

export const SIDES = ['LEFT', 'RIGHT'] as const;
export type Side = (typeof SIDES)[number];

/**
 * The ten places a 10 g monofilament is applied, in the order an examiner works: the toes,
 * across the metatarsal heads, then the arch, the heel and the instep.
 *
 * The order is the server's and must not be re-sorted here. Ten rather than the four some
 * protocols use — four is faster and misses the early loss that is the whole reason for
 * screening.
 */
export const MONOFILAMENT_SITES = [
  'hallux',
  'toe_3',
  'toe_5',
  'mth_1',
  'mth_3',
  'mth_5',
  'medial_arch',
  'lateral_arch',
  'heel',
  'dorsum',
] as const;
export type MonofilamentSite = (typeof MONOFILAMENT_SITES)[number];

// --- the fields ---

export type Section = 'foot' | 'neuropathy' | 'retinopathy' | 'cardiovascular';

/**
 * How a finding is answered.
 *
 * `coded` and `boolean` are both rows of buttons on screen and differ only in where the
 * words come from: a coded finding's options are the server's vocabulary, a boolean's are
 * yes and no. `monofilament` is the foot diagram and `score` is the one number in the
 * examination.
 */
export type FieldKind = 'coded' | 'boolean' | 'monofilament' | 'score';

export interface ExamField {
  /** The observation code. The side, where there is one, is part of it. */
  code: string;
  /**
   * The message stem for this finding's label. One stem for both sides: the side is the
   * heading above the group, so "Vibration sense" is written once and read twice.
   */
  label: string;
  section: Section;
  side?: Side;
  kind: FieldKind;
}

/** One foot's findings, in the order the examiner's hands move. */
const FOOT_FINDINGS: readonly { stem: string; label: string; kind: FieldKind }[] = [
  { stem: 'MONOFILAMENT_', label: 'MONOFILAMENT', kind: 'monofilament' },
  { stem: 'VIBRATION_', label: 'VIBRATION', kind: 'coded' },
  { stem: 'ANKLE_REFLEX_', label: 'ANKLE_REFLEX', kind: 'coded' },
  { stem: 'DP_PULSE_', label: 'DP_PULSE', kind: 'coded' },
  { stem: 'PT_PULSE_', label: 'PT_PULSE', kind: 'coded' },
  { stem: 'FOOT_DEFORMITY_', label: 'FOOT_DEFORMITY', kind: 'coded' },
  { stem: 'FOOT_SKIN_', label: 'FOOT_SKIN', kind: 'coded' },
  { stem: 'FOOT_ULCER_', label: 'FOOT_ULCER', kind: 'coded' },
];

/**
 * Every field, in examination order.
 *
 * Both feet come first and complete, because the diabetic foot is what criterion 1 is timed
 * against and what the risk category is derived from. A left foot examined among the right
 * foot's findings is a foot nobody can trust afterwards.
 */
export const EXAM_FIELDS: readonly ExamField[] = Object.freeze([
  ...SIDES.flatMap((side) =>
    FOOT_FINDINGS.map((finding) => ({
      code: `${finding.stem}${side}`,
      label: finding.label,
      section: 'foot' as const,
      side,
      kind: finding.kind,
    })),
  ),

  {
    code: 'NEUROPATHY_SYMPTOM_SCORE',
    label: 'NEUROPATHY_SYMPTOM_SCORE',
    section: 'neuropathy' as const,
    kind: 'score' as const,
  },

  // Screening status before the grades, because "when did anybody last look at these eyes"
  // is the question a consultation turns on, and a grade only exists if somebody looked.
  {
    code: 'RETINOPATHY_SCREEN',
    label: 'RETINOPATHY_SCREEN',
    section: 'retinopathy' as const,
    kind: 'coded' as const,
  },
  ...SIDES.map((side) => ({
    code: `RETINOPATHY_${side}`,
    label: 'RETINOPATHY',
    section: 'retinopathy' as const,
    side,
    kind: 'coded' as const,
  })),
  ...SIDES.map((side) => ({
    code: `MACULOPATHY_${side}`,
    label: 'MACULOPATHY',
    section: 'retinopathy' as const,
    side,
    kind: 'boolean' as const,
  })),

  {
    code: 'HEART_SOUNDS',
    label: 'HEART_SOUNDS',
    section: 'cardiovascular' as const,
    kind: 'coded' as const,
  },
  { code: 'MURMUR', label: 'MURMUR', section: 'cardiovascular' as const, kind: 'coded' as const },
  { code: 'JVP', label: 'JVP', section: 'cardiovascular' as const, kind: 'coded' as const },
  {
    code: 'PERIPHERAL_OEDEMA',
    label: 'PERIPHERAL_OEDEMA',
    section: 'cardiovascular' as const,
    kind: 'coded' as const,
  },
  ...SIDES.map((side) => ({
    code: `CAROTID_BRUIT_${side}`,
    label: 'CAROTID_BRUIT',
    section: 'cardiovascular' as const,
    side,
    kind: 'boolean' as const,
  })),
]);

/** The fields of one section, in examination order; narrowed to one side when asked. */
export function fieldsIn(section: Section, side?: Side): readonly ExamField[] {
  return EXAM_FIELDS.filter(
    (field) => field.section === section && (side === undefined || field.side === side),
  );
}

// --- the form ---

/** Whether the filament was felt at a site, or whether nobody has said yet. */
export type SiteState = 'unknown' | 'felt' | 'not_felt';

export interface MonofilamentState {
  /** Only the sites somebody has answered. An absent site is not an answer of "no". */
  felt: Partial<Record<MonofilamentSite, boolean>>;
  /** A foot that could not be tested at all: a dressing, an amputation, a refused sock. */
  notTested: boolean;
  /** Why not. Recorded with the finding, because "not tested" alone is a hole. */
  reason: string;
}

export interface ExamForm {
  /** Coded answers, keyed by observation code. Absent or empty means nobody has answered. */
  coded: Record<string, string>;
  /** Yes/no findings, keyed by code. Absent means unanswered — which is not the same as no. */
  flags: Record<string, boolean>;
  /** The neuropathy symptom score, held as text for the same reason every other number is. */
  score: string;
  monofilament: Record<Side, MonofilamentState>;
}

export function emptyMonofilament(): MonofilamentState {
  return { felt: {}, notTested: false, reason: '' };
}

export function emptyExam(): ExamForm {
  return {
    coded: {},
    flags: {},
    score: '',
    monofilament: { LEFT: emptyMonofilament(), RIGHT: emptyMonofilament() },
  };
}

// --- what a tap does ---

export function setCoded(form: ExamForm, code: string, valueCode: string): ExamForm {
  return { ...form, coded: { ...form.coded, [code]: valueCode } };
}

export function setFlag(form: ExamForm, code: string, value: boolean): ExamForm {
  return { ...form, flags: { ...form.flags, [code]: value } };
}

export function setScore(form: ExamForm, text: string): ExamForm {
  return { ...form, score: text };
}

function withFoot(form: ExamForm, side: Side, next: MonofilamentState): ExamForm {
  return { ...form, monofilament: { ...form.monofilament, [side]: next } };
}

export function siteState(state: MonofilamentState, site: MonofilamentSite): SiteState {
  const answer = state.felt[site];
  if (answer === undefined) return 'unknown';
  return answer ? 'felt' : 'not_felt';
}

/**
 * One tap on one site: unknown → felt → not felt → unknown.
 *
 * The common answer comes first so the ordinary foot is one tap per site, and the third tap
 * clears the site rather than looping straight back to "felt". Without that escape a
 * mis-tapped site can only be corrected by tapping past the wrong answer again, and an
 * examiner in a hurry leaves whichever answer is showing.
 */
export function tapSite(form: ExamForm, side: Side, site: MonofilamentSite): ExamForm {
  const state = form.monofilament[side];
  const felt = { ...state.felt };
  switch (siteState(state, site)) {
    case 'unknown':
      felt[site] = true;
      break;
    case 'felt':
      felt[site] = false;
      break;
    default:
      delete felt[site];
  }
  // Answering a site is a statement that this foot *was* tested.
  return withFoot(form, side, { ...state, felt, notTested: false, reason: '' });
}

/**
 * Every site felt, in one tap.
 *
 * The commonest foot in a screening clinic is a normal one, and ten taps to say so is how a
 * two-minute examination becomes a five-minute one. This is a positive assertion the
 * examiner makes after testing the foot, not a default: nothing sets it but a press.
 */
export function markAllFelt(form: ExamForm, side: Side): ExamForm {
  const felt: Partial<Record<MonofilamentSite, boolean>> = {};
  for (const site of MONOFILAMENT_SITES) felt[site] = true;
  return withFoot(form, side, { felt, notTested: false, reason: '' });
}

/** A foot that could not be tested. The sites go, because a foot not tested has none. */
export function setNotTested(form: ExamForm, side: Side, notTested: boolean): ExamForm {
  const state = form.monofilament[side];
  if (!notTested) return withFoot(form, side, { ...state, notTested: false });
  return withFoot(form, side, { felt: {}, notTested: true, reason: state.reason });
}

export function setNotTestedReason(form: ExamForm, side: Side, reason: string): ExamForm {
  return withFoot(form, side, { ...form.monofilament[side], reason });
}

// --- the monofilament's payload ---

export type MonofilamentPayload =
  { not_tested: true } | { felt: Record<MonofilamentSite, boolean> };

/** The sites where the filament was not felt. The finding the test exists to produce. */
export function missedSites(state: MonofilamentState): MonofilamentSite[] {
  return MONOFILAMENT_SITES.filter((site) => state.felt[site] === false);
}

/** How many of the ten somebody has answered. */
export function answeredSites(state: MonofilamentState): number {
  return MONOFILAMENT_SITES.filter((site) => state.felt[site] !== undefined).length;
}

/**
 * A foot the examiner started and did not finish.
 *
 * Nine sites is refused by the server, and rightly: a missing site recorded as "not felt"
 * invents a finding and recorded as "felt" hides one. There is no third thing to do with
 * it, so the screen refuses to save until the examiner goes back to the foot — which is the
 * only place the answer exists.
 */
export function monofilamentInterrupted(state: MonofilamentState): boolean {
  if (state.notTested) return false;
  const answered = answeredSites(state);
  return answered > 0 && answered < MONOFILAMENT_SITES.length;
}

/**
 * What one foot's test writes, or nothing.
 *
 * `not_tested` travels alone — no `felt` key at all — because the server reads this payload
 * with unknown fields disallowed and a foot that was not tested has no sites.
 */
export function monofilamentPayload(state: MonofilamentState): MonofilamentPayload | null {
  if (state.notTested) return { not_tested: true };
  if (answeredSites(state) !== MONOFILAMENT_SITES.length) return null;
  const felt = {} as Record<MonofilamentSite, boolean>;
  // Built from the site list rather than from the object's own keys, so the payload always
  // holds all ten in the server's order however the examiner tapped around the foot.
  for (const site of MONOFILAMENT_SITES) felt[site] = state.felt[site] === true;
  return { felt };
}

// --- the one number ---

export const SCORE_MIN = 0;
export const SCORE_MAX = 13;

/**
 * The neuropathy symptom score as typed.
 *
 * Zero is a real answer here and the commonest one — a patient with no symptoms — which is
 * why this cannot reuse the anthropometry rule that treats a non-positive number as "not
 * measured". A weight of zero is a refusal; a symptom score of zero is a finding.
 */
export function parsedScore(text: string): number | null {
  const trimmed = text.trim();
  if (trimmed === '') return null;
  const value = Number(trimmed);
  if (!Number.isFinite(value)) return null;
  return value;
}

/**
 * A score the instrument cannot produce.
 *
 * Checked here so the examiner sees it while the patient is still in the chair, rather than
 * as a 422 in a corridor. The server's band is authoritative; this is the copy that arrives
 * in time to be acted on.
 */
export function scoreOutOfRange(form: ExamForm): boolean {
  const value = parsedScore(form.score);
  if (value === null) return false;
  return !Number.isInteger(value) || value < SCORE_MIN || value > SCORE_MAX;
}

// --- the vocabulary ---

export interface Answer {
  code: string;
  value_code: string;
  display_en: string;
  display_bn: string;
  ordering: number;
  is_normal: boolean;
}

/**
 * The answers one coded finding may take, in the server's own order.
 *
 * Filtered, never sorted. The order is clinical — present, diminished, absent is a scale —
 * and sorting it here by anything at all would put "absent" first on half the screens and
 * make an examiner read every list twice. The server has already done the only ordering
 * that means anything.
 */
export function answersFor(answers: readonly Answer[], code: string): readonly Answer[] {
  return answers.filter((answer) => answer.code === code);
}

/** The answer that means nothing abnormal: the first one so marked, for the same reason. */
export function normalAnswerFor(answers: readonly Answer[], code: string): Answer | null {
  return answersFor(answers, code).find((answer) => answer.is_normal) ?? null;
}

// --- what is still missing ---

/**
 * The codes nobody has answered, in examination order.
 *
 * Shown as a count rather than as a refusal to save. A screening clinic examines what it can
 * reach — a patient with a dressing on one foot, an eye that could not be seen — and a form
 * that demanded every field would be a form somebody fills with whatever clears it.
 */
export function stillBlank(form: ExamForm): string[] {
  return EXAM_FIELDS.filter((field) => !isAnswered(form, field)).map((field) => field.code);
}

/**
 * The foot a field belongs to.
 *
 * Read off the field's own side rather than passed in beside it, for the same reason the
 * side lives in the code: there is no moment where a foot and its findings are apart, so
 * there is no moment where they can be paired up wrongly.
 */
function footOf(form: ExamForm, field: ExamField): MonofilamentState {
  return form.monofilament[field.side === 'RIGHT' ? 'RIGHT' : 'LEFT'];
}

function isAnswered(form: ExamForm, field: ExamField): boolean {
  switch (field.kind) {
    case 'monofilament':
      return monofilamentPayload(footOf(form, field)) !== null;
    case 'score':
      return parsedScore(form.score) !== null;
    case 'boolean':
      return form.flags[field.code] !== undefined;
    default:
      return (form.coded[field.code] ?? '') !== '';
  }
}

/** True when the form holds nothing worth saving at all. */
export function isBlank(form: ExamForm): boolean {
  return stillBlank(form).length === EXAM_FIELDS.length;
}

/** The feet the examiner started and did not finish. */
export function interruptedFeet(form: ExamForm): Side[] {
  return SIDES.filter((side) => monofilamentInterrupted(form.monofilament[side]));
}

/**
 * Whether this form may be written.
 *
 * Two refusals and no others. A part-tested foot, because the server will not take it and
 * dropping it silently would lose the test the examiner performed; and a score outside the
 * instrument's range, because it is a typing slip that the record would carry for years.
 * Everything else — every blank field — saves, because an incomplete examination honestly
 * recorded is worth more than a complete one somebody invented.
 */
export function canSave(form: ExamForm): boolean {
  return !isBlank(form) && interruptedFeet(form).length === 0 && !scoreOutOfRange(form);
}

// --- what gets sent ---

/**
 * The server's own ceiling on a batch, mirrored.
 *
 * A whole examination is twenty-eight values, which is more than one transaction may carry,
 * so `toExamBatch` returns several. The number is here rather than in the screen because
 * exceeding it is a 422 the examiner sees after the patient has left.
 */
export const MAX_BATCH = 20;

/**
 * The groups a form is written in.
 *
 * A foot is one transaction. Not tidiness: the risk category is derived from that foot's
 * findings, and a record that held four of a foot's eight would carry a category computed
 * from half an examination — wrong in the direction that matters. Everything else shares a
 * batch because nothing in it is derived from anything else in it.
 */
export const EXAM_GROUPS = ['FOOT_LEFT', 'FOOT_RIGHT', 'GENERAL'] as const;
export type ExamGroup = (typeof EXAM_GROUPS)[number];

export function groupOf(field: ExamField): ExamGroup {
  if (field.section !== 'foot') return 'GENERAL';
  return field.side === 'RIGHT' ? 'FOOT_RIGHT' : 'FOOT_LEFT';
}

/**
 * What the server derives from an examination.
 *
 * A literal tuple rather than `string[]`, so a name this server does not know is a compile
 * error here rather than a 422 in a corridor.
 */
export const EXAM_DERIVATIONS = ['FOOT_RISK_LEFT', 'FOOT_RISK_RIGHT'] as const;
export type ExamDerivation = (typeof EXAM_DERIVATIONS)[number];

/** The derivations the server computes for each group once its findings have landed. */
const DERIVED_BY_GROUP: Partial<Record<ExamGroup, readonly ExamDerivation[]>> = {
  FOOT_LEFT: ['FOOT_RISK_LEFT'],
  FOOT_RIGHT: ['FOOT_RISK_RIGHT'],
};

export interface ExamObservation {
  event_id: string;
  code: string;
  value?: number;
  unit?: string;
  value_code?: string;
  value_bool?: boolean;
  value_json?: MonofilamentPayload;
  note?: string;
}

export interface ExamBatch {
  event_id: string;
  patient_id: string;
  visit_id?: string;
  observations: ExamObservation[];
  derive?: ExamDerivation[];
}

/**
 * The examination as requests.
 *
 * Several rather than one, and the split is clinical rather than arithmetic: each foot is
 * its own transaction so that its derived risk category can never be computed from a
 * half-written foot.
 *
 * The risk category is **named, not sent**. It is what a structured foot examination exists
 * to produce, and an examiner who could type it would be back to an opinion with a dropdown
 * in front of it. Naming it in the same batch as the findings also means the server derives
 * it inside the transaction that wrote them, so it never sees a foot mid-write.
 *
 * A blank field contributes nothing. There is no "normal" default anywhere on this path: a
 * finding that reaches the record is a finding somebody made.
 */
export function toExamBatch(
  form: ExamForm,
  ids: {
    /** A stable id per group, so a retry writes the same values rather than a second set. */
    batch: (group: ExamGroup) => string;
    patient: string;
    visit?: string;
    perValue: (code: string) => string;
  },
): ExamBatch[] {
  const byGroup = new Map<ExamGroup, ExamObservation[]>();
  const push = (group: ExamGroup, observation: ExamObservation) => {
    const list = byGroup.get(group) ?? [];
    list.push(observation);
    byGroup.set(group, list);
  };

  for (const field of EXAM_FIELDS) {
    const group = groupOf(field);
    const event_id = () => ids.perValue(field.code);

    if (field.kind === 'monofilament') {
      const state = footOf(form, field);
      const payload = monofilamentPayload(state);
      if (payload === null) continue;
      const observation: ExamObservation = {
        event_id: event_id(),
        code: field.code,
        value_json: payload,
      };
      // The reason travels with the finding rather than in a note somebody types later:
      // "not tested" on its own is a hole in the record that nobody can fill afterwards.
      if (state.notTested && state.reason.trim() !== '') observation.note = state.reason.trim();
      push(group, observation);
      continue;
    }

    if (field.kind === 'score') {
      const value = parsedScore(form.score);
      if (value === null) continue;
      // The unit is the dimensionless one the registry gives this code. A unit-bearing code
      // sent without it is refused, and a score is unit-bearing in the same sense a ratio is.
      push(group, { event_id: event_id(), code: field.code, value, unit: '1' });
      continue;
    }

    if (field.kind === 'boolean') {
      const value = form.flags[field.code];
      if (value === undefined) continue;
      push(group, { event_id: event_id(), code: field.code, value_bool: value });
      continue;
    }

    const valueCode = form.coded[field.code] ?? '';
    if (valueCode === '') continue;
    push(group, { event_id: event_id(), code: field.code, value_code: valueCode });
  }

  const batches: ExamBatch[] = [];
  for (const group of EXAM_GROUPS) {
    const observations = byGroup.get(group);
    if (observations === undefined || observations.length === 0) continue;
    const batch: ExamBatch = {
      event_id: ids.batch(group),
      patient_id: ids.patient,
      observations,
    };
    if (ids.visit !== undefined) batch.visit_id = ids.visit;
    const derive = DERIVED_BY_GROUP[group];
    if (derive !== undefined) batch.derive = [...derive];
    batches.push(batch);
  }
  return batches;
}

/** The risk category the server derived for each foot, read back out of what it wrote. */
export type FootRisk = 'very_low' | 'low' | 'moderate' | 'high';

export function footRiskFrom(
  rows: { code: string; value_code?: string | null }[] | undefined,
): Partial<Record<Side, FootRisk>> {
  const out: Partial<Record<Side, FootRisk>> = {};
  if (rows === undefined) return out;
  for (const side of SIDES) {
    // Newest first from the API, so the first sighting of the code is the current category.
    const row = rows.find((r) => r.code === `FOOT_RISK_${side}`);
    const value = row?.value_code;
    if (value === 'very_low' || value === 'low' || value === 'moderate' || value === 'high') {
      out[side] = value;
    }
  }
  return out;
}
