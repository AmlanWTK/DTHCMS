import type { components } from '@dthcms/api-client';

/**
 * Station 8's exercise assessment and plan, as data (CP60, blueprint §3 step 8, §12.1).
 *
 * # The rule this whole file is arranged around
 *
 * **The screen never receives a contraindicated exercise, and nothing here behaves as though it
 * might.** `GET /v1/patients/{id}/exercise/options` computes the permitted set on the server and
 * the excluded rows never leave the process — so there is no filter in this file, no
 * `contraindicated` flag to style differently, no "show all", and no list of exercises held
 * anywhere but the one the server just sent. `offerRows` maps the payload's own array in the
 * payload's own order and drops nothing; a test reads the codes back to prove it.
 *
 * The consequence worth stating plainly: **this file contains no exercise code and no exercise
 * name, in either language.** A screen that held a copy of the library would be one tap and one
 * bug away from offering a jumping routine to a patient who cannot feel their feet — which is the
 * failure the whole checkpoint exists to make impossible. A test greps the feature for the
 * seeded codes.
 *
 * # What is shown instead of the excluded rows
 *
 * A count and the reasons, and the reasons are named **by condition** — because the exercises are
 * not on the device to name. `exclusionReading` copies `excluded` off the payload rather than
 * subtracting anything: the per-condition counts overlap (jogging is excluded by neuropathy *and*
 * by an open ulcer), so a client that added them up would report a larger number than the server
 * did, about the same patient, on the same screen.
 *
 * # Nothing here multiplies a target into a weekly total
 *
 * `minutes_per_week` is computed once on the server, per item and per plan, so that a screen, a
 * printed sheet and §12.1's extract cannot each round it differently. `sheetRows` copies both
 * figures across and this file contains no arithmetic on either.
 *
 * # Why the decisions are here and not in the screen
 *
 * The same rule stations 2, 3, 4, 5 and 7 follow: a React Native component cannot be rendered
 * outside a device, so anything it decides is a decision nobody checks. What station 8 decides is
 * which step the operator is on, what counts as a target, and what may be sent — and all three
 * are pure functions with tests beside them.
 */

// --- what the contract gives us ---

/** One thing that can stop somebody exercising, with the **question** the station asks. */
export type Contraindication = components['schemas']['Contraindication'];

/**
 * One row of the library, as it is offered and as it is printed.
 *
 * Only ever reached through `Options` or a `Plan`. There is no route that returns these
 * unfiltered for a patient, and this application must never acquire a call that looks like one.
 */
export type Exercise = components['schemas']['Exercise'];

/** One reason the offered list is shorter than the library. A condition, never an exercise. */
export type Exclusion = components['schemas']['ExerciseExclusion'];

/** What this patient may be offered, and why it is not more. */
export type Options = components['schemas']['ExerciseOptions'];

/** What station 8 found. The filter reads this and nothing a client sends. */
export type Assessment = components['schemas']['ExerciseAssessment'];

/** The routine a patient was given, and the findings it was filtered against. */
export type Plan = components['schemas']['ExercisePlan'];

/** One target on that routine, with the instruction that gets printed. */
export type PlanItem = components['schemas']['ExercisePlanItem'];

/** The interface language. Local rather than imported: this file must stay renderer-free. */
export type Locale = 'en' | 'bn';

// --- the words on a row, in the reader's language ---

/**
 * One piece of the server's text, and which language it turned out to be in.
 *
 * Invariant 88 refuses a library row, or an exclusion reason, that reads in only one language, so
 * a missing translation is an edge case rather than a shape to design around. It is still worth
 * one honest line: a Bangla-reading specialist handed English with no explanation is one who
 * thinks the app has switched languages on them.
 *
 * Deliberately its own copy rather than an import from the nutrition or lifestyle feature. A
 * feature that reached into another's internals for a ten-line helper would couple two stations
 * so that neither could change its mind.
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

// --- what went wrong, and what the operator can do about it ---

/** Which call a trouble belongs to. Context for the sentence, not a branch. */
export const ATTEMPTS = ['questions', 'options', 'assessment', 'plan', 'record'] as const;
export type Attempt = (typeof ATTEMPTS)[number];

/**
 * A refusal, an unreachable server, and a server that answered with something else.
 *
 * The same three shapes stations 3, 4, 5 and 7 use, with the status, the server's own code and
 * the field it named kept: what an operator should do next depends on which refusal it was, and
 * a message alone cannot say.
 */
export interface Trouble {
  kind: 'refused' | 'unreachable' | 'failed';
  attempt: Attempt;
  /** The HTTP status, or 0 when there was no answer at all. */
  status: number;
  /**
   * The server's own code for the refusal, or empty.
   *
   * The only part of an error a client may branch on, and this station branches on three of
   * them. A message is written for a person: it gets translated, shortened and improved, and a
   * screen that matched on its words would change behaviour the day somebody rewrote a sentence.
   */
  code: string;
  /**
   * The field the server named on a 422, or empty.
   *
   * Only ever a field that carries a **sentence**. `exercise_code` and `contraindication_code`
   * carry codes rather than prose, and putting one of those here would draw `SEVERE_NEUROPATHY`
   * at an operator as though it were the explanation — see `api.ts`'s `FIELD_ORDER`.
   */
  field: string;
  message: string;
  /**
   * The exercise the server refused, from `fields.exercise_code`. Empty when it named none.
   *
   * Machine-readable on purpose, and the reason the refusal is worth more than a banner: the
   * screen can put the sentence on the row the operator chose rather than at the top of a list of
   * nine, where they have to work out which one it is about.
   */
  exercise: string;
  /** The condition that forbids it, from `fields.contraindication_code`. Empty when none. */
  condition: string;
  /**
   * The condition codes an assessment failed to ask about, from `fields.missing_conditions`.
   *
   * Codes, out of the prose. `fields.asked` carries the same list inside a sentence that gets
   * translated, shortened and improved; a screen that parsed that sentence to find out which
   * questions were new would break the day somebody rewrote it.
   */
  missing: string[];
}

