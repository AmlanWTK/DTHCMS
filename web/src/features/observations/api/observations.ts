import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * Reading recorded values, and the chain a corrected one leaves behind (CP62, §4.3).
 *
 * # Why the observations feature grew an API module at CP62
 *
 * Until now this feature was one component: `DualUnitValue`, the only way a clinical number
 * is drawn. Nothing on the web read observations, because every screen that needed one — the
 * growth chart, the critical-value board — read a purpose-built endpoint that already
 * carried what it needed. CP62's criterion 5 asks for something none of those can answer:
 * *the value history shows the complete chain with both attributions*, which means the rows
 * themselves, the replaced ones included.
 *
 * # The chain is built from `replaced_by`, not from the order rows arrive in
 *
 * `GET /v1/patients/{id}/observations/{code}/history` answers newest first, and that is a
 * sort, not a structure. One code accumulates several independent measurements — a height at
 * three visits — and each of those may have been corrected once or twice. Reading them as
 * one flat list would put last March's correction between today's height and yesterday's,
 * and a physician asking "what did this say before" would have to reconstruct the links by
 * eye. `replaced_by` is the link the server already records; `chainsOf` follows it.
 *
 * # Nothing here decides what a value *means*
 *
 * There is no predicate in this module that says a value is wrong, implausible or better
 * than another. A correction is a fact about a number, and the moment a helper here started
 * ranking two rows the screen above it would start reading as a verdict on whoever typed the
 * first one.
 */

export type Observation = components['schemas']['Observation'];
export type ObservationCode = components['schemas']['ObservationCode'];
export type MeasurementUnit = components['schemas']['MeasurementUnit'];
export type ObservationStatus = Observation['status'];
export type ObservationCategory = Observation['category'];

/**
 * The cache keys, held beside the calls.
 *
 * Applying a correction changes three of these at once — the flagged code's history, the
 * patient's current values, and every derived code recomputed from it — and the components
 * that read them are in three different files. Two spellings of one key is a chain view that
 * still shows the old height under a request that has just been answered.
 */
export const OBSERVATION_CODES_KEY = ['observations', 'codes'] as const;

export function patientObservationsKey(patientId: string) {
  return ['observations', 'current', patientId] as const;
}

/**
 * One code's history, or — with the code left off — every code's history for this patient.
 *
 * The second form is what a correction invalidates. Applying one recomputes every live derived
 * value in the same transaction, and the client does not know which derivations read the code
 * that moved: that table is the server's, and a copy here would be a second opinion that goes
 * stale the day a formula changes. The patient's whole history subtree is the honest answer,
 * and it is one key rather than a guess at a list of codes.
 */
export function observationHistoryKey(patientId: string, code?: string) {
  return code === undefined
    ? (['observations', 'history', patientId] as const)
    : (['observations', 'history', patientId, code] as const);
}

export function observationKey(observationId: string) {
  return ['observations', 'one', observationId] as const;
}

/**
 * The code registry: what each kind of value is, and which units it may be entered in.
 *
 * Reference data with no patient in it, fetched once. Two things on this surface need it and
 * neither can invent them: a chain view that named `BODY_HEIGHT` on screen would be showing
 * a physician a database identifier, and a correction form that offered a free-text unit box
 * would let somebody answer 150 cm with 140 kg.
 */
export async function listObservationCodes(): Promise<ObservationCode[]> {
  const body = await unwrap(api.GET('/v1/observations/codes'));
  return body.codes;
}

/**
 * This patient's current values — one row per code, the replaced ones excluded.
 *
 * Read here for two questions the history cannot answer: which codes this patient has any
 * value for at all, and what each derived value was computed from *now*. See `derivedFrom`.
 */
export async function listPatientObservations(patientId: string): Promise<Observation[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/observations', { params: { path: { id: patientId } } }),
  );
  return body.observations;
}

/**
 * Every value ever recorded for one code, replaced rows included.
 *
 * The replaced rows are the point. Criterion 1 is that the original value is never altered
 * and remains visible, and this endpoint is the only place in the contract where it is
 * visible — the current-values read drops it by design.
 */
export async function listObservationHistory(
  patientId: string,
  code: string,
): Promise<Observation[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/observations/{code}/history', {
      params: { path: { id: patientId, code } },
    }),
  );
  return body.observations;
}

/** One value, by id. What the corrections queue reads: a request names a value, not a number. */
export async function getObservation(observationId: string): Promise<Observation> {
  const body = await unwrap(
    api.GET('/v1/observations/{id}', { params: { path: { id: observationId } } }),
  );
  return body.observation;
}

