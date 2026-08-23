/**
 * The DTHCMS design tokens: one source, resolved once, consumed by web, mobile and print.
 *
 * Acceptance criterion 1 of CP09 is "one token source feeds web, mobile and print". That
 * is enforced structurally rather than by convention: the JSON files under tokens/ are the
 * only place a value is written down, this module is the only thing that reads them, and
 * every output artefact is generated from what this module exports. A hex code in a
 * component, a spacing value in a stylesheet, or a second copy of the scale in the mobile
 * theme would all be visible in review as something that did not come from here.
 */

import colorSource from './tokens/color.json' with { type: 'json' };
import semanticSource from './tokens/semantic.json' with { type: 'json' };
import typographySource from './tokens/typography.json' with { type: 'json' };
import layoutSource from './tokens/layout.json' with { type: 'json' };
import motionSource from './tokens/motion.json' with { type: 'json' };
import elevationSource from './tokens/elevation.json' with { type: 'json' };

import { oklchToRgb, toHex } from './color/space.js';
import { bestForeground } from './color/contrast.js';

export * from './color/space.js';
export * from './color/contrast.js';
export * from './color/vision.js';

// ---------------------------------------------------------------------------
// Ramps
// ---------------------------------------------------------------------------

export type RampStep =
  '50' | '100' | '200' | '300' | '400' | '500' | '600' | '700' | '800' | '900' | '950';

export type Ramp = Record<RampStep, string>;

const RAMP_STEPS = Object.keys(colorSource.ramp.steps) as RampStep[];

/**
 * Builds a ramp from one hue and one chroma ceiling.
 *
 * This is what makes "changing the brand is one number" true rather than aspirational.
 * A hand-picked ramp has to be re-checked by eye and re-tested for contrast after any
 * change; a generated one lands on the same lightness targets every time, so the
 * contrast assertions in the test suite hold for whatever hue is chosen.
 */
export function buildRamp(hue: number, chromaCeiling: number): Ramp {
  const ramp = {} as Ramp;

  for (const step of RAMP_STEPS) {
    const spec = colorSource.ramp.steps[step];
    ramp[step] = toHex(oklchToRgb({ l: spec.l, c: chromaCeiling * spec.chroma, h: hue }));
  }

  return ramp;
}

export type ClinicalHue = keyof typeof colorSource.clinical;

const CLINICAL_HUES = Object.keys(colorSource.clinical).filter(
  (key) => !key.startsWith('$'),
) as ClinicalHue[];

export const ramps = {
  brand: buildRamp(colorSource.brand.hue, colorSource.brand.chroma),
  neutral: buildRamp(colorSource.neutral.hue, colorSource.neutral.chroma),
  ...(Object.fromEntries(
    CLINICAL_HUES.map((name) => {
      // The $comment entries in the JSON make this an index type union with string[].
      // Narrowing here rather than stripping the comments: a token file whose reasoning
      // lives somewhere else is a token file whose reasoning gets lost.
      const spec = colorSource.clinical[name] as { hue: number; chroma: number };
      return [name, buildRamp(spec.hue, spec.chroma)];
    }),
  ) as Record<ClinicalHue, Ramp>),
} as const;

export type RampName = keyof typeof ramps;

// ---------------------------------------------------------------------------
// Semantic roles
// ---------------------------------------------------------------------------

export type Theme = 'light' | 'dark';

/** Resolves a reference such as "neutral.200" or "absolute.white" to a hex value. */
export function resolveReference(reference: string): string {
  const [rampName, step] = reference.split('.');

  if (rampName === 'absolute') {
    const value = colorSource.absolute[step as 'white' | 'black'];
    if (value === undefined) {
      throw new Error(`unknown absolute colour "${reference}"`);
    }
    return value;
  }

  const ramp = ramps[rampName as RampName];
  if (ramp === undefined) {
    throw new Error(`unknown ramp "${rampName}" in reference "${reference}"`);
  }

  const value = ramp[step as RampStep];
  if (value === undefined) {
    throw new Error(`unknown step "${step}" in reference "${reference}"`);
  }
  return value;
}

type RoleGroup = Record<string, string>;
type ResolvedTheme = Record<string, RoleGroup>;