/**
 * Nobody has answered the questions yet, so there is no honest list to give.
 *
 * **Not a failure.** It is the station being told which step it is on, and the screen shows the
 * questions rather than an empty list or a red banner. Matched on the server's code and never on
 * the status: 409 is also how a replayed event id and an idempotency clash arrive, and neither of
 * those means "go and ask the questions".
 */
export const CODE_NO_ASSESSMENT = 'EXERCISE_NO_ASSESSMENT';

/**
 * A colleague recorded a newer assessment while this operator was choosing.
 *
 * The plan they were about to issue was built against the wrong findings — and every check on
 * the way in would have passed, because they would all have been checking the wrong assessment.
 * The answer is to read the options again and look at the list that is true now.
 */
export const CODE_SUPERSEDED = 'EXERCISE_ASSESSMENT_SUPERSEDED';

/**
 * The refusal this checkpoint exists for.
 *
 * It should be unreachable from this screen: the only list the screen holds is the one the server
 * computed, and `toPlanBody` refuses to build a body for anything outside it. Meeting it means
 * the library or the assessment moved between the list being drawn and the plan being sent, so
 * the way on is the same as a supersede — read the options again.
 */
export const CODE_CONTRAINDICATED = 'EXERCISE_CONTRAINDICATED';

/**
 * An exercise left the library between the list being drawn and the plan being sent.
 *
 * A **409**, not a 422, and that distinction is the whole reason this predicate can be simple: a
 * retirement says the list moved, not that the request was wrong. Alongside
 * `EXERCISE_NO_ASSESSMENT` and `EXERCISE_ASSESSMENT_SUPERSEDED` it is one of the answers that
 * mean *fetch the options again*, and it names the exercise in `fields.exercise_code`.
 *
 * This replaces the reasoning an earlier version of this file carried, which read *any* 422 on
 * `targets` as "the list moved" on the strength of `toPlanBody` guaranteeing its own body. That
 * was true of this screen and of nothing else. It is not needed now, and a rule that only held
 * because one caller was careful is a rule the next caller does not have.
 */
export const CODE_RETIRED = 'EXERCISE_RETIRED';

export function retired(trouble: Trouble): boolean {
  return trouble.code === CODE_RETIRED;
}

export function noAssessment(trouble: Trouble): boolean {
  return trouble.code === CODE_NO_ASSESSMENT;
}

export function superseded(trouble: Trouble): boolean {
  return trouble.code === CODE_SUPERSEDED;
}

export function contraindicated(trouble: Trouble): boolean {
  return trouble.code === CODE_CONTRAINDICATED;
}

/**
 * The catalogue this tablet is working from is older than the clinic's.
 *
 * `POST /v1/exercise/assessments` refuses an assessment that leaves a live condition unanswered
 * and names the missing codes in `fields.asked`. From this screen that is only reachable one way:
 * a condition was added to the catalogue after this tablet fetched it — an offline morning, a
 * rolling deploy — because `toAssessmentBody` will not build a body until every question the
 * tablet *knows about* is answered.
 *
 * So the operator has not made a mistake and there is nothing on the form to correct. The way on
 * is to read the catalogue again, which puts the new question on the screen.
 *
 * Matched on the field rather than a code because the server answers with the generic
 * `VALIDATION_FAILED`; there is no code that distinguishes it. See the note in `api.ts`.
 */
export function questionsMissing(trouble: Trouble): boolean {
  return trouble.missing.length > 0 || (trouble.status === 422 && trouble.field === 'asked');
}

/**
 * Whether this refusal is about one particular exercise the operator chose.
 *
 * Every target refusal now carries `fields.exercise_code` — contraindicated, not in the library,
 * not two numbers, on the plan twice — so the sentence goes on that row rather than in a banner
 * above a list of nine, where the operator would have to work out which of their choices it names.
 */
export function namesExercise(trouble: Trouble | null | undefined): boolean {
  return trouble !== null && trouble !== undefined && trouble.exercise !== '';
}

/**
 * Which row on the screen the refusal belongs on, or empty.
 *
 * Not simply the exercise the server named, and the difference is a refusal that would otherwise
 * be drawn nowhere at all: `EXERCISE_RETIRED` names its exercise too, and by the time the screen
 * has read the options again — which is what a retirement means — that exercise is not on the
 * list. A banner suppressed because "the row has it" and a row that no longer exists is a
 * refusal the operator never sees.
 *
 * So the code has to be on the list currently being drawn. Everything else keeps its sentence in
 * the banner, where there is something to read it.
 */
export function refusedRow(
  trouble: Trouble | null | undefined,
  options: Options | null | undefined,
): string {
  if (!namesExercise(trouble) || options === null || options === undefined) return '';
  const code = trouble!.exercise;
  return options.exercises.some((exercise) => exercise.code === code) ? code : '';
}

/**
 * Whether this is something to draw as a failure at all.
 *
 * `EXERCISE_NO_ASSESSMENT` is the one that is not. A patient who has not been through the
 * questions is an ordinary first visit, and a red banner over the form the operator is about to
 * fill in teaches them to read past red banners.
 */
export function readsAsFailure(trouble: Trouble | null | undefined): boolean {
  if (trouble === null || trouble === undefined) return false;
  return !noAssessment(trouble);
}

/**
 * What the operator can actually do about it.
 *
 * Five answers and no sixth, because a screen that offered "try again" for every failure would
 * train people to press it at the failures where pressing it cannot help.
 *
 * **This is the only thing the screen writes about a refusal.** The sentence describing what was
 * refused is the server's and is shown unchanged; these say what the screen has already done and
 * what to do next, which is the one thing the server cannot know. See `troubleKey`.
 */
export const ADVICE = ['ask', 'reread', 'review', 'retry', 'none'] as const;
export type Advice = (typeof ADVICE)[number];

