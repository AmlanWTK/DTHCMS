import { useColorScheme } from 'react-native';

import { androidElevation, theme } from '@dthcms/design-tokens/nativewind';

/**
 * The design tokens, as the mobile app consumes them.
 *
 * NativeWind classes cover what does not change with the theme — spacing, radius, type
 * scale — straight from the generated Tailwind theme. Semantic colour does change with
 * the theme, and Tailwind class names cannot switch a palette at runtime, so colour
 * comes through this hook instead: the same generated module, the role picked by the
 * device's colour scheme.
 *
 * The discipline this preserves is CP09's: one token source feeds web, mobile and print.
 * Nothing in `mobile/src` may contain a colour literal — a test greps for them — so a
 * screen physically cannot drift from the other two surfaces.
 */

type ThemeColors = typeof theme.colors;

export type SemanticColors = ThemeColors['light'];
export type StatusColors = ThemeColors['status'];
export type ColorSchemeName = 'light' | 'dark';

/** The semantic palette for the active colour scheme. */
export function useTokens(): {
  scheme: ColorSchemeName;
  colors: SemanticColors;
  status: { [K in keyof StatusColors]: StatusColors[K]['light'] };
} {
  const scheme: ColorSchemeName = useColorScheme() === 'dark' ? 'dark' : 'light';

  const status = Object.fromEntries(
    Object.entries(theme.colors.status).map(([name, perScheme]) => [name, perScheme[scheme]]),
  ) as { [K in keyof StatusColors]: StatusColors[K]['light'] };

  return { scheme, colors: theme.colors[scheme] as SemanticColors, status };
}

export { theme, androidElevation };
