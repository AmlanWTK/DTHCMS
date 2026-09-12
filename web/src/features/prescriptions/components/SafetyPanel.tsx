'use client';

import { useLocale, useTranslations } from 'next-intl';

import { Icon } from '@dthcms/ui';

import { bilingual } from '@/lib/bilingual';
import type { Locale } from '@/lib/i18n/config';

import type { SafetyResult } from '../api/prescriptions';

/**
 * The safety findings, as they are while the prescription is being written (CP78 in CP81's screen).
 *
 * # The single most important display decision in this checkpoint
 *
 * This clinic's rule library has forty-eight rules and **none of them is approved**, so every
 * check comes back `NO_RULES_APPROVED`. That is correct behaviour — CP77's whole design is that a
 * rule nobody has read cannot fire — and it means the panel a physician sees all day is the empty
 * one.
 *
 * An empty panel reads as a clean result. A green tick, a grey "no findings", a quiet space where
 * warnings would be: all three tell a physician that four medicines were checked and passed. None
 * of that happened. Nothing was checked.
 *
 * So `NO_RULES_APPROVED` is rendered as the **loudest state on the screen**, not the quietest:
 *
 *   - a bordered block in the warning treatment, not a grey note;
 *   - the sentence the server wrote, which ends "This is not a clean result — it is no result";
 *   - the count of medicines that went unchecked, stated as a number;
 *   - a line saying what would make it different — a physician approving a rule — and where;
 *   - **no tick, no green, and no word meaning safe anywhere in the component.**
 *
 * `prescriptions.test.tsx` fails if any of the words a physician could read as reassurance
 * appears while `rules_live` is zero. That test is the point of this file.
 *
 * # Why `CLEAR_WITHIN_COVERAGE` is also not green
 *
 * The best answer the engine can give is "every approved rule that was about these drugs was
 * satisfied", which is a statement about the library's coverage rather than about the patient.
 * `uncovered_count` sits beside it and is never hidden. The component's strongest visual state is
 * neutral, and it is deliberately not celebratory.
 */

export interface SafetyPanelProps {
  result?: SafetyResult;
  /** True while a check is in flight for the current set of lines. */
  checking: boolean;
  /** True when the last check failed. An engine that did not answer is not a clean engine. */
  failed: boolean;
  itemCount: number;
}

/**
 * The severities that get an outline of their own. A finding that only exists in a list is a
 * finding somebody scrolls past.
 */
const TONE: Record<string, string> = {
  BLOCK: 'app-safety__finding--block',
  WARN: 'app-safety__finding--warn',
  INFO: 'app-safety__finding--info',
};

export function SafetyPanel({ result, checking, failed, itemCount }: SafetyPanelProps) {
  const t = useTranslations('prescriptions');
  const locale = useLocale() as Locale;

  if (failed) {
    // An engine that did not answer is reported as an engine that did not answer. The
    // alternative — leaving the last good result on screen — would show findings for a
    // prescription that has since changed.
    return (
      <section className="app-safety app-safety--unchecked" aria-live="polite">
        <h3 className="app-safety__verdict">{t('safety.failedTitle')}</h3>
        <p className="app-safety__summary">{t('safety.failedBody')}</p>
      </section>
    );
  }

  if (!result) {
    return (
      <section className="app-safety" aria-live="polite">
        <h3 className="app-safety__verdict">
          {itemCount === 0 ? t('safety.nothingYet') : t('safety.running')}
        </h3>
        <p className="app-safety__summary">
          {itemCount === 0 ? t('safety.nothingYetBody') : t('safety.runningBody')}
        </p>
      </section>
    );
  }

  const nothingChecked = result.rules_live === 0;
  const blocked = result.verdict === 'BLOCKED';

  return (
    <section
      className={[
        'app-safety',
        nothingChecked ? 'app-safety--unchecked' : '',
        blocked ? 'app-safety--blocked' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      aria-live="polite"
      data-verdict={result.verdict}
      data-testid="safety-panel"
    >
      <h3 className="app-safety__verdict">
        <Icon name={nothingChecked || blocked ? 'octagon-alert' : 'shield-check'} size={18} />
        {t(`safety.verdict.${result.verdict}`)}
        {checking ? <span className="app-safety__pending">{t('safety.rechecking')}</span> : null}
      </h3>

      {/* The server's own sentence, in the reader's language. Written there rather than here so
          that a screen which renders only this line is still honest — and this is that screen. */}
      <p className="app-safety__summary">
        {t('safety.summary', { summary: pick(result.summary_en, result.summary_bn, locale) })}
      </p>

      {nothingChecked ? (
        <div className="app-safety__unchecked-detail">
          <p>{t('safety.noRules.what', { count: itemCount })}</p>
          <p>{t('safety.noRules.why', { total: result.rules_live })}</p>
          <p>{t('safety.noRules.how')}</p>
        </div>
      ) : null}

      {result.findings.length > 0 ? (
        <ul className="app-safety__findings">
          {result.findings.map((finding, index) => (
            <li
              key={`${finding.rule_code}-${index}`}
              className={['app-safety__finding', TONE[finding.severity] ?? ''].join(' ')}
            >
              <p className="app-safety__finding-head">
                <strong>{finding.rule_code}</strong>
                <span>{t(`safety.severity.${finding.severity}`)}</span>
                {finding.outcome === 'CANNOT_VERIFY' ? (
                  <span className="app-safety__cannot">{t('safety.cannotVerify')}</span>
                ) : null}
              </p>
              <p className="app-safety__finding-body">
                {pick(finding.message_en, finding.message_bn, locale)}
              </p>
              {pick(finding.advice_en ?? '', finding.advice_bn ?? '', locale) ? (
                <p className="app-safety__finding-advice">
                  {pick(finding.advice_en ?? '', finding.advice_bn ?? '', locale)}
                </p>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}

      {/* Coverage is always drawn, including on the empty library, because "no rule was about
          this drug" and "this drug passed" are opposite facts and neither is the absence of a
          row. */}
      {result.coverage.length > 0 ? (
        <ul className="app-safety__coverage">
          {result.coverage.map((entry) => (
            <li key={entry.ref} data-state={entry.state}>
              <span className="app-safety__coverage-label">{entry.label}</span>
              <span className="app-safety__coverage-state">
                {t(`safety.coverage.${entry.state}`, { rules: entry.rules_considered })}
              </span>
            </li>
          ))}
        </ul>
      ) : null}

      {result.uncovered_count > 0 ? (
        <p className="app-safety__uncovered">
          {t('safety.uncovered', { count: result.uncovered_count })}
        </p>
      ) : null}
    </section>
  );
}

/**
 * The server's own sentence in the reader's language.
 *
 * The component picks one of two; it never composes a sentence of its own. A screen that
 * assembled "N rules" + "found" + "issues" would be a second author of clinical wording, and the
 * second author is the one who eventually writes "no issues found".
 */
function pick(en: string, bn: string, locale: Locale): string {
  return bilingual(en, bn, locale)?.text ?? '';
}