export function adviceFor(trouble: Trouble): Advice {
  // The questions have not been asked. Retrying the same read fails identically for ever; the
  // way on is the form, and the screen has already moved to it.
  if (noAssessment(trouble)) return 'ask';

  // A question this tablet had never heard of. Also the form, but a different sentence: the
  // screen has read the catalogue again by the time this is drawn and marked what is new, and an
  // operator told only "answer the questions" would re-read all six looking for the change.
  if (questionsMissing(trouble)) return 'reread';

  // The list moved, or one choice on it did. All three are answered by looking at the list that
  // is true now, which the screen has already fetched by the time this is drawn.
  if (superseded(trouble) || retired(trouble) || contraindicated(trouble)) return 'review';

  if (trouble.kind === 'unreachable') return 'retry';

  // The hat being worn does not hold `observation.write.exercise`, or this facility does not
  // have that patient. A different hat or a different person; nothing on this screen.
  if (trouble.status === 403 || trouble.status === 404) return 'none';

  // Any other refusal is about what was sent, and the server has said what. Retrying an
  // unchanged request would fail identically.
  if (trouble.status === 422 || trouble.status === 409) return 'none';

  return 'retry';
}

/**
 * Whether the server wrote a sentence for this, or the screen has to.
 *
 * True for every refusal the API answers with: `errs.New` takes both languages and this station's
 * handlers fill them in. False for a request that never arrived and for an answer this
 * application does not understand — the two cases where there is no server sentence to show,
 * because there was no server.
 */
export function serverSpoke(trouble: Trouble): boolean {
  return trouble.message.trim() !== '';
}

/**
 * The screen's **own** sentence — used only when the server had none.
 *
 * Three keys, one per shape, and deliberately no key for any refusal the server names. An earlier
 * version of this file had one for each: `trouble.contraindicated` read "That exercise is not safe
 * for this patient's recorded condition, so it cannot go on the plan", drawn in semibold *above*
 * the server's own sentence in grey. That is a client-side paraphrase of the one refusal this
 * whole checkpoint exists to deliver, and it was wrong in three ways at once: it was a second
 * source of truth for that sentence; it would drift the first time a clinician improved the
 * server's wording; and by being the bolder of the two it made the paraphrase the headline and
 * the reason from `core.exercise_contraindication` — the sentence a clinician is meant to argue
 * with — a footnote.
 *
 * So the rule is now: **the server's sentence is shown, once, unchanged, and the screen adds only
 * what it did and what to do next** (`adviceFor`). These three keys cover the cases where there is
 * no server sentence because there was no server.
 */
export function troubleKey(trouble: Trouble): string {
  return `trouble.${trouble.kind}`;
}

/**
 * Why the operator is being sent back to the list.
 *
 * Three refusals mean the same act — read the options again — and they are kept apart because the
 * sentence beside the fresh list is different, and so is what the operator should think about. A
 * supersede is a colleague's work on this patient. A contraindication refused at the last moment
 * names an exercise and a condition, and the screen puts that sentence on the row. `moved` is the
 * library itself having changed under the operator — a row retired between the list being drawn
 * and the plan being sent.
 *
 * All three are conflicts or a refusal the server gave its own code, so this reads codes and
 * nothing else. Null for everything else, which is what stops the screen throwing away a
 * half-built plan over a dropped connection or a mistyped number.
 */
export const REVIEWS = ['superseded', 'contraindicated', 'moved'] as const;
export type Review = (typeof REVIEWS)[number];

export function reviewFor(trouble: Trouble | null | undefined): Review | null {
  if (trouble === null || trouble === undefined) return null;
  if (superseded(trouble)) return 'superseded';
  if (contraindicated(trouble)) return 'contraindicated';
  if (retired(trouble)) return 'moved';
  return null;
}

// --- which of the three steps the station is on ---

/**
 * The station in the order it works: the questions, the choosing, the sheet.
 *
 * Three steps rather than one long form, because the middle one cannot be drawn until the server
 * has answered the first. There is no list to choose from until the questions have been answered
 * — that is the point of `EXERCISE_NO_ASSESSMENT` — so a screen that showed the choices beside
 * the questions would be a screen showing an empty list, or worse, a full one.
 */
export const STEPS = ['questions', 'choose', 'sheet'] as const;
export type Step = (typeof STEPS)[number];

/** What the station has in hand. Every field is something the server said. */
export interface Stage {
  /**
   * The permitted set, or null.
   *
   * Null is the honest state before the server has answered, and it is what a 409
   * `EXERCISE_NO_ASSESSMENT` leaves behind. There is deliberately no "the library, unfiltered"
   * value this could fall back to.
   */
  options: Options | null;
  /** The plan as it stands, or null before one has been issued. */
  plan: Plan | null;
  /** True while the operator has deliberately opened the questions again. */
  asking: boolean;
}

export function emptyStage(): Stage {
  return { options: null, plan: null, asking: false };
}

/**
 * Which step to draw.
 *
 * The 409 is handled first and by name. A screen that treated it as an error would show an empty
 * list and a red banner to an operator whose next act is to ask five questions — and the operator
 * would learn to press "try again", which cannot ever work.
 */
export function stepFor(stage: Stage, trouble?: Trouble | null): Step {
  if (trouble !== null && trouble !== undefined && noAssessment(trouble)) return 'questions';
  if (stage.asking) return 'questions';
  if (stage.options === null) return 'questions';
  return stage.plan === null ? 'choose' : 'sheet';
}

// --- step 1: the questions ---

/**
 * One condition as the station asks about it.
 *
 * `question` is the primary text and `name` is the short label kept for the record's sake. The
 * order matters and it is this way round on purpose: a checkbox saying "neuropathy" gets ticked
 * for tingling toes, and one asking whether protective sensation is lost at a monofilament site
 * does not. Every exclusion downstream is only as good as the answer to that question.
 */
export interface QuestionRow {
  code: string;
  question: Wording;
  name: Wording;
  /** Where the record may already hold the answer, so a station can pre-fill rather than ask. */
  fromObservation: string;
  /** What the operator answered, or null while the question is still open. */
  answer: ConditionAnswer | null;
  /**
   * Added to the catalogue since this tablet last read it.
   *
   * From `fields.missing_conditions` on the refusal that sent the screen back here, so the
   * operator can see which of five questions is the new one instead of re-reading all of them.
   * The codes come out of the payload rather than out of the prose beside them.
   */
  newly: boolean;
}

/** The catalogue in the order the station works through it, however it arrived. */
function inOrder(catalogue: readonly Contraindication[] | undefined): Contraindication[] {
  return [...(catalogue ?? [])].sort(
    (a, b) => a.ordering - b.ordering || a.code.localeCompare(b.code),
  );
}

