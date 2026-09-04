'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useCallback, useMemo, useState } from 'react';

import { ApiError, queryKeys } from '@dthcms/api-client';
import { AlertBanner, Badge, Button, Card, EmptyState, Skeleton } from '@dthcms/ui';

import { useRealtimeTopics } from '@/features/realtime';
import { useSessionStore } from '@/stores/session';

import {
  readBoard,
  reroute,
  type BoardEntry,
  type BoardStation,
  type BoardSuggestion,
  type Heat,
} from '../api/board';

/**
 * The Clinic Traffic Control board (CP40, §5.2).
 *
 * # Two audiences, one component
 *
 * The wall display and the floor supervisor's phone are the same board at two sizes, not two
 * boards. `density="wall"` scales the type up and drops every control; `density="compact"`
 * keeps the controls and fits a column list on a phone. Building them as one component is
 * what stops the wall and the phone disagreeing about who is where, which is the single
 * thing a traffic board must never do.
 *
 * # Colour is not the only signal
 *
 * A bottleneck is red, *and* it carries a word, *and* it sorts its badge to the front. Red
 * alone fails for the roughly one in twelve men who will work in this clinic, and it fails
 * completely on a projector with a dying lamp — which is the actual failure mode of a screen
 * that has been on a wall for three years.
 *
 * # What is not here
 *
 * No name, no diagnosis, no reason for a priority. That is enforced on the server and in the
 * database; this component could not render them if it tried, because they are not in the
 * payload. What it does render is `flagged` — *that* somebody is being seen first, never
 * why.
 */

const REFRESH_MS = 15_000;

export function TrafficBoard({
  density = 'compact',
  day,
}: {
  /** `wall` is the display in the waiting area: big type, no controls. */
  density?: 'wall' | 'compact';
  day?: string;
}) {
  const t = useTranslations('board');
  const facilityId = useSessionStore((state) => state.user?.facilityId ?? null);
  const client = useQueryClient();

  const board = useQuery({
    queryKey: [...queryKeys.queue(facilityId ?? 'unknown'), 'board', day ?? 'today'],
    queryFn: () => readBoard(day),
    // A poll *and* the socket. The socket is what makes the two-second criterion true; the
    // poll is what makes the board correct after a night when the socket died and nobody
    // was watching. A wall display is the one screen in the building nobody reloads.
    refetchInterval: REFRESH_MS,
    enabled: facilityId !== null,
  });

  // The queue topic. Its permission is `board.read`, which is the only one the display's
  // own account holds.
  useRealtimeTopics(useMemo(() => (facilityId ? [`queue:${facilityId}`] : []), [facilityId]));

  const [moving, setMoving] = useState<BoardSuggestionish | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const apply = useCallback(
    async (entryId: string, to: string, reason: string) => {
      setBusy(true);
      setFailed(null);
      try {
        await reroute(entryId, to, reason);
        setMoving(null);
        await client.invalidateQueries({ queryKey: queryKeys.queue(facilityId ?? 'unknown') });
      } catch (error) {
        // 409 is the interesting one: two supervisors on the same board, one a second
        // slower. It is not a failure to retry — it is a board that has moved on.
        setFailed(
          error instanceof ApiError && error.status === 409 ? t('moved') : t('rerouteFailed'),
        );
        await board.refetch();
      } finally {
        setBusy(false);
      }
    },
    [board, client, facilityId, t],
  );

  if (board.isPending) {
    return <Skeleton height="12rem" />;
  }
  if (board.isError || !board.data) {
    return <AlertBanner tone="critical" title={t('unavailable')} />;
  }

  const { stations, suggestions, waiting_total: waitingTotal } = board.data;
  const worst = stations.filter((station) => station.heat === 'bottleneck');

  return (
    <section className="app-board" data-density={density} aria-label={t('title')}>
      <header className="app-board__summary">
        <div className="app-board__count">
          <span className="app-board__count-value">{waitingTotal}</span>
          <span className="app-board__count-label">{t('waitingNow')}</span>
        </div>
        <div className="app-board__count">
          <span className="app-board__count-value">{board.data.in_building_total}</span>
          <span className="app-board__count-label">{t('inBuilding')}</span>
        </div>
        {worst.length > 0 && (
          <p className="app-board__alarm" data-testid="board-bottleneck-summary">
            {t('bottleneckAt', {
              stations: worst.map((s) => t(`station.${s.station_code}`)).join(', '),
            })}
          </p>
        )}
      </header>

      {failed && <AlertBanner tone="borderline" title={failed} onDismiss={() => setFailed(null)} />}

      {stations.length === 0 ? (
        <EmptyState icon="clock" title={t('empty.title')}>
          {t('empty.body')}
        </EmptyState>
      ) : (
        <ol className="app-board__grid">
          {stations.map((station) => (
            <StationColumn
              key={station.station_code}
              station={station}
              density={density}
              onMove={
                density === 'compact'
                  ? (entry) =>
                      setMoving({
                        entry_id: entry.entry_id,
                        label: entry.label,
                        from: station.station_code,
                        to: '',
                        waited_seconds: entry.waited_seconds,
                        because: '',
                      })
                  : undefined
              }
            />
          ))}
        </ol>
      )}

      {density === 'compact' && suggestions.length > 0 && (
        <Card elevation="raised" className="app-board__suggestions">
          <h3>{t('suggestions')}</h3>
          <ul>
            {suggestions.map((suggestion) => (
              <li key={suggestion.entry_id}>
                <div>
                  <p className="app-board__suggestion-move">
                    {t('moveFromTo', {
                      label: suggestion.label,
                      from: t(`station.${suggestion.from}`),
                      to: t(`station.${suggestion.to}`),
                    })}
                  </p>
                  {/* Composed here, in the language the board is being read in, from the
                      facts the server sent. A ready-made English sentence full of raw
                      station codes is not something a supervisor reading Bangla can
                      evaluate in one glance — and a suggestion nobody can evaluate is one
                      they will ignore or obey blindly. */}
                  <p className="app-board__suggestion-why">{becauseOf(suggestion, t)}</p>
                </div>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={busy}
                  onClick={() => setMoving({ ...suggestion, because: becauseOf(suggestion, t) })}
                >
                  {t('apply')}
                </Button>
              </li>
            ))}
          </ul>
        </Card>
      )}

      {moving && (
        <RerouteForm
          suggestion={moving}
          stations={stations}
          busy={busy}
          onCancel={() => setMoving(null)}
          onConfirm={apply}
        />
      )}
    </section>
  );
}

