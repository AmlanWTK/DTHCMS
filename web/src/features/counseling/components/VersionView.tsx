'use client';

import { useMutation } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Card, EmptyState } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  contentApproved,
  draftCounselingVersion,
  groupByRoom,
  isEditable,
  publishedBySystem,
  type CounselingRoom,
  type CounselingVersion,
} from '../api/counseling';

import { ItemLines } from './ItemLines';

/**
 * One revision, read only (CP55, §5.1, [R-07]).
 *
 * # There is no edit affordance here, and its absence is the checkpoint
 *
 * A published version is frozen by a database trigger; editing one answers `409`. This
 * component could have rendered the editor with its inputs disabled, and that would have
 * been the worse thing to build. A disabled field is a field somebody can see, aim at, and
 * fail to use — it teaches "you are not allowed to do this *right now*", which invites
 * hunting for the state in which they would be. The model that is actually true is
 * different: **published means done, and you revise by drafting.** So the version renders as
 * text, and the only thing on the screen is the act that is genuinely available.
 *
 * `counseling.test.tsx` has a named test whose whole job is to fail if a textbox, a combobox
 * or a checkbox ever appears inside a published version.
 *
 * # Why the attribution says "published with the system"
 *
 * The seeded diabetes version carries `published_source: 'MIGRATION'` and no `published_by`,
 * because no person published it. Rendering the author field of that row as a blank would
 * read as data missing; inventing a name would be the only attribution in this system naming
 * somebody who did not do the thing. So the fact gets a sentence of its own.
 *
 * # Why the items are grouped by room
 *
 * §5.2's sequence. A counsellor works through the counselling room's items, walks the patient
 * to the nutrition room, then to the insulin corner — the grouping *is* the flow, and an
 * author reviewing a version needs to see it in the shape the floor will meet it in.
 */
export interface VersionViewProps {
  templateId: string;
  version: CounselingVersion;
  rooms: readonly CounselingRoom[];
  /** Called with the new draft once one has been opened from this version. */
  onDrafted?: (version: CounselingVersion) => void;
}

export function VersionView({ templateId, version, rooms, onDrafted }: VersionViewProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;
  const mayWrite = usePermission('counseling.templates.write');

  const [refusal, setRefusal] = useState<string | null>(null);

  const draft = useMutation({
    mutationFn: () => draftCounselingVersion(templateId),
    onSuccess: (created) => {
      setRefusal(null);
      onDrafted?.(created);
    },
    onError: () => setRefusal(t('version.draftFailed')),
  });

  const groups = groupByRoom(version.items ?? [], rooms);
  const frozen = !isEditable(version);

  return (
    <section
      className="app-counseling-version"
      aria-label={t('version.heading', { version: version.version })}
      data-testid="version-view"
      data-status={version.status}
      data-editable="false"
    >
      <header className="app-counseling-version__head">
        <h3 className="app-counseling-version__title">
          {t('version.heading', { version: version.version })}
        </h3>
        <p className="app-counseling-version__status" data-testid="version-status">
          {t(`version.status.${version.status}`)}
        </p>
      </header>

      {frozen && (
        // Said before the items, because it is the answer to the question the author came
        // with. "Where do I change this" is answered by the next control, not by a search.
        <AlertBanner tone="info" title={t('version.frozen')}>
          {t('version.frozenBody')}
        </AlertBanner>
      )}

      {!contentApproved(version) && (
        // D-53 is open. This is a proposal, whatever its status column says.
        <AlertBanner
          tone="borderline"
          title={t('approval.notApproved')}
          className="app-counseling-version__unapproved"
        >
          {t('approval.notApprovedBody')}
        </AlertBanner>
      )}

      <dl className="app-counseling-version__facts">
        <dt>{t('version.drafted')}</dt>
        <dd data-testid="version-created">
          {formatDateTime(Date.parse(version.created_at), locale)}
        </dd>

        {version.published_at && (
          <>
            <dt>{t('version.publishedOn')}</dt>
            <dd data-testid="version-published">
              {formatDateTime(Date.parse(version.published_at), locale)}
            </dd>
          </>
        )}

        {version.status !== 'DRAFT' && (
          <>
            <dt>{t('version.publishedByLabel')}</dt>
            <dd data-testid="version-attribution">
              {publishedBySystem(version)
                ? t('version.publishedBySystem')
                : t('version.publishedByPerson')}
            </dd>
          </>
        )}

        <dt>{t('version.notes')}</dt>
        <dd data-testid="version-notes">
          {version.notes && version.notes.trim() !== '' ? version.notes : t('version.noNotes')}
        </dd>
      </dl>

      {publishedBySystem(version) && (
        <p className="app-counseling-version__migration" data-testid="version-migration-note">
          {t('version.publishedBySystemBody')}
        </p>
      )}

      {groups.length === 0 ? (
        <EmptyState icon="inbox" title={t('version.empty.title')}>
          {t('version.empty.body')}
        </EmptyState>
      ) : (
        <ol className="app-counseling-rooms">
          {groups.map((group) => (
            <li key={group.code} className="app-counseling-room" data-room={group.code}>
              <ItemLines group={group} locale={locale} />
            </li>
          ))}
        </ol>
      )}

      {mayWrite && (
        <Card elevation="flat" className="app-counseling-revise">
          <h4 className="app-counseling-revise__title">{t('version.draftNew')}</h4>
          <p className="app-page__description">{t('version.draftNewBody')}</p>
          {refusal && <AlertBanner tone="critical" title={refusal} />}
          <Button
            variant="primary"
            loading={draft.isPending}
            data-testid="draft-new-version"
            onClick={() => draft.mutate()}
          >
            {t('version.draftNew')}
          </Button>
        </Card>
      )}
    </section>
  );
}