/**
 * The catalogue, in the order the station works through it.
 *
 * Sorted by the server's own `ordering` and then by code, so two tablets ask the five questions
 * in the same sequence — an operator who has learned the order reads faster than one who has to
 * find each question.
 */
export function questionRows(
  catalogue: readonly Contraindication[] | undefined,
  draft: AssessmentDraft,
  locale: Locale,
  newlyAdded: readonly string[] = [],
): QuestionRow[] {
  const added = new Set(newlyAdded);
  return inOrder(catalogue).map((row) => ({
    code: row.code,
    question: wordingOf(row.question_en, row.question_bn, locale),
    name: wordingOf(row.name_en, row.name_bn, locale),
    fromObservation: (row.from_observation ?? '').trim(),
    answer: draft.answers[row.code] ?? null,
    newly: added.has(row.code),
  }));
}

/**
 * Whether the patient walks without help, as three states.
 *
 * `notAsked` is the default and it is a different record from `no`. The contract says so in as
 * many words — *absent is not false* — and the reason is the one this whole checkpoint turns on:
 * a patient nobody asked and a patient who cannot walk unaided must not look the same in the
 * ledger, because somebody will read the second meaning out of the first.
 */
export const WALKS_ANSWERS = ['yes', 'no', 'notAsked'] as const;
export type WalksAnswer = (typeof WALKS_ANSWERS)[number];

/**
 * What the operator answered about one condition.
 *
 * Two states and no third, because the *unanswered* state is the absence of a key rather than a
 * value — see `unanswered`. A question with an explicit "not asked" option would be a question
 * operators learn to leave alone on a busy morning, and the event carries no such thing: it
 * carries the codes that apply, so a code's absence has to mean "asked, and no".
 */
export const CONDITION_ANSWERS = ['yes', 'no'] as const;
export type ConditionAnswer = (typeof CONDITION_ANSWERS)[number];

/** The findings as they are being taken. Nothing here is a body until `toAssessmentBody`. */
export interface AssessmentDraft {
  /** What was answered, by condition code. A missing key is a question nobody has answered yet. */
  answers: Readonly<Record<string, ConditionAnswer>>;
  walks: WalksAnswer;
  /** As typed. Parsed once, in `walkMinutesProblem` and `toAssessmentBody`. */
  walkMinutes: string;
  jointPain: string;
}

export function emptyAssessment(): AssessmentDraft {
  return { answers: {}, walks: 'notAsked', walkMinutes: '', jointPain: '' };
}

/** Answer one question. Pressing the answer already given takes it back to unanswered. */
export function answerCondition(
  draft: AssessmentDraft,
  code: string,
  answer: ConditionAnswer,
): AssessmentDraft {
  const answers = { ...draft.answers };
  if (answers[code] === answer) delete answers[code];
  else answers[code] = answer;
  return { ...draft, answers };
}

/**
 * The questions still open, in the catalogue's order.
 *
 * **Every question has to be answered before the findings can be recorded**, and the server now
 * says so too: `POST /v1/exercise/assessments` refuses a body whose `asked` does not cover every
 * live condition, naming the missing codes. This guard is kept in front of it — a refusal the
 * operator meets at the button, naming the question in front of them, is better than one that
 * arrives after a round trip in a sentence they have to decode.
 *
 * It exists because `contraindications: []` is read downstream as *none of these apply*, which is
 * the fact the whole permitted-exercise filter turns on. A screen that let an operator skip the
 * neuropathy question and then sent an empty list would produce an assessment indistinguishable
 * from a careful one, after which the filter computes the list as though the answer were no.
 */
export function unanswered(
  draft: AssessmentDraft,
  catalogue: readonly Contraindication[] | undefined,
): string[] {
  return inOrder(catalogue)
    .filter((row) => draft.answers[row.code] === undefined)
    .map((row) => row.code);
}

/**
 * The codes that were **put to the patient**, in the catalogue's own order.
 *
 * Sent as `asked` alongside `contraindications`, and it is the half that makes the other half
 * mean anything: without it, "we asked all five and none apply" and "we asked two and skipped the
 * neuropathy question" are byte-identical rows, and the second one filters nothing.
 *
 * Derived from the answers rather than from the catalogue, so it says what this station actually
 * put rather than what it happened to have downloaded. In practice the two are the same set —
 * `toAssessmentBody` refuses until every question is answered — but a stale catalogue makes them
 * differ, and in that case the server's refusal names exactly the gap.
 */
export function askedOf(
  draft: AssessmentDraft,
  catalogue: readonly Contraindication[] | undefined,
): string[] {
  return inOrder(catalogue)
    .filter((row) => draft.answers[row.code] !== undefined)
    .map((row) => row.code);
}

/** The codes answered "yes", in the catalogue's own order. */
export function conditionsOf(
  draft: AssessmentDraft,
  catalogue: readonly Contraindication[] | undefined,
): string[] {
  return inOrder(catalogue)
    .filter((row) => draft.answers[row.code] === 'yes')
    .map((row) => row.code);
}

export function answerWalks(draft: AssessmentDraft, answer: WalksAnswer): AssessmentDraft {
  return { ...draft, walks: answer };
}

export function typeWalkMinutes(draft: AssessmentDraft, text: string): AssessmentDraft {
  return { ...draft, walkMinutes: text };
}

export function typeJointPain(draft: AssessmentDraft, text: string): AssessmentDraft {
  return { ...draft, jointPain: text };
}

/**
 * The band the contract accepts for a day's walking.
 *
 * Mirrored from `api/openapi.yaml` so the operator is told before they press rather than by a
 * 422 afterwards. The server is still the one that enforces it — this is a courtesy, not a rule,
 * and a client that treated it as the rule would be a second implementation of the contract.
 */
export const WALK_MINUTES = Object.freeze({ min: 0, max: 600 });

export const NUMBER_PROBLEMS = ['notANumber', 'outOfRange'] as const;
export type NumberProblem = (typeof NUMBER_PROBLEMS)[number];

