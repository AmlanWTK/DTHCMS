'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Button, Skeleton } from '@dthcms/ui';

import { PageHeader } from '@/components/PageHeader';
import { formatDate } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  COUNSELING_ROOMS_KEY,
  COUNSELING_TEMPLATES_KEY,
  contentApproved,
  counselingVersionKey,
  counselingVersionsKey,
  getCounselingVersion,
  isEditable,
  listCounselingRooms,
  listCounselingTemplates,
  listCounselingVersions,
  publishedVersionOf,
  versionToOpen,
  type CounselingVersion,
} from '../api/counseling';

import { CounsellorPreview } from './CounsellorPreview';
import { DraftEditor } from './DraftEditor';
import { PublishPanel } from './PublishPanel';
import { VersionView } from './VersionView';
import { templateTitle } from './counselingText';

/**
 * One checklist, and everything that may be done to it (CP55, §5.1, [R-07]).
 *
 * # The one decision this component makes
 *
 * Which surface a version gets. A draft, to somebody who may write, gets the editor. Anything
 * else gets `VersionView`, which has no inputs at all. That single branch is where the frozen
 * model lives in this feature: there is no third state where a published version renders the
 * editor "read-only", because a disabled form is a form somebody keeps trying to use, and the
 * true model is that publishing is finished and revising means drafting.
 *
 * # Why the publish panel is here and not in the editor
 *
 * Saving and publishing are different acts, so they are not built by the same component and
 * do not sit beside each other. The editor owns saving; this owns publishing, and passes it
 * the version **as the server holds it** together with whether the editor has anything
 * unsent — because publishing puts the saved version on the floor and a physician who edited
 * without saving would ship the previous text while reading the new one.
 *
 * # Why the version history is always on screen
 *
 * A completed counselling session retains the version it used, which is the whole reason
 * versions are frozen. That fact is unreadable unless the versions are visible: an author
 * looking at version 4 needs to see that 3 is live, 2 is retired and 1 was published by the
 * migration that seeded the clinic.
 */
export interface TemplateWorkspaceProps {
  templateId: string;
  /** The version to open on. Defaults to the newest draft, else what is live. */
  initialVersion?: number;
}

/** Reference data. Re-reading the three rooms on every render would be a round trip for nothing. */
const ROOMS_STALE_MS = 60 * 60 * 1000;

export function TemplateWorkspace({ templateId, initialVersion }: TemplateWorkspaceProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;
  const client = useQueryClient();

  const mayWrite = usePermission('counseling.templates.write');

  const [chosen, setChosen] = useState<number | undefined>(initialVersion);
  const [unsaved, setUnsaved] = useState(false);

  const rooms = useQuery({
    queryKey: COUNSELING_ROOMS_KEY,
    queryFn: listCounselingRooms,
    staleTime: ROOMS_STALE_MS,
  });

  const templates = useQuery({
    queryKey: COUNSELING_TEMPLATES_KEY,
    queryFn: listCounselingTemplates,
  });

  const versions = useQuery({
    queryKey: counselingVersionsKey(templateId),
    queryFn: () => listCounselingVersions(templateId),
  });

  const showing = chosen ?? (versions.data ? versionToOpen(versions.data)?.version : undefined);

  const version = useQuery({
    queryKey: counselingVersionKey(templateId, showing ?? 0),
    queryFn: () => getCounselingVersion(templateId, showing as number),
    enabled: showing !== undefined,
  });

  if (rooms.isPending || templates.isPending || versions.isPending) {
    return <Skeleton height="20rem" />;
  }

  if (rooms.isError || templates.isError || versions.isError) {
    return (
      <AlertBanner tone="critical" title={t('unavailable')}>
        {t('unavailableBody')}
      </AlertBanner>
    );
  }

  const template = templates.data?.find((one) => one.id === templateId);
  const live = publishedVersionOf(versions.data ?? []);

  /** Everything that changed when a version was written. Publishing moves all three. */
  function refresh() {
    void client.invalidateQueries({ queryKey: counselingVersionsKey(templateId) });
    void client.invalidateQueries({ queryKey: COUNSELING_TEMPLATES_KEY });
    if (showing !== undefined) {
      void client.invalidateQueries({ queryKey: counselingVersionKey(templateId, showing) });
    }
  }

  function openVersion(opened: CounselingVersion) {
    setUnsaved(false);
    setChosen(opened.version);
    refresh();
  }

  return (
    <div className="app-counseling-workspace" data-testid="template-workspace">
      <p className="app-counseling-workspace__back">
        <Link href="/counseling/templates">{t('workspace.back')}</Link>
      </p>

      {/* The same header component every other screen opens with, so this page has one h1
          and the code and the approval state hang off it rather than competing with it. */}
      <PageHeader
        title={template ? templateTitle(template, locale) : t('workspace.unknownTemplate')}
        description={
          <span className="app-counseling-workspace__head">
            {template && <span className="app-counseling-row__code">{template.code}</span>}
            {template && !contentApproved(template) && (
              <span className="app-counseling-row__unapproved" data-testid="workspace-unapproved">
                {t('approval.notApproved')}
              </span>
            )}
          </span>
        }
      />

      <section className="app-counseling-history" aria-label={t('version.history')}>
        <h3 className="app-counseling-history__title">{t('version.history')}</h3>
        <ul className="app-counseling-history__list" data-testid="version-history">
          {(versions.data ?? []).map((one) => (
            <li key={one.version}>
              <Button
                variant={one.version === showing ? 'primary' : 'quiet'}
                size="sm"
                aria-current={one.version === showing ? 'true' : undefined}
                data-testid={`version-tab-${one.version}`}
                onClick={() => {
                  setUnsaved(false);
                  setChosen(one.version);
                }}
              >
                {t('version.tab', {
                  version: one.version,
                  status: t(`version.status.${one.status}`),
                })}
              </Button>
              <span className="app-counseling-history__when">
                {formatDate(Date.parse(one.created_at), locale)}
              </span>
            </li>
          ))}
        </ul>
      </section>

      {version.isPending && showing !== undefined && <Skeleton height="16rem" />}

      {version.isError && (
        <AlertBanner tone="critical" title={t('version.unavailable')}>
          {t('unavailableBody')}
        </AlertBanner>
      )}

      {version.data && (
        <>
          {isEditable(version.data) && mayWrite ? (
            <>
              {/* Keyed by the version, so moving between two drafts remounts the editor.
                  Without it React keeps the component in place and its rows are state, which
                  means the previous version's items stay on screen under the new version's
                  heading — and a save would write them over the draft the author thinks they
                  are looking at. The publish card is keyed with it for the same reason: a
                  refusal about version 2 must not still be on screen above version 3. */}
              <DraftEditor
                key={`draft-${version.data.version}`}
                templateId={templateId}
                version={version.data}
                rooms={rooms.data ?? []}
                onDirtyChange={setUnsaved}
                onSaved={() => refresh()}
              />
              <PublishPanel
                key={`publish-${version.data.version}`}
                templateId={templateId}
                version={version.data}
                {...(live === undefined ? {} : { currentlyLive: live.version })}
                unsaved={unsaved}
                onPublished={openVersion}
              />
            </>
          ) : (
            <VersionView
              templateId={templateId}
              version={version.data}
              rooms={rooms.data ?? []}
              onDrafted={openVersion}
            />
          )}

          <CounsellorPreview
            version={version.data}
            rooms={rooms.data ?? []}
            initialLanguage={locale}
          />
        </>
      )}
    </div>
  );
}
