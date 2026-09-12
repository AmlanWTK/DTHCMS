'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useRef, useState } from 'react';

import { AlertBanner, Button, Card, EmptyState } from '@dthcms/ui';

import { ApiError } from '@/lib/api';
import { formatCount } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  FORMULARY_KEY,
  REVIEW_KEY,
  acceptedRows,
  rejectedRows,
  runImport,
  type FormularyImport,
} from '../api/formulary';

/**
 * Uploading a price list, and reading back what happened to every line (CP75 criterion 2).
 *
 * # Two buttons, and the safe one is on the left
 *
 * **Check the file** runs a dry run: every line is validated, the whole report is written, and
 * not one price moves. **Apply** does it for real, and it is only offered *after* a check has
 * been run — a screen whose first button wrote 250 prices would make the dry run something
 * somebody had to remember to ask for, which is the same as not having one.
 *
 * # The rejections are the screen
 *
 * They are drawn first, largest, with the line number from the person's own spreadsheet and the
 * message in their own language. The accepted lines are a count, because nobody reads two hundred
 * rows that worked.
 *
 * This is deliberately not a summary that says "6 rows failed". A person cannot act on that; they
 * can act on "line 5: 0.335 has more precision than one poisha".
 *
 * # Partial is the point, and it is said out loud
 *
 * The good lines import even when others fail. That is stated on the panel rather than left to be
 * discovered, because a pharmacist who assumed all-or-nothing and saw six errors would re-upload
 * the whole corrected file — and the second upload would be a no-op for the rows that had already
 * landed, which is fine, but they would not know that either.
 */
export function ImportPanel() {
  const t = useTranslations('formulary');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();

  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [report, setReport] = useState<FormularyImport | null>(null);

  const upload = useMutation({
    mutationFn: (apply: boolean) => runImport({ file: file as File, apply, verified: confirmed }),
    onSuccess: (result) => {
      setReport(result);
      if (result.mode === 'APPLY') {
        void queryClient.invalidateQueries({ queryKey: FORMULARY_KEY });
        void queryClient.invalidateQueries({ queryKey: REVIEW_KEY });
      }
    },
  });

  const failure = upload.error instanceof ApiError ? upload.error : null;
  const checked = report?.mode === 'DRY_RUN';
  const rejected = report ? rejectedRows(report) : [];

  return (
    <Card className="formulary-import">
      <h2 className="app-card__title">{t('import.title')}</h2>
      <p className="formulary-import__explain">{t('import.explain')}</p>
      <p className="formulary-import__columns">
        <code>{t('import.columns')}</code>
      </p>

      <div className="formulary-import__controls">
        <input
          ref={inputRef}
          type="file"
          accept=".csv,text/csv"
          aria-label={t('import.choose')}
          onChange={(event) => {
            setFile(event.target.files?.[0] ?? null);
            setReport(null);
            upload.reset();
          }}
        />
        <label className="formulary-priceform__check">
          <input
            type="checkbox"
            checked={confirmed}
            onChange={(event) => setConfirmed(event.target.checked)}
          />
          <span>{t('import.confirm')}</span>
        </label>
        <p className="formulary-priceform__help">{t('import.confirmHelp')}</p>

        <div className="formulary-import__buttons">
          <Button
            variant="secondary"
            disabled={!file || upload.isPending}
            onClick={() => upload.mutate(false)}
          >
            {upload.isPending && !checked ? t('import.checking') : t('import.check')}
          </Button>
          {/* Only after a check. See the note above. */}
          {checked ? (
            <Button disabled={upload.isPending} onClick={() => upload.mutate(true)}>
              {t('import.apply', { count: formatCount(report.rows_accepted, locale) })}
            </Button>
          ) : null}
        </div>
      </div>

      {failure ? (
        <AlertBanner tone="critical" title={t('import.refused')}>
          {locale === 'bn'
            ? (failure.fieldsBN.file ?? failure.messageBN)
            : (failure.fields.file ?? failure.messageEN)}
        </AlertBanner>
      ) : null}

      {report ? (
        <div className="formulary-import__report">
          <p className="formulary-import__summary">
            {t(report.mode === 'DRY_RUN' ? 'import.summaryDry' : 'import.summaryApplied', {
              file: report.filename,
              accepted: formatCount(report.rows_accepted, locale),
              rejected: formatCount(report.rows_rejected, locale),
              total: formatCount(report.rows_total, locale),
            })}
          </p>

          {rejected.length > 0 ? (
            <>
              <h3 className="formulary-import__heading">
                {t('import.rejectedHeading', { count: formatCount(rejected.length, locale) })}
              </h3>
              {/* The good rows still went in — said here, beside the failures, because this is
                  the moment somebody decides whether to re-upload the whole file. */}
              <p className="formulary-import__partial">
                {t(report.mode === 'DRY_RUN' ? 'import.partialDry' : 'import.partialApplied', {
                  count: formatCount(acceptedRows(report).length, locale),
                })}
              </p>
              <div className="app-table-wrap">
                <table className="app-table formulary-import__rows">
                  <thead>
                    <tr>
                      <th scope="col">{t('import.line')}</th>
                      <th scope="col">{t('import.what')}</th>
                      <th scope="col">{t('import.column')}</th>
                      <th scope="col">{t('import.problem')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rejected.map((row) => (
                      <tr key={row.line}>
                        <th scope="row" className="formulary-import__line">
                          {row.line}
                        </th>
                        <td lang="en">{row.trade_name ?? '—'}</td>
                        <td>
                          <code>{row.field ?? '—'}</code>
                        </td>
                        <td>
                          {/* The server sends both languages on every rejection and a database
                              constraint refuses one that does not. This picks the reader's. */}
                          {locale === 'bn' ? row.message_bn : row.message_en}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          ) : (
            <EmptyState icon="check" title={t('import.allGood')}>
              {t('import.allGoodBody', { count: formatCount(report.rows_accepted, locale) })}
            </EmptyState>
          )}
        </div>
      ) : null}
    </Card>
  );
}
