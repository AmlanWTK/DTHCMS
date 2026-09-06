import { ApiError, NetworkError, fieldCodes, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type {
  Assessment,
  AssessmentBody,
  Attempt,
  Contraindication,
  Locale,
  Options,
  Plan,
  PlanBody,
  Trouble,
} from './state';

/**
 * Station 8's five calls, from the floor (CP60).
 *
 * # The call that does not exist, and must never be added
 *
 * **There is no call here that returns the exercise library.** There is no route to call: the
 * contract has `GET /v1/exercise/contraindications` for the *conditions* — which contains no
 * exercises — and `GET /v1/patients/{id}/exercise/options`, which applies the filter on the
 * server. A route that returned the whole library would be the thing acceptance criterion 1
 * forbids whatever a screen did with it afterwards, and a function here that fetched one would
 * put the excluded rows on the device where a bug, or a "show all", is the only thing between a
 * patient with an insensate foot and a jumping routine.
 *
 * So `readOptions` is the only way this application ever learns that an exercise exists, and a
 * test greps this feature for any sign of a second one.
 *
 * # `recordAssessment` returns the list, and that is why it is called rather than a refetch
 *
 * `POST /v1/exercise/assessments` answers with `{assessment, options}` together. The alternative
 * — record, then fetch — leaves a window in which the station holds findings and no list, and the
 * natural thing for a client to do in that window is show a library it already has. Returning
 * both makes the filtered list the only list the screen ever holds, so this function returns the
 * pair unflattened and the screen uses the options that came with the assessment.
 *
 * # Every write carries its own event id, and the same id is the idempotency key
 *
 * An assessment sent twice over a bad connection must be one event in the ledger — one actor, one
 * moment — so the caller supplies the event id in the body, the same value goes on
 * `Idempotency-Key`, and a retry re-sends both unchanged. Asking the questions *again*, on
 * purpose, is a new act with a new id: the server supersedes the previous assessment rather than
 * editing it, because a patient whose foot ulcer healed between visits has a new answer, not a
 * corrected one.
 */

/**
 * The conditions, with the question the station actually asks.
 *
 * Unfiltered, and readable by anybody who may read a value, because it contains no exercises and
 * because a physician's view renders a recorded contraindication by its name. No query parameter
 * is sent and there is none to send: the catalogue is five rows and the station asks all five.
 */
export async function listContraindications(): Promise<Contraindication[]> {
  const body = await unwrap(api.GET('/v1/exercise/contraindications'));
  return body.contraindications;
}

/**
 * What this patient may be offered, and why it is not more.
 *
 * Throws `ApiError` with `EXERCISE_NO_ASSESSMENT` (409) when nobody has answered the questions.
 * That is not a failure and the caller must not draw it as one — it is the station being told to
 * ask first. `state.noAssessment` is how a caller recognises it, and `state.stepFor` is where the
 * routing decision lives.
 *
 * The tempting alternative to that 409 would be the whole library on the reasoning that nothing
 * is *known* to be contraindicated, and it is wrong in the one way that matters here: "no
 * contraindications recorded" and "no contraindications" are different facts.
 */
export async function readOptions(patientId: string): Promise<Options> {
  return unwrap(
    api.GET('/v1/patients/{id}/exercise/options', { params: { path: { id: patientId } } }),
  );
}

/**
 * The findings and the plan as they stand, both allowed to be null.
 *
 * Read on arrival. A patient who has not been to station 8 has no findings and no plan, and that
 * is an ordinary first visit rather than a broken route — which is why this endpoint answers 200
 * with two nulls rather than 404.
 */
export async function readExerciseRecord(
  patientId: string,
): Promise<{ assessment: Assessment | null; plan: Plan | null }> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/exercise', { params: { path: { id: patientId } } }),
  );
  return { assessment: body.assessment ?? null, plan: body.plan ?? null };
}

/**
 * The questions, answered — and the options they permit, on the same response.
 *
 * The body carries **both** `asked` and `contraindications`: what was put to the patient, and what
 * of it applies. The server refuses a body whose `asked` does not cover every live condition and
 * names the missing codes in `fields.asked` — which from this screen means only one thing, that a
 * condition was added to the catalogue after this tablet fetched it. `state.questionsMissing`
 * recognises that case, and the screen answers it by reading the catalogue again rather than by
 * blaming the operator.
 *
 * Returned as the pair the server sends rather than flattened, so no caller can mistake the list
 * for something it assembled and no caller is tempted to fetch one separately.
 */
export async function recordAssessment(
  body: AssessmentBody,
): Promise<{ assessment: Assessment; options: Options }> {
  const written = await unwrap(
    api.POST('/v1/exercise/assessments', {
      params: { header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id } },
      body,
    }),
  );
  return { assessment: written.assessment, options: written.options };
}

