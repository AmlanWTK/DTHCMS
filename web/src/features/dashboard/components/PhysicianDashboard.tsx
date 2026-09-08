'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useRef, useState } from 'react';

import { Button, ErrorState, Icon, Skeleton } from '@dthcms/ui';

import { AllergyBanner } from '@/features/allergies';
import { useRealtimeTopics } from '@/features/realtime';
import { ApiError } from '@/lib/api';
import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';

import {
  dashboardKey,
  primeDashboardCaches,
  readDashboard,
  type PhysicianDashboard as DashboardView,
} from '../api/dashboard';

import { AssistantPanel } from './AssistantPanel';
import { ShortcutHelp } from './ShortcutHelp';
import { SnapshotPanel } from './SnapshotPanel';
import { StatusWord } from './StatusWord';
import { SummaryPanel } from './SummaryPanel';
import { useDashboardLayout } from './useDashboardLayout';
import { useDashboardShortcuts } from './useDashboardShortcuts';

/**
 * The physician's three-panel dashboard (CP73, §8) — the screen a consultant spends the
 * working day in.
 *
 * # One request
 *
 * The whole screen is `GET /v1/patients/{id}/dashboard`. That is the checkpoint's acceptance
 * criterion and it is fragile, because several of the components below fetch for themselves —
 * so the aggregate primes their caches before they mount (see `primeDashboardCaches`). The
 * one deliberate exception is the critical-value strip, which holds its own poll for CP50's
 * stated reason: a board that trusted only the socket would go quiet after a dropped
 * connection and look exactly like a clinic with nothing wrong in it.
 *
 * # Realtime, and why it is invalidation and not a socket write
 *
 * The screen subscribes to `patient:{id}` while it is mounted, and every message on that
 * topic invalidates `['patient', id]` — a prefix of this screen's key — so a blood pressure
 * typed at station 2 appears here without a refresh. What arrives on the socket is a
 * *notification*: nothing writes a value into the cache from a message, because two paths
 * producing what a clinician reads is one path too many, and on the day they disagree the
 * number on screen is one no endpoint returned.
 *
 * # The layout and the open decision
 *
 * Three columns on a desk, stacked on a tablet. Left and centre open; right open and
 * collapsible, remembered per person. That is the plan's open decision answered as a
 * proposal — see `useDashboardLayout`, and `docs/progress.md`, which records it as awaiting
 * Dr. Nahid's confirmation.
 *
 * # Print
 *
 * `p`, or the button. The print stylesheet flattens the three columns into one flow, keeps
 * the AI enclosure and its words, and drops the controls — a printed summary with an
 * *Accept* button on it is a document that misrepresents what happened.
 */

export interface PhysicianDashboardProps {
  patientId: string;
  /** Pins the screen to one visit. Omitted means the open one, or the most recent. */
  visitId?: string;
}

