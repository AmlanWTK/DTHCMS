import type { components } from '@dthcms/api-client';
import {
  isComplete,
  normalisePhone,
  readDate,
  requiredState,
  type DateParts,
  type RequiredState,
} from '@dthcms/shared-schemas';

/**
 * The request body, typed against the contract — the same type the web desk builds.
 *
 * Typed rather than loose so criterion 1 is a compile error rather than a discovered
 * difference: a patient registered on a phone must be indistinguishable in the record from
 * one registered at the desk, `device_id` excepted.
 */
export type PatientRegistration = components['schemas']['PatientRegistration'];

/**
 * The station app's registration flow (CP33).
 *
 * Registration is deliberately the *secondary* surface here. It involves more typing than
 * any other station, and a keyboard beats a phone at typing — so the web desk (CP32) is
 * primary, and this exists for the two cases the desk cannot cover: the desk is busy, and
 * the registrar is at an outreach camp with no desk at all.
 *
 * That sets the shape: **one section per screen**, large targets, pickers instead of free
 * text wherever the value is from a closed list, and a draft that survives an interruption
 * — because on a phone an interruption is not an edge case.
 *
 * The rules themselves are `@dthcms/shared-schemas`, the same module the web desk imports.
 * Not "the same rules" — the same code. CP33's second criterion asks for that to be
 * provable, and two copies of a validation rule are two rules.
 */

export interface RegistrationValues {
  nameEN: string;
  nameBN: string;
  sex: string;
  date: DateParts;
  dobSource: string;
  phone: string;
  phoneSecondary: string;
  division: string;
  district: string;
  upazila: string;
  addressLine: string;
  nationalID: string;
  emergencyName: string;
  emergencyRelation: string;
  emergencyPhone: string;
  education: string;
  occupation: string;
  income: string;
  household: string;
  residence: string;
  payer: string;
  consentReference: string;
}

export const BLANK: RegistrationValues = {
  nameEN: '',
  nameBN: '',
  sex: '',
  date: { day: '', month: '', year: '' },
  dobSource: '',
  phone: '',
  phoneSecondary: '',
  division: '',
  district: '',
  upazila: '',
  addressLine: '',
  nationalID: '',
  emergencyName: '',
  emergencyRelation: '',
  emergencyPhone: '',
  education: '',
  occupation: '',
  income: '',
  household: '',
  residence: '',
  payer: '',
  consentReference: '',
};

/**
 * The steps, in order.
 *
 * `required` marks the ones that cannot be skipped. The rest can be jumped past with
 * "Skip", because at an outreach camp the operator is standing in a field and the queue
 * behind them is real.
 */
export const STEPS = [
  { id: 'identity', required: true },
  { id: 'birth', required: true },
  { id: 'contact', required: true },
  { id: 'address', required: false },
  { id: 'identifiers', required: false },
  { id: 'emergency', required: false },
  { id: 'background', required: false },
  { id: 'consent', required: true },
  { id: 'review', required: true },
] as const;

export type StepID = (typeof STEPS)[number]['id'];

/** Which required pieces are present. The same function the web desk calls. */
export function required(values: RegistrationValues): RequiredState {
  return requiredState({
    nameEN: values.nameEN,
    sex: values.sex as PatientRegistration['sex'],
    date: values.date,
    dobSource: values.dobSource,
    phone: values.phone,
    consentReference: values.consentReference,
  });
}

export function complete(values: RegistrationValues): boolean {
  return isComplete(required(values));
}

/**
 * Whether the operator may leave a step.
 *
 * Only the required steps block, and each blocks on its own fields alone. Blocking the
 * whole flow on a field three screens away is how a step-by-step form becomes a maze.
 */
export function canAdvance(step: StepID, values: RegistrationValues): boolean {
  const state = required(values);
  switch (step) {
    case 'identity':
      return state.nameEN && state.sex;
    case 'birth':
      return state.birthDate;
    case 'contact':
      return state.phone;
    case 'consent':
      return state.consent;
    case 'review':
      return complete(values);
    default:
      return true;
  }
}

/** The request body, in the shape the API takes. Identical to the web desk's. */
export function toRegistration(values: RegistrationValues, eventID: string): PatientRegistration {
  const parsed = readDate(values.date);
  return {
    event_id: eventID,
    name_en: values.nameEN.trim(),
    name_bn: values.nameBN.trim(),
    sex: values.sex as PatientRegistration['sex'],
    birth_date: parsed?.iso ?? '',
    dob_precision: (parsed?.precision ?? 'day') as PatientRegistration['dob_precision'],
    dob_source: values.dobSource as PatientRegistration['dob_source'],
    // Normalised here as well as on the server, so the record a phone produces and the
    // record a desk produces are byte-identical (criterion 1).
    phone_primary: normalisePhone(values.phone) ?? values.phone.trim(),
    phone_secondary: values.phoneSecondary.trim(),
    division: values.division.trim(),
    district: values.district.trim(),
    upazila: values.upazila.trim(),
    address_line: values.addressLine.trim(),
    postcode: '',
    emergency_name: values.emergencyName.trim(),
    emergency_relation: values.emergencyRelation.trim(),
    emergency_phone: values.emergencyPhone.trim(),
    education_level: values.education as PatientRegistration['education_level'],
    occupation_category: values.occupation as PatientRegistration['occupation_category'],
    income_band: values.income as PatientRegistration['income_band'],
    household_size: values.household ? Number(values.household) : undefined,
    residence_type: values.residence as PatientRegistration['residence_type'],
    medicine_payer: values.payer as PatientRegistration['medicine_payer'],
    identifiers: values.nationalID.trim() ? { national_id: values.nationalID.trim() } : undefined,
    consent_reference: values.consentReference.trim(),
  };
}
