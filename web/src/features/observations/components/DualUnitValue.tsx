import { useLocale, useTranslations } from 'next-intl';

import { dualUnit, hasSecondaryUnit } from '@dthcms/clinical-calc';

/**
 * Every clinical value, in both units (CP44, [R-08], blueprint §2).
 *
 * A named non-negotiable: the clinical unit with the patient-familiar equivalent beneath it.
 * A height is 168 cm *and* 5′6″, because the clinician thinks in one and the patient thinks
 * in the other, and a screen that makes either of them convert in their head is a screen
 * where somebody converts wrongly.
 *
 * # Why this is a component and not a formatter
 *
 * "Show both units" implemented as a helper each screen calls is a rule that will be
 * followed on nine screens and forgotten on the tenth. As a component it is the only way to
 * render a clinical value at all — and `web/test/dual-unit.test.ts` fails a build where a
 * screen renders one directly, which is what turns a review checklist item into a test.
 *
 * # The visual treatment
 *
 * Primary prominent, secondary beneath in muted type, at a fixed relationship. It is not a
 * per-screen decision: two screens showing the same value with different emphasis is two
 * screens a clinician has to read differently, and the whole point of a shared component is
 * that a value looks the same wherever it appears — including on the printed prescription
 * (CP89), which imports the same pair-builder.
 *
 * Unknown units render the number alone rather than nothing. A screen that showed a blank
 * because a unit was added to the registry and not to the display table would be a screen
 * hiding a clinical value, which is worse than one showing it without a conversion.
 */
export function DualUnitValue({
  value,
  unit,
  code,
  label,
  size = 'md',
}: {
  /** The canonical value — `observation.value` from the API, in `observation.unit`. */
  value: number | null | undefined;
  /** The canonical unit — `observation.unit`. */
  unit: string;
  /**
   * The observation code. Matters for one thing: whether a centimetre value is spoken in
   * feet and inches (a height) or in plain inches (a waist).
   */
  code?: string;
  /** What this value is, when the surrounding layout does not already say. */
  label?: string;
  size?: 'sm' | 'md' | 'lg';
}) {
  const t = useTranslations('observations');
  const locale = useLocale();

  if (value === null || value === undefined || !Number.isFinite(value)) {
    // "Not recorded" and "zero" are different facts, and a dash that could be either is a
    // dash a clinician has to go and check.
    return (
      <span className="app-dual" data-size={size} data-testid="dual-unit">
        {label && <span className="app-dual__label">{label}</span>}
        <span className="app-dual__absent">{t('notRecorded')}</span>
      </span>
    );
  }

  const pair = dualUnit(value, unit, code);
  const language = locale === 'bn' ? 'bn' : 'en';

  return (
    <span className="app-dual" data-size={size} data-testid="dual-unit">
      {label && <span className="app-dual__label">{label}</span>}
      <span className="app-dual__primary">
        <span className="app-dual__number">{pair.primary.text}</span>
        <span className="app-dual__unit">{unitLabel(pair.primary.unit, language)}</span>
      </span>
      {pair.secondary && (
        <span className="app-dual__secondary" data-testid="dual-unit-secondary">
          {pair.secondary.text}
          {/* Feet and inches carry their marks in the text itself; everything else needs
              its unit written after the number. */}
          {pair.secondary.unit !== 'in' && ` ${unitLabel(pair.secondary.unit, language)}`}
        </span>
      )}
      {!pair.secondary && !hasSecondaryUnit(unit) && (
        // Nothing. The absence is deliberate — see DISPLAY_PAIRS — and a placeholder here
        // would suggest a conversion had failed.
        <></>
      )}
    </span>
  );
}

/**
 * Unit codes to what a person reads.
 *
 * The codes are UCUM (`[lb_av]`, `mm[Hg]`, `mL/min/{1.73_m2}`) because that is what an
 * interoperable record needs; nobody wants to read them. The mapping is here rather than
 * fetched with the units so a tablet with no signal still draws "kg" and not "kg" spelled as
 * a bracket expression.
 */
const UNIT_LABELS: Record<string, { en: string; bn: string }> = {
  cm: { en: 'cm', bn: 'সেমি' },
  m: { en: 'm', bn: 'মিটার' },
  in: { en: 'in', bn: 'ইঞ্চি' },
  '[ft_i]': { en: 'ft', bn: 'ফুট' },
  kg: { en: 'kg', bn: 'কেজি' },
  g: { en: 'g', bn: 'গ্রাম' },
  '[lb_av]': { en: 'lb', bn: 'পাউন্ড' },
  'mm[Hg]': { en: 'mmHg', bn: 'মিমি পারদ' },
  kPa: { en: 'kPa', bn: 'কিপিএ' },
  Cel: { en: '°C', bn: '°সে' },
  '[degF]': { en: '°F', bn: '°ফা' },
  '/min': { en: '/min', bn: '/মিনিট' },
  '%': { en: '%', bn: '%' },
  'kcal/d': { en: 'kcal/day', bn: 'কিলোক্যালরি/দিন' },
  'kg/m2': { en: 'kg/m²', bn: 'কেজি/মি²' },
  m2: { en: 'm²', bn: 'মি²' },
  '1': { en: '', bn: '' },
  'mL/min/{1.73_m2}': { en: 'mL/min/1.73m²', bn: 'মিলি/মিনিট/১.৭৩মি²' },
  'mmol/L': { en: 'mmol/L', bn: 'মিলিমোল/লি' },
  'mg/dL': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'mmol/L#chol': { en: 'mmol/L', bn: 'মিলিমোল/লি' },
  'mg/dL#chol': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'mmol/L#trig': { en: 'mmol/L', bn: 'মিলিমোল/লি' },
  'mg/dL#trig': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'umol/L': { en: 'µmol/L', bn: 'মাইক্রোমোল/লি' },
  'mg/dL#cr': { en: 'mg/dL', bn: 'মিগ্রা/ডেসিলি' },
  'mmol/mol': { en: 'mmol/mol', bn: 'মিলিমোল/মোল' },
  '%#ngsp': { en: '%', bn: '%' },
};

export function unitLabel(code: string, language: 'en' | 'bn'): string {
  const entry = UNIT_LABELS[code];
  // An unknown code renders as itself rather than as nothing: a bracket expression on screen
  // is a bug somebody reports, and a blank is a bug nobody notices.
  return entry ? entry[language] : code;
}
