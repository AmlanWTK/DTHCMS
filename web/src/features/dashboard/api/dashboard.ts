import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';
import type { QueryClient } from '@tanstack/react-query';

import { allergyStateKey } from '@/features/allergies';
import { api, unwrap } from '@/lib/api';

/**
 * The physician's dashboard, typed against the contract (CP73, §8).
 *
 * # One call, and the cache priming that keeps it one call
 *
 * `GET /v1/patients/{id}/dashboard` returns every panel §8 asks for. That is the checkpoint's
 * own acceptance criterion, and it is fragile in a way that is worth naming: the components
 * these panels are drawn with **already exist**, and several of them fetch for themselves.
 * `AllergyBanner` reads `allergyStateKey(patientId)`; the alert strip reads the patient's
 * alert key. Rendering them inside a dashboard that has already fetched the same data would
 * produce exactly the twelve requests this endpoint exists to replace — and worse, twelve
 * requests plus one.
 *
 * So the aggregate **primes their caches** (see [primeDashboardCaches]). The existing
 * components are used unchanged, their `useQuery` finds fresh data under their own key, and
 * no second request is made. Navigating away to the allergy panel still refetches, exactly as
 * before, because nothing about those components changed.
 *
 * The alternative — passing the data down as props — would mean forking every one of those
 * components into a "fetching" and a "given" variant, which is four components maintained in
 * pairs and a guarantee that the pair drifts.
 *
 * # Why the key sits under the patient's prefix
 *
 * `['patient', id, 'dashboard']`, so that CP27's realtime invalidations reach it without this
 * feature teaching the shared message router about a new key. Every message carrying a
 * `patient_id` invalidates `['patient', id]`, which is a prefix of this. A value recorded at
 * a station, an alert raised, a counselling item ticked: all of them already land here.
 */

export type PhysicianDashboard = components['schemas']['PhysicianDashboard'];
export type DashboardIdentity = components['schemas']['DashboardIdentity'];
export type DashboardVisit = components['schemas']['DashboardVisit'];
export type DashboardAccess = components['schemas']['DashboardAccess'];
export type DashboardBodyMass = components['schemas']['DashboardBodyMass'];
export type DashboardTrend = components['schemas']['DashboardTrend'];
export type DashboardSummary = components['schemas']['DashboardSummary'];
export type DashboardAssistant = components['schemas']['DashboardAssistant'];
export type DashboardSuggestion = components['schemas']['DashboardSuggestion'];
export type DashboardOmission = components['schemas']['DashboardOmission'];
export type SuggestionDecision = components['schemas']['SuggestionDecision'];
export type SuggestionDecisionKind = SuggestionDecision['kind'];
export type SuggestionKind = DashboardSuggestion['kind'];
export type SuggestionOrigin = DashboardSuggestion['origin'];

/** The cache key. Under the patient's prefix — see the note above. */
export function dashboardKey(patientId: string, visitId?: string) {
  return ['patient', patientId, 'dashboard', visitId ?? null] as const;
}

/** Today's patients, for the picker shown when no patient has been chosen. */
export function todaysPatientsKey() {
  return ['patients', 'today'] as const;
}

/** The whole screen, in one request. */
export async function readDashboard(
  patientId: string,
  visitId?: string,
): Promise<PhysicianDashboard> {
  return unwrap(
    api.GET('/v1/patients/{id}/dashboard', {
      params: {
        path: { id: patientId },
        query: visitId === undefined ? {} : { visit_id: visitId },
      },
    }),
  ) as Promise<PhysicianDashboard>;
}

/** Who was registered today. The picker's list, and honestly labelled as that. */
export async function listTodaysPatients() {
  const body = await unwrap(api.GET('/v1/patients/today', { params: { query: {} } }));
  return body.patients;
}

export interface DecisionInput {
  patientId: string;
  visitId: string;
  ref: string;
  decision: SuggestionDecisionKind;
  edited?: string;
  note?: string;
}

/**
 * Accept, edit or reject one drafted suggestion.
 *
 * **This records an intent. It does not write a prescription.** §7.3 makes that split
 * permanent, and CP81 owns the prescription with its interaction check, its dose validation
 * and its signature. The screen says so where the buttons are; this is where the sentence is
 * true.
 */
export async function decideSuggestion(input: DecisionInput): Promise<SuggestionDecision> {
  const body = await unwrap(
    api.POST('/v1/patients/{id}/dashboard/suggestions/{ref}/decision', {
      params: { ...writing(), path: { id: input.patientId, ref: input.ref } },
      body: {
        event_id: crypto.randomUUID(),
        visit_id: input.visitId,
        decision: input.decision,
        ...(input.edited === undefined ? {} : { edited: input.edited }),
        ...(input.note === undefined ? {} : { note: input.note }),
      },
    }),
  );
  return body.decision;
}

