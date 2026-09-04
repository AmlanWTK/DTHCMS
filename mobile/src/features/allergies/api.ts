import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

import type {
  AllergyAssertionRequest,
  AllergyChange,
  AllergyReaction,
  AllergyRecording,
  AllergyState,
  AllergyWithdrawal,
  Locale,
} from './state';

/**
 * The seven allergy calls, from station 4 (CP54).
 *
 * # A thin binding, and the one thing that is deliberately missing
 *
 * Every function below is one of the contract's endpoints, unwrapped into a value or a thrown
 * error like every other call this app makes. What is worth stating is what is **not** here.
 *
 * **There is no call that clears the gate.** The gate is a trigger on the queue table, and the
 * only things that satisfy it are an allergy recorded, "no known allergies" asserted, or
 * "unable to assess" with its reason — three writes, three endpoints, three real answers. There
 * is no fourth function, no `skip`, no `proceed`, and no parameter on any of these that means
 * "anyway". A client-side helper would be the easiest place in the whole system to grow one,
 * and the plan's own risk note says what happens next: within a month the override is the
 * normal path, and the checkpoint is a habit people route around rather than a checkpoint.
 *
 * # Every write returns the whole state, and the caller must read it
 *
 * Recording, asserting and both withdrawals all answer with `AllergyState` rather than with
 * the row they wrote. That is the contract's decision and the right one: withdrawing the last
 * allergy can drop the patient back to whatever assertion stands behind it, or to nothing at
 * all, which **re-closes the gate**. A caller that patched its own list would be guessing at
 * the answer the database already gave it.
 *
 * # Every write carries its own event id, and the same id is the idempotency key
 *
 * The clinic's link drops for seconds at a time (ADR-0004). An assertion sent twice over a bad
 * connection must be one assertion in the ledger — with one actor and one moment — so the
 * caller supplies the event id in the body, the same value goes on the `Idempotency-Key`
 * header, and a retry re-sends both unchanged.
 */

/**
 * The reaction vocabulary. Fetched once, and short on purpose.
 *
 * A list nobody can hold in their head is one people pick the first item from, and this
 * question gets asked in seconds while a queue waits. `is_emergency` comes back on every row
 * because it is a property of the reaction rather than of the severity somebody ticked beside
 * it — anaphylaxis is an emergency whatever was chosen from the list.
 */
export async function listReactions(): Promise<AllergyReaction[]> {
  const body = await unwrap(api.GET('/v1/allergies/reactions'));
  return body.reactions;
}

/**
 * The patient's allergy status, and what is recorded.
 *
 * The status is answered as well as the list, because an empty list and "nobody has asked" are
 * opposite facts and a screen that drew both as blank would be lying about one of them.
 */
export async function getAllergyState(patientId: string): Promise<AllergyState> {
  return unwrap(api.GET('/v1/patients/{id}/allergies', { params: { path: { id: patientId } } }));
}

/**
 * Everything ever said about this patient's allergies, newest first, withdrawn entries
 * included — which is the reason the endpoint exists.
 */
export async function listAllergyChanges(patientId: string): Promise<AllergyChange[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/allergies/history', { params: { path: { id: patientId } } }),
  );
  return body.changes;
}

/**
 * Record one allergy.
 *
 * The first of the three answers, and the one this feature is built to make cheap: if it cost
 * twenty presses while "no known allergies" cost one, the incentive would point at the claim
 * rather than the finding, and reflexive NKA is the risk the plan names.
 *
 * This does not withdraw an earlier assertion and must not be made to. Somebody asked in March
 * and was told there were none; somebody found one in June. Both are true about their own
 * moment, and the server decides which one the status is.
 */
export async function recordAllergy(
  patientId: string,
  body: AllergyRecording,
): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/patients/{id}/allergies', {
      params: { path: { id: patientId }, header: guard(body.event_id) },
      body,
    }),
  );
}

/**
 * State, in your own name, what the allergy answer is.
 *
 * The other two answers, and between them the whole of acceptance criterion 2: there is no
 * column in this system that means "no allergies" by being blank, so the only way to say it is
 * a positive act with an actor behind it.
 *
 * The body comes from `toAssertion`, which builds one for exactly two kinds and refuses an
 * `UNABLE_TO_ASSESS` with no reason. This function takes the finished body rather than a kind
 * and a reason precisely so there is no second place those rules could be spelled differently.
 */
