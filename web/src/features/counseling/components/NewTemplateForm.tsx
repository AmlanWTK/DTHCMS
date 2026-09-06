'use client';

import { useMutation } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState, type FormEvent } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import {
  createCounselingTemplate,
  suggestItemCode,
  type CounselingTemplate,
} from '../api/counseling';

/**
 * Start a new checklist — acceptance criterion 1, in one form (CP55).
 *
 * The whole checkpoint reduces to this working: Dr. Nahid needs a thyroid checklist, and
 * nobody writes any code. Three fields, because the server takes three, and the server takes
 * a title in **both** languages at creation rather than at publication: a template named only
 * in English is one half the counsellors cannot find in a list, and the first moment somebody
 * is thinking about what to call it is the cheapest moment to ask for both.
 *
 * The code is separate from the title and is upper-cased as it is typed, because it is not a
 * name — it is what an assignment rule matches and what an audit entry says, and it does not
 * change when somebody rewords the title. It is suggested from the English title so that a
 * physician need not invent `THYROID`, and it stays editable so that they can.
 */
export interface NewTemplateFormProps {
  onCreated?: (template: CounselingTemplate) => void;
  onCancel?: () => void;
}

export function NewTemplateForm({ onCreated, onCancel }: NewTemplateFormProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;

  const [code, setCode] = useState('');
  const [codeTouched, setCodeTouched] = useState(false);
  const [titleEN, setTitleEN] = useState('');
  const [titleBN, setTitleBN] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: createCounselingTemplate,
    onSuccess: (template) => onCreated?.(template),
    onError: (error: unknown) => {
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        if (Object.keys(named).length > 0) {
          setFields(named);
          setRefusal(null);
          return;
        }
      }
      setRefusal(t('new.failed'));
    },
  });

  function changeTitleEN(value: string) {
    setTitleEN(value);
    // Only while the author has not written a code of their own. Overwriting one they typed
    // would silently rename the thing every future assignment rule and audit entry points at.
    if (!codeTouched) setCode(suggestItemCode(value));
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (create.isPending) return;

    const missing: Record<string, string> = {};
    if (code.trim() === '') missing.code = t('new.required.code');
    if (titleEN.trim() === '') missing.title_en = t('new.required.title_en');
    if (titleBN.trim() === '') missing.title_bn = t('new.required.title_bn');
    if (Object.keys(missing).length > 0) {
      setFields(missing);
      setRefusal(null);
      return;
    }

    setFields({});
    setRefusal(null);
    create.mutate({
      code: code.trim().toUpperCase(),
      title_en: titleEN.trim(),
      title_bn: titleBN.trim(),
    });
  }

  return (
    <Card elevation="raised" className="app-counseling-form">
      <form onSubmit={submit} noValidate aria-label={t('new.title')}>
        <h3>{t('new.title')}</h3>
        <p className="app-page__description">{t('new.body')}</p>

        {refusal && <AlertBanner tone="critical" title={refusal} />}

        <Input
          label={t('new.titleEN')}
          description={t('new.titleENHint')}
          value={titleEN}
          error={fields.title_en}
          required
          data-testid="new-template-title-en"
          onChange={(event) => changeTitleEN(event.target.value)}
        />

        <Input
          label={t('new.titleBN')}
          description={t('new.titleBNHint')}
          lang="bn"
          value={titleBN}
          error={fields.title_bn}
          required
          data-testid="new-template-title-bn"
          onChange={(event) => setTitleBN(event.target.value)}
        />

        <Input
          label={t('new.code')}
          description={t('new.codeHint')}
          placeholder="THYROID"
          value={code}
          error={fields.code}
          required
          spellCheck={false}
          data-testid="new-template-code"
          onChange={(event) => {
            setCodeTouched(true);
            setCode(event.target.value.toUpperCase());
          }}
        />

        <div className="app-actions">
          <Button
            variant="primary"
            type="submit"
            loading={create.isPending}
            data-testid="new-template-create"
          >
            {t('new.create')}
          </Button>
          {onCancel && (
            <Button variant="quiet" type="button" disabled={create.isPending} onClick={onCancel}>
              {t('cancel')}
            </Button>
          )}
        </div>
      </form>
    </Card>
  );
}