/**
 * Hands the aggregate's contents to the components that would otherwise fetch them.
 *
 * # Why the caller runs this during render, and not in an effect or in the fetch
 *
 * It was written as an effect first — `useEffect(() => prime(...), [data])` — on the argument
 * that a side effect during render is worse than one after it. That version **did not work**,
 * and the way it failed is the reason this comment is long: an effect runs *after* the commit
 * that renders the children, so `AllergyBanner` mounted, found an empty cache and issued its
 * own request before the priming ever ran. The screen was correct and made two requests, which
 * is exactly the failure the acceptance criterion is about, and exactly the kind that is
 * invisible on a developer's machine and expensive on a clinic's wifi. The test named
 * *reads the dashboard once and asks nothing else for the allergy strip* is what caught it.
 *
 * The second attempt put it inside the `queryFn`, which is early enough — and is wrong for a
 * different reason: a query served from the cache never calls its `queryFn` at all. A
 * physician returning to a patient within the stale window would get the dashboard instantly
 * from the cache and the allergy strip would fetch, which is the same bug on the path a busy
 * morning takes most often.
 *
 * So the call site is the render, guarded by a ref so that it happens once per distinct
 * payload rather than on every parent render. Writing to an external store during render is
 * ordinarily a smell; here the store is a cache, the write is idempotent, and the alternative
 * is a screen that quietly stops satisfying its own acceptance criterion.
 *
 * # Why `setQueryData` and not `initialData`
 *
 * `initialData` seeds a key only if nothing is there and does not update when the seed
 * changes, so a dashboard that refetched after a realtime message would hand the allergy strip
 * its first payload forever. `setQueryData` writes the current answer every time, and the
 * component's own `staleTime` then governs whether it asks again.
 *
 * # Why the allergy state is primed and the alerts are not
 *
 * The allergy strip fetches for itself and is rendered here, so priming its key is what makes
 * "one request" true. The critical-value strip is deliberately **not** rendered from the
 * aggregate: it holds its own realtime subscription and its own poll, and CP50's own note
 * says why — *"a board that trusted only the socket would go quiet after a dropped connection
 * and look exactly like a clinic with nothing wrong in it"*. Priming a key whose owner polls
 * anyway would buy nothing and would put a second writer on it.
 *
 * The aggregate still carries the alerts, because the snapshot draws them itself and because
 * the payload has to be complete for a client that is not this one.
 */
export function primeDashboardCaches(client: QueryClient, view: PhysicianDashboard): void {
  if (view.allergies) {
    client.setQueryData(allergyStateKey(view.patient.id), view.allergies);
  }
}

/**
 * Whether a panel was withheld, and why.
 *
 * A helper rather than a `find` at each call site, because the question is asked by six
 * panels and the answer decides which of two very different sentences each one draws: "this
 * patient has none" against "you were not shown this". Getting that wrong is the failure the
 * `omitted` list exists to prevent, and a helper is how the check stays the same in all six
 * places.
 */
export function omissionFor(
  view: PhysicianDashboard,
  panel: string,
): DashboardOmission | undefined {
  return view.omitted.find((entry) => entry.panel === panel);
}

/**
 * The suggestions in the order the panel draws them.
 *
 * Undecided first, then decided, and within each group in the order the server sent. A
 * physician working down the panel is answering the ones they have not answered; a decided
 * item that stayed in place would make them re-read the same line every time the summary
 * refreshed. Nothing is hidden — a rejection that vanished would leave no evidence the draft
 * was considered, which is half of why the decision is recorded at all.
 */
export function suggestionsInWorkingOrder(
  suggestions: readonly DashboardSuggestion[],
): DashboardSuggestion[] {
  const undecided = suggestions.filter((item) => item.decision === undefined);
  const decided = suggestions.filter((item) => item.decision !== undefined);
  return [...undecided, ...decided];
}

/**
 * Whether a decision was made against an older run than the one on screen.
 *
 * A re-run produces a new generation and often the same suggestions. The decision is still
 * shown — making a physician answer the same drafts every time the summary refreshes is how a
 * panel teaches people to click through it — but it is shown with a note, because their
 * earlier answer was about a sentence drawn from different data.
 */
export function decidedAgainstAnOlderRun(
  suggestion: DashboardSuggestion,
  generation: number,
): boolean {
  return suggestion.decision !== undefined && suggestion.decision.generation < generation;
}