/**
 * What is wrong with the walking figure, or null.
 *
 * Blank is not a problem: the field is optional precisely because requiring it would produce
 * invented numbers, and an invented number is worse than no number in the one analysis that
 * reads it.
 */
export function walkMinutesProblem(draft: AssessmentDraft): NumberProblem | null {
  const typed = draft.walkMinutes.trim();
  if (typed === '') return null;
  if (!/^\d{1,4}$/.test(typed)) return 'notANumber';
  const value = Number(typed);
  if (value < WALK_MINUTES.min || value > WALK_MINUTES.max) return 'outOfRange';
  return null;
}

/** Exactly the body `POST /v1/exercise/assessments` takes. */
export interface AssessmentBody {
  event_id: string;
  patient_id: string;
  visit_id?: string;
  walks_unaided?: boolean;
  walk_minutes?: number;
  joint_pain?: string;
  /**
   * The conditions that were put to the patient. Required, never null.
   *
   * The server refuses a body whose `asked` does not cover every live condition, and refuses a
   * `contraindications` entry that is not in it (invariant 89).
   */
  asked: string[];
  /** Never omitted, never null. See below. */
  contraindications: string[];
  note?: string;
}

/**
 * The findings, ready to send, or null when they are not a request yet.
 *
 * Four things are worth naming.
 *
 * **`asked` and `contraindications` travel together.** What was put to the patient, and what of it
 * applies. Sending only the second made "we asked all five and none apply" and "we asked two and
 * skipped the neuropathy question" byte-identical rows, after which the filter computed the
 * permitted list as though the unasked question had been answered no. Every code in
 * `contraindications` is also in `asked` by construction here — the draft cannot hold an answer of
 * "yes" to a question it never rendered — which is invariant 89 kept on this side as well.
 *
 * **`contraindications` is always an array, including when it is empty.** "None apply" is the
 * fact the whole filter turns on, and an absent list is indistinguishable from a station that
 * never asked. The permitted set is computed from this, so an event that said "not asked" where
 * it meant "none apply" would be a lie in the ledger — and the ledger is the copy that survives.
 *
 * **`walks_unaided` is omitted when nobody asked**, rather than sent as false. Same argument, one
 * field along.
 *
 * **Every question must have been answered**, or this is null. The server says so too and names
 * what is missing; this guard is in front of it so the operator meets the refusal at the button,
 * beside the question, rather than after a round trip.
 *
 * The codes come from the catalogue and are sorted into its order, so the ledger holds the
 * conditions in the sequence the station asks them whichever order the operator tapped.
 */
export function toAssessmentBody(
  draft: AssessmentDraft,
  catalogue: readonly Contraindication[] | undefined,
  ids: { event: string; patient: string; visit?: string },
): AssessmentBody | null {
  if (ids.patient.trim() === '') return null;
  if (walkMinutesProblem(draft) !== null) return null;
  if ((catalogue ?? []).length === 0) return null;
  if (unanswered(draft, catalogue).length > 0) return null;

  const body: AssessmentBody = {
    event_id: ids.event,
    patient_id: ids.patient,
    asked: askedOf(draft, catalogue),
    contraindications: conditionsOf(draft, catalogue),
  };
  const visit = (ids.visit ?? '').trim();
  if (visit !== '') body.visit_id = visit;
  if (draft.walks !== 'notAsked') body.walks_unaided = draft.walks === 'yes';
  const minutes = draft.walkMinutes.trim();
  if (minutes !== '') body.walk_minutes = Number(minutes);
  const pain = draft.jointPain.trim();
  if (pain !== '') body.joint_pain = pain;
  return body;
}

/** What the assessment on record says, for the line above the list. */
export interface AssessmentReading {
  id: string;
  /** The condition codes, resolved to the catalogue's names where it is in hand. */
  conditions: { code: string; name: Wording }[];
  /**
   * How many questions were put to the patient when this assessment was taken.
   *
   * Drawn beside the findings because it is what makes "none apply" mean anything, and because it
   * is the number that explains a `NOT_ASKED` exclusion: an assessment that asked five questions
   * is complete-for-its-time even after a sixth condition joins the catalogue.
   */
  askedCount: number;
  walks: WalksAnswer;
  walkMinutes: number | null;
  jointPain: string;
  superseded: boolean;
  recordedAt: string;
}

/**
 * The findings the list was filtered against, in words.
 *
 * Drawn above the choices because a clinician who disagrees with an exclusion needs to see what
 * was answered before they can disagree with it. A condition whose name is not in the catalogue
 * this tablet holds is shown by its code rather than dropped — a finding the screen cannot name
 * is still a finding that filtered the list.
 */
export function assessmentReading(
  assessment: Assessment | null | undefined,
  catalogue: readonly Contraindication[] | undefined,
  locale: Locale,
): AssessmentReading | null {
  if (assessment === null || assessment === undefined) return null;
  const named = new Map((catalogue ?? []).map((row) => [row.code, row]));
  return {
    id: assessment.id,
    conditions: (assessment.contraindications ?? []).map((code) => {
      const row = named.get(code);
      return {
        code,
        name:
          row === undefined
            ? wordingOf(code, code, locale)
            : wordingOf(row.name_en, row.name_bn, locale),
      };
    }),
    askedCount: (assessment.asked ?? []).length,
    walks:
      assessment.walks_unaided === undefined ? 'notAsked' : assessment.walks_unaided ? 'yes' : 'no',
    walkMinutes: assessment.walk_minutes ?? null,
    jointPain: (assessment.joint_pain ?? '').trim(),
    superseded: assessment.status === 'SUPERSEDED',
    recordedAt: assessment.recorded_at,
  };
}

// --- step 2: what may be offered, and why it is not more ---

/** One exercise the server permitted, exactly as it arrived. */
export interface OfferRow {
  code: string;
  name: Wording;
  /** What the patient is told to do. The sentence that gets printed and handed over. */
  how: Wording;
  kind: Exercise['kind'];
  intensity: Exercise['intensity'];
  impact: Exercise['impact'];
  needsEquipment: boolean;
  canDoAtHome: boolean;
  approved: boolean;
}

