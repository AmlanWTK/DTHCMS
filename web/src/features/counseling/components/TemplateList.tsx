'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Badge, Button, Card, EmptyState, Skeleton } from '@dthcms/ui';

import { formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  COUNSELING_TEMPLATES_KEY,
  contentApproved,
  listCounselingTemplates,
  type CounselingTemplate,
} from '../api/counseling';

import { NewTemplateForm } from './NewTemplateForm';
import { templateTitle } from './counselingText';

/**
 * Every checklist the clinic has, and what state each one is in (CP55, §5.1, [R-07]).
 *
 * The screen a physician opens to find their own work. Four facts per row, and each is here
 * because leaving it out produces a specific wrong belief:
 *
 *  - **The live version number.** Without it, a physician who published version 3 last month
 *    and drafted version 4 last week cannot tell which one a counsellor is reading today.
 *  - **Whether anything is published at all.** A template with no published version is not a
 *    broken row; it is work in progress, and it belongs in this list precisely so its author
 *    can get back to it. Drawn as its own state rather than as a blank version column.
 *  - **The draft count.** An open draft is unfinished business. A checklist that looks
 *    settled while somebody has half a revision open is how two people end up editing it.
 *  - **Whether a clinician has approved the content.** D-53 is open, and the seeded diabetes
 *    checklist is the reason this column exists: its seven items are transcribed from §5.1
 *    and are the launch minimum, not a clinical author's list. It is published, it is live,
 *    and it is a proposal — and a screen that showed only "live" would present it as settled.
 *
 * Starting a new checklist is on this screen rather than behind a menu, because that is
 * acceptance criterion 1: Dr. Nahid authors a thyroid template and publishes it, and the
 * first step of that must not be a thing to hunt for.
 */
export function TemplateList() {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const mayWrite = usePermission('counseling.templates.write');
  const [starting, setStarting] = useState(false);

  const templates = useQuery({
    queryKey: COUNSELING_TEMPLATES_KEY,
    queryFn: listCounselingTemplates,
  });

  if (templates.isPending) return <Skeleton height="14rem" />;

  if (templates.isError || !templates.data) {
    // Critical rather than quiet. An author who cannot see the list has no way to tell
    // whether their draft exists, and the safe-looking assumption — that it does not — is
    // the one that gets a second copy of a checklist written.
    return (
      <AlertBanner tone="critical" title={t('unavailable')}>
        {t('unavailableBody')}
      </AlertBanner>
    );
  }

  return (
    <section className="app-counseling" aria-label={t('list.title')} data-testid="template-list">
      {mayWrite &&
        (starting ? (
          <NewTemplateForm
            onCreated={() => {
              void client.invalidateQueries({ queryKey: COUNSELING_TEMPLATES_KEY });
              setStarting(false);
            }}
            onCancel={() => setStarting(false)}
          />
        ) : (
          <div className="app-counseling__actions">
            <Button variant="primary" onClick={() => setStarting(true)}>
              {t('new.start')}
            </Button>
          </div>
        ))}

      {templates.data.length === 0 ? (
        <EmptyState icon="inbox" title={t('list.empty.title')}>
          {t('list.empty.body')}
        </EmptyState>
      ) : (
        <ul className="app-counseling__list">
          {templates.data.map((template) => (
            <li key={template.id}>
              <TemplateRow template={template} locale={locale} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function TemplateRow({ template, locale }: { template: CounselingTemplate; locale: Locale }) {
  const t = useTranslations('counseling');
  const title = templateTitle(template, locale);
  const live = template.published_version;
  const drafts = template.draft_count ?? 0;
  const approved = contentApproved(template);

  return (
    <Card elevation="raised" className="app-counseling-row">
      <div className="app-counseling-row__head">
        <h3 className="app-counseling-row__title">
          <Link href={`/counseling/templates/${template.id}`}>{title}</Link>
        </h3>
        {/* The code, in ASCII, because it is the identifier an assignment rule and an audit
            entry name — and the thing a physician quotes down the phone. */}
        <span className="app-counseling-row__code">{template.code}</span>
        {template.retired && <Badge>{t('list.retired')}</Badge>}
      </div>

      <p className="app-counseling-row__state" data-testid={`template-state-${template.code}`}>
        {live === undefined ? (
          <span data-testid={`template-unpublished-${template.code}`}>
            {t('list.notPublished')}
          </span>
        ) : (
          <span>
            {t('list.published', { version: live })}
            {template.published_at
              ? ` · ${t('list.liveSince', {
                  when: formatDate(Date.parse(template.published_at), locale),
                })}`
              : ''}
          </span>
        )}
        {' · '}
        <span data-testid={`template-drafts-${template.code}`}>
          {drafts === 0 ? t('list.noDrafts') : t('list.drafts', { count: drafts })}
        </span>
      </p>

      {/* D-53. Said in words on the row, not implied by the absence of a tick: the seeded
          diabetes checklist is live *and* unapproved, and those two facts have to be
          readable at the same time. */}
      {!approved && (
        <p
          className="app-counseling-row__unapproved"
          data-testid={`template-unapproved-${template.code}`}
        >
          {t('approval.notApproved')}
        </p>
      )}

      <p className="app-counseling-row__link">
        <Link href={`/counseling/templates/${template.id}`}>{t('list.open', { title })}</Link>
      </p>
    </Card>
  );
}