/**
 * The precise shape of a resolved theme, derived from the JSON.
 *
 * Without this, every consumer indexes into Record<string, Record<string, string>> and —
 * with noUncheckedIndexedAccess on, which it should be — has to null-check
 * `themes.light.surface.base`. Nobody does that more than twice before reaching for a
 * hex code instead, which is the one thing this package exists to prevent. A precise type
 * also means a typo in a role name is a compile error rather than `undefined` painted as
 * a background colour.
 */
type MetaKey = `$${string}`;
type Roles<T> = { [K in Exclude<keyof T, MetaKey>]: string };
export type ThemeRoles = {
  [G in Exclude<keyof typeof semanticSource.light, MetaKey>]: Roles<
    (typeof semanticSource.light)[G]
  >;
};

/**
 * Foregrounds that may be chosen automatically.
 *
 * White and the darkest neutral rather than white and pure black: pure black on a
 * saturated colour vibrates unpleasantly, and neutral.950 is indistinguishable from black
 * at a glance while sitting in the same family as every other text colour.
 */
const AUTO_FOREGROUNDS = [colorSource.absolute.white, ramps.neutral['950']] as const;

const AUTO_PREFIX = 'auto-contrast:';

/**
 * Resolves one theme in two passes.
 *
 * The second pass exists for `auto-contrast:` references, which name another role rather
 * than a ramp step and therefore cannot be resolved until that role has a value. Two
 * passes rather than a dependency graph: one level of indirection is all this needs, and
 * a reference chain deeper than that would be a sign the token set had become clever.
 */
function resolveTheme(source: Record<string, unknown>): ResolvedTheme {
  const resolved: ResolvedTheme = {};
  const deferred: Array<{ group: string; role: string; target: string }> = [];

  for (const [group, roles] of Object.entries(source)) {
    if (group.startsWith('$') || typeof roles !== 'object' || roles === null) continue;

    const resolvedGroup: RoleGroup = {};
    for (const [role, reference] of Object.entries(roles as Record<string, unknown>)) {
      if (role.startsWith('$') || typeof reference !== 'string') continue;

      if (reference.startsWith(AUTO_PREFIX)) {
        deferred.push({ group, role, target: reference.slice(AUTO_PREFIX.length) });
        continue;
      }
      resolvedGroup[role] = resolveReference(reference);
    }
    resolved[group] = resolvedGroup;
  }

  for (const { group, role, target } of deferred) {
    const [targetGroup, targetRole] = target.split('.');
    const background = resolved[targetGroup ?? '']?.[targetRole ?? ''];
    if (background === undefined) {
      throw new Error(`auto-contrast target "${target}" for ${group}.${role} does not resolve`);
    }
    const holder = resolved[group];
    if (holder === undefined) {
      throw new Error(`auto-contrast group "${group}" does not exist`);
    }
    holder[role] = bestForeground(background, AUTO_FOREGROUNDS);
  }

  return resolved;
}

export const themes: Record<Theme, ThemeRoles> = {
  light: resolveTheme(semanticSource.light as Record<string, unknown>) as unknown as ThemeRoles,
  dark: resolveTheme(semanticSource.dark as Record<string, unknown>) as unknown as ThemeRoles,
};

/**
 * The same data, indexable by string.
 *
 * For the contrast test, which walks the contract in semantic.json and therefore holds
 * role names as strings. Kept separate so that a loose lookup is a deliberate choice at
 * the one place it is needed, rather than the type every consumer gets.
 */
export const themesByName: Record<Theme, ResolvedTheme> = themes as unknown as Record<
  Theme,
  ResolvedTheme
>;

// ---------------------------------------------------------------------------
// Clinical status
// ---------------------------------------------------------------------------

export type ClinicalStatusName = Exclude<keyof typeof semanticSource.clinicalStatus, '$comment'>;

export interface BilingualText {
  en: string;
  bn: string;
}

/**
 * A clinical status, with everything needed to render it accessibly.
 *
 * The icon and the label are not optional and cannot be made optional. That is the
 * mechanism behind acceptance criterion 4: a component receiving one of these has an
 * icon and a label available, so rendering the colour alone requires deliberately
 * discarding them rather than merely forgetting.
 */
