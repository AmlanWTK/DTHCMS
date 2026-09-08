import { useCallback, useEffect, useState } from 'react';
import { ScrollView, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { MeasurementField } from '@/components/MeasurementField';
import { localStore } from '@/lib/local-store';
import { escalate, readOutbox, referenceOf, resubmit, type OutboxRow } from '@/lib/sync';
import { theme, useTokens } from '@/lib/tokens';
import { useSession } from '@/stores/session';

import { useSyncMetrics } from './SyncProvider';
import { itemsOf, refusedValueOf, type SyncItem } from './state';

/**
 * Every entry this tablet is holding, one line each (CP67).
 *
 * # Why counts were not enough
 *
 * CP66's panel says "3 need your attention", and three is exactly the wrong amount of information:
 * enough to worry an operator, not enough for them to do anything. This screen is the list — what
 * each entry is, when it was taken, why it did not go, and the two things a person can do about
 * it. It is one tap from the pill in the header and one tap from the panel, and nothing else in
 * the application links to it, because it is a screen for a moment rather than a place to work.
 *
 * # Four groups, ordered by who has to act
 *
 * `itemsOf` decides which group a row is in and the order within each; this file draws them in the
 * order the operator can act on them. Refusals first because they are the operator's, escalations
 * next because they are a record of something already done, the clinic's hold after that, and the
 * ordinary queue last — the largest group and the least interesting, which is the right way round
 * for a screen somebody opens because something is wrong.
 *
 * # Nothing here draws a clinical value except in the one place it is being corrected
 *
 * The list is entry kind, time, state, reason. Not the measurement, not the patient's name — the
 * same discipline the clinic's own triage list keeps for the same reason (`docs/sync.md`: a
 * supervisor sees "eleven blood pressures and two weights"). A screen about delivery does not
 * need a number on it, and this one is opened at a station with somebody else's patient in the
 * chair. The exception is the correction sheet, which cannot work without showing the operator
 * what they are correcting, and which is opened one entry at a time by a deliberate press.
 */
export function SyncItems({ onChanged }: { onChanged?: () => void | Promise<unknown> }) {
  const t = useTranslations('sync');
  const { colors } = useTokens();
  const permissions = useSession((state) => state.operator?.permissions);
  const { refresh } = useSyncMetrics();
  const [rows, setRows] = useState<OutboxRow[] | null>(null);
  const store = localStore();

  const read = useCallback(async () => {
    if (!store) return;
    setRows(await readOutbox(store));
  }, [store]);

  const changed = useCallback(async () => {
    // The list, the header pill and whatever asked for this screen, in that order. All three read
    // the same tables and an operator who corrects an entry watches all three at once; one of them
    // lagging by a poll interval is the indicator disagreeing with itself.
    await read();
    await refresh();
    await onChanged?.();
  }, [read, refresh, onChanged]);

  useEffect(() => {
    void read();
    const timer = setInterval(() => void read(), 5_000);
    return () => clearInterval(timer);
  }, [read]);

  if (!store || rows === null) {
    return (
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('locked')}
      </AppText>
    );
  }

  const items = itemsOf(rows, { permissions });
  const empty =
    items.needsYou.length === 0 &&
    items.escalated.length === 0 &&
    items.atClinic.length === 0 &&
    items.onItsWay.length === 0;

  return (
    <ScrollView
      contentContainerStyle={{ gap: theme.spacing['5'], paddingBottom: theme.spacing['12'] }}
    >
      {empty ? <AppText>{t('groupEmpty')}</AppText> : null}

      <Group title={t('groupNeedsYou')} items={items.needsYou}>
        {(item) => (
          <ActionableEntry key={item.eventId} item={item} rows={rows} onChanged={changed} />
        )}
      </Group>

      <Group title={t('groupEscalated')} items={items.escalated}>
        {(item) => (
          <ActionableEntry key={item.eventId} item={item} rows={rows} onChanged={changed} />
        )}
      </Group>

      <Group title={t('groupAtClinic')} items={items.atClinic}>
        {(item) => <Entry key={item.eventId} item={item} />}
      </Group>

      {items.atClinic.length > 0 ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('heldAtClinicNote')}
        </AppText>
      ) : null}

      <Group title={t('groupOnItsWay')} items={items.onItsWay}>
        {(item) => <Entry key={item.eventId} item={item} />}
      </Group>
    </ScrollView>
  );
}

function Group({
  title,
  items,
  children,
}: {
  title: string;
  items: SyncItem[];
  children: (item: SyncItem) => React.ReactNode;
}) {
  if (items.length === 0) return null;
  return (
    <View style={{ gap: theme.spacing['2'] }}>
      <AppText weight="semibold">{title}</AppText>
      {items.map((item) => children(item))}
    </View>
  );
}

/** One entry, with no act attached to it. */
function Entry({ item, children }: { item: SyncItem; children?: React.ReactNode }) {
  const t = useTranslations('sync');
  const { colors } = useTokens();

  return (
    <View
      testID={`sync-item-${item.eventId}`}
      style={{
        gap: theme.spacing['1'],
        padding: theme.spacing['3'],
        borderRadius: theme.borderRadius.md,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText weight="semibold">{t(item.entryKey as never)}</AppText>
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {t('recordedAt', { when: new Date(item.occurredAt).toLocaleString() })}
      </AppText>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t(item.statusKey as never)}
      </AppText>
      {item.reason === null ? null : (
        <>
          <AppText size="sm">{t(item.reason.key as never)}</AppText>
          {/*
            The clinic's own sentence, drawn only where `reasonFor` allowed it — which is only for
            a reader holding the permission the API itself demands before it will show anybody the
            contents of a refused event. On a station tablet this is almost always null, and the
            sentence above is the whole answer.
          */}
          {item.reason.prose === null ? null : (
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('clinicSaid', { reason: item.reason.prose })}
            </AppText>
          )}
        </>
      )}
      {children}
    </View>
  );
}