/** A suggestion, or a move a supervisor started themselves from a column. */
interface BoardSuggestionish {
  entry_id: string;
  label: string;
  from: string;
  to: string;
  waited_seconds: number;
  /** Already composed, in the reader's language. Pre-fills the reason field. */
  because: string;
}

/**
 * The sentence under the button, in the language the board is being read in.
 *
 * It doubles as the reason the reroute is recorded with, which is why it is composed rather
 * than templated at render time: what gets stored in the ledger is what the supervisor saw
 * and accepted.
 */
function becauseOf(
  suggestion: BoardSuggestion,
  t: (key: string, values?: Record<string, string | number>) => string,
): string {
  return t('because', {
    from: t(`station.${suggestion.from}`),
    to: t(`station.${suggestion.to}`),
    waiting: suggestion.from_waiting,
  });
}

function StationColumn({
  station,
  density,
  onMove,
}: {
  station: BoardStation;
  density: 'wall' | 'compact';
  onMove?: (entry: BoardEntry) => void;
}) {
  const t = useTranslations('board');

  return (
    <li
      className="app-board__station"
      data-heat={station.heat}
      data-testid={`board-${station.station_code}`}
    >
      <header>
        <h3>{t(`station.${station.station_code}`)}</h3>
        {/* Colour is never the only signal: the level is spelled out, so the board still
            works for a colour-blind supervisor and on a projector with a tired lamp. */}
        <span data-testid="heat">
          <Badge tone={station.heat === 'calm' ? 'neutral' : 'info'}>
            {t(`heat.${station.heat}`)}
          </Badge>
        </span>
      </header>
      <p className="app-board__station-numbers">
        <span className="app-board__waiting">{station.waiting}</span>
        <span className="app-board__longest">
          {t('longestWait', { minutes: Math.floor(station.longest_wait_seconds / 60) })}
        </span>
      </p>
      <ul className="app-board__entries">
        {station.entries.map((entry) => (
          <li key={entry.entry_id} data-status={entry.status} data-flagged={entry.flagged}>
            <span className="app-board__label">{entry.label}</span>
            <span className="app-board__waited">{Math.floor(entry.waited_seconds / 60)}′</span>
            {entry.flagged && (
              <span className="app-board__flag" title={t('priority')} aria-label={t('priority')}>
                ▲
              </span>
            )}
            {entry.counseling_done && (
              <span className="app-board__tick" aria-label={t('counseled')}>
                ✓
              </span>
            )}
            {density === 'compact' && onMove && entry.status === 'waiting' && (
              <Button
                size="sm"
                variant="quiet"
                aria-label={t('moveNamed', { label: entry.label })}
                onClick={() => onMove(entry)}
              >
                {t('move')}
              </Button>
            )}
          </li>
        ))}
      </ul>
    </li>
  );
}

/**
 * The reroute confirmation.
 *
 * The reason field is required and has a floor of five characters, because the server
 * refuses anything shorter and a form that lets somebody type "x" and then shows them a
 * validation error is a form that taught them nothing.
 */
function RerouteForm({
  suggestion,
  stations,
  busy,
  onCancel,
  onConfirm,
}: {
  suggestion: BoardSuggestionish;
  stations: BoardStation[];
  busy: boolean;
  onCancel: () => void;
  onConfirm: (entryId: string, to: string, reason: string) => void;
}) {
  const t = useTranslations('board');
  const [to, setTo] = useState(suggestion.to);
  const [reason, setReason] = useState(suggestion.because);
  const valid = to !== '' && to !== suggestion.from && reason.trim().length >= 5;

  return (
    <Card elevation="floating" className="app-board__reroute">
      <h3>{t('rerouteTitle', { label: suggestion.label })}</h3>
      <label>
        {t('destination')}
        <select value={to} onChange={(event) => setTo(event.target.value)}>
          <option value="">{t('choose')}</option>
          {stations
            .filter((station) => station.station_code !== suggestion.from)
            .map((station) => (
              <option key={station.station_code} value={station.station_code}>
                {t(`station.${station.station_code}`)}
              </option>
            ))}
        </select>
      </label>
      <label>
        {t('reason')}
        <input
          type="text"
          value={reason}
          minLength={5}
          onChange={(event) => setReason(event.target.value)}
          placeholder={t('reasonPlaceholder')}
        />
      </label>
      <div className="app-board__reroute-actions">
        <Button variant="quiet" onClick={onCancel} disabled={busy}>
          {t('cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={!valid || busy}
          onClick={() => onConfirm(suggestion.entry_id, to, reason.trim())}
        >
          {t('confirmMove')}
        </Button>
      </div>
    </Card>
  );
}

export type { Heat };
