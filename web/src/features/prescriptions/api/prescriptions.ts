import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The prescription editor's API surface (CP81, against CP80's aggregate).
 *
 * # There is no client-side draft store, and that is the autosave
 *
 * The obvious shape for "draft autosave" is a local buffer flushed on a timer. This does not have
 * one, because CP80 already built the write path and a second store would be a second truth about
 * what is on the prescription — and the one in the browser would be the one that disagreed.
 *
 * So **a completed line is written to the server the instant it is completed**, as a
 * `PRESCRIPTION_ITEM_ADDED` event with its own attribution and its own captured price. There is
 * nothing to flush and nothing to reconcile. What a browser crash loses is exactly the line being
 * typed at the moment of the crash, and nothing else; the editor says so in words rather than
 * showing a "saving…" indicator that implies a queue.
 *
 * ADR-0010 is satisfied by construction rather than by care: there is no `localStorage` call in
 * this feature at all, so there is nothing here that could hold a token.
 *
 * # Every write carries an idempotency key
 *
 * `writing()` mints one. The editor is a screen somebody uses on a clinic's wifi with a patient
 * waiting, and a dropped response on "add metformin" must not become two metformin lines in an
 * append-only ledger.
 */

export type Prescription = components['schemas']['Prescription'];
export type PrescriptionItem = components['schemas']['PrescriptionItem'];
export type PrescribingDefault = components['schemas']['PrescribingDefault'];
export type InstructionTemplate = components['schemas']['InstructionTemplate'];
export type PrintModel = components['schemas']['PrintModel'];
export type SafetyResult = components['schemas']['MedicationSafetyCheckResult'];
export type SafetyFinding = components['schemas']['MedicationRuleFinding'];
export type SafetyCoverage = components['schemas']['MedicationSafetyCoverage'];

export const PRESCRIPTION_KEY = ['prescriptions'] as const;

export function prescriptionKey(id: string) {
  return ['prescriptions', id] as const;
}
export function printModelKey(id: string) {
  return ['prescriptions', id, 'print-model'] as const;
}
export function safetyKey(id: string, revision: number) {
  // The revision is in the key on purpose. A safety result is about a particular set of lines,
  // and a cache that keyed only on the prescription would show the findings for four medicines
  // beside a list of five — which is the "clean result that never ran" failure CP78's own
  // header is about, one item late.
  return ['prescriptions', id, 'safety', revision] as const;
}
export const DEFAULTS_KEY = ['prescribing-defaults'] as const;
export const TEMPLATES_KEY = ['instruction-templates'] as const;
export function patientPrescriptionsKey(patientId: string) {
  return ['patients', patientId, 'prescriptions'] as const;
}

/* ------------------------------------------------------------------------- */
/* Reads                                                                      */
/* ------------------------------------------------------------------------- */

export async function getPrescription(id: string) {
  return unwrap(api.GET('/v1/prescriptions/{id}', { params: { path: { id } } }));
}

export async function getPrintModel(id: string): Promise<PrintModel> {
  return unwrap(api.GET('/v1/prescriptions/{id}/print-model', { params: { path: { id } } }));
}

export async function listPatientPrescriptions(patientId: string) {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/prescriptions', {
      params: { path: { id: patientId }, query: { limit: 10 } },
    }),
  );
  return body.prescriptions ?? [];
}

export async function listPrescribingDefaults() {
  return unwrap(api.GET('/v1/prescribing-defaults', {}));
}

export async function listInstructionTemplates() {
  return unwrap(api.GET('/v1/instruction-templates', {}));
}

/* ------------------------------------------------------------------------- */
/* Writes                                                                     */
/* ------------------------------------------------------------------------- */

export async function createDraft(input: {
  patientId: string;
  visitId: string;
  carryForwardFrom?: string;
}) {
  return unwrap(
    api.POST('/v1/prescriptions', {
      params: { ...writing() },
      body: {
        patient_id: input.patientId,
        visit_id: input.visitId,
        carry_forward_from: input.carryForwardFrom,
        // Never defaulted true. CP80 refuses a carry-forward without it, and the refusal is
        // the point: a physician who did not mean to carry forward and got last month's six
        // drugs might not notice.
        carry_forward_confirmed: input.carryForwardFrom ? true : undefined,
      },
    }),
  );
}

