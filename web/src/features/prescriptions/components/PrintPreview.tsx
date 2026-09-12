'use client';

import { useLocale, useTranslations } from 'next-intl';

import type { Locale } from '@/lib/i18n/config';

import type { PrintModel } from '../api/prescriptions';

/**
 * The sheet as it will print (CP81 criterion 5).
 *
 * # This component decides nothing
 *
 * Every string it draws came resolved from `GET /v1/prescriptions/{id}/print-model`: the order of
 * the lines, the joined directions in each language, what is omitted and why, the price caveat,
 * the status warning. This file chooses type, spacing and where things sit on the page — and
 * nothing else.
 *
 * That is the whole contract with CP89, and it is what makes "the preview matches the printed
 * output" a property rather than a hope. CP89 renders the same model; if it needs something the
 * model does not carry, the fix is a field on the model and a version bump, because the moment
 * the print service reads the prescription directly there are two layouts again.
 *
 * `data-print-model-version` and `data-content-hash` are on the root element so a browser test can
 * assert that what was drawn is the model the server produced — and so that CP89's own proof can
 * compare the hash it rendered against the hash the preview showed.
 *
 * # Why both languages are on the sheet at once
 *
 * Because the paper is read by two people. The pharmacist reads the medicine and the directions in
 * English; the patient and the family read the instruction in Bangla. Switching the interface
 * language must not change what is printed — so the preview shows both, exactly as the paper will,
 * and only the surrounding furniture follows the reader.
 */
export function PrintPreview({ model }: { model?: PrintModel }) {
  const t = useTranslations('prescriptions');
  const locale = useLocale() as Locale;

  if (!model) {
    return (
      <section className="app-preview app-preview--empty">
        <p>{t('preview.loading')}</p>
      </section>
    );
  }

  const caveat = locale === 'bn' ? model.status_caveat_bn : model.status_caveat_en;

  return (
    <section
      className="app-preview"
      data-testid="print-preview"
      data-print-model-version={model.version}
      data-content-hash={model.content_hash}
      aria-label={t('preview.title')}
    >
      <header className="app-preview__header">
        <p className="app-preview__clinic">{t('preview.clinic')}</p>
        <p className="app-preview__clinic-note">{t('preview.clinicPlaceholder')}</p>
      </header>

      {caveat ? (
        <p className="app-preview__caveat" data-testid="preview-caveat">
          {caveat}
        </p>
      ) : null}

      <dl className="app-preview__patient">
        {model.patient.resolved ? (
          <>
            <div>
              <dt>{t('preview.patient')}</dt>
              <dd>
                {model.patient.name_en}
                {model.patient.name_bn ? ` · ${model.patient.name_bn}` : ''}
              </dd>
            </div>
            <div>
              <dt>{t('preview.clinicalId')}</dt>
              <dd className="app-preview__mono">{model.patient.clinical_id}</dd>
            </div>
            <div>
              <dt>{t('preview.ageSex')}</dt>
              <dd>
                {locale === 'bn' ? model.patient.age_text_bn : model.patient.age_text_en} ·{' '}
                {locale === 'bn' ? model.patient.sex_bn : model.patient.sex_en}
              </dd>
            </div>
            <div>
              <dt>{t('preview.writtenOn')}</dt>
              <dd className="app-preview__mono">{model.patient.written_on}</dd>
            </div>
          </>
        ) : (
          <div>
            <dt>{t('preview.patient')}</dt>
            <dd className="app-preview__unresolved">
              {locale === 'bn'
                ? model.patient.unresolved_note_bn
                : model.patient.unresolved_note_en}
            </dd>
          </div>
        )}
      </dl>

      {model.lines.length === 0 ? (
        <p className="app-preview__nothing">{t('preview.noLines')}</p>
      ) : (
        <ol className="app-preview__lines">
          {model.lines.map((line) => (
            <li key={line.item_id} className="app-preview__line">
              <p className="app-preview__medicine">
                <span className="app-preview__ordinal">{line.ordinal}.</span>
                <strong>{line.medicine}</strong>
                {line.generic ? <span className="app-preview__generic">{line.generic}</span> : null}
              </p>
              {/* Both, always, and never one chosen by the interface language: the pharmacist
                  and the patient read different halves of the same piece of paper. */}
              <p className="app-preview__directions">{line.directions_en}</p>
              <p className="app-preview__directions app-preview__directions--bn" lang="bn">
                {line.directions_bn}
              </p>
              {line.instruction_en ? (
                <p className="app-preview__instruction">{line.instruction_en}</p>
              ) : null}
              {line.instruction_bn ? (
                <p className="app-preview__instruction" lang="bn">
                  {line.instruction_bn}
                </p>
              ) : null}
              {line.price_text ? (
                <p className="app-preview__price">
                  {t('preview.price', { amount: line.price_text })}
                  {line.price_unverified ? ` · ${t('preview.priceUnchecked')}` : ''}
                </p>
              ) : (
                <p className="app-preview__price app-preview__price--none">
                  {t('preview.noPrice')}
                </p>
              )}
            </li>
          ))}
        </ol>
      )}

      {model.price.caveat_en ? (
        <p className="app-preview__price-caveat">
          {locale === 'bn' ? model.price.caveat_bn : model.price.caveat_en}
        </p>
      ) : null}

      {model.omitted.length > 0 ? (
        <ul className="app-preview__omitted" data-testid="preview-omitted">
          {model.omitted.map((entry) => (
            <li key={entry.item_id}>
              <s>{entry.medicine}</s> — {locale === 'bn' ? entry.reason_bn : entry.reason_en}
            </li>
          ))}
        </ul>
      ) : null}

      <footer className="app-preview__signature">
        <p>{locale === 'bn' ? model.signature.note_bn : model.signature.note_en}</p>
      </footer>
    </section>
  );
}