export function PhysicianDashboard({ patientId, visitId }: PhysicianDashboardProps) {
  const t = useTranslations('dashboard');
  const locale = useLocale() as Locale;
  const client = useQueryClient();
  const layout = useDashboardLayout();
  const [helpOpen, setHelpOpen] = useState(false);

  const snapshotRef = useRef<HTMLElement>(null);
  const summaryRef = useRef<HTMLElement>(null);
  const assistantRef = useRef<HTMLElement>(null);

  // The socket, for as long as this screen is on. CP27's rule: a screen asks for what it is
  // showing, and releases it when the operator navigates away — which is what keeps the
  // gateway's per-message authorisation work proportional to what people are looking at.
  useRealtimeTopics([`patient:${patientId}`]);

  const dashboard = useQuery({
    queryKey: dashboardKey(patientId, visitId),
    queryFn: () => readDashboard(patientId, visitId),
  });

  /*
   * The panels that fetch for themselves are handed the aggregate's answer *here*, during the
   * render that will mount them — not in an effect, which runs a commit too late, and not in
   * the fetch, which a cache hit never calls. `primeDashboardCaches` carries the full argument
   * and the two versions that did not work.
   *
   * The ref makes it once per distinct payload. Without it every parent render would rewrite
   * the same value and notify the strip's observer for nothing.
   */
  const primed = useRef<DashboardView | null>(null);
  if (dashboard.data && primed.current !== dashboard.data) {
    primed.current = dashboard.data;
    primeDashboardCaches(client, dashboard.data);
  }

  const focusPanel = useCallback((panel: 'snapshot' | 'summary' | 'assistant') => {
    const target =
      panel === 'snapshot'
        ? snapshotRef.current
        : panel === 'summary'
          ? summaryRef.current
          : assistantRef.current;
    target?.focus();
    // Guarded because `scrollIntoView` is absent in jsdom and in a few embedded browsers, and
    // a missing scroll must never cost the focus move above it — the focus is what makes the
    // shortcut work for a keyboard and a screen reader, and the scroll is the nicety.
    target?.scrollIntoView?.({ block: 'start', behavior: 'smooth' });
  }, []);

  useDashboardShortcuts({
    focusPanel,
    toggleAssistant: layout.toggleRight,
    print: () => window.print(),
    toggleHelp: () => setHelpOpen((open) => !open),
    closeOverlays: () => setHelpOpen(false),
  });

  if (dashboard.isPending) {
    return (
      <div className="dash" data-testid="dashboard-loading">
        <Skeleton height="4rem" />
        <div className="dash__columns">
          <Skeleton height="24rem" />
          <Skeleton height="24rem" />
          <Skeleton height="24rem" />
        </div>
      </div>
    );
  }

  if (dashboard.isError || !dashboard.data) {
    return <DashboardRefusal error={dashboard.error} patientId={patientId} />;
  }

  const view = dashboard.data;

  return (
    <div className="dash" data-testid="dashboard" data-right-open={layout.rightOpen}>
      <DashboardHeader view={view} locale={locale} />

      <div className="dash__toolbar">
        <Button
          variant="quiet"
          size="sm"
          onClick={layout.toggleRight}
          aria-expanded={layout.rightOpen}
          aria-controls="dash-assistant"
          data-testid="toggle-assistant"
        >
          <Icon name={layout.rightOpen ? 'chevron-right' : 'chevron-down'} aria-hidden />
          {t(layout.rightOpen ? 'hideAssistant' : 'showAssistant')}
        </Button>
        <Button variant="quiet" size="sm" onClick={() => window.print()} data-testid="print">
          <Icon name="scroll-text" aria-hidden />
          {t('print')}
        </Button>
        <Button
          variant="quiet"
          size="sm"
          onClick={() => setHelpOpen((open) => !open)}
          aria-expanded={helpOpen}
          data-testid="shortcuts"
        >
          <Icon name="help-circle" aria-hidden />
          {t('shortcutsLabel')}
        </Button>
        <span className="dash__asof" data-testid="dashboard-asof">
          {t('asOf', { at: formatDateTime(Date.parse(view.as_of), locale) })}
        </span>
      </div>

      {helpOpen && <ShortcutHelp onClose={() => setHelpOpen(false)} />}

      <div className="dash__columns">
        <section
          className="dash__column dash__column--snapshot"
          ref={snapshotRef}
          tabIndex={-1}
          aria-labelledby="dash-snapshot-heading"
          id="dash-snapshot"
        >
          <SnapshotPanel view={view} />
        </section>

        <section
          className="dash__column dash__column--summary"
          ref={summaryRef}
          tabIndex={-1}
          aria-labelledby="dash-summary-heading"
          id="dash-summary"
        >
          <SummaryPanel view={view} />
        </section>

        {/*
          Hidden with `hidden` rather than unmounted. Unmounting would throw away the edit a
          physician had half-typed into a rejection note when they collapsed the column by
          accident — and would re-run the panel's effects on every toggle.
        */}
        <section
          className="dash__column dash__column--assistant"
          ref={assistantRef}
          tabIndex={-1}
          aria-labelledby="dash-assistant-heading"
          id="dash-assistant"
          hidden={!layout.rightOpen}
        >
          <AssistantPanel view={view} />
        </section>
      </div>
    </div>
  );
}

/**
 * The patient header: who this is, the allergy strip, and the basis this record is open on.
 *
 * The allergy strip is `AllergyBanner`, unchanged from CP54, reading the cache the aggregate
 * primed. It is here rather than inside the snapshot column so that it stays visible while
 * that column scrolls: it is a safety element, and a safety element that scrolls away is a
 * decoration.
 */
