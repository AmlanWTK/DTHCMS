'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';

import { Button, Icon } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  approveInstructionTemplate,
  approvePrescribingDefault,
  listInstructionTemplates,
  listPrescribingDefaults,
} from '../api/prescriptions';

/**
 * Where the suggestions stop being suggestions (CP81, migration 00064).
 *
 * # Why this screen has to exist at all
 *
 * The dose defaults and the patient instructions ship unapproved, which is the right default and
 * is also a dead end unless there is somewhere to approve them. Without this screen the editor
 * would say "nobody has checked this" for ever, about content that a physician reading it for
 * twenty minutes could put his name to.
 *
 * # Why it is not inside the editor
 *
 * Approving a clinic-wide default is a different act from writing one prescription, and putting an
 * "approve this" button beside a dose a physician is about to prescribe would conflate them — the
 * fast answer to "is this dose right for this patient" is not an answer to "is this the dose this
 * clinic should suggest to everybody".
 *
 * # What is shown, and in what order
 *
 * Unapproved first, because that is the working list. Each row carries the guidance it was drafted
 * from and the reasoning in the reader's language, because the question being asked is whether to
 * agree with a clinical claim, and a claim with no source behind it cannot be agreed with.
 */
export function PrescribingDefaultsConsole() {
  const t = useTranslations('prescriptions');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();
  const mayApprove = usePermission('medicationRules.publish');

  const defaults = useQuery({
    queryKey: ['prescribing-defaults'],
    queryFn: listPrescribingDefaults,
  });
  const templates = useQuery({
    queryKey: ['instruction-templates'],
    queryFn: listInstructionTemplates,
  });

  const approveDefault = useMutation({
    mutationFn: approvePrescribingDefault,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['prescribing-defaults'] }),
  });
  const approveTemplate = useMutation({
    mutationFn: approveInstructionTemplate,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['instruction-templates'] }),
  });

  const rows = [...(defaults.data?.defaults ?? [])].sort(
    (a, b) =>
      Number(a.approval.approved) - Number(b.approval.approved) ||
      a.generic_name.localeCompare(b.generic_name),
  );
  const sentences = [...(templates.data?.templates ?? [])].sort(
    (a, b) => Number(a.approval.approved) - Number(b.approval.approved) || a.ordering - b.ordering,
  );

  return (
    <div className="app-stack">
      <section className="app-card">
        <h2 className="app-prescribe__heading">
          {t('defaults.title', {
            approved: defaults.data?.approved ?? 0,
            total: defaults.data?.total ?? 0,
          })}
        </h2>
        <p className="app-prescribe__note">{t('defaults.lede')}</p>
        <ul className="app-defaults">
          {rows.map((row) => (
            <li key={row.id} className="app-defaults__row" data-approved={row.approval.approved}>
              <div>
                <p className="app-defaults__drug">
                  <strong>{row.generic_name}</strong>
                  <span>{row.strength || t('defaults.anyStrength')}</span>
                </p>
                <p className="app-defaults__dose">
                  {row.dose} · {locale === 'bn' ? row.frequency_bn : row.frequency}
                  {row.duration_days ? ` · ${t('defaults.days', { days: row.duration_days })}` : ''}
                </p>
                <p className="app-defaults__why">
                  {locale === 'bn' ? row.rationale_bn : row.rationale_en}
                </p>
                <p className="app-defaults__source">{row.approval.source_citation}</p>
              </div>
              <div className="app-defaults__state">
                {row.approval.approved ? (
                  <span className="app-defaults__approved">
                    <Icon name="check" size={14} />
                    {t('defaults.approvedBy', { who: row.approval.approved_by_name ?? '' })}
                  </span>
                ) : (
                  <>
                    <span className="app-defaults__unapproved">{t('defaults.unapproved')}</span>
                    {mayApprove ? (
                      <Button
                        variant="secondary"
                        onClick={() => approveDefault.mutate(row.id)}
                        disabled={approveDefault.isPending}
                      >
                        {t('defaults.approve')}
                      </Button>
                    ) : null}
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      </section>

      <section className="app-card">
        <h2 className="app-prescribe__heading">
          {t('instructions.title', {
            approved: templates.data?.approved ?? 0,
            total: templates.data?.total ?? 0,
          })}
        </h2>
        <p className="app-prescribe__note">{t('instructions.lede')}</p>
        <ul className="app-defaults">
          {sentences.map((row) => (
            <li key={row.id} className="app-defaults__row" data-approved={row.approval.approved}>
              <div>
                <p className="app-defaults__drug">
                  <strong>{locale === 'bn' ? row.label_bn : row.label_en}</strong>
                  <span className="app-defaults__code">{row.code}</span>
                </p>
                {/* Both languages, always, and not the reader's own: what is being approved is
                    the sentence a patient will read, and half the clinic's patients read the
                    other one. */}
                <p className="app-defaults__why">{row.text_en}</p>
                <p className="app-defaults__why" lang="bn">
                  {row.text_bn}
                </p>
                <p className="app-defaults__source">{row.approval.source_citation}</p>
              </div>
              <div className="app-defaults__state">
                {row.approval.approved ? (
                  <span className="app-defaults__approved">
                    <Icon name="check" size={14} />
                    {t('defaults.approvedBy', { who: row.approval.approved_by_name ?? '' })}
                  </span>
                ) : (
                  <>
                    <span className="app-defaults__unapproved">{t('defaults.unapproved')}</span>
                    {mayApprove ? (
                      <Button
                        variant="secondary"
                        onClick={() => approveTemplate.mutate(row.id)}
                        disabled={approveTemplate.isPending}
                      >
                        {t('defaults.approve')}
                      </Button>
                    ) : null}
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