/**
 * The permitted set, in the server's order, with nothing added and nothing removed.
 *
 * This function is where the checkpoint's acceptance criterion is either kept or lost, so it is
 * worth being blunt about what it does not do: it does not filter, it does not sort, it does not
 * hide, and it has no second argument that could ever make it return a different list. What the
 * server sent is what the operator sees. An empty array is a real answer — a patient for whom
 * nothing in the library is safe today — and is drawn as one rather than as a loading state.
 */
export function offerRows(options: Options | null | undefined, locale: Locale): OfferRow[] {
  if (options === null || options === undefined) return [];
  return options.exercises.map((exercise) => ({
    code: exercise.code,
    name: wordingOf(exercise.name_en, exercise.name_bn, locale),
    how: wordingOf(exercise.how_en, exercise.how_bn, locale),
    kind: exercise.kind,
    intensity: exercise.intensity,
    impact: exercise.impact,
    needsEquipment: exercise.needs_equipment,
    canDoAtHome: exercise.can_do_at_home,
    approved: exercise.approved,
  }));
}

/**
 * How many of the offered exercises nobody at this clinic has approved. Today, all of them.
 *
 * A counter rather than a `filter().length`, and that is not style: **nothing in this feature may
 * filter the array the server sent**, and a test asserts it by grepping for exactly that. A rule
 * with one sanctioned exception is a rule whose next exception is an argument rather than a
 * failure, so the exception is not taken.
 */
export function unapproved(options: Options | null | undefined): number {
  if (options === null || options === undefined) return 0;
  let count = 0;
  for (const exercise of options.exercises) {
    if (!exercise.approved) count += 1;
  }
  return count;
}

/**
 * Whether an exclusion is a finding about this patient or a question nobody has put yet.
 *
 * `APPLIES` is a condition the assessment recorded. `NOT_ASKED` is a condition added to the
 * catalogue *since* the assessment was taken: it excludes, because absence of evidence is not
 * evidence of absence, but nobody has claimed the patient has it.
 *
 * They must never be drawn with the same sentence. The operator's next act differs — one is a
 * conversation with a physician about an exclusion they may disagree with, the other is asking
 * the patient a question — and folding them together would tell an operator that a patient has a
 * condition nobody has asked them about, which is a claim nobody made.
 */
export const EXCLUSION_STATUSES = ['APPLIES', 'NOT_ASKED'] as const;
export type ExclusionStatus = (typeof EXCLUSION_STATUSES)[number];

/** One reason the list is shorter, by condition, with what kind of reason it is. */
export interface ExclusionReason {
  code: string;
  name: Wording;
  status: ExclusionStatus;
  excluded: number;
}

/** The exclusion, said out loud: how many, and on account of what. */
export interface ExclusionReading {
  /** How many exercises exist, so a short list can say it is short on purpose. */
  librarySize: number;
  /** How many are on the screen. */
  shown: number;
  /** The server's figure, copied. Never a subtraction and never a sum of the reasons. */
  excluded: number;
  /**
   * By condition, **in the server's order**. There is no exercise name here, because there is no
   * exercise here.
   *
   * Not re-sorted into findings-first: the payload's order is the catalogue's, which is the order
   * the operator was asked the questions in, and a screen that regrouped them would be a screen
   * whose reasons appear in a different order from the questions that produced them.
   */
  reasons: ExclusionReason[];
  /**
   * How many of the reasons are questions nobody has put yet.
   *
   * Drawn as its own line, because the whole set of them is answered by one act — take the
   * assessment again — and an operator who has to count greyer sentences to notice that is an
   * operator who will not notice.
   */
  unasked: number;
}

/**
 * Why the list is shorter than the library, in a form a clinician can argue with.
 *
 * Both halves earn their place. A specialist looking at eight options needs to know the library
 * holds twelve, or a list that is short on purpose is indistinguishable from a table that is
 * missing rows. And a clinician who disagrees with an exclusion needs a **sentence** to disagree
 * with, named by the condition — which is the only thing the server sent, and the only thing this
 * screen could name if it wanted to.
 *
 * `excluded` is copied off the payload and is deliberately not `librarySize - shown` and
 * deliberately not the sum of `reasons[].excluded`. The per-condition counts overlap, so a client
 * that added them would report a bigger number than the server does about the same patient.
 *
 * Each reason carries its `status`, and the screen must draw the two kinds with different
 * sentences: `APPLIES` is a finding about this patient, `NOT_ASKED` is a question added to the
 * catalogue since the assessment was taken. Rendering the second as though the patient had the
 * condition would put a diagnosis on the screen that nobody made.
 *
 * Null when there is nothing to say — no options in hand, or nothing excluded and no reason
 * given. A banner saying "0 not shown" is a banner people learn to skip past.
 */
export function exclusionReading(
  options: Options | null | undefined,
  locale: Locale,
): ExclusionReading | null {
  if (options === null || options === undefined) return null;
  if (options.excluded <= 0 && options.reasons.length === 0) return null;
  let unasked = 0;
  for (const reason of options.reasons) {
    if (reason.status === 'NOT_ASKED') unasked += 1;
  }
  return {
    librarySize: options.library_size,
    shown: options.exercises.length,
    excluded: options.excluded,
    reasons: options.reasons.map((reason) => ({
      code: reason.code,
      name: wordingOf(reason.name_en, reason.name_bn, locale),
      // Copied, never inferred. A screen that worked out for itself whether a condition had been
      // asked about would be a screen that could get it wrong in the direction of telling an
      // operator a patient has something nobody asked them about.
      status: reason.status,
      excluded: reason.excluded,
    })),
    unasked,
  };
}

// --- the targets, which are two numbers each ---

/**
 * The bands the contract accepts.
 *
 * Mirrored from `api/openapi.yaml` for the same reason `WALK_MINUTES` is: so that a number the
 * server will refuse is refused under the operator's thumb, while the patient is still in the
 * chair, rather than after they have chosen six exercises. The server enforces them; invariant 87
 * refuses a target that is not two numbers whatever a client sends.
 */
export const TIMES_PER_WEEK = Object.freeze({ min: 1, max: 14 });
export const MINUTES_PER_SESSION = Object.freeze({ min: 1, max: 240 });

