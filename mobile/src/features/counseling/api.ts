import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type {
  Attempt,
  CompleteRequest,
  CounselingChecklist,
  CounselingGate,
  CounselingSession,
  Locale,
  StartRequest,
  TickRequest,
  Trouble,
  UntickRequest,
} from './state';

/**
 * The counselling session calls, from the floor (CP56, §5.3).
 *
 * # A thin binding, and the one thing that is deliberately missing
 *
 * Every function below is one of the contract's endpoints, unwrapped into a value or a thrown
 * error like every other call this app makes. What is worth stating is what is **not** here.
 *
 * **There is no function that takes a list of item codes.** No `tickAll`, no `coverRest`, no
 * `PUT /ticks` with seven codes on it, and no parameter anywhere below typed as an array of
 * strings. Criterion 1 — every tick individually attributed — is only as strong as the
 * smallest act a client can send, and §5.4's method is the physician asking the patient "what
 * were you told about injection sites?" and then looking at who told them. A batch call would
 * attribute seven acts to one press and leave that question with no answer.
 *
 * The cost is one request per item. It is paid where it is cheapest: each tick is a small
 * request a phone can retry, and the offline queue (CP64–CP67) already has exactly this shape
 * to replay.
 *
 * **Completing ticks nothing.** `completeCounselingSession` sends its event id and nothing
 * else. A completion that covered the outstanding items would be the batch this file refuses
 * to have, wearing a different name, and it would let a session be closed by somebody who
 * counselled nobody.
 *
 * # Every write returns the whole session, and the caller must read it
 *
 * Ticking, un-ticking and completing all answer with `CounselingSession` rather than with the
 * row they wrote. That is the contract's decision and the right one: a tick changes the
 * outstanding list, which comes from the same database function CP57's gate reads, and a caller
 * that patched its own copy would be keeping a second, staler account of what is still missing
 * — the exact disagreement that puts a green tick on a phone while a gate refuses the patient.
 *
 * # Every write carries its own event id, and the same id is the idempotency key
 *
 * The clinic's link drops for seconds at a time (ADR-0004). A tick sent twice over a bad
 * connection must be one tick in the ledger — one actor, one moment — so the caller supplies
 * the event id in the body, the same value goes on the `Idempotency-Key` header, and a retry
 * re-sends both unchanged.
 *
 * # The gate is read here, and there is no function that overrides it (CP57)
 *
 * `getCounselingGate` is a read and the only gate call in this file. The valve —
 * `POST /v1/counseling/visits/{id}/gate/override` — is deliberately absent, and its absence is
 * structural rather than an oversight: it needs `counseling.gate.override`, which a station
 * operator does not hold, so a control wired to it would be a button that answers 403 with a
 * patient waiting. Adding the call here is what a screen would need before it could draw one,
 * and this is the sentence somebody would have to delete first.
 */

/**
 * Which checklists this visit calls for, and which of them somebody is already walking.
 *
 * The question a counsellor's phone has to ask before it can start anything. Without it the only
 * way to open a session would be to show every checklist in the clinic and have the counsellor
 * pick one — a clinical assignment made by the person least placed to make it, which is exactly
 * what §5.1's rules exist to avoid. Matched against the patient's live coded comorbidities, so
 * this app never has an opinion about which list a diagnosis calls for.
 *
 * `session_id` is why it is asked at all rather than only on an empty screen: it names the
 * session already open for this visit and checklist, and a phone that started a second one
 * would give two counsellors two half-ticked copies of the same list.
 *
 * It reads with **either** `counseling.tick` or `counseling.session.read`. A reviewer or a
 * physician's panel has to ask the same question to explain why a patient was held — what a
 * visit *should* have been walked through is the half a gate refusal turns on — and while this
 * needed the tick permission they got a 403 and an empty screen instead. Only the control that
 * opens a checklist is a counsellor's; the answer is not.
 *
 * There is no room catalogue fetch beside it any more. `room_station` on the item now says which
 * queue a room belongs to, which was the only thing the second request bought.
 */
export async function listCounselingChecklistsForVisit(
  visitId: string,
): Promise<CounselingChecklist[]> {
  const body = await unwrap(
    api.GET('/v1/counseling/visits/{visitId}/checklists', {
      params: { path: { visitId } },
    }),
  );
  return body.checklists;
}

