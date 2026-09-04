import { MONOFILAMENT_SITES, SIDES, type Side } from './form';

/**
 * What this patient's record asks the examiner to look at today (CP51 criterion 4).
 *
 * # Why prompts and not a longer form
 *
 * Criterion 1 gives the whole examination two minutes, so the screen cannot ask everything
 * of everybody. What it can do is ask the *right* extra thing of the few patients whose
 * record already says it matters: the foot that has ulcerated once is the foot that
 * ulcerates again, and the eyes nobody has looked at are the eyes that go blind. Those are
 * not judgements the examiner should have to make by reading a history at the same time as
 * holding a monofilament.
 *
 * # Why every rule is its own exported function
 *
 * A prompt nobody can test is a prompt that quietly stops firing, and a prompt that stops
 * firing fails silently — no error, no missing field, just a patient who was not asked. So
 * each rule is a named function over the record, `PROMPT_RULES` is the list the screen runs,
 * and the test file names each rule and its counter-example. Adding a rule is one entry in
 * that list; forgetting to test it is then visible rather than invisible.
 *
 * # Why these read the record and not the form
 *
 * A prompt is what the examiner reads *before* touching the patient — the history decides
 * it, and today's findings do not. A rule that watched the form would appear and disappear
 * as the examiner tapped, which is exactly when they are not reading.
 */

/** One observation as the patient's history returns it. */
export interface HistoryRow {
  code: string;
  value_code?: string | null;
  value_bool?: boolean | null;
  value_json?: unknown;
  effective_at?: string;
  status?: string;
}

export type PromptId =
  'ulcer_history' | 'sensation_lost' | 'retinopathy_severe' | 'retinopathy_due';

export interface Prompt {
  /** Which rule fired. Stable, so a report can count how often each one is acted on. */
  id: PromptId;
  /** The key of the sentence to show, under the `examination` message namespace. */
  messageKey: string;
  /**
   * The findings this prompt is about. The screen uses them to send the examiner straight
   * to the fields; a prompt that only said "check the feet" would be a prompt they read and
   * then have to go looking.
   */
  fields: readonly string[];
  /** Set where the history is one-sided, so the sentence can name the foot or the eye. */
  side?: Side;
}

/**
 * A correction is not a finding.
 *
 * A grade somebody typed wrongly and fixed an hour later would otherwise prompt for an ulcer
 * at every visit for the rest of the patient's life.
 */
function isActive(row: HistoryRow): boolean {
  return row.status === undefined || row.status === 'ACTIVE';
}

/** The current value of a code: the rows arrive newest first, so the first sighting wins. */
function latest(rows: readonly HistoryRow[], code: string): HistoryRow | null {
  return rows.find((row) => row.code === code && isActive(row)) ?? null;
}

/**
 * A foot that has ulcerated, or lost part of itself, at any point on record.
 *
 * Every visit, for ever, and deliberately: IWGDF puts a previously ulcerated foot in its
 * highest risk category whatever the foot looks like today, because a well-healed foot
 * examines normally right up until it ulcerates again. Reading only the *current* grade
 * would silence this rule the moment the ulcer healed — which is the moment it starts
 * mattering most.
 *
 * Both feet are prompted whichever one the history is about. The contralateral foot of a
 * patient who has ulcerated once is itself a high-risk foot, and an examiner who documents
 * only the interesting one leaves the record unable to say the other was ever looked at.
 */
export function priorUlcerOrAmputation(rows: readonly HistoryRow[]): readonly Prompt[] {
  const found = rows.some((row) => {
    if (!isActive(row)) return false;
    const value = row.value_code ?? '';
    if (value === '') return false;
    for (const side of SIDES) {
      if (row.code === `FOOT_ULCER_${side}` && value !== 'grade_0') return true;
      if (row.code === `FOOT_DEFORMITY_${side}` && value === 'amputation') return true;
    }
    return false;
  });
  if (!found) return [];
  return [
    {
      id: 'ulcer_history',
      messageKey: 'prompt.ulcerHistory',
      fields: SIDES.map((side) => `FOOT_ULCER_${side}`),
    },
  ];
}

/**
 * Two sites of ten is the line every published protocol draws.
 *
 * One site missed is within the noise of a hurried examination; two is loss of protective
 * sensation, and the same threshold the server derives the risk category from. Mirrored
 * rather than imported because the server's copy is the authoritative one — this copy exists
 * only to decide what to ask, and a disagreement between them costs a prompt, not a record.
 */
export const LOST_PROTECTIVE_SENSATION = 2;

