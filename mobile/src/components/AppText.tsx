import { Text, type TextProps } from 'react-native';

import { theme } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

/**
 * The Text every screen uses.
 *
 * This component is where CP09's bilingual typography rule lives on mobile: the font
 * family and the line height switch together with the language, never separately.
 * Bengali glyphs hang from a headstroke and conjuncts drop below the baseline, so at
 * Latin leading the matras of one line touch the descenders of the line above —
 * legible in a sentence, unreadable in a dense queue list.
 *
 * `variant="clinicalValue"` pins the Latin face and tabular-style figures regardless of
 * interface language: a glucose reading must look identical in both interfaces, because
 * digits that change shape with the language are digits somebody transcribes wrongly
 * onto a paper chart.
 */

type Step = keyof typeof theme.fontSize;

export interface AppTextProps extends TextProps {
  size?: Step;
  weight?: keyof typeof theme.fontWeight;
  variant?: 'ui' | 'clinicalValue';
}

const FAMILY_BY_WEIGHT: Record<string, { latin: string; bengali: string }> = {
  '400': { latin: 'Inter', bengali: 'NotoSansBengali' },
  '500': { latin: 'Inter-Medium', bengali: 'NotoSansBengali-Medium' },
  '600': { latin: 'Inter-SemiBold', bengali: 'NotoSansBengali-SemiBold' },
  '700': { latin: 'Inter-Bold', bengali: 'NotoSansBengali-Bold' },
};

export function AppText({
  size = 'base',
  weight = 'regular',
  variant = 'ui',
  style,
  ...rest
}: AppTextProps) {
  const language = usePreferences((state) => state.language);

  const script = variant === 'clinicalValue' || language === 'en' ? 'latin' : 'bengali';
  const resolvedWeight = String(theme.fontWeight[weight]);
  const family = FAMILY_BY_WEIGHT[resolvedWeight] ?? FAMILY_BY_WEIGHT['400']!;

  const fontSize = theme.fontSize[size];
  const leading = theme.lineHeight[size][script === 'latin' ? 'latin' : 'bengali'];

  return (
    <Text
      {...rest}
      style={[
        {
          fontFamily: family[script],
          fontSize,
          // RN takes an absolute line height, not a ratio.
          lineHeight: Math.round(fontSize * leading),
          ...(variant === 'clinicalValue' ? { fontVariant: ['tabular-nums'] as const } : {}),
        },
        style,
      ]}
    />
  );
}
