import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type {
  AssessmentBody,
  Attempt,
  Catalogue,
  InstrumentResponse,
  Locale,
  NumbersBatch,
  Observation,
  Scoring,
  Trouble,
} from './state';

/**
 * Station 3's lifestyle calls, from the floor (CP58).
 *
 * # Six calls, and the one that is deliberately missing
 *
 * The catalogue and its version, the assessment, the recompute, the patient's previous
 * responses, and the batch that writes §3 step 3's four plain numbers. What is worth stating is
 * what is **not** here.
 *
 * **There is no call that computes a score on this side.** `scoreLifestyle` asks the server to
 * recompute from the record and returns what it wrote; nothing here assembles a number, and this
 * file must never grow a function that does. §12's cohorting reads the stored value, and a screen
 * with its own copy of the arithmetic is a second answer to the question the research asks.
 *
 * `scoreLifestyle` **writes** — a new `LIFESTYLE_RISK` derived observation when there is enough
 * to compute one — which is why it is called after a save and never on arrival at the screen. A
 * derived value appended every time somebody opens a tab is ledger noise nobody asked for.
 *
 * **There is no call that filters the catalogue.** `listInstruments` sends no query at all —
 * not `names_only`, and not a usable-only filter, because the contract has none and must not
 * acquire one. The three instruments this clinic may not run are the point of the response
 * (D-26): a screen that asked the server to leave them out would turn a recorded decision into
 * a silent gap, and a clinician who expected PHQ-9 would report the app as broken.
 *
 * # The catalogue is fetched once and worked from offline
 *
 * `GET /v1/assessments/instruments` returns every question and every option in one request,
 * which is why the station tablet asks for it at the start of a clinic session and then does
 * not ask again. A questionnaire that needed a round trip per item is a questionnaire that
 * stalls halfway through when the clinic's link drops for its usual few seconds (ADR-0004).
 *
 * # Every write carries its own event id, and the same id is the idempotency key
 *
 * A questionnaire sent twice over a bad connection must be one response in the ledger — one
 * actor, one moment — so the caller supplies the event id in the body, the same value goes on
 * `Idempotency-Key`, and a retry re-sends both unchanged. Answering *again*, on purpose, is a
 * new act with a new id: the server supersedes the previous response rather than editing it,
 * because an operator who ran the questionnaire twice because the patient corrected themselves
 * has produced two facts.
 */

/**
 * The whole catalogue, questions included, with the unusable instruments in it.
 *
 * Readable by anybody who records or reads values: a questionnaire is published literature with
 * no patient in it, and gating it behind the lifestyle write permission would leave a
 * nutritionist unable to see what station 3 asks.
 */
export async function listInstruments(): Promise<Catalogue> {
  const body = await unwrap(api.GET('/v1/assessments/instruments'));
  return { instruments: body.instruments, version: body.catalogue_version };
}

/**
 * The catalogue's version, and nothing else at all.
 *
 * The cheap re-check. A tablet holds the catalogue for a morning, and this is how it finds out
 * that a version was republished at a moment when nothing is half answered — rather than at
 * submit, with a 422 on an item code and a patient who has just been asked every question on a
 * form that no longer exists.
 *
 * `version_only` rather than `names_only`: the latter still carries five instruments' names,
 * purposes and licence notes, which is a lot to pay to learn one timestamp on a connection that
 * drops for seconds at a time.
 */
export async function readCatalogueVersion(): Promise<string> {
  const body = await unwrap(
    api.GET('/v1/assessments/instruments', { params: { query: { version_only: '1' } } }),
  );
  return body.catalogue_version;
}

/**
 * Where this patient stands, without writing anything.
 *
 * Called when the screen opens. It is the same `scoring` shape the writes return and its `score`
 * is always null by construction — it does not derive — so the figure still comes from the
 * patient's current `LIFESTYLE_RISK`. What it supplies is `assessed`, `missing` and `minimum`
 * from the first render, which is what an operator sitting down beside a patient actually needs:
 * not the number, but which of the four things they still have to ask about.
 */
export async function getLifestyleScoring(patientId: string): Promise<Scoring> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/lifestyle-scoring', { params: { path: { id: patientId } } }),
  );
  return body.scoring;
}