/**
 * Every checklist this visit has been walked through, oldest first.
 *
 * The index, without items or ticks — but **with** the outstanding list, one row at a time, so
 * an empty one here means "nothing is missing" rather than "you were not told". It reads with
 * `counseling.session.read`.
 *
 * It is asked beside the checklist answer rather than instead of it, and no longer because that
 * one was a 403 for a reader. The checklist answer carries one row per checklist, so a list
 * walked, finished and opened again names only the later session; this one names every session
 * there is. `choicesOf` merges them, and the index only ever adds.
 *
 * A session already open is named by the checklist answer as well, and that is the one a
 * counsellor resumes from — two half-ticked copies of the same list is how two people each cover
 * half of it and each believe the other did the rest.
 */
export async function listCounselingSessionsForVisit(
  visitId: string,
): Promise<CounselingSession[]> {
  const body = await unwrap(
    api.GET('/v1/counseling/visits/{visitId}/sessions', {
      params: { path: { visitId } },
    }),
  );
  return body.sessions;
}

/**
 * One session: its frozen list, every tick made on it, and what is still outstanding.
 *
 * The withdrawn ticks come back too, because "ticked at 11:02 and taken back at 11:04" is an
 * answer to "what was covered" rather than the absence of one. `outstanding` is the server's,
 * from the gate's own function; nothing on this side recomputes it.
 */
export async function getCounselingSession(sessionId: string): Promise<CounselingSession> {
  const body = await unwrap(
    api.GET('/v1/counseling/sessions/{sessionId}', { params: { path: { sessionId } } }),
  );
  return body.session;
}

/**
 * Whether counselling is holding this patient, and exactly what is missing (CP57, §5.5).
 *
 * **This read is criterion 2.** The queue's own refusal names the missing items in a sentence,
 * in both languages, and that would satisfy the words — but a sentence cannot be tapped, and it
 * cannot say which of §5.2's three rooms an item belongs to. This answer says both, one item at
 * a time, and it says whether anybody has opened the checklist at all: `session_id` is absent
 * where nobody has, which is the difference between "go back and finish it" and "nobody has
 * started this" — two different rooms, and sending half the patients to the wrong one is the
 * failure the structured answer exists to prevent.
 *
 * **It is the only request the blocked screen makes.** Each missing item carries the room's own
 * words and the checklist's own title as well as its codes, and the list arrives ordered by
 * checklist, then by the room's place in the walk, then by item. So there is no room-catalogue
 * fetch beside this and no merge with the visit's checklist answer: a phone holding nothing but
 * a refusal can draw the whole panel, which is the situation the panel exists for.
 *
 * It reads with `counseling.session.read`, which the counsellor, the nutritionist, the exercise
 * and prescription-education officers, the clinical assistant, the junior doctor, the physician,
 * quality and the administrator all hold. It is a read and it writes nothing: `blocked` is the
 * server's answer to what the queue will do, and a client that worked that out for itself would
 * be a second, staler account of a rule enforced by a trigger on the queue table.
 */
export async function getCounselingGate(visitId: string): Promise<CounselingGate> {
  const body = await unwrap(
    api.GET('/v1/counseling/visits/{visitId}/gate', { params: { path: { visitId } } }),
  );
  return body.gate;
}

/**
 * Open a checklist for the patient in the room.
 *
 * Idempotent at the server: a phone that lost the reply and pressed start again lands back in
 * the session it already has, answered 200 instead of 201. Which checklist a patient gets is an
 * assignment rule keyed on a coded diagnosis (CP55) and never this app's guess, so the template
 * id is an input here rather than something chosen.
 */
export async function startCounselingSession(body: StartRequest): Promise<CounselingSession> {
  const answer = await unwrap(
    api.POST('/v1/counseling/sessions', { params: { header: guard(body.event_id) }, body }),
  );
  return answer.session;
}

/**
 * Cover one item.
 *
 * One item, one request, one event, one actor. **This function is acceptance criterion 1**, and
 * the argument list is where it is enforced: a session and a body carrying a single
 * `item_code`. The body comes from `toTick`, which cannot build one for two codes.
 *
 * Re-ticking an item somebody took back is allowed and lands on the same row, by whoever ticked
 * it this time. The un-tick stays in the ledger.
 *
 * An item that already has a **live** tick is refused with `409` and the code
 * `COUNSELING_ITEM_ALREADY_COVERED`, rather than re-attributed — unless the body carries the
 * event id the stored tick was written with, in which case the contract promises `200` with the
 * session. That is what a lost reply or an offline replay looks like, and it is why a retry must
 * re-send the id its first attempt was made with; `eventFor` is where that is decided, and it is
 * the only place in this feature that decides it.
 */