export interface LineInput {
  productId?: string;
  label?: string;
  lineNo?: number;
  dose: string;
  dailyDose?: number;
  doseUnit?: string;
  frequency: string;
  durationDays?: number;
  route?: string;
  instructionsEn?: string;
  instructionsBn?: string;
}

function lineBody(line: LineInput) {
  return {
    product_id: line.productId,
    label: line.label,
    line_no: line.lineNo,
    dose: line.dose,
    daily_dose: line.dailyDose,
    dose_unit: line.doseUnit,
    frequency: line.frequency,
    duration_days: line.durationDays,
    route: line.route,
    instructions_en: line.instructionsEn,
    instructions_bn: line.instructionsBn,
  };
}

export async function addItem(prescriptionId: string, line: LineInput) {
  return unwrap(
    api.POST('/v1/prescriptions/{id}/items', {
      params: { ...writing(), path: { id: prescriptionId } },
      body: lineBody(line),
    }),
  );
}

export async function modifyItem(prescriptionId: string, itemId: string, line: LineInput) {
  return unwrap(
    api.PATCH('/v1/prescriptions/{id}/items/{itemId}', {
      params: { ...writing(), path: { id: prescriptionId, itemId } },
      body: lineBody(line),
    }),
  );
}

/**
 * Take a medicine off a draft.
 *
 * The reason is required by the server and is not defaulted here. CP80 keeps the row with
 * `removed_at`, `removed_by` and `removed_reason` because "what was on this prescription at
 * 14:05" has to stay answerable after the item came off at 14:06 — and a reason the browser
 * invented would make that answer useless.
 */
export async function removeItem(prescriptionId: string, itemId: string, reason: string) {
  return unwrap(
    api.DELETE('/v1/prescriptions/{id}/items/{itemId}', {
      params: { ...writing(), path: { id: prescriptionId, itemId } },
      body: { reason },
    }),
  );
}

export async function runSafetyCheck(prescriptionId: string) {
  const body = await unwrap(
    api.POST('/v1/prescriptions/{id}/safety-check', {
      params: { ...writing(), path: { id: prescriptionId } },
      body: {},
    }),
  );
  return body.result as SafetyResult;
}

export async function approvePrescribingDefault(id: string) {
  return unwrap(
    api.POST('/v1/prescribing-defaults/{id}/approval', {
      params: { ...writing(), path: { id } },
    }),
  );
}

export async function approveInstructionTemplate(id: string) {
  return unwrap(
    api.POST('/v1/instruction-templates/{id}/approval', {
      params: { ...writing(), path: { id } },
    }),
  );
}

/* ------------------------------------------------------------------------- */
/* Resolving a suggestion                                                     */
/* ------------------------------------------------------------------------- */

/**
 * The suggestion that applies to one generic at one strength, or nothing.
 *
 * **The exact strength wins; a row with an empty strength is the molecule-wide fallback.** This
 * mirrors `prescription.ResolveDefault` in Go deliberately, and the two are tested against the
 * same cases — the failure otherwise is metformin's 500 mg suggestion offered for a 1000 mg
 * tablet, which is half the dose offered confidently.
 *
 * Returning `undefined` is the common case: two hundred and fifty products, twenty-eight
 * suggestions. Nothing is offered for the rest, because a fallback that was right often enough
 * to be trusted would be the machine prescribing.
 */
export function resolveDefault(
  all: PrescribingDefault[],
  genericName: string,
  strength: string,
): PrescribingDefault | undefined {
  const generic = genericName.trim().toLowerCase();
  const wanted = strength.trim().toLowerCase();
  let fallback: PrescribingDefault | undefined;
  for (const candidate of all) {
    if (candidate.generic_name.trim().toLowerCase() !== generic) continue;
    const candidateStrength = (candidate.strength ?? '').trim().toLowerCase();
    if (candidateStrength !== '' && candidateStrength === wanted) return candidate;
    if (candidateStrength === '') fallback = candidate;
  }
  return fallback;
}