/** Whether this row is the one that stands today. `ACTIVE` is the server's own word. */
export function isCurrent(observation: Observation): boolean {
  return observation.status === 'ACTIVE';
}

/**
 * Whether a later row replaced this one — because it was wrong, or because it was re-measured.
 *
 * `CORRECTED` and `SUPERSEDED` are deliberately not collapsed anywhere in this feature.
 * "Somebody typed the wrong number" and "the value was right and has been re-derived" are
 * different facts about different people, and a predicate that answered "replaced" for both
 * would let a screen put an error on the record of somebody who made none.
 */
export function isReplaced(observation: Observation): boolean {
  return observation.status !== 'ACTIVE';
}

/**
 * One line of a value's descent: the row, and the row that replaced it.
 *
 * A chain is oldest first, which is the order the events happened in and the order the
 * sentence reads in — *150 was recorded, then corrected to 140*. Reversing it to put the
 * current value at the top would put the answer before the question.
 */
export type ObservationChain = readonly Observation[];

/**
 * The history, resolved into chains by following `replaced_by`.
 *
 * # Why heads are found by exclusion rather than by status
 *
 * A head is a row nothing points at. Asking `status === 'ACTIVE'` instead would find the
 * *end* of each chain and miss every chain whose current row was itself later superseded —
 * and it would find nothing at all in a history where every row has been replaced, which is
 * exactly the history somebody is reading when they ask what happened.
 *
 * # Why a row pointing outside the list still forms a chain
 *
 * `limit` can cut a history off mid-chain, and a row whose `replaced_by` names an id that
 * did not come back simply ends there. That is what the response says; inventing a
 * continuation, or hiding the row because its successor is missing, would both be a
 * statement about data this client does not have.
 *
 * # Why there is a visited set
 *
 * `replaced_by` is a foreign key and a cycle in it should be impossible. `should be` is not
 * a reason to write a loop that hangs the browser on a patient's record: a cycle stops the
 * walk and the rows already collected are still drawn, because a chain view that renders
 * most of a chain is worth more to a physician than a blank screen.
 *
 * Chains are ordered by their current row, newest first — today's measurement above last
 * March's, which is the order every other clinical list in this application reads in.
 */
export function chainsOf(observations: readonly Observation[]): ObservationChain[] {
  const byId = new Map(observations.map((observation) => [observation.id, observation]));
  const replacements = new Set(
    observations
      .map((observation) => observation.replaced_by)
      .filter((id): id is string => id !== undefined),
  );

  const chains: Observation[][] = [];
  for (const observation of observations) {
    if (replacements.has(observation.id)) continue;

    const chain: Observation[] = [];
    const seen = new Set<string>();
    let step: Observation | undefined = observation;
    while (step !== undefined && !seen.has(step.id)) {
      seen.add(step.id);
      chain.push(step);
      step = step.replaced_by === undefined ? undefined : byId.get(step.replaced_by);
    }
    chains.push(chain);
  }

  return chains.sort((a, b) => instant(latest(b)) - instant(latest(a)));
}

/** The row that stands at the end of a chain — the current value, where one is current. */
export function latest(chain: ObservationChain): Observation | undefined {
  return chain[chain.length - 1];
}

/** The row a chain began with. The original, which criterion 1 says is never altered. */
export function original(chain: ObservationChain): Observation | undefined {
  return chain[0];
}

/**
 * When a value was true, as a number, falling back to when it was written down.
 *
 * `effective_at` is the clinical instant and is what a chain is sorted by. A row whose
 * timestamps will not parse sorts last rather than throwing `NaN` through the comparator,
 * where it would silently scramble the whole list.
 */
function instant(observation: Observation | undefined): number {
  if (observation === undefined) return 0;
  const effective = Date.parse(observation.effective_at);
  if (Number.isFinite(effective)) return effective;
  const recorded = Date.parse(observation.recorded_at);
  return Number.isFinite(recorded) ? recorded : 0;
}