export async function tickCounselingItem(
  sessionId: string,
  body: TickRequest,
): Promise<CounselingSession> {
  const answer = await unwrap(
    api.POST('/v1/counseling/sessions/{sessionId}/ticks', {
      params: { path: { sessionId }, header: guard(body.event_id) },
      body,
    }),
  );
  return answer.session;
}

/**
 * Take a tick back, and say why.
 *
 * **This function is acceptance criterion 3.** The reason is required by the event's own
 * validation and by a database constraint; `toUntick` refuses an empty one before it becomes a
 * request. That is not the enforcement — it is the sentence a person reads while they can still
 * act on it.
 *
 * Never a deletion. The row stays, the reason is attached, and both halves show on the item —
 * because the interesting record is that somebody covered an item and then somebody decided
 * they had not.
 */
export async function untickCounselingItem(
  sessionId: string,
  body: UntickRequest,
): Promise<CounselingSession> {
  const answer = await unwrap(
    api.POST('/v1/counseling/sessions/{sessionId}/unticks', {
      params: { path: { sessionId }, header: guard(body.event_id) },
      body,
    }),
  );
  return answer.session;
}

/**
 * Finish the session.
 *
 * It ticks nothing and it refuses nothing. A session may be completed with items outstanding —
 * the patient left, the interpreter did not arrive — and the record then says exactly that.
 * Whether such a visit may reach the physician is CP57's gate to decide, and it can only decide
 * it because this call did not quietly prevent the situation being recorded.
 */
export async function completeCounselingSession(
  sessionId: string,
  body: CompleteRequest,
): Promise<CounselingSession> {
  const answer = await unwrap(
    api.POST('/v1/counseling/sessions/{sessionId}/complete', {
      params: { path: { sessionId }, header: guard(body.event_id) },
      body,
    }),
  );
  return answer.session;
}

/**
 * The forgery guard and the idempotency key, on every write.
 *
 * One helper rather than four copies: a write that forgot either is a refusal the counsellor
 * meets mid-sentence, in front of a patient. The key is the event id from the body, so a retry
 * over a stuttering link re-sends the same attempt and the ledger keeps one tick — which
 * matters here more than most places, because two tick rows would be two people claiming to
 * have covered the same item at two moments, one of whom does not exist.
 */
function guard(eventId: string): { 'X-Requested-With': 'DTHCMS'; 'Idempotency-Key': string } {
  return { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': eventId };
}

// --- what went wrong, in the three ways it can ---

/**
 * The fields the server can name on a refusal, in the order a counsellor can act on them.
 *
 * `item_code` first because it is the one that means something structural — the phone is
 * holding a list from before a republish — and a refusal naming two fields should be reported
 * by the one that changes what the person does next, rather than by whichever key serialised
 * first.
 */
const FIELD_ORDER = ['item_code', 'reason', 'note', 'template_id', 'visit_id', 'patient_id'];

function refusalOf(named: Record<string, string>): { field: string; message: string } {
  for (const field of FIELD_ORDER) {
    const message = named[field];
    if (message !== undefined) return { field, message };
  }
  // A field this build has never heard of is still shown, with its name, so a support call can
  // quote it — sorted, so two operators do not read different sentences.
  for (const [field, message] of Object.entries(named).sort((a, b) => a[0].localeCompare(b[0]))) {
    return { field, message };
  }
  return { field: '', message: '' };
}

/**
 * What went wrong, in the three shapes this station knows how to say.
 *
 * A refusal is shown in the server's own words, because the rules behind it are the database's
 * and a client that paraphrased them would be inventing a second, staler account of a clinical
 * rule it does not own. What this app adds is the status, the act it was refusing, and the
 * server's **code** — carried through untouched, because it is the only part of an error a
 * client may branch on. `adviceFor` reads the status to say whether the answer is to reload, to
 * press again or to stop pressing; `alreadyCovered` reads the code to tell a colleague having
 * got there first from a session somebody closed, which arrive with the same 409.
 *
 * Nothing here logs. A counselling refusal names an item code and sometimes a counsellor's own
 * words about a patient, and neither belongs in a log line.
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
    // 403, 404 and 409 are refusals too, and the status is what tells those three apart: a hat
    // that does not tick, a session that is gone, and a conflict — which the code then splits
    // again into the four things a conflict can be.
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

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