export interface ClinicalStatus {
  name: ClinicalStatusName;
  icon: string;
  priority: number;
  label: BilingualText;
  description: BilingualText;
  /**
   * Colour roles per theme.
   *
   * onSolid is computed rather than declared: a badge filled with the status colour needs
   * a foreground that is readable against *that* colour, and which of white or near-black
   * wins differs between amber and red.
   */
  colors: Record<
    Theme,
    Record<'surface' | 'border' | 'solid' | 'onSolid' | 'text' | 'icon', string>
  >;
}

const STATUS_NAMES = Object.keys(semanticSource.clinicalStatus).filter(
  (key) => !key.startsWith('$'),
) as ClinicalStatusName[];

function buildStatus(name: ClinicalStatusName): ClinicalStatus {
  const source = semanticSource.clinicalStatus[name] as {
    hue: ClinicalHue;
    icon: string;
    priority: number;
    label: BilingualText;
    description: BilingualText;
  };

  const ramp = ramps[source.hue];
  const mapping = semanticSource.statusRamp;

  const colorsFor = (theme: Theme) => {
    const steps = mapping[theme];
    const solid = ramp[steps.solid as RampStep];
    return {
      surface: ramp[steps.surface as RampStep],
      border: ramp[steps.border as RampStep],
      solid,
      onSolid: bestForeground(solid, AUTO_FOREGROUNDS),
      text: ramp[steps.text as RampStep],
      icon: ramp[steps.icon as RampStep],
    };
  };

  return {
    name,
    icon: source.icon,
    priority: source.priority,
    label: source.label,
    description: source.description,
    colors: { light: colorsFor('light'), dark: colorsFor('dark') },
  };
}

export const clinicalStatuses: Record<ClinicalStatusName, ClinicalStatus> = Object.fromEntries(
  STATUS_NAMES.map((name) => [name, buildStatus(name)]),
) as Record<ClinicalStatusName, ClinicalStatus>;

export const clinicalStatusNames: readonly ClinicalStatusName[] = STATUS_NAMES;

// ---------------------------------------------------------------------------
// Everything else
// ---------------------------------------------------------------------------

export type Script = 'latin' | 'bengali';
export type Language = 'en' | 'bn';

/** The script a language is written in. Bengali interfaces still render Latin numerals. */
export const scriptForLanguage: Record<Language, Script> = {
  en: 'latin',
  bn: 'bengali',
};

export const typography = typographySource;
export const layout = layoutSource;
export const motion = motionSource;
export const elevation = elevationSource;
export const brandSource = colorSource.brand;

export type TypeStep = Exclude<keyof typeof typographySource.scale, '$comment'>;
export type TypeRole = Exclude<keyof typeof typographySource.role, '$comment'>;

export const typeSteps = Object.keys(typographySource.scale).filter(
  (key) => !key.startsWith('$'),
) as TypeStep[];

export const typeRoles = Object.keys(typographySource.role).filter(
  (key) => !key.startsWith('$'),
) as TypeRole[];

/**
 * Resolves a type role into the values a renderer needs, for one script.
 *
 * `family: "auto"` means "follow the interface language"; a role that pins a family —
 * clinicalValue does — keeps it regardless, which is why a glucose reading looks the
 * same in a Bengali interface as in an English one.
 */
export function resolveTypeRole(
  role: TypeRole,
  script: Script,
): {
  fontSize: number;
  lineHeight: number;
  letterSpacing: number;
  fontWeight: number;
  fontFamily: readonly string[];
  tabular: boolean;
} {
  const spec = typographySource.role[role] as {
    step: TypeStep;
    weight: keyof typeof typographySource.weight;
    family: 'auto' | 'latin' | 'bengali' | 'mono';
    tabular?: boolean;
  };

  const step = typographySource.scale[spec.step] as {
    size: number;
    lineHeight: { latin: number; bengali: number };
    tracking: number;
  };

  const family = spec.family === 'auto' ? script : spec.family;
  const lineHeightScript = family === 'bengali' ? 'bengali' : 'latin';

  return {
    fontSize: step.size,
    lineHeight: step.lineHeight[lineHeightScript],
    letterSpacing: step.tracking,
    fontWeight: typographySource.weight[spec.weight] as number,
    fontFamily: typographySource.family[family].stack,
    tabular: spec.tabular === true,
  };
}
