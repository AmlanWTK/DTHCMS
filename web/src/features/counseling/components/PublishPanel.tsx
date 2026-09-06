'use client';

import { useMutation } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useId, useRef, useState } from 'react';

import { ApiError, NetworkError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';
import type { Locale } from '@/lib/i18n/config';
import { usePermission } from '@/lib/use-permission';

import {
  publishBlockers,
  publishCounselingVersion,
  type CounselingVersion,
  type PublishBlocker,
} from '../api/counseling';

/**
 * Putting a checklist on the floor (CP55, §5.1).
 *
 * # Three deliberate acts, not one
 *
 * Publishing is separated from saving by more than a label. It is its own panel, its own
 * mutation, its own permission and its own step-up purpose, and reaching it takes three
 * distinct decisions: press *Publish*, read what it will do and press the confirmation, then
 * prove it is you with a fresh second factor. The first press does not call anything — there
 * is a named test for that — because the failure this guards against is not malice. It is a
 * physician fixing a typo at the end of a clinic day who hits the wrong control, and changes
 * what every counsellor asks every patient from that second onwards.
 *
 * # The confirmation states consequences, not the action
 *
 * "Are you sure?" tells somebody nothing they did not know when they pressed the button. So
 * the dialog says the three things that are actually true and are not obvious: it goes live
 * on every phone on the floor within seconds; **this version is frozen forever** and the only
 * way to change it afterwards is to draft another; and whatever is live now is retired. The
 * third is the one people are surprised by.
 *
 * # Why it publishes the saved version and says so
 *
 * The endpoint publishes what the server holds. If the editor has changes it has not sent,
 * publishing would put the previous text on every phone while the author reads the new text
 * on screen and believes it shipped. So unsaved work blocks the act, in words, above the
 * button — this is the one place a blocked control is right, because the fix is one press
 * away and naming it is more use than letting the wrong version go out.
 *
 * # Why the server's 422 is shown with an item beside it
 *
 * `ErrNotBilingual` comes back as one message against the field `items`. That is correct for
 * an API and useless on a nine-row screen. The server's sentence is shown verbatim, because
 * it is the authority; underneath it, `publishBlockers` names which items it meant.
 *
 * # Why one of these two states blocks the button and the other does not
 *
 * The distinction is who can be right about it, and it is worth stating because it looks
 * inconsistent at a glance.
 *
 * **Unsaved work blocks.** That request would *succeed* and do something other than what the
 * author is looking at. No server can catch it — the server does not know what is on this
 * screen — so the only place it can be caught is here, and letting it through means the
 * previous text goes to every phone while the author believes the new text shipped.
 *
 * **An item missing a language does not block.** That request would *fail*, and the server
 * is the authority on whether it fails: it publishes what it holds, which may not be what
 * this client last read — another author may have saved, a room may have left the
 * vocabulary. A button greyed out by a stale local judgement is a physician who cannot
 * publish a checklist that is actually fine, with nothing on screen explaining why. So the
 * gap is named loudly, directly above the button, and the button still works. The step-up
 * that a doomed attempt spends is the price of that, and it is the reason the guidance is
 * where it is rather than somewhere tidier.
 */
export interface PublishPanelProps {
  templateId: string;
  version: CounselingVersion;
  /** What is live now and will be retired. Absent when this would be the first publication. */
  currentlyLive?: number;
  /** True while the editor holds changes the server has not got. */
  unsaved?: boolean;
  onPublished?: (version: CounselingVersion) => void;
}

export function PublishPanel({
  templateId,
  version,
  currentlyLive,
  unsaved = false,
  onPublished,
}: PublishPanelProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;
  const requestStepUp = useStepUp();
  const mayPublish = usePermission('counseling.templates.publish');

  const [asking, setAsking] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  /** The items the server's refusal was about. Ours, because the server names the list. */
  const [refused, setRefused] = useState<PublishBlocker[]>([]);

  const saved = version.items ?? [];
  const blockers = publishBlockers(saved);

  const publish = useMutation({
    mutationFn: async () => {
      const token = await requestStepUp(
        'counseling.publish',
        t('publish.stepUp', {
          version: version.version,
        }),
      );
      return publishCounselingVersion(token, templateId, version.version);
    },
    onSuccess: (published) => {
      setAsking(false);
      setRefusal(null);
      setRefused([]);
      onPublished?.(published);
    },
    onError: (error: unknown) => {
      if (error instanceof StepUpCancelled) {
        // Somebody changed their mind at the code prompt. Not a failure, and not something
        // to report back at them as one.
        setAsking(false);
        return;
      }
      if (error instanceof ApiError) {
        const named = fieldMessages(error, locale);
        const message =
          Object.values(named)[0] ?? (locale === 'bn' ? error.messageBN : error.messageEN);
        setRefusal(message);
        // 422 against `items` means the bilingual rule or the empty-checklist rule. Work out
        // which items the server was talking about and show them beside its sentence.
        setRefused(error.status === 422 ? publishBlockers(saved) : []);
        return;
      }
      setRefused([]);
      setRefusal(error instanceof NetworkError ? t('offline') : t('publish.failed'));
    },
  });

  if (!mayPublish) return null;

  // Only the thing this client is the authority on. See the note above.
  const ready = !unsaved;

  return (
    // A plain section rather than a Card: this needs an identity of its own on the page and
    // a rule down its edge that says which of the two acts it is. It is deliberately not
    // drawn like the editor it sits under.
    <section className="app-counseling-publish" data-testid="publish-panel">
      <h4 className="app-counseling-publish__title">{t('publish.title')}</h4>
      <p className="app-page__description">{t('publish.body')}</p>

      {refusal && (
        <AlertBanner tone="critical" title={refusal}>
          {refused.length > 0 && (
            <ul data-testid="publish-refusal-items">
              {refused.map((blocker) => (
                <li
                  key={
                    blocker.kind === 'no_items'
                      ? 'no_items'
                      : `${blocker.itemCode}-${blocker.missing}`
                  }
                >
                  {blocker.kind === 'no_items'
                    ? t('publish.blocker.no_items')
                    : t(`publish.blocker.${blocker.missing}`, {
                        position: blocker.ordering,
                        code: blocker.itemCode,
                      })}
                </li>
              ))}
            </ul>
          )}
        </AlertBanner>
      )}

      {unsaved && (
        <AlertBanner tone="borderline" title={t('publish.unsaved')}>
          {t('publish.unsavedBody')}
        </AlertBanner>
      )}

      {blockers.length > 0 && (
        <AlertBanner tone="borderline" title={t('publish.notReady')}>
          <ul data-testid="publish-blockers">
            {blockers.map((blocker) => (
              <li
                key={
                  blocker.kind === 'no_items'
                    ? 'no_items'
                    : `${blocker.itemCode}-${blocker.missing}`
                }
              >
                {blocker.kind === 'no_items'
                  ? t('publish.blocker.no_items')
                  : t(`publish.blocker.${blocker.missing}`, {
                      position: blocker.ordering,
                      code: blocker.itemCode,
                    })}
              </li>
            ))}
          </ul>
        </AlertBanner>
      )}

      <Button
        variant="danger"
        disabled={!ready}
        data-testid="publish-open"
        onClick={() => {
          setRefusal(null);
          setRefused([]);
          setAsking(true);
        }}
      >
        {t('publish.action', { version: version.version })}
      </Button>

      <PublishConfirm
        open={asking}
        version={version.version}
        currentlyLive={currentlyLive}
        busy={publish.isPending}
        onConfirm={() => publish.mutate()}
        onCancel={() => setAsking(false)}
      />
    </section>
  );
}

/**
 * The confirmation.
 *
 * A modal dialog rather than an inline expander, because the point is to stop: it takes the
 * focus, it names the three consequences, and its confirming button is the second deliberate
 * act. The cancel is first in the markup and is the ordinary-weight control; the confirm is
 * `danger`, and it says what it will do rather than "OK".
 */
function PublishConfirm({
  open,
  version,
  currentlyLive,
  busy,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  version: number;
  currentlyLive: number | undefined;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const t = useTranslations('counseling');
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    // jsdom implements showModal; a browser that does not would leave the dialog hidden, so
    // the open attribute is set as well rather than relying on one of the two.
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);

  if (!open) return null;

  return (
    <dialog
      ref={ref}
      className="app-dialog"
      aria-labelledby={titleId}
      data-testid="publish-confirm"
      onCancel={onCancel}
    >
      <div className="app-stack">
        <h2 className="app-dialog__title" id={titleId}>
          {t('publish.confirm.title', { version })}
        </h2>

        {/* The three things that are true and not obvious. A list rather than a paragraph:
            this is read in a hurry, and the third one is the surprise. */}
        <ul className="app-counseling-publish__consequences" data-testid="publish-consequences">
          <li>{t('publish.confirm.live')}</li>
          <li>{t('publish.confirm.frozen', { version })}</li>
          <li>
            {currentlyLive === undefined
              ? t('publish.confirm.nothingRetired')
              : t('publish.confirm.retires', { version: currentlyLive })}
          </li>
        </ul>

        <p className="app-page__description">{t('publish.confirm.stepUpNote')}</p>

        <div className="app-actions">
          <Button
            variant="secondary"
            onClick={onCancel}
            disabled={busy}
            data-testid="publish-cancel"
          >
            {t('cancel')}
          </Button>
          <Button
            variant="danger"
            loading={busy}
            onClick={onConfirm}
            data-testid="publish-confirm-action"
          >
            {t('publish.confirm.action', { version })}
          </Button>
        </div>
      </div>
    </dialog>
  );
}