/** The sites of a stored monofilament payload, or null when it does not hold any. */
function feltSites(value: unknown): Record<string, boolean> | null {
  if (typeof value !== 'object' || value === null) return null;
  const payload = value as Record<string, unknown>;
  // A foot that was not tested is not a foot with sensation: the honest answer is that
  // nobody knows, and "nobody knows" is not evidence of loss.
  if (payload.not_tested === true) return null;
  const felt = payload.felt;
  if (typeof felt !== 'object' || felt === null) return null;
  const out: Record<string, boolean> = {};
  for (const [site, answer] of Object.entries(felt as Record<string, unknown>)) {
    if (typeof answer === 'boolean') out[site] = answer;
  }
  return out;
}

/**
 * A foot that had already lost protective sensation when it was last tested.
 *
 * The most recent test on that foot, not any test ever: unlike an ulcer, this is a finding
 * that is re-measured at every visit and re-stratified from what the filament says today.
 * What it earns is a prompt to test the foot again, which is the only thing that could
 * change the answer.
 */
export function priorLossOfProtectiveSensation(rows: readonly HistoryRow[]): readonly Prompt[] {
  const out: Prompt[] = [];
  for (const side of SIDES) {
    const row = latest(rows, `MONOFILAMENT_${side}`);
    if (row === null) continue;
    const felt = feltSites(row.value_json);
    if (felt === null) continue;
    const missed = MONOFILAMENT_SITES.filter((site) => felt[site] === false).length;
    if (missed < LOST_PROTECTIVE_SENSATION) continue;
    out.push({
      id: 'sensation_lost',
      messageKey: 'prompt.sensationLost',
      fields: [`MONOFILAMENT_${side}`],
      side,
    });
  }
  return out;
}

/** The grades at which an eye is at risk of losing sight before the next annual screen. */
const SIGHT_THREATENING = ['severe_npdr', 'pdr'];

/**
 * An eye that has already been graded severe or proliferative.
 *
 * Any such grade on record, not merely the current one, for the same reason as the ulcer:
 * treated proliferative retinopathy grades back down, and a rule that read only the latest
 * value would stop asking about the eye that was lasered.
 */
export function priorSightThreateningRetinopathy(rows: readonly HistoryRow[]): readonly Prompt[] {
  const out: Prompt[] = [];
  for (const side of SIDES) {
    const found = rows.some(
      (row) =>
        isActive(row) &&
        row.code === `RETINOPATHY_${side}` &&
        SIGHT_THREATENING.includes(row.value_code ?? ''),
    );
    if (!found) continue;
    out.push({
      id: 'retinopathy_severe',
      messageKey: 'prompt.retinopathySevere',
      fields: [`RETINOPATHY_${side}`],
      side,
    });
  }
  return out;
}

/** The screening statuses that mean nobody has looked at these eyes recently enough. */
const SCREENING_OWED = ['never', 'due'];

/**
 * Eyes the record says are owed a look.
 *
 * The **current** status, because this one is a standing state rather than a historical
 * event: a patient screened last month is not due whatever last year's status said.
 *
 * A patient with no screening status at all is not prompted. Absence is not the same
 * statement as "never" — the record has not been asked yet, and a first-visit patient would
 * otherwise arrive at station 5 under a list of prompts for every screening the clinic does,
 * which is how a prompt turns into wallpaper.
 */
export function retinopathyScreeningDue(rows: readonly HistoryRow[]): readonly Prompt[] {
  const row = latest(rows, 'RETINOPATHY_SCREEN');
  if (row === null) return [];
  if (!SCREENING_OWED.includes(row.value_code ?? '')) return [];
  return [
    {
      id: 'retinopathy_due',
      messageKey: 'prompt.retinopathyDue',
      fields: ['RETINOPATHY_SCREEN'],
    },
  ];
}

/**
 * Every rule, in the order the prompts are read.
 *
 * The order is what a clinician would look at first if only one thing got looked at: the
 * foot that has already broken down, then the foot that cannot feel, then the eye that is
 * already losing sight, then the eye nobody has seen. Alphabetical or definition order would
 * put whichever rule was written last at the top of somebody's screen.
 */
export const PROMPT_RULES: readonly ((rows: readonly HistoryRow[]) => readonly Prompt[])[] =
  Object.freeze([
    priorUlcerOrAmputation,
    priorLossOfProtectiveSensation,
    priorSightThreateningRetinopathy,
    retinopathyScreeningDue,
  ]);

/** Everything this patient's record asks for, in that order. */
export function promptsFor(rows: readonly HistoryRow[]): Prompt[] {
  return PROMPT_RULES.flatMap((rule) => [...rule(rows)]);
}