/**
 * The routine, issued.
 *
 * Refused with `EXERCISE_ASSESSMENT_SUPERSEDED` (409) when a colleague recorded newer findings
 * while the operator was choosing, and with `EXERCISE_CONTRAINDICATED` (422) if a target is
 * outside the permitted set — which should be unreachable from this screen, because the only list
 * it holds is the one the server computed. The 422 now names the exercise, the condition and the
 * mapping table's own reason, with the codes in `fields.exercise_code` and
 * `fields.contraindication_code`, so the sentence goes on the row rather than above the list.
 *
 * An exercise retired between the list being drawn and the plan being sent is
 * `EXERCISE_RETIRED` — a **409**, because it says the list moved rather than that the request was
 * wrong, which is exactly the distinction a client needs to decide whether to refetch. All three
 * are answered the same way: read the options again. `state.reviewFor` is where that lives.
 *
 * Every other refusal here is a 422 about the request — an exercise not in the library at all, a
 * target that is not two numbers, one exercise twice — and each names the offending code in
 * `fields.exercise_code`, so the sentence goes on the row rather than in a banner.
 */
export async function issuePlan(body: PlanBody): Promise<Plan> {
  const written = await unwrap(
    api.POST('/v1/exercise/plans', {
      params: { header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id } },
      body,
    }),
  );
  return written.plan;
}

/**
 * What went wrong, in the three shapes it can arrive in.
 *
 * The same translation stations 3, 4, 5 and 7 use. The server's `code` is kept because it is the
 * only part of a refusal a client may branch on, and this station branches on three of them —
 * `EXERCISE_NO_ASSESSMENT` is a step rather than a failure, and the other two send the operator
 * back to a freshly read list.
 */
export function troubleOf(error: unknown, locale: Locale, attempt: Attempt): Trouble {
  const nothing = { exercise: '', condition: '', missing: [] };

  if (error instanceof NetworkError) {
    return {
      kind: 'unreachable',
      attempt,
      status: 0,
      code: '',
      field: '',
      message: '',
      ...nothing,
    };
  }

  if (error instanceof ApiError) {
    /*
     * The code-carrying fields are read whatever the status is, because they arrive on a 409 as
     * well as on a 422: `EXERCISE_RETIRED` names the exercise it is about and carries no sentence
     * under `targets` at all. Reading them only on validation failures was a bug waiting for that
     * refusal.
     */
    const named = fieldMessages(error, locale);
    const codes = {
      exercise: (named[FIELD_EXERCISE] ?? '').trim(),
      condition: (named[FIELD_CONDITION] ?? '').trim(),
      missing: fieldCodes(error, FIELD_MISSING),
    };

    if (error.status === 422) {
      const refusal = refusalOf(named);
      return {
        kind: 'refused',
        attempt,
        status: error.status,
        code: error.code,
        field: refusal.field,
        // A 422 that named no field at all is still a refusal, and the server still wrote a
        // sentence for it. Showing nothing would be the one outcome worse than either.
        message: refusal.message === '' ? messageOf(error, locale) : refusal.message,
        ...codes,
      };
    }
    return {
      kind:
        error.status === 403 || error.status === 404 || error.status === 409 ? 'refused' : 'failed',
      attempt,
      status: error.status,
      code: error.code,
      field: '',
      message: messageOf(error, locale),
      ...codes,
    };
  }

  // Something that is not an error this app throws. There is no code to carry and the screen
  // supplies the sentence.
  return { kind: 'failed', attempt, status: 0, code: '', field: '', message: '', ...nothing };
}

/**
 * The three `fields` entries that carry **codes** rather than a sentence.
 *
 * Every target refusal names the exercise it is about, and the incomplete-assessment refusal
 * names the conditions nobody asked about; the prose for each is under `targets` or `asked`. Go
 * marshals a map with its keys sorted, so `contraindication_code` arrives before `targets` — and
 * a client that took "the first field the server named" would draw `SEVERE_NEUROPATHY` at an
 * operator as though that were the explanation. Hence both this list and `FIELD_ORDER` below.
 */
const FIELD_EXERCISE = 'exercise_code';
const FIELD_CONDITION = 'contraindication_code';
const FIELD_MISSING = 'missing_conditions';

/**
 * The fields the server can name on a 422, in the order an operator can act on them.
 *
 * `targets` first because it is the one that carries the refusal an operator reads. `asked` next
 * because it is the only other one this screen branches on — a catalogue this tablet has not
 * caught up with. The rest are the body's own identifiers and are a client bug rather than
 * something a person at a tablet can fix; they are here so the sentence is still shown.
 */
const FIELD_ORDER = [
  'targets',
  'asked',
  'contraindications',
  'assessment_id',
  'patient_id',
  'visit_id',
  'event_id',
];

/**
 * Which field a refusal is about, and what the server said about it.
 *
 * Known fields in the order above; anything else by name, sorted, so two operators meeting the
 * same refusal read the same sentence rather than whichever key serialised first. The two
 * code-carrying fields are never chosen, because a code is not a sentence.
 */
function refusalOf(named: Record<string, string>): { field: string; message: string } {
  for (const field of FIELD_ORDER) {
    const message = named[field];
    if (message !== undefined) return { field, message };
  }
  for (const [field, message] of Object.entries(named).sort((a, b) => a[0].localeCompare(b[0]))) {
    if (field === FIELD_EXERCISE || field === FIELD_CONDITION || field === FIELD_MISSING) continue;
    return { field, message };
  }
  return { field: '', message: '' };
}

/**
 * The server's own sentence, in the reader's language.
 *
 * Both languages are on every refusal this API writes — `errs.New` takes the pair — so the
 * operator reads the refusal in the language they are working in rather than in whichever one the
 * handler happened to list first.
 */
function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