function DashboardHeader({ view, locale }: { view: DashboardView; locale: Locale }) {
  const t = useTranslations('dashboard');
  const name =
    locale === 'bn' && view.patient.name_bn ? view.patient.name_bn : view.patient.name_en;

  return (
    <header className="dash-header" data-testid="dashboard-header">
      <div className="dash-header__identity">
        <h1 className="dash-header__name">{name}</h1>
        {/* A clinical id is an identifier: ASCII digits in both languages, because it is read
            back at a desk and copied onto paper. */}
        <span className="dash-header__id">{view.patient.clinical_id}</span>
        <span className="dash-header__age">
          {t(`age.${ageUnit(view.patient.age_text)}`, { n: ageNumber(view.patient.age_text) })}
        </span>
        <span className="dash-header__sex">{t(`sex.${view.patient.sex}`)}</span>
        {view.visit && (
          <span className="dash-header__visit" data-open={view.visit.open}>
            <StatusWord status={view.visit.open ? 'normal' : 'stale'} testId="dashboard-visit">
              {t(view.visit.open ? 'visitOpen' : 'visitClosed', { code: view.visit.visit_code })}
            </StatusWord>
          </span>
        )}
      </div>

      {view.access.basis === 'BREAK_GLASS' && view.access.break_glass && (
        // CP22 alarms every administrator when the door opens. What it cannot do is tell the
        // person using it, while they are using it — and an emergency access somebody has
        // forgotten is open has stopped being an emergency. Their own justification is read
        // back to them, which is the most effective reminder available.
        <div className="dash-header__breakglass" role="alert" data-testid="break-glass-banner">
          <Icon name="key-round" aria-hidden />
          <div>
            <strong>{t('breakGlass.title')}</strong>
            <p>{t('breakGlass.body', { justification: view.access.break_glass.justification })}</p>
            <p className="dash-header__breakglass-until">
              {t('breakGlass.until', {
                at: formatDateTime(Date.parse(view.access.break_glass.expires_at), locale),
              })}
              {view.access.break_glass.acknowledged
                ? ` · ${t('breakGlass.acknowledged')}`
                : ` · ${t('breakGlass.notAcknowledged')}`}
            </p>
          </div>
        </div>
      )}

      {/*
        Given the state rather than left to fetch it — see `AllergyBannerProps.state`. This is
        what makes "one request" a property of the code rather than of a cache policy set in
        another file.
      */}
      <AllergyBanner patientId={view.patient.id} state={view.allergies ?? undefined} />
    </header>
  );
}

/**
 * What the screen says when the server refuses.
 *
 * # Why a 403 offers the emergency door and does not explain itself
 *
 * The server answers the same 403 whatever the reason, deliberately: a refusal that said "this
 * patient is outside your scope" would be confirming that the patient exists, which is what
 * `TestAForbiddenAnswerDoesNotRevealExistence` exists to prevent. So this screen does not ask
 * why. It offers the break-glass console — which is CP22's, already built, and which takes a
 * patient and a typed justification — and lets the physician decide whether the situation
 * warrants it.
 *
 * That is the honest shape of "break-glass path if the patient is outside the physician's
 * normal scope": the door is offered on any refusal, the justification is typed, every
 * administrator is told, and nothing about the refusal leaks.
 */
function DashboardRefusal({ error, patientId }: { error: unknown; patientId: string }) {
  const t = useTranslations('dashboard');
  const forbidden = error instanceof ApiError && error.status === 403;
  const missing = error instanceof ApiError && error.status === 404;

  return (
    <div className="dash dash--refused" data-testid="dashboard-refused">
      <ErrorState
        title={t(forbidden ? 'refused.title' : missing ? 'missing.title' : 'failed.title')}
      >
        <p>{t(forbidden ? 'refused.body' : missing ? 'missing.body' : 'failed.body')}</p>
        {forbidden && (
          <p>
            <Link
              className="app-link"
              href={`/break-glass?patient=${patientId}`}
              data-testid="break-glass-link"
            >
              {t('refused.breakGlass')}
            </Link>
          </p>
        )}
      </ErrorState>
    </div>
  );
}

/**
 * The age's unit, from the server's `43y` / `8m` / `12d`.
 *
 * Composed here rather than sent as a sentence because the sentence has to be in the reader's
 * language and the server has no business holding two of them for a number. The unit is the
 * last character; the number is the rest. A shape this build does not recognise falls back to
 * years, which is wrong for an infant and visible — rather than blank, which is not.
 */
function ageUnit(text: string): 'y' | 'm' | 'd' {
  const last = text.slice(-1);
  return last === 'm' || last === 'd' ? last : 'y';
}

function ageNumber(text: string): number {
  const parsed = Number.parseInt(text, 10);
  return Number.isFinite(parsed) ? parsed : 0;
}
