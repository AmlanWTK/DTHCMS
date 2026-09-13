import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The AI prescribing panel's API surface (CP82).
 *
 * # Three functions, and one of them deliberately takes one suggestion
 *
 * `decideSuggestion` takes a single id. There is no `acceptAll`, no array parameter and no batch
 * endpoint to call — the specification is explicit that *"there is no code path — no batch accept,
 * no 'accept all', no default-on — by which a suggestion becomes a line without a person doing it
 * one line at a time"*, and the cheapest way for that to stay true in the browser is for there to
 * be nothing here to call.
 *
 * If somebody adds one later they will have to add it here first, in a diff that reads as adding
 * exactly the thing the checkpoint forbade.
 *
 * # `ask` is a write, not a read
 *
 * It records a run whatever happens — including a refusal and a failure — so it is a POST with an
 * idempotency key. A physician pressing the button twice on bad wifi must not spend two model
 * calls on one patient.
 *
 * # Every state is a 200
 *
 * `getSuggestions` never 404s. "Nobody has asked", "the model was not asked because this patient
 * has no recorded allergy status", "the model proposed nothing" and "the model could not be
 * reached" are four different facts and the panel shows a different sentence for each. A client
 * that treated an empty list as one state would collapse them into an empty column.
 */

export type AIPrescribingRun = components['schemas']['AIPrescribingRun'];
export type AIPrescribingSuggestion = components['schemas']['AIPrescribingSuggestion'];
export type AISuggestionDecision = components['schemas']['AISuggestionDecision'];
export type AISuggestionRejectReason = components['schemas']['AISuggestionRejectReason'];

export function aiSuggestionsKey(prescriptionId: string) {
  return ['prescriptions', prescriptionId, 'ai-suggestions'] as const;
}

export const REJECT_REASONS_KEY = ['ai-suggestion-reject-reasons'] as const;

export async function getSuggestions(prescriptionId: string): Promise<AIPrescribingRun> {
  return unwrap(
    api.GET('/v1/prescriptions/{id}/ai-suggestions', { params: { path: { id: prescriptionId } } }),
  );
}

export async function listRejectReasons() {
  const body = await unwrap(api.GET('/v1/ai-suggestion-reject-reasons', {}));
  return body.reasons ?? [];
}

export async function askForSuggestions(prescriptionId: string): Promise<AIPrescribingRun> {
  return unwrap(
    api.POST('/v1/prescriptions/{id}/ai-suggestions', {
      params: { path: { id: prescriptionId }, ...writing() },
    }),
  );
}

/** What the physician changed, for an edit. Absent fields keep the suggestion's own values. */
export interface SuggestionEdit {
  dose?: string;
  frequency?: string;
  durationDays?: number;
  route?: string;
}

/**
 * One physician's answer to one suggestion.
 *
 * The signature is singular all the way down: one prescription, one suggestion, one decision. The
 * body carries no product and no label, so this cannot be used to put a medicine on a prescription
 * that the server did not itself propose.
 */
export async function decideSuggestion(input: {
  prescriptionId: string;
  suggestionId: string;
  decision: 'ACCEPTED' | 'EDITED' | 'REJECTED';
  edit?: SuggestionEdit;
  rejectReasonCode?: string;
  rejectNote?: string;
}) {
  return unwrap(
    api.POST('/v1/prescriptions/{id}/ai-suggestions/{suggestionId}/decision', {
      params: {
        path: { id: input.prescriptionId, suggestionId: input.suggestionId },
        ...writing(),
      },
      body: {
        decision: input.decision,
        dose: input.edit?.dose,
        frequency: input.edit?.frequency,
        duration_days: input.edit?.durationDays,
        route: input.edit?.route,
        // Sent only on a rejection. The server refuses them on anything else, and sending them
        // anyway would turn a physician's acceptance into a 422 he cannot explain.
        reject_reason_code: input.decision === 'REJECTED' ? input.rejectReasonCode : undefined,
        reject_note: input.decision === 'REJECTED' ? input.rejectNote : undefined,
      },
    }),
  );
}

/**
 * What a suggestion's state is, for a screen.
 *
 * **Derived from the absence of `decision`, never from a field.** There is no `UNACTIONED` value
 * in the API or in the database behind it, because a suggestion nobody answered is not a
 * rejection — and a client that compared against a string would be one default away from reading
 * silence as a decision.
 */
export function suggestionState(
  suggestion: AIPrescribingSuggestion,
): 'UNACTIONED' | 'ACCEPTED' | 'EDITED' | 'REJECTED' {
  return suggestion.decision?.decision ?? 'UNACTIONED';
}

export function isUnactioned(suggestion: AIPrescribingSuggestion): boolean {
  return suggestion.decision === undefined || suggestion.decision === null;
}