/** One chosen exercise and its two numbers, as they are being typed. */
export interface TargetDraft {
  code: string;
  times: string;
  minutes: string;
}

/** The chosen targets. Order is not meaningful here; `toPlanBody` puts them in the list's order. */
export type Chosen = readonly TargetDraft[];

export function noTargets(): Chosen {
  return [];
}

/**
 * Choose an exercise, or take it back off the plan.
 *
 * Choosing does not fill in a number. There is no default of "three times a week for thirty
 * minutes" anywhere in this file, and there must not be: a target the operator did not choose is
 * a number §12.1's adherence analysis would read as a clinical decision.
 */
export function chooseExercise(chosen: Chosen, code: string): Chosen {
  const present = chosen.some((target) => target.code === code);
  if (present) return chosen.filter((target) => target.code !== code);
  return [...chosen, { code, times: '', minutes: '' }];
}

export function typeTimes(chosen: Chosen, code: string, text: string): Chosen {
  return chosen.map((target) => (target.code === code ? { ...target, times: text } : target));
}

export function typeMinutes(chosen: Chosen, code: string, text: string): Chosen {
  return chosen.map((target) => (target.code === code ? { ...target, minutes: text } : target));
}

export function targetFor(chosen: Chosen, code: string): TargetDraft | null {
  return chosen.find((target) => target.code === code) ?? null;
}

export function isChosen(chosen: Chosen, code: string): boolean {
  return chosen.some((target) => target.code === code);
}

/**
 * What is wrong with one target, in the order an operator can act on it.
 *
 * `times` and `minutes` are the empty ones — the field has not been filled in — and the two
 * `…Range` values are a number the contract will not take. They are kept apart because the
 * sentence differs: "say how many times a week" and "at most 14 times a week" are different
 * instructions, and one message covering both would be vague enough to cover neither.
 */
export const TARGET_PROBLEMS = ['times', 'timesRange', 'minutes', 'minutesRange'] as const;
export type TargetProblem = (typeof TARGET_PROBLEMS)[number];

export function targetProblem(target: TargetDraft): TargetProblem | null {
  const times = target.times.trim();
  if (times === '') return 'times';
  if (!/^\d{1,3}$/.test(times)) return 'timesRange';
  const perWeek = Number(times);
  if (perWeek < TIMES_PER_WEEK.min || perWeek > TIMES_PER_WEEK.max) return 'timesRange';

  const minutes = target.minutes.trim();
  if (minutes === '') return 'minutes';
  if (!/^\d{1,3}$/.test(minutes)) return 'minutesRange';
  const perSession = Number(minutes);
  if (perSession < MINUTES_PER_SESSION.min || perSession > MINUTES_PER_SESSION.max) {
    return 'minutesRange';
  }
  return null;
}

/** Every chosen target that is not yet a target, by code. */
export function targetProblems(chosen: Chosen): Record<string, TargetProblem> {
  const out: Record<string, TargetProblem> = {};
  for (const target of chosen) {
    const problem = targetProblem(target);
    if (problem !== null) out[target.code] = problem;
  }
  return out;
}

/** The codes that still need a number. Empty is the only state from which a plan may be sent. */
export function incomplete(chosen: Chosen): string[] {
  return chosen.filter((target) => targetProblem(target) !== null).map((target) => target.code);
}

/**
 * Any exercise chosen more than once.
 *
 * The server refuses a duplicate rather than keeping the first — two entries carry two different
 * weekly totals, and silently keeping one puts a number in §12.1's adherence data that nobody
 * chose. It cannot happen from this screen, where `chooseExercise` toggles a code that is either
 * present or absent, so this is a guard on the one thing `toPlanBody` would otherwise take on
 * trust from a caller. Empty in every state this screen can reach.
 */
export function duplicated(chosen: Chosen): string[] {
  const seen = new Set<string>();
  const twice = new Set<string>();
  for (const target of chosen) {
    if (seen.has(target.code)) twice.add(target.code);
    seen.add(target.code);
  }
  return [...twice];
}

/** One target on the wire. */
export interface PlanTarget {
  exercise_code: string;
  times_per_week: number;
  minutes_per_session: number;
  ordering: number;
}

/** Exactly the body `POST /v1/exercise/plans` takes. */
export interface PlanBody {
  event_id: string;
  patient_id: string;
  visit_id?: string;
  /** The findings the operator was looking at. From the options payload, never invented. */
  assessment_id: string;
  targets: PlanTarget[];
  note?: string;
}

/**
 * The plan, ready to issue, or null when it is not a request yet.
 *
 * Null in four cases, and each one is a refusal the operator must meet at the button rather than
 * in a 422:
 *
 *  - no options in hand, so there is no `assessment_id` to freeze the plan against;
 *  - nothing chosen at all;
 *  - **any chosen target missing either of its two numbers, or holding one the contract will not
 *    take.** This is §12.1's requirement rather than a form's preference — "walk more" is
 *    unanalysable, and the exercise–outcome correlation computes adherence from these two
 *    integers — so an incomplete target is made impossible to submit rather than left for the
 *    server to refuse;
 *  - a chosen code that is not in the list the server sent, or one chosen twice. Neither can
 *    happen from this screen — the only codes it can choose came out of `offerRows`, and
 *    `chooseExercise` toggles rather than appends — but both are refused here so that a future
 *    caller holding a stale or hand-built selection is stopped on this side too, and so that the
 *    one place a target could be smuggled in is a place with a test on it.
 *
 * None of the four is reachable from this screen, which is the point: a `targets` 422 arriving
 * here means something under the screen changed, and the server's sentence — which now names the
 * exercise — is drawn on the row it is about.
 *
 * `assessment_id` comes off the options payload. It is the whole staleness mechanism: the server
 * compares it with the current assessment and answers `EXERCISE_ASSESSMENT_SUPERSEDED` when a
 * colleague has recorded newer findings, and a client that sent anything else — the assessment it
 * recorded itself, say, or one it kept from a previous patient — would defeat the check.
 *
 * The targets go out in the **offered list's order** rather than the tapping order, so two
 * operators who chose the same three exercises hand the patient the same sheet in the same
 * sequence.
 */