/**
 * The derived values this patient has that were computed from one code (criterion 4).
 *
 * # Why this reads `inputs` rather than a table of formulas
 *
 * The server knows which derivations read a height; this client does not, and a copy of that
 * table here would be a second opinion that disagrees the day a formula changes. `inputs` is
 * not a guess about what a formula reads — it is the record of what this particular value
 * *was given*, stored beside it at computation time. A BMI that names `BODY_HEIGHT` in its
 * inputs was computed from this patient's height, and that is a fact rather than an
 * inference.
 *
 * A derived value whose inputs are absent — an older row, or a derivation that stored none —
 * is not returned. Nothing is claimed about it, which is the honest answer: the alternative
 * is guessing from its code, and a BMI wrongly listed as unmoved is worse than one not
 * listed.
 */
export function derivedFrom(observations: readonly Observation[], code: string): Observation[] {
  return observations.filter(
    (observation) =>
      observation.category === 'DERIVED' &&
      observation.inputs !== undefined &&
      Object.prototype.hasOwnProperty.call(observation.inputs, code),
  );
}

/**
 * What a derived value was computed from, for one input code, in canonical units.
 *
 * `undefined` when the row does not record it. Not zero: a BMI computed from a height of
 * zero is impossible and a BMI whose inputs were not recorded is ordinary, and a caller that
 * could not tell them apart would draw the second as the first.
 */
export function inputSeen(observation: Observation, code: string): number | undefined {
  return observation.inputs?.[code];
}

/**
 * Whether a derived value was computed from the value that stands today (criterion 4).
 *
 * # What this answers, and what it deliberately does not
 *
 * The server recomputes every live derived value inside the correction's own transaction, so
 * the ordinary answer after a correction is `true` — the BMI moved. It is not *always* true:
 * the cascade names a derivation it could not recompute rather than failing the correction,
 * because a corrected height that could not be saved is worse than a stale BMI beside a
 * corrected height. That failure does not reach the contract in any field, so this is how a
 * screen can still tell: the BMI says it was computed from 150, the height now says 140, and
 * the two disagree.
 *
 * `null` where there is nothing to compare — the derived row records no input for this code,
 * or the current value is not numeric. A boolean there would round "we cannot tell" up to
 * "it is fine", which is the one answer a physician must not be given.
 *
 * The comparison has a tolerance because both numbers are floating point and arrived by
 * different routes: the input was stored when the formula ran, the current value came back
 * from a fresh read. Two numbers that agree to within a thousandth of a canonical unit are
 * the same measurement; treating them as different would report every BMI in the clinic as
 * stale.
 */
export function computedFromCurrent(
  derived: Observation,
  code: string,
  current: Observation | undefined,
): boolean | null {
  const saw = inputSeen(derived, code);
  const now = current?.value;
  if (saw === undefined || now === undefined) return null;
  if (!Number.isFinite(saw) || !Number.isFinite(now)) return null;
  return Math.abs(saw - now) < 0.001;
}

/**
 * The registry entry for one code, or `undefined` while the registry is still arriving.
 *
 * Callers fall back to the code itself rather than to a blank. `BODY_HEIGHT` on a physician's
 * screen is a database identifier and a poor label; a blank where the name of the measurement
 * belongs is a screen that has stopped saying what the number is.
 */
export function codeEntry(
  codes: readonly ObservationCode[],
  code: string,
): ObservationCode | undefined {
  return codes.find((entry) => entry.code === code);
}

/**
 * Every unit a value of this code may be entered in, canonical first.
 *
 * Canonical first because it is the unit the clinic works in and the one the value will
 * almost always be corrected in; the rest follow so that "entered in the wrong unit" — one of
 * the server's own reason codes — is a correction somebody can actually make. An empty list
 * is a unitless code, and a form must then send no unit at all rather than an empty string.
 */
export function unitsFor(entry: ObservationCode | undefined): MeasurementUnit[] {
  const units = entry?.units ?? [];
  return [...units].sort((a, b) => Number(b.is_canonical) - Number(a.is_canonical));
}

/**
 * The unit a value was entered in, which is not always the unit it is stored in.
 *
 * 154 lb is stored as 69.85 kg and read back as 154 lb, because the operator typed pounds and
 * a correction form that pre-filled kilograms would be asking them to check a number they
 * never wrote. `entered_unit` where the record has one, the canonical unit otherwise.
 */
export function enteredUnitOf(observation: Observation): string | undefined {
  const entered = (observation.entered_unit ?? '').trim();
  if (entered !== '') return entered;
  const canonical = (observation.unit ?? '').trim();
  return canonical === '' ? undefined : canonical;
}

/** The number as the operator typed it, falling back to the canonical one. See `enteredUnitOf`. */
export function enteredValueOf(observation: Observation): number | undefined {
  return observation.entered_value ?? observation.value;
}
