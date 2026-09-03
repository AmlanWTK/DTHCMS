'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useState, type FormEvent } from 'react';

import { ApiError, NetworkError } from '@dthcms/api-client';
import { AlertBanner, Badge, Button, Card, Input, Select, Skeleton } from '@dthcms/ui';

import {
  endBreakGlass,
  exportTrail,
  listAuditEvents,
  listAuditKinds,
  listBreakGlass,
  verifyChain,
  type AuditEvent,
  type AuditFilter,
  type AuditKind,
  type BreakGlassAccess,
  type ChainVerification,
} from '../api/audit';

/**
 * Administration → Audit trail (CP22): who did what, as sentences.
 *
 * The four filters the blueprint names — person, day, patient, kind — and a table of
 * sentences in the interface language, newest first. Two more things live here because
 * they are about the trail as a whole: verifying the chain, and taking it away as a signed
 * PDF. Both are themselves recorded, and the person sees their own row appear.
 */
export function AuditViewer() {
  const t = useTranslations('audit');
  const locale = useLocale();

  const [kinds, setKinds] = useState<AuditKind[]>([]);
  const [filter, setFilter] = useState<AuditFilter>({});
  const [applied, setApplied] = useState<AuditFilter>({});
  const [events, setEvents] = useState<AuditEvent[] | null>(null);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notice, setNotice] = useState<{
    tone: 'critical' | 'info' | 'borderline';
    text: string;
  } | null>(null);
  const [verification, setVerification] = useState<ChainVerification | null>(null);
  const [busy, setBusy] = useState<'verify' | 'export' | 'more' | null>(null);
  const [openDoors, setOpenDoors] = useState<BreakGlassAccess[]>([]);
  const [closing, setClosing] = useState<string | null>(null);

  const explain = useCallback(
    (error: unknown): string => {
      if (error instanceof NetworkError) return t('offline');
      if (error instanceof ApiError) return locale === 'bn' ? error.messageBN : error.messageEN;
      return t('unexpected');
    },
    [t, locale],
  );

  const load = useCallback(
    async (f: AuditFilter) => {
      try {
        const page = await listAuditEvents(f);
        setEvents(page.events);
        setNextBefore(page.nextBefore);
        setLoadError(null);
      } catch (error) {
        setLoadError(explain(error));
      }
    },
    [explain],
  );

  const loadDoors = useCallback(async () => {
    try {
      setOpenDoors(await listBreakGlass());
    } catch {
      setOpenDoors([]);
    }
  }, []);

  useEffect(() => {
    listAuditKinds()
      .then(setKinds)
      .catch(() => setKinds([]));
    void load({});
    void loadDoors();
  }, [load, loadDoors]);

  async function closeDoor(access: BreakGlassAccess) {
    setClosing(access.id);
    try {
      await endBreakGlass(access.id, t('doors.closedReason'));
      setNotice({ tone: 'info', text: t('doors.closed') });
      await Promise.all([loadDoors(), load(applied)]);
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setClosing(null);
    }
  }

  function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setApplied(filter);
    setEvents(null);
    void load(filter);
  }

  function clear() {
    setFilter({});
    setApplied({});
    setEvents(null);
    void load({});
  }

  async function more() {
    if (!nextBefore || busy) return;
    setBusy('more');
    try {
      const page = await listAuditEvents(applied, nextBefore);
      setEvents((prev) => [...(prev ?? []), ...page.events]);
      setNextBefore(page.nextBefore);
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setBusy(null);
    }
  }

  async function verify() {
    setBusy('verify');
    setNotice(null);
    try {
      const result = await verifyChain();
      setVerification(result);
      await load(applied);
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setBusy(null);
    }
  }

  async function download() {
    setBusy('export');
    setNotice(null);
    try {
      const out = await exportTrail(applied);
      save(out.blob, out.filename);
      save(
        new Blob([JSON.stringify(out.signature, null, 2)], { type: 'application/json' }),
        `${out.filename}.sig.json`,
      );
      setNotice({
        tone: 'info',
        text: t('export.done', { file: out.filename, key: out.signature.key_id }),
      });
      await load(applied);
    } catch (error) {
      setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setBusy(null);
    }
  }

  const kindLabel = (kind: AuditKind) => (locale === 'bn' ? kind.label_bn : kind.label_en);

  return (
    <div className="app-stack">
      {notice && (
        <AlertBanner tone={notice.tone} title={notice.text} onDismiss={() => setNotice(null)} />
      )}

      <Card header={<h2 className="app-card__title">{t('filters.title')}</h2>}>
        <form className="app-stack" onSubmit={apply} noValidate>
          <div className="app-filter-grid">
            <Input
              label={t('filters.person')}
              name="person"
              value={filter.person ?? ''}
              onChange={(e) => setFilter({ ...filter, person: e.target.value })}
              description={t('filters.personHint')}
              autoCapitalize="characters"
            />
            <Select
              label={t('filters.kind')}
              name="kind"
              value={filter.kind || 'any'}
              onChange={(e) =>
                setFilter({ ...filter, kind: e.target.value === 'any' ? '' : e.target.value })
              }
              options={[
                { value: 'any', label: t('filters.anyKind') },
                ...kinds.map((k) => ({ value: k.kind, label: kindLabel(k) })),
              ]}
            />
            <Input
              label={t('filters.from')}
              name="from"
              type="date"
              value={filter.from ?? ''}
              onChange={(e) => setFilter({ ...filter, from: e.target.value })}
            />
            <Input
              label={t('filters.to')}
              name="to"
              type="date"
              value={filter.to ?? ''}
              onChange={(e) => setFilter({ ...filter, to: e.target.value })}
            />
            <Input
              label={t('filters.patient')}
              name="patient"
              value={filter.patient ?? ''}
              onChange={(e) => setFilter({ ...filter, patient: e.target.value })}
              description={t('filters.patientHint')}
              spellCheck={false}
            />
          </div>
          <div className="app-actions">
            <Button type="submit" variant="primary" size="sm">
              {t('filters.apply')}
            </Button>
            <Button type="button" variant="quiet" size="sm" onClick={clear}>
              {t('filters.clear')}
            </Button>
            <span className="app-actions__spacer" />
            <Button
              type="button"
              variant="secondary"
              size="sm"
              iconStart="shield-check"
              onClick={() => void verify()}
              loading={busy === 'verify'}
              disabled={busy !== null}
            >
              {t('chain.verify')}
            </Button>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              iconStart="scroll-text"
              onClick={() => void download()}
              loading={busy === 'export'}
              disabled={busy !== null}
            >
              {t('export.button')}
            </Button>
          </div>
        </form>
      </Card>

      {verification && <ChainResult result={verification} />}

      {openDoors.length > 0 && (
        <Card header={<h2 className="app-card__title">{t('doors.title')}</h2>}>
          <ul className="app-role-list">
            {openDoors.map((access) => (
              <li key={access.id}>
                <div>
                  <div className="app-table__primary">
                    {access.scope_kind === 'patient'
                      ? t('doors.patient', { id: access.scope_ref })
                      : access.scope_ref}
                  </div>
                  <div className="app-table__secondary">
                    {t('doors.who', {
                      role: access.active_role || '—',
                      until: formatWhen(access.expires_at, locale),
                    })}
                    {' · '}
                    {access.acknowledged_at ? (
                      <Badge tone="brand">{t('doors.acknowledged')}</Badge>
                    ) : (
                      <Badge tone="info">{t('doors.notYetSeen')}</Badge>
                    )}
                  </div>
                  <div className="app-table__secondary">{access.justification}</div>
                </div>
                <Button
                  variant="danger"
                  size="sm"
                  onClick={() => void closeDoor(access)}
                  loading={closing === access.id}
                  disabled={closing !== null}
                >
                  {t('doors.close')}
                </Button>
              </li>
            ))}
          </ul>
        </Card>
      )}

      <Card header={<h2 className="app-card__title">{t('list.title')}</h2>}>
        {loadError ? (
          <AlertBanner tone="critical" title={loadError}>
            <Button variant="secondary" size="sm" onClick={() => void load(applied)}>
              {t('retry')}
            </Button>
          </AlertBanner>
        ) : events === null ? (
          <div className="app-stack" aria-busy="true">
            <Skeleton />
            <Skeleton />
            <Skeleton />
          </div>
        ) : events.length === 0 ? (
          <p className="app-page__description">{t('list.empty')}</p>
        ) : (
          <div className="app-stack">
            <div className="app-table-wrap">
              <table className="app-table app-audit-table">
                <thead>
                  <tr>
                    <th scope="col">{t('list.when')}</th>
                    <th scope="col">{t('list.what')}</th>
                    <th scope="col">{t('list.kind')}</th>
                  </tr>
                </thead>
                <tbody>
                  {events.map((ev) => (
                    <EventRow key={ev.seq} event={ev} locale={locale} />
                  ))}
                </tbody>
              </table>
            </div>
            {nextBefore && (
              <div className="app-actions">
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => void more()}
                  loading={busy === 'more'}
                >
                  {t('list.more')}
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}

function EventRow({ event, locale }: { event: AuditEvent; locale: string }) {
  const t = useTranslations('audit');
  const sentence = locale === 'bn' ? event.sentence_bn : event.sentence_en;
  const label = locale === 'bn' ? event.label_bn : event.label_en;
  const alarming = event.kind.startsWith('break_glass') || event.kind === 'audit.chain_broken';
  return (
    <tr data-kind={event.kind}>
      <td className="app-audit-table__when">
        <time dateTime={event.recorded_at}>{formatWhen(event.recorded_at, locale)}</time>
        <div className="app-table__secondary">#{event.seq}</div>
      </td>
      <td>
        <div className="app-audit-table__sentence">{sentence}</div>
        {event.actor_role && (
          <div className="app-table__secondary">{t('list.asRole', { role: event.actor_role })}</div>
        )}
      </td>
      <td>
        <Badge tone={alarming ? 'info' : 'neutral'}>{label}</Badge>
      </td>
    </tr>
  );
}

function ChainResult({ result }: { result: ChainVerification }) {
  const t = useTranslations('audit');
  if (result.ok) {
    return (
      <AlertBanner tone="info" title={t('chain.okTitle', { count: result.checked })}>
        {result.strays > 0
          ? t('chain.strays', { count: result.strays })
          : t('chain.okBody', { head: result.head_seq })}
      </AlertBanner>
    );
  }
  return (
    <AlertBanner tone="critical" title={t('chain.brokenTitle', { seq: result.broken_at ?? 0 })}>
      {result.problem}
    </AlertBanner>
  );
}

export function formatWhen(iso: string, locale: string): string {
  return new Date(iso).toLocaleString(locale === 'bn' ? 'bn-BD' : 'en-GB', {
    timeZone: 'Asia/Dhaka',
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function save(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // Revoked on the next tick so the click has started the download first.
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
