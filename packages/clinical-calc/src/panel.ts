import {
  bmi,
  bmrMifflin,
  classify,
  idealBodyWeight,
  whr,
  type CalcRefusalReason,
  type CalcResult,
  type ObesityClass,
  type Sex,
} from './index';

/**
 * The anthropometry panel (CP45, P-4).
 *
 * # Why a panel and not four calls
 *
 * The station screen shows BMI, its class, BMR and ideal weight *while the operator is still
 * typing*, and it has to show them in a half-typed state: a height and no weight yet, a
 * weight the operator is in the middle of correcting. Four separate calls, each with its own
 * idea of what "not yet" means, is four places for the screen to disagree with itself — and
 * the screen that matters is the phone, where four half-answers is what an operator actually
 * sees for the first fifteen seconds of every entry.
 *
 * So the composition itself is a unit: one input, one answer, one rule about missing values.
 * And because the composition is a unit, it is a unit the parity harness can hold — the phone
 * and the server compute the same panel from the same numbers, which is criterion (2) of this
 * checkpoint.
 *
 * # What "missing" means here
 *
 * Nothing. Not an error, not a zero, not a NaN. A value the panel cannot compute is absent
 * from the result and named in `needs`, because "we have not measured their height" is the
 * sentence the operator needs, and it is a different sentence from "that height cannot be
 * right" — which is what `refused` is for.
 */

/**
 * What an anthropometry screen has as the operator types. Every measurement is nullable
 * because "not typed yet" is the state the screen spends most of its life in, and a zero
 * would be a measurement.
 */
export interface PanelInput {
  weightKg: number | null;
  heightCm: number | null;
  waistCm: number | null;
  hipCm: number | null;
  /**
   * From the patient record, never from the screen. BMR moves with both, and a field an
   * operator could edit is a field that changes a clinical number.
   */
  ageYears: number;
  sex: Sex;
  /** Picks the obesity cut-offs. True for this clinic; see `classify`. */
  asian: boolean;
}

export interface Panel {
  bmi?: CalcResult;
  obesity_class?: ObesityClass;
  obesity_class_version?: string;
  bmr?: CalcResult;
  ideal_body_weight?: CalcResult;
  whr?: CalcResult;
  /**
   * The measurements each absent value is waiting for. The screen turns this into a
   * sentence; this library does not compose sentences, for the same reason the traffic
   * board does not (CP40) — the reader may be reading Bangla.
   */
  needs?: Record<string, string[]>;
  /**
   * The values an equation would not produce even though its inputs were present. A reason
   * code, not a message, and the same four codes the Go library returns — which is what
   * lets the parity fixtures compare the two panels field by field.
   */
  refused?: Record<string, CalcRefusalReason>;
}

/**
 * Bumped when the *composition* changes — which formula feeds which slot, or what counts as
 * missing. Not when one of the underlying formulas is revised: those carry their own
 * versions, and a stored value names the one it used.
 */
export const PANEL_VERSION = '1.0.0';

export function anthroPanel(input: PanelInput): Panel {
  const out: Panel = {};
  const needs: Record<string, string[]> = {};
  const refused: Record<string, CalcRefusalReason> = {};

  const { weightKg, heightCm, waistCm, hipCm } = input;

  if (weightKg === null && heightCm === null) needs.bmi = ['weight', 'height'];
  else if (weightKg === null) needs.bmi = ['weight'];
  else if (heightCm === null) needs.bmi = ['height'];
  else {
    const value = bmi(weightKg, heightCm);
    if (!value.ok) refused.bmi = value.reason;
    else {
      out.bmi = value.result;
      const band = classify(value.result.value, input.asian);
      if (!band.ok) refused.obesity_class = (band as { reason: CalcRefusalReason }).reason;
      else {
        out.obesity_class = band.class;
        out.obesity_class_version = band.version;
      }
    }
  }

  if (weightKg === null && heightCm === null) needs.bmr = ['weight', 'height'];
  else if (weightKg === null) needs.bmr = ['weight'];
  else if (heightCm === null) needs.bmr = ['height'];
  else {
    const value = bmrMifflin(weightKg, heightCm, input.ageYears, input.sex);
    if (!value.ok) refused.bmr = value.reason;
    else out.bmr = value.result;
  }

  if (heightCm === null) needs.ideal_body_weight = ['height'];
  else {
    const value = idealBodyWeight(heightCm, input.sex);
    if (!value.ok) refused.ideal_body_weight = value.reason;
    else out.ideal_body_weight = value.result;
  }

  if (waistCm === null && hipCm === null) needs.whr = ['waist', 'hip'];
  else if (waistCm === null) needs.whr = ['waist'];
  else if (hipCm === null) needs.whr = ['hip'];
  else {
    const value = whr(waistCm, hipCm);
    if (!value.ok) refused.whr = value.reason;
    else out.whr = value.result;
  }

  if (Object.keys(needs).length > 0) out.needs = needs;
  if (Object.keys(refused).length > 0) out.refused = refused;
  return out;
}