export async function assertAllergyStatus(
  patientId: string,
  body: AllergyAssertionRequest,
): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/patients/{id}/allergies/assert', {
      params: { path: { id: patientId }, header: guard(body.event_id) },
      body,
    }),
  );
}

/**
 * Take back an allergy that should not have been recorded.
 *
 * Never a deletion. The row stays, the reason is attached, and both halves show in the change
 * history — because the next clinician needs to know a colleague once believed it.
 *
 * The response carries the resulting status because withdrawing the last recorded allergy can
 * re-close the gate, and the caller must not be left to guess.
 */
export async function withdrawAllergy(
  allergyId: string,
  body: AllergyWithdrawal,
): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/allergies/{allergyId}/withdraw', {
      params: { path: { allergyId }, header: guard(body.event_id) },
      body,
    }),
  );
}

/**
 * Take back a "no known allergies" or an "unable to assess".
 *
 * The one that matters is the first: an officer who tapped it on the wrong patient has put a
 * claim into a clinical record that a prescriber will rely on, so the way back has to exist, be
 * attributed, and leave a trace. Withdrawing the standing assertion re-closes the gate unless
 * allergies are recorded, and the response says what the status now is.
 */
export async function withdrawAllergyAssertion(
  assertionId: string,
  body: AllergyWithdrawal,
): Promise<AllergyState> {
  return unwrap(
    api.POST('/v1/allergies/assertions/{assertionId}/withdraw', {
      params: { path: { assertionId }, header: guard(body.event_id) },
      body,
    }),
  );
}

/**
 * The forgery guard and the idempotency key, on every write.
 *
 * One helper rather than four copies: a write that forgot either is a refusal the officer meets
 * mid-sentence. The key is the event id from the body, so a retry over a stuttering link
 * re-sends the same attempt and the ledger keeps one event — which for an assertion matters
 * more than anywhere else in this app, because two rows would be two people claiming the same
 * thing at two moments, one of whom does not exist.
 */
function guard(eventId: string): { 'X-Requested-With': 'DTHCMS'; 'Idempotency-Key': string } {
  return { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': eventId };
}

// --- what went wrong, in the three ways it can ---

/**
 * A refusal, an unreachable server, and a server that answered with something else.
 *
 * The same three shapes station 4's history uses, and deliberately a separate copy: the fields
 * an allergy refusal can name are this step's, and a shared helper would need the field list
 * passed in, which is one more argument for a caller to get wrong. Worth folding together the
 * day a third caller needs it, and not before.
 */
export type Trouble =
  | { kind: 'refused'; field: string; message: string }
  | { kind: 'unreachable' }
  | { kind: 'failed'; message: string };

/**
 * The fields the server can name on a refusal, in the order an officer can act on them.
 *
 * What the reaction was to first, then what it did, then how bad and how sure — the order the
 * question is actually asked in — and the reason last, because it belongs to the two acts that
 * take one rather than to the form. A refusal naming two fields is reported by the one the
 * person at the tablet can do something about, rather than by whichever key serialised first.
 */
const FIELD_ORDER = [
  'code',
  'code_system',
  'code_version',
  'said',
  'reaction',
  'severity',
  'certainty',
  'note',
  'kind',
  'reason',
];

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
 * What went wrong, in the three shapes this step knows how to say.
 *
 * A refusal is shown in the server's own words, because the rules behind it are the database's
 * and a client that paraphrased them would be inventing a second, staler account of a clinical
 * rule it does not own.
 *
 * Everything else is the server being absent, and here that costs more than at any other
 * station on this app: the officer cannot record the allergy, the gate stays shut, and the
 * patient waits. The screen says exactly that rather than offering a way round it.
 */
export function troubleOf(error: unknown, locale: Locale): Trouble {
  if (error instanceof NetworkError) return { kind: 'unreachable' };

  if (error instanceof ApiError) {
    if (error.status === 422) {
      const refusal = refusalOf(fieldMessages(error, locale));
      return {
        kind: 'refused',
        field: refusal.field,
        message: refusal.message === '' ? messageOf(error, locale) : refusal.message,
      };
    }
    return { kind: 'failed', message: messageOf(error, locale) };
  }

  // Something that is not an error this app throws. The screen supplies the sentence.
  return { kind: 'failed', message: '' };
}

function messageOf(error: InstanceType<typeof ApiError>, locale: Locale): string {
  const bengali = error.messageBN.trim();
  if (locale === 'bn' && bengali !== '') return bengali;
  return error.messageEN.trim();
}