export function toPlanBody(
  chosen: Chosen,
  options: Options | null | undefined,
  ids: { event: string; patient: string; visit?: string },
): PlanBody | null {
  if (options === null || options === undefined) return null;
  if (ids.patient.trim() === '') return null;
  if (chosen.length === 0) return null;
  if (incomplete(chosen).length > 0) return null;
  if (duplicated(chosen).length > 0) return null;

  const order = new Map(options.exercises.map((exercise, index) => [exercise.code, index]));
  for (const target of chosen) {
    if (!order.has(target.code)) return null;
  }

  const targets = [...chosen]
    .sort((a, b) => (order.get(a.code) ?? 0) - (order.get(b.code) ?? 0))
    .map((target, index) => ({
      exercise_code: target.code,
      times_per_week: Number(target.times.trim()),
      minutes_per_session: Number(target.minutes.trim()),
      ordering: index + 1,
    }));

  const body: PlanBody = {
    event_id: ids.event,
    patient_id: ids.patient,
    assessment_id: options.assessment_id,
    targets,
  };
  const visit = (ids.visit ?? '').trim();
  if (visit !== '') body.visit_id = visit;
  return body;
}

/**
 * The selection, carried across a fresh list.
 *
 * Called after a supersede or a last-moment refusal, once the options have been read again. A
 * target whose exercise the new list does not offer is dropped, and its code is reported so the
 * screen can say which choice went and why the operator should look again.
 *
 * Naming a dropped exercise is not the thing criterion 1 forbids, and the difference is worth
 * being exact about: this screen may name an exercise the **server gave it** and has now taken
 * back, because the operator already saw it and chose it. It may never name one it was never
 * given — and it could not, because those never reached the device.
 */
/**
 * The display names for a set of codes, read off the list they came from.
 *
 * Used for the choices a fresh list no longer offers. The names have to be resolved against the
 * **old** payload — the one the operator was looking at when they chose — because by the time the
 * screen draws the notice the new list is in hand and the dropped rows are not in it. Falling back
 * to the code is honest rather than tidy: a name the screen cannot find is still a choice that was
 * taken off the plan, and saying nothing about it would be worse than saying it awkwardly.
 */
export function namesOf(
  codes: readonly string[],
  options: Options | null | undefined,
  locale: Locale,
): string[] {
  const named = new Map((options?.exercises ?? []).map((exercise) => [exercise.code, exercise]));
  return codes.map((code) => {
    const row = named.get(code);
    if (row === undefined) return code;
    const wording = wordingOf(row.name_en, row.name_bn, locale);
    return wording.text === '' ? code : wording.text;
  });
}

export function keepOffered(
  chosen: Chosen,
  options: Options | null | undefined,
): { chosen: Chosen; dropped: string[] } {
  if (options === null || options === undefined) return { chosen: [], dropped: [] };
  const offered = new Set(options.exercises.map((exercise) => exercise.code));
  return {
    chosen: chosen.filter((target) => offered.has(target.code)),
    dropped: chosen.filter((target) => !offered.has(target.code)).map((target) => target.code),
  };
}

// --- step 3: the sheet the patient is handed ---

/** One line of the routine, as it is printed. */
export interface SheetRow {
  code: string;
  name: Wording;
  /**
   * The instruction, joined from the library by the server so a correction reaches a reprint.
   *
   * Required on the payload since the contract was corrected: criterion 3 depends on it — this is
   * the sheet the patient is handed — invariant 88 refuses a library row that reads in only one
   * language, and the plan item's foreign key to `core.exercise` stops the row it is joined from
   * being deleted. `wordingOf` still carries the one-language case, because a *future* library row
   * added past the invariant is the sort of thing an operator should be told about rather than
   * shown a blank line for.
   */
  how: Wording;
  timesPerWeek: number;
  minutesPerSession: number;
  /** The server's figure for this line. Nothing here multiplies the two above. */
  minutesPerWeek: number;
  needsEquipment: boolean;
  canDoAtHome: boolean;
  approved: boolean;
  note: string;
}

/** The routine as it stands, with the server's own totals. */
export interface PlanReading {
  id: string;
  /** Frozen at issue. The findings this plan was filtered against, whatever is true now. */
  assessmentId: string;
  superseded: boolean;
  /** The plan's total, computed once on the server. The number a follow-up compares against. */
  minutesPerWeek: number;
  rows: SheetRow[];
  /** How many lines of the sheet nobody at this clinic has approved. */
  unapproved: number;
  issuedAt: string;
  note: string;
}

/**
 * The plan, read for printing.
 *
 * Sorted by the ordering the server stored, then by code, so a reprint tomorrow reads in the same
 * sequence as the sheet the patient took home today. Every figure is copied; there is no
 * arithmetic in this function and there must not be, because `minutes_per_week` is derived once
 * on the server precisely so that a screen, a printed sheet and §12.1's extract cannot each round
 * it differently.
 */
export function planReading(plan: Plan | null | undefined, locale: Locale): PlanReading | null {
  if (plan === null || plan === undefined) return null;
  const items = [...plan.items];
  items.sort(
    (a, b) =>
      (a.ordering ?? 100) - (b.ordering ?? 100) || a.exercise_code.localeCompare(b.exercise_code),
  );
  const rows = items.map((item) => sheetRow(item, locale));
  return {
    id: plan.id,
    assessmentId: plan.assessment_id,
    superseded: plan.status === 'SUPERSEDED',
    minutesPerWeek: plan.minutes_per_week,
    rows,
    unapproved: rows.filter((row) => !row.approved).length,
    issuedAt: plan.issued_at,
    note: (plan.note ?? '').trim(),
  };
}

function sheetRow(item: PlanItem, locale: Locale): SheetRow {
  return {
    code: item.exercise_code,
    name: wordingOf(item.name_en, item.name_bn, locale),
    how: wordingOf(item.how_en, item.how_bn, locale),
    timesPerWeek: item.times_per_week,
    minutesPerSession: item.minutes_per_session,
    minutesPerWeek: item.minutes_per_week,
    needsEquipment: item.needs_equipment,
    canDoAtHome: item.can_do_at_home,
    approved: item.approved,
    note: (item.note ?? '').trim(),
  };
}
