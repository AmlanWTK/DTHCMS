'use client';

import { useTransition } from 'react';
import { useLocale, useTranslations } from 'next-intl';

import { Button } from '@dthcms/ui';

import { setLocale } from '@/lib/i18n/actions';
import { LOCALES, LOCALE_NAMES, type Locale } from '@/lib/i18n/config';

/**
 * The language switch.
 *
 * Every option is shown at once rather than hidden behind a dropdown. There are two, the
 * clinic uses both every day, and a person who has landed in the wrong language is
 * exactly the person least able to find a control labelled in it.
 *
 * Each option is labelled in its own language — "English", "বাংলা" — because a label
 * reading "Bengali" is no use to someone who cannot read English.
 */
export function LanguageToggle() {
  const active = useLocale() as Locale;
  const t = useTranslations('language');
  const [pending, startTransition] = useTransition();

  return (
    <div className="app-language-toggle" role="group" aria-label={t('label')}>
      {LOCALES.map((locale) => (
        <Button
          key={locale}
          size="sm"
          variant={locale === active ? 'secondary' : 'quiet'}
          aria-pressed={locale === active}
          aria-label={t('switchTo', { language: LOCALE_NAMES[locale] })}
          disabled={pending}
          lang={locale}
          onClick={() => {
            if (locale === active) return;
            startTransition(async () => {
              await setLocale(locale);
            });
          }}
        >
          {LOCALE_NAMES[locale]}
        </Button>
      ))}
    </div>
  );
}