/** Every questionnaire this patient has answered, newest first, superseded ones included. */
export async function listPatientAssessments(patientId: string): Promise<InstrumentResponse[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/assessments', { params: { path: { id: patientId } } }),
  );
  return body.responses;
}

/**
 * One questionnaire, answered, and the composite if one could be computed.
 *
 * `scoring.score` is **allowed to be null** and the caller must handle it as an answer rather
 * than as a failure: below `scoring.minimum` assessed domains there is nothing honest to
 * compute, and the response itself is still recorded. The rest of `scoring` says which domains
 * were seen and which are wanted, so no client has to work that out for itself.
 *
 * Returned as the pair the server sends rather than flattened, so no caller can mistake a
 * missing score for a missing response.
 */
export async function recordAssessment(
  body: AssessmentBody,
): Promise<{ response: InstrumentResponse; scoring: Scoring }> {
  const written = await unwrap(
    api.POST('/v1/assessments', {
      params: { header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id } },
      body,
    }),
  );
  return { response: written.response, scoring: written.scoring };
}

/**
 * Recompute the composite from the record as it stands.
 *
 * Called after the four numbers save, because "type the four numbers, save" is the commonest
 * act at this station and it can take a patient from two assessed domains to four without a
 * questionnaire being answered at all. Before this endpoint existed the score stayed stale
 * exactly when the operator had just finished making it computable.
 *
 * It writes, so it takes its own event id: the same one goes on `Idempotency-Key`, and a retry
 * of the same save re-sends it unchanged rather than appending a second identical score.
 */
export async function scoreLifestyle(
  patientId: string,
  eventId: string,
  visitId?: string,
): Promise<Scoring> {
  const body: { patient_id: string; visit_id?: string } = { patient_id: patientId };
  if (visitId !== undefined && visitId.trim() !== '') body.visit_id = visitId.trim();
  const written = await unwrap(
    api.POST('/v1/assessments/score', {
      params: { header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': eventId } },
      body,
    }),
  );
  return written.scoring;
}

/**
 * §3 step 3's four plain numbers, through the write path every station value uses.
 *
 * Deliberately `/v1/observations/batch` rather than anything of this feature's own. A count of
 * cigarettes is an ordinary CP42 observation — it belongs in the same table, under the same
 * plausibility rules and the same correction cascade as a weight — and a station that invented
 * a second way to write a number would be a station whose values the corrections queue, the
 * timeline and the research extract could not see.
 *
 * The response carries the derived values in the order they were named, so `PACK_YEARS` comes
 * back computed by the server. It also carries any critical alerts the write raised, which the
 * screen must raise in the same instant rather than after another round trip.
 */
export async function recordLifestyleNumbers(body: NumbersBatch): Promise<{
  observations: Observation[];
  alerts: unknown;
  escalateVerbally: boolean;
}> {
  const written = await unwrap(
    api.POST('/v1/observations/batch', {
      params: { header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id } },
      body,
    }),
  );
  return {
    observations: written.observations,
    alerts: written.alerts,
    escalateVerbally: written.escalate_verbally === true,
  };
}

/**
 * What went wrong, in the three shapes it can arrive in.
 *
 * The same translation stations 3 and 4 use. The server's `code` is kept because it is the only
 * part of a refusal a client may branch on, and this station has one it must branch on:
 * `INSTRUMENT_NOT_LICENSED` is a 422 about the questionnaire, not about the operator.
 */
export function troubleOf(error: unknown, locale: Locale, attempt: Attempt): Trouble {
  if (error instanceof NetworkError) {
    return { kind: 'unreachable', attempt, status: 0, code: '', field: '', message: '' };
  }

  if (error instanceof ApiError) {
    if (error.status === 422) {
      const refusal = refusalOf(fieldMessages(error, locale));
      return {
        kind: 'refused',
        attempt,
        status: error.status,
        code: error.code,
        field: refusal.field,
        message: refusal.message === '' ? messageOf(error, locale) : refusal.message,
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
    };
  }

  // Something that is not an error this app throws. There is no code to carry and the screen
  // supplies the sentence.
  return { kind: 'failed', attempt, status: 0, code: '', field: '', message: '' };
}

/** The first field the server named, with its sentence. */
function refusalOf(fields: Record<string, string>): { field: string; message: string } {
  const [field, message] = Object.entries(fields)[0] ?? ['', ''];
  return { field, message };
}

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
