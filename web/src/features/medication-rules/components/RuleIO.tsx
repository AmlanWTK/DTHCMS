'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useRef, useState } from 'react';

import { AlertBanner, Button, Card } from '@dthcms/ui';

import { exportRules, importRules, RULES_KEY, type ImportReport } from '../api/rules';

/**
 * Import and export, for reviewing the library away from the screen (CP77).
 *
 * # Why this exists at all
 *
 * Reviewing forty rules on a web page is not how a physician reviews forty rules. He wants them
 * in a file he can read on a plane, mark up, and send to a colleague at BIRDEM to argue with.
 *
 * # Everything imported arrives unapproved, and the panel says so before the file is chosen
 *
 * A file is a thing anybody can edit. The import reads the approval fields and ignores them, and
 * the report names what it ignored — because a physician who exported an approved rule and
 * imported it back would otherwise reasonably expect it to still be approved.
 *
 * # The dry run is the default and is a separate button
 *
 * Not a checkbox that could be left ticked. "Check the file" and "apply it" are different
 * decisions about somebody else's document.
 */
export function RuleIO() {
  const t = useTranslations('medicationRules');
  const queryClient = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [text, setText] = useState('');

  const download = useMutation({
    mutationFn: exportRules,
    onSuccess: (doc) => setText(JSON.stringify(doc, null, 2)),
  });

  const run = useMutation({
    mutationFn: ({ apply }: { apply: boolean }) => importRules(JSON.parse(text), apply),
    onSuccess: (result) => {
      setReport(result);
      if (!result.dry_run) void queryClient.invalidateQueries({ queryKey: RULES_KEY });
    },
  });

  return (
    <Card>
      <h3>{t('io.title')}</h3>

      <div className="rules-actions">
        <Button variant="secondary" onClick={() => download.mutate()}>
          {t('io.export')}
        </Button>
        <input
          ref={fileInput}
          type="file"
          accept="application/json"
          aria-label={t('io.import')}
          onChange={async (e) => {
            const file = e.target.files?.[0];
            if (file) setText(await file.text());
          }}
        />
      </div>
      <p className="app-page__description">{t('io.exportHelp')}</p>
      <p className="app-page__description">{t('io.importHelp')}</p>

      <textarea
        className="rules-io-text"
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={8}
        aria-label={t('io.title')}
      />

      <div className="rules-actions">
        <Button
          variant="secondary"
          onClick={() => run.mutate({ apply: false })}
          disabled={!text.trim()}
        >
          {t('io.dryRun')}
        </Button>
        <Button onClick={() => run.mutate({ apply: true })} disabled={!text.trim()}>
          {t('io.apply')}
        </Button>
      </div>

      {report ? (
        <section className="rules-report">
          <AlertBanner tone={report.rejected > 0 ? 'borderline' : 'info'} title={t('io.report')}>
            <p>
              {t('io.accepted')}: {report.accepted} · {t('io.rejected')}: {report.rejected} ·{' '}
              {t('io.skipped')}: {report.skipped}
            </p>
            <p>
              {t('io.ignored')}: {report.ignored_fields_en.join(', ')}
            </p>
          </AlertBanner>
          <ul className="rules-outcomes">
            {report.outcomes
              .filter((o) => o.result === 'REJECTED')
              .map((o) => (
                <li key={`${o.line}-${o.code}`}>
                  <code>
                    {o.line}. {o.code}
                  </code>{' '}
                  {o.reason_en}
                </li>
              ))}
          </ul>
        </section>
      ) : null}
    </Card>
  );
}