/**
 * One entry with the two acts on it.
 *
 * The correction sheet opens inside the row rather than on another screen. An operator correcting
 * a measurement is holding a paper chart or looking at a patient; a modal that hid the reason
 * they are correcting it would make them press back to re-read it.
 */
function ActionableEntry({
  item,
  rows,
  onChanged,
}: {
  item: SyncItem;
  rows: OutboxRow[];
  onChanged: () => void | Promise<unknown>;
}) {
  const t = useTranslations('sync');
  const { colors } = useTokens();
  const [correcting, setCorrecting] = useState(false);
  const [text, setText] = useState('');
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const row = rows.find((one) => one.eventId === item.eventId);
  const refused = row === undefined ? null : refusedValueOf(row);

  const onEscalate = useCallback(async () => {
    const store = localStore();
    if (store === null) return;
    setBusy(true);
    try {
      const outcome = await escalate(store, item.eventId);
      setNote(
        outcome === 'escalated' || outcome === 'already'
          ? t('escalateDone', { reference: referenceOf(item.eventId) })
          : t('correctGone'),
      );
      await onChanged();
    } finally {
      setBusy(false);
    }
  }, [item.eventId, onChanged, t]);

  const onSend = useCallback(async () => {
    const store = localStore();
    if (store === null || refused === null) return;
    const value = Number(text.trim());
    // Refused here rather than by the store, so the operator hears it while they are still looking
    // at the field. An empty string is `Number('') === 0`, which is why the trim and the emptiness
    // check are both needed: a blank field must never become a measurement of zero.
    if (text.trim() === '' || !Number.isFinite(value)) {
      setNote(t('correctNotNumber'));
      return;
    }
    setBusy(true);
    try {
      const result = await resubmit(
        store,
        item.eventId,
        { value, unit: refused.unit },
        { newId: () => crypto.randomUUID(), now: () => Date.now() },
      );
      setNote(result.outcome === 'resubmitted' ? t('correctDone') : t('correctGone'));
      setCorrecting(false);
      await onChanged();
    } finally {
      setBusy(false);
    }
  }, [item.eventId, onChanged, refused, t, text]);

  return (
    <Entry item={item}>
      <View style={{ gap: theme.spacing['2'], paddingTop: theme.spacing['1'] }}>
        {note === null ? null : (
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {note}
          </AppText>
        )}

        {correcting && refused !== null ? (
          <View style={{ gap: theme.spacing['3'] }}>
            <AppText size="sm" weight="semibold">
              {t('correctTitle')}
            </AppText>
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('correctWas', { value: String(refused.value), unit: refused.unit })}
            </AppText>
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('correctHint', { when: new Date(item.occurredAt).toLocaleString() })}
            </AppText>
            <MeasurementField
              testID={`correct-${item.eventId}`}
              label={t(item.entryKey as never)}
              value={text}
              unit={refused.unit}
              // One unit, and it is the one the entry was refused in. A correction is a restatement
              // of the same measurement; letting the unit change here would turn a typo fix into a
              // silent conversion, which is the error that reaches a dose.
              units={[refused.unit]}
              onChangeValue={setText}
              onChangeUnit={() => {}}
            />
            <AppButton
              testID={`correct-send-${item.eventId}`}
              label={t('correctSave')}
              disabled={busy}
              onPress={() => void onSend()}
            />
            <AppButton
              label={t('correctCancel')}
              variant="secondary"
              disabled={busy}
              onPress={() => setCorrecting(false)}
            />
          </View>
        ) : null}

        {!correcting && item.acts.includes('correct') ? (
          <AppButton
            testID={`act-correct-${item.eventId}`}
            label={t('actCorrect')}
            disabled={busy}
            onPress={() => {
              setNote(null);
              setText(refused === null ? '' : String(refused.value));
              setCorrecting(true);
            }}
          />
        ) : null}

        {!correcting && item.state === 'NEEDS_ATTENTION' ? (
          <View style={{ gap: theme.spacing['1'] }}>
            {/*
              The sentence above the button, not after it. "Escalate" reads as "send it to
              somebody", and this one does not send it anywhere — the entry is on this tablet and
              nowhere else. An operator who pressed it believing otherwise would put the tablet
              down and walk away from a measurement nobody else can see, which is precisely the
              silent loss the whole offline design exists to prevent, arriving through a button we
              added to help.
            */}
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('escalateExplain')}
            </AppText>
            <AppButton
              testID={`act-escalate-${item.eventId}`}
              label={t('actEscalate')}
              variant="secondary"
              disabled={busy}
              onPress={() => void onEscalate()}
            />
          </View>
        ) : null}

        {item.state === 'ESCALATED' ? (
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('referenceLabel', { reference: item.reference })}
          </AppText>
        ) : null}
      </View>
    </Entry>
  );
}
