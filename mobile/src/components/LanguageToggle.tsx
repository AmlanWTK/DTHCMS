import { Pressable, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { theme, useTokens } from '@/lib/tokens';
import { usePreferences, type Language } from '@/stores/preferences';
import { AppText } from '@/components/AppText';

/**
 * The language switch — same reasoning as the web shell's.
 *
 * Both options visible at once, each labelled in its own language: a person who has
 * landed in the wrong language is exactly the person least able to find a control
 * labelled in it.
 */

const LANGUAGE_NAMES: Record<Language, string> = { en: 'English', bn: 'বাংলা' };

export function LanguageToggle() {
  const t = useTranslations('language');
  const active = usePreferences((state) => state.language);
  const setLanguage = usePreferences((state) => state.setLanguage);
  const { colors } = useTokens();

  return (
    <View accessibilityRole="radiogroup" accessibilityLabel={t('label')} className="flex-row gap-2">
      {(Object.keys(LANGUAGE_NAMES) as Language[]).map((language) => {
        const selected = language === active;
        return (
          <Pressable
            key={language}
            accessibilityRole="radio"
            accessibilityState={{ selected }}
            accessibilityLabel={t('switchTo', { language: LANGUAGE_NAMES[language] })}
            onPress={() => setLanguage(language)}
            style={{
              minHeight: theme.size.touchTarget,
              minWidth: theme.size.touchTarget,
              paddingHorizontal: theme.spacing['4'],
              borderRadius: theme.borderRadius.md,
              alignItems: 'center',
              justifyContent: 'center',
              backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
              borderWidth: 1,
              borderColor: selected ? colors.brand.border : colors.border.subtle,
            }}
          >
            <AppText
              weight={selected ? 'semibold' : 'regular'}
              style={{ color: selected ? colors.brand.text : colors.text.secondary }}
            >
              {LANGUAGE_NAMES[language]}
            </AppText>
          </Pressable>
        );
      })}
    </View>
  );
}
