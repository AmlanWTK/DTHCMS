import { useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Pressable, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { MeasurementField } from '@/components/MeasurementField';
import { EnteredBy, ofObservation } from '@/features/attribution';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';
import { useSession } from '@/stores/session';

import {
  CORRECTIONS_QUERY_PREFIX,
  applyCorrection,
  rejectCorrection,
  troubleOf,
  useCorrectionReasons,
  useFlaggedObservation,
  useMyCorrections,
} from './api';
import {
  adviceFor,
  alreadyAnswered,
  applyProblem,
  attemptKey,
  draftFor,
  eventFor,
  flaggedValueOf,
  holdEvent,
  keepsItsEvent,
  notThisRole,
  notYours,
  queueOf,
  rejectProblem,
  releaseEvent,
  requestReadingOf,
  toApply,
  toReject,
  unitsFor,
  type CorrectionReason,
  type CorrectionRequest,
  type Draft,
  type FlaggedValue,
  type HeldEvents,
  type Locale,
  type Observation,
  type Problem,
  type RequestReading,
  type Trouble,
} from './state';

/**
 * What I am being asked to fix (CP62, §4.3, [R-04]).
 *
 * # Everything here is arrangement; every decision is in `state.ts`
 *
 * The same split as every other station: this component cannot be rendered in the container
 * the tests run in, so anything it decided would be a decision nobody checks — and what it
 * would be deciding is what one press writes into a patient's record. `requestReadingOf` says
 * what a request says, `applyProblem` and `rejectProblem` say whether a draft is an answer,
 * `toApply` and `toReject` build the bodies, and `queueOf` keeps the server's order. What is
 * left here is where those sit.
 *
 * # This screen is a colleague asking a question, and it is drawn like one
 *
 * §4.3's case is a physician who is sure a height of 150 should be 140. The request went to
 * the person who typed it, because an operator who never learns they mistyped will mistype
 * again — and the moment that mechanism feels like a reprimand, people stop entering values
 * under their own name and start asking somebody else to type for them. So: no red, no
 * warning tone, no count of past flags, no severity. The panel wears the ordinary card
 * treatment with a brand-coloured edge, exactly like the rest of the application, and it says
 * out loud at the bottom that a value questioned and put right is the system working.
 *
 * The `transcription` flag on a reason — the one CP63 counts — is never drawn here. It is a
 * property of a taxonomy, not a fact about a colleague, and printing "this counts as a
 * transcription error" beside somebody's own mistake is precisely the metric the plan's risk
 * note warns about.
 *
 * # One request open at a time
 *
 * A request opens to show the value, and only one does. Two clinical values on one screen with
 * two keypads under them is how the wrong one gets typed into, and this screen exists because
 * somebody typed into the wrong field once already. A queue of exactly one opens by itself:
 * making an operator tap a list of one to reach the only thing on it is a tap that teaches
 * nothing.
 *
 * # Two acts, drawn differently on purpose
 *
 * **Correcting** is the primary act and it is one act: the value in the unit it was typed in,
 * a keypad, a button. The note is optional and behind nothing.
 *
 * **Saying it stands** is deliberately one step further away — a control that reveals a reason
 * field — and the sentence explaining *why* a reason is required sits above that field rather
 * than in a validation message under it. That sentence is the mechanism: "no" with no reason
 * teaches the physician nothing, leaves them unable to tell a disagreement from an oversight,
 * and stops them flagging. It is not a nicety and it is not an error message.
 *
 * # Nothing here is gated on a permission
 *
 * Answering a request routed to you needs none, and a client-side check would be a locked
 * control in front of the one person entitled to press it. The only refusal this screen knows
 * how to draw is the server's own.
 *
 * # An empty screen is never left to speak for itself
 *
 * Nothing open, a read the hat cannot make, a request answered on another device: each has its
 * own sentence. An operator who pressed a button and then met a blank list will assume they
 * broke something, and go and find somebody to ask.
 */
export function CorrectionInbox() {
  const t = useTranslations('corrections');
  const { colors } = useTokens();
  const locale = usePreferences((state) => state.language) as Locale;
  const me = useSession((state) => state.operator?.id ?? '');
  const queryClient = useQueryClient();

  // A refusal, an outcome and a problem each name their own sentence, so the key is a value
  // rather than a literal and `useTranslations` cannot type it — the same cast the other
  // stations use, with the same guarantee behind it: `corrections.test.ts` asserts every key
  // this feature can produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;

  const [all, setAll] = useState(false);
  const [opened, setOpened] = useState('');
  const [touched, setTouched] = useState(false);

  const requests = useMyCorrections(me !== '', all);
  const reasons = useCorrectionReasons(me !== '');
  const queue = useMemo(() => queueOf(requests.data), [requests.data]);

  // The single open request opens itself, once. Once the operator has touched the list their
  // choice stands: a card that reopened under them after they closed it would be the screen
  // arguing with the person holding it.
  useEffect(() => {
    if (touched || queue.focus === '') return;
    setOpened(queue.focus);
  }, [queue.focus, touched]);

  const reread = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: CORRECTIONS_QUERY_PREFIX });
  }, [queryClient]);

  const onAnswered = useCallback(() => {
    // Answered requests stay on screen from here on. A row that vanished at the moment it was
    // answered would leave the operator with no confirmation of what they had just written
    // into somebody's record — and, when it was answered elsewhere, with a blank where the
    // thing they were about to do used to be.
    setAll(true);
    reread();
    // The flagged row is now CORRECTED and a new one holds the value, so nothing this screen
    // cached about it is still true.
    void queryClient.invalidateQueries({ queryKey: ['observation'] });
  }, [queryClient, reread]);

  const failure = requests.error;
  const trouble = failure === null ? null : troubleOf(failure, locale, 'read');

  return (
    <View testID="correction-inbox" style={{ gap: theme.spacing['4'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('intro')}
      </AppText>

      {trouble !== null ? (
        <Panel>
          <AppText testID="correction-read-failed" size="base">
            {notThisRole(trouble) ? t('notThisRole') : say(`trouble.${trouble.kind}`)}
          </AppText>
          {trouble.message === '' ? null : (
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {trouble.message}
            </AppText>
          )}
          <AppText size="sm">{say(`advice.${adviceFor(trouble)}`)}</AppText>
        </Panel>
      ) : requests.isPending ? (
        <AppText size="base">{t('loading')}</AppText>
      ) : queue.open.length === 0 && queue.answered.length === 0 ? (
        <Panel>
          <AppText testID="correction-empty" size="base" weight="semibold">
            {t('empty')}
          </AppText>
          <AppText size="sm">{t('emptyDetail')}</AppText>
        </Panel>
      ) : null}

      {queue.open.map((request) => (
        <RequestCard
          key={request.id}
          request={request}
          reasons={reasons.data}
          locale={locale}
          expanded={opened === request.id}
          onToggle={() => {
            setTouched(true);
            setOpened((current) => (current === request.id ? '' : request.id));
          }}
          onAnswered={onAnswered}
        />
      ))}

      {/* The answered ones, on request. Not by default: this is a queue of things to do, and a
          list that mixed the done with the outstanding is one where the outstanding gets
          missed. But never unreachable, because "I answered that yesterday" is a thing an
          operator needs to be able to check. */}
      <Pressable
        testID="correction-toggle-answered"
        accessibilityRole="button"
        accessibilityState={{ expanded: all }}
        onPress={() => setAll((was) => !was)}
        style={{
          minHeight: theme.size.touchTargetCompact,
          justifyContent: 'center',
        }}
      >
        <AppText size="sm" weight="semibold" style={{ color: colors.text.link }}>
          {all ? t('hideAnswered') : t('showAnswered')}
        </AppText>
      </Pressable>

      {all && queue.answered.length === 0 ? (
        <AppText testID="correction-none-answered" size="sm" style={{ color: colors.text.muted }}>
          {t('emptyAnswered')}
        </AppText>
      ) : null}

      {all
        ? queue.answered.map((request) => (
            <RequestCard
              key={request.id}
              request={request}
              reasons={reasons.data}
              locale={locale}
              expanded={opened === request.id}
              onToggle={() => {
                setTouched(true);
                setOpened((current) => (current === request.id ? '' : request.id));
              }}
              onAnswered={onAnswered}
            />
          ))
        : null}

      {/* Said out loud, at the bottom, every time. The plan's risk note is that a quality
          mechanism which feels punitive makes staff hide errors instead of correcting them,
          and the cheapest defence against that is to keep saying what this screen is for. */}
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {t('notBlame')}
      </AppText>
    </View>
  );
}

type Say = (key: string, values?: Record<string, string>) => string;

/**
 * One request: what was flagged, who says so, why — and then the acts.
 *
 * The three facts come before anything else and before any control, because an operator who
 * cannot see who is asking or what they saw has only a number and a demand. The value itself
 * is one level in, behind the press that opens the card, so a queue of ten requests is not ten
 * patients' measurements on one screen.
 */
function RequestCard({
  request,
  reasons,
  locale,
  expanded,
  onToggle,
  onAnswered,
}: {
  request: CorrectionRequest;
  reasons: readonly CorrectionReason[] | undefined;
  locale: Locale;
  expanded: boolean;
  onToggle: () => void;
  onAnswered: () => void;
}) {
  const t = useTranslations('corrections');
  const tRoot = useTranslations();
  const { colors } = useTokens();
  const say = t as unknown as Say;
  // The measurement's name lives under the station that owns it — `anthropometry.field.height`
  // — rather than being copied into this namespace, so a height is called the same thing on
  // the form that captured it and on the screen that questions it.
  const sayGlobal = tRoot as unknown as Say;

  const reading = requestReadingOf(request, reasons, locale);
  const name =
    reading.measurement.key === null
      ? reading.measurement.code
      : sayGlobal(reading.measurement.key);
  const who = reading.who === '' ? t('someoneElse') : reading.who;

  return (
    <View
      testID={`correction-${request.id}`}
      style={{
        gap: theme.spacing['3'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: theme.size.borderWidth.thin,
        // The ordinary card edge with a brand accent, and no status tone at all. A red border
        // here would be this screen's first and loudest statement, and what it would be
        // stating is that somebody is in trouble.
        borderColor: reading.open ? colors.brand.border : colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <Pressable
        testID={`correction-${request.id}-toggle`}
        accessibilityRole="button"
        accessibilityState={{ expanded }}
        accessibilityLabel={t('flaggedBy', { who, measurement: name })}
        onPress={onToggle}
        style={{
          minHeight: theme.size.touchTarget,
          justifyContent: 'center',
          gap: theme.spacing['1'],
        }}
      >
        <AppText size="lg" weight="semibold">
          {name}
        </AppText>
        <AppText size="base">{t('flaggedBy', { who, measurement: name })}</AppText>
        {reading.role === '' ? null : (
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('flaggerRole', {
              role: reading.roleKey === null ? reading.role : sayGlobal(reading.roleKey),
            })}
          </AppText>
        )}
        <AppText size="2xs" weight="semibold" style={{ color: colors.text.link }}>
          {expanded ? t('close') : t('open')}
        </AppText>
      </Pressable>

      {/* Why, and what they actually saw. The code can be counted and cannot say "the tape was
          against the wall, not the patient"; the note can say it and cannot be counted. Both,
          always, and the note's absence said rather than left as a gap. */}
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText testID={`correction-${request.id}-reason`} size="base">
          {t('reason', { reason: reading.reason })}
        </AppText>
        {reading.note === '' ? (
          <AppText size="sm" style={{ color: colors.text.muted }}>
            {t('noNote')}
          </AppText>
        ) : (
          <AppText size="sm">{t('theirNote', { note: reading.note })}</AppText>
        )}
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {reading.when.known
            ? t('askedAt', { date: reading.when.date, time: reading.when.time })
            : t('askedWhenUnknown')}
        </AppText>
      </View>

      {reading.open ? null : <Answered reading={reading} say={say} />}

      {expanded ? (
        <OpenRequest
          request={request}
          reading={reading}
          measurement={name}
          locale={locale}
          say={say}
          onAnswered={onAnswered}
        />
      ) : null}
    </View>
  );
}

/**
 * How a request was answered, and what moved with it.
 *
 * `OVERRIDDEN` keeps its own sentence: it means somebody else corrected the value, and telling
 * an operator they put something right when a supervisor did would be untrue about their own
 * record in the one place that record is visible to them.
 *
 * The recomputed list is criterion 3 arriving on the operator's screen. A code that could not
 * be recomputed is drawn as its own row rather than filtered out — a physician reading "height
 * corrected" while a stale BMI sits beside it is the disagreement the whole transaction exists
 * to prevent, and this is the one case the server cannot prevent it in.
 */
function Answered({ reading, say }: { reading: RequestReading; say: Say }) {
  const t = useTranslations('corrections');
  const { colors } = useTokens();

  return (
    <View
      testID={`correction-${reading.id}-answered`}
      style={{
        gap: theme.spacing['1'],
        paddingTop: theme.spacing['2'],
        borderTopWidth: theme.size.borderWidth.thin,
        borderTopColor: colors.border.subtle,
      }}
    >
      <AppText size="base" weight="semibold">
        {say(`outcome.${reading.outcome}`)}
      </AppText>
      {reading.answeredAt?.known === true ? (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('answeredAt', { date: reading.answeredAt.date, time: reading.answeredAt.time })}
        </AppText>
      ) : null}
      {reading.answerNote === '' ? null : (
        <AppText size="sm">{t('answerNote', { note: reading.answerNote })}</AppText>
      )}
      {reading.recomputed.length === 0 ? null : (
        <View style={{ gap: theme.spacing['0.5'], paddingTop: theme.spacing['1'] }}>
          <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
            {t('recomputedHeading')}
          </AppText>
          {reading.recomputed.map((row) => (
            <AppText
              key={row.code}
              testID={`correction-recomputed-${row.code}`}
              size="sm"
              style={{ color: row.done ? colors.text.secondary : colors.text.primary }}
            >
              {row.done
                ? t('recomputedDone', { code: row.code })
                : t('recomputedNot', { code: row.code })}
            </AppText>
          ))}
        </View>
      )}
    </View>
  );
}

/**
 * The value that was questioned, and the two things that can be done about it.
 *
 * The observation is read here rather than with the queue: the request carries an id and a
 * code and not a number, which is the contract's decision and the right one — a list of ten
 * requests would otherwise put ten patients' clinical values on one screen. So the read
 * happens for the one request somebody has actually opened.
 *
 * A value that has not arrived is said so. An empty keypad under a heading is a form an
 * operator will fill in from memory.
 */
function OpenRequest({
  request,
  reading,
  measurement,
  locale,
  say,
  onAnswered,
}: {
  request: CorrectionRequest;
  reading: RequestReading;
  /** The measurement's name, already in the reader's language. */
  measurement: string;
  locale: Locale;
  say: Say;
  onAnswered: () => void;
}) {
  const t = useTranslations('corrections');
  const { colors } = useTokens();
  const observation = useFlaggedObservation(request.observation_id);
  const row = observation.data;

  const flagged = useMemo(() => flaggedValueOf(row), [row]);
  const [draft, setDraft] = useState<Draft>(() => draftFor(row));

  // Seeded when the stored value arrives, and again only if it actually changes. React Query's
  // structural sharing hands back the same object for a refetch that returned the same row, so
  // a background refresh does not wipe out what the operator has typed; a row that genuinely
  // changed underneath them is a different value, and a form still holding the old one would
  // be a correction to a number nobody is looking at any more.
  useEffect(() => {
    if (row === undefined) return;
    setDraft(draftFor(row));
  }, [row]);

  return (
    <View
      testID={`correction-${request.id}-detail`}
      style={{
        gap: theme.spacing['4'],
        paddingTop: theme.spacing['3'],
        borderTopWidth: theme.size.borderWidth.thin,
        borderTopColor: colors.border.subtle,
      }}
    >
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
          {t('valueHeading')}
        </AppText>
        {observation.isPending ? (
          <AppText size="base">{t('valueLoading')}</AppText>
        ) : row === undefined ? (
          <AppText testID={`correction-${request.id}-no-value`} size="base">
            {t('valueMissing')}
          </AppText>
        ) : (
          <StoredValue row={row} flagged={flagged} say={say} />
        )}
      </View>

      {reading.open && row !== undefined ? (
        <Answer
          request={request}
          flagged={flagged}
          measurement={measurement}
          draft={draft}
          setDraft={setDraft}
          locale={locale}
          say={say}
          onAnswered={onAnswered}
        />
      ) : null}
    </View>
  );
}

/**
 * The number as it stands, with the person who entered it named beside it (CP61).
 *
 * Every clinical value this application renders goes through `EnteredBy`, and this one most of
 * all: the whole subject of this screen is who entered a value and whether it is right. The
 * attribution here will usually name the reader themselves, which is exactly the point —
 * "entered by you at 09:14, at anthropometry" is the sentence that makes a flag legible.
 */
function StoredValue({ row, flagged, say }: { row: Observation; flagged: FlaggedValue; say: Say }) {
  const t = useTranslations('corrections');
  const { colors } = useTokens();

  return (
    <View style={{ gap: theme.spacing['2'] }}>
      {/* At display size and in the Latin face, like every other clinical number in this
          application: digits that change shape with the interface language are digits somebody
          transcribes wrongly, which is the very failure this screen exists to undo. */}
      <AppText testID="correction-stored-value" size="3xl" weight="bold" variant="clinicalValue">
        {storedWords(flagged, t)}
      </AppText>
      {flagged.kind === 'number' || flagged.kind === 'words' || flagged.kind === 'yesno' ? null : (
        <AppText testID="correction-cannot" size="sm" style={{ color: colors.text.secondary }}>
          {say(`cannot.${flagged.kind}`)}
        </AppText>
      )}
      <EnteredBy provenance={ofObservation(row)} testID="correction-entered-by" />
    </View>
  );
}

/**
 * The stored value in words, whatever shape it is. Never a blank where a number belongs.
 *
 * On `shape` rather than on `kind`: a derived value cannot be retyped here and is still a
 * number, and "not recorded" where a flagged BMI of 22.4 should be would leave an operator
 * with no idea what anybody is talking about.
 */
function storedWords(flagged: FlaggedValue, t: ReturnType<typeof useTranslations>): string {
  switch (flagged.shape) {
    case 'number':
      return flagged.value === null
        ? t('valueNotRecorded')
        : `${flagged.value}${flagged.unit === '' ? '' : ` ${flagged.unit}`}`;
    case 'words':
      return flagged.words === '' ? t('valueNotRecorded') : flagged.words;
    case 'yesno':
      return flagged.bool === null ? t('valueNotRecorded') : flagged.bool ? t('yes') : t('no');
    case 'coded':
      return flagged.concept === '' ? t('valueNotRecorded') : flagged.concept;
    default:
      return t('valueNotRecorded');
  }
}

/**
 * The two acts.
 *
 * Correcting first and reachable in one press; saying it stands second, behind a control that
 * reveals the reason field and the sentence explaining why the reason is required. The order
 * and the distance are the design: the common case is a typo somebody is about to fix, and the
 * uncommon one is a disagreement that is worth a colleague's sentence.
 */
function Answer({
  request,
  flagged,
  measurement,
  draft,
  setDraft,
  locale,
  say,
  onAnswered,
}: {
  request: CorrectionRequest;
  flagged: FlaggedValue;
  measurement: string;
  draft: Draft;
  setDraft: (next: Draft) => void;
  locale: Locale;
  say: Say;
  onAnswered: () => void;
}) {
  const t = useTranslations('corrections');
  const { colors } = useTokens();

  const [held, setHeld] = useState<HeldEvents>({});
  const [busy, setBusy] = useState<'' | 'apply' | 'reject'>('');
  // One slot per act rather than one between them. "Say why the value stands" belongs under the
  // reason field, and a shared slot would draw it under the keypad — where it reads as an
  // objection to the number the operator has just typed.
  const [problem, setProblem] = useState<Problem | null>(null);
  const [standsProblem, setStandsProblem] = useState<Problem | null>(null);
  const [trouble, setTrouble] = useState<Trouble | null>(null);
  const [standing, setStanding] = useState(false);
  const [reason, setReason] = useState('');

  const answerable =
    flagged.kind === 'number' || flagged.kind === 'words' || flagged.kind === 'yesno';

  /**
   * One attempt, sent and accounted for.
   *
   * The event id is minted once and held: it is also the `Idempotency-Key`, so a retry after a
   * reply that never arrived is answered with the stored response rather than becoming a
   * conflict against this phone's own successful write — which on this screen would tell an
   * operator that somebody else got there first, when the somebody was themselves.
   */
  const run = useCallback(
    async (act: 'apply' | 'reject', send: (id: string, event: string) => Promise<unknown>) => {
      const key = attemptKey(act, request.id);
      const event = eventFor(held, key, crypto.randomUUID());
      setHeld((current) => holdEvent(current, key, event));
      setBusy(act);
      setTrouble(null);
      try {
        await send(request.id, event);
        setHeld((current) => releaseEvent(current, key));
        onAnswered();
      } catch (error) {
        const failure = troubleOf(error, locale, act);
        setTrouble(failure);
        // A refusal forgets its id: nothing was written, and whatever the operator does next is
        // a new act rather than a replay of one the server has already answered.
        if (!keepsItsEvent(failure)) setHeld((current) => releaseEvent(current, key));
        // Answered while this phone was holding it. The row is not dropped — it is re-read and
        // shown as answered, so the operator sees what happened rather than a gap.
        if (alreadyAnswered(failure)) onAnswered();
      } finally {
        setBusy('');
      }
    },
    [held, locale, onAnswered, request.id],
  );

  const correct = useCallback(() => {
    const found = applyProblem(draft, flagged);
    setProblem(found);
    if (found !== null) return;
    void run('apply', (id, event) => {
      const body = toApply(draft, flagged, event);
      // Unreachable — `applyProblem` has already said the draft is an answer — and returning
      // rather than asserting, because a thrown error here would be an operator watching a
      // correction fail for a reason nobody can explain.
      if (body === null) return Promise.resolve(null);
      return applyCorrection(id, body);
    });
  }, [draft, flagged, run]);

  const stands = useCallback(() => {
    const found = rejectProblem(reason);
    setStandsProblem(found);
    if (found !== null) return;
    void run('reject', (id, event) => {
      const body = toReject(reason, event);
      if (body === null) return Promise.resolve(null);
      return rejectCorrection(id, body);
    });
  }, [reason, run]);

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      {answerable ? (
        <View style={{ gap: theme.spacing['2'] }}>
          <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
            {t('correctHeading')}
          </AppText>
          <AppText size="sm">{t('correctHint')}</AppText>

          {flagged.kind === 'number' ? (
            <MeasurementField
              label={measurement}
              value={draft.text}
              unit={draft.unit}
              units={unitsFor(request.code, flagged.unit)}
              onChangeValue={(text) => {
                setProblem(null);
                setDraft({ ...draft, text });
              }}
              onChangeUnit={(unit) => {
                setProblem(null);
                setDraft({ ...draft, unit });
              }}
              testID={`correction-value-${request.id}`}
            />
          ) : flagged.kind === 'words' ? (
            <TextInput
              testID={`correction-words-${request.id}`}
              value={draft.text}
              onChangeText={(text) => {
                setProblem(null);
                setDraft({ ...draft, text });
              }}
              accessibilityLabel={measurement}
              multiline
              style={inputStyle(colors)}
            />
          ) : (
            <View style={{ flexDirection: 'row', gap: theme.spacing['2'] }}>
              {[true, false].map((option) => (
                <Choice
                  key={String(option)}
                  testID={`correction-${option ? 'yes' : 'no'}-${request.id}`}
                  label={option ? t('yes') : t('no')}
                  selected={draft.bool === option}
                  onPress={() => {
                    setProblem(null);
                    setDraft({ ...draft, bool: option });
                  }}
                />
              ))}
            </View>
          )}

          {/* Optional, and it goes in with the correction. It is the corrector's own sentence
              to the person who flagged it — "the tape had slipped" — and it is never seeded
              from the note on the original, which belongs to whoever wrote that. */}
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('noteLabel')}
          </AppText>
          <TextInput
            testID={`correction-note-${request.id}`}
            value={draft.note}
            onChangeText={(note) => {
              setProblem(null);
              setDraft({ ...draft, note });
            }}
            placeholder={t('notePlaceholder')}
            placeholderTextColor={colors.text.muted}
            multiline
            accessibilityLabel={t('noteLabel')}
            style={inputStyle(colors)}
          />

          {problem === null ? null : (
            <AppText testID={`correction-problem-${request.id}`} size="sm" weight="medium">
              {say(`problem.${problem}`)}
            </AppText>
          )}

          <Act
            testID={`correction-apply-${request.id}`}
            label={busy === 'apply' ? t('applying') : t('apply')}
            busy={busy !== ''}
            primary
            onPress={correct}
          />
        </View>
      ) : null}

      {/* The other answer. One step further away than correcting, and never hidden: a value
          somebody was sure about and could not defend is a value that gets quietly changed. */}
      <View style={{ gap: theme.spacing['2'] }}>
        {standing ? (
          <>
            <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
              {t('standsHeading')}
            </AppText>
            {/* Above the field, not under it as a validation message. This sentence is the
                mechanism, not the error. */}
            <AppText size="sm">{t('standsWhy')}</AppText>
            <TextInput
              testID={`correction-reason-${request.id}`}
              value={reason}
              onChangeText={(next) => {
                setStandsProblem(null);
                setReason(next);
              }}
              placeholder={t('standsReasonPlaceholder')}
              placeholderTextColor={colors.text.muted}
              multiline
              accessibilityLabel={t('standsReasonLabel')}
              style={inputStyle(colors)}
            />
            {standsProblem === null ? null : (
              <AppText testID={`correction-stands-problem-${request.id}`} size="sm" weight="medium">
                {say(`problem.${standsProblem}`)}
              </AppText>
            )}
            <Act
              testID={`correction-reject-${request.id}`}
              label={busy === 'reject' ? t('rejecting') : t('reject')}
              busy={busy !== ''}
              primary={false}
              onPress={stands}
            />
            <Pressable
              testID={`correction-stands-cancel-${request.id}`}
              accessibilityRole="button"
              onPress={() => {
                setStanding(false);
                setStandsProblem(null);
              }}
              style={{ minHeight: theme.size.touchTargetCompact, justifyContent: 'center' }}
            >
              <AppText size="sm" style={{ color: colors.text.link }}>
                {t('standsCancel')}
              </AppText>
            </Pressable>
          </>
        ) : (
          <Pressable
            testID={`correction-stands-${request.id}`}
            accessibilityRole="button"
            onPress={() => setStanding(true)}
            style={{ minHeight: theme.size.touchTarget, justifyContent: 'center' }}
          >
            <AppText size="base" weight="semibold" style={{ color: colors.text.link }}>
              {t('standsOpen')}
            </AppText>
          </Pressable>
        )}
      </View>

      {trouble === null ? null : (
        <View testID={`correction-trouble-${request.id}`} style={{ gap: theme.spacing['1'] }}>
          <AppText size="base" weight="semibold">
            {alreadyAnswered(trouble)
              ? t('answeredElsewhere')
              : notYours(trouble)
                ? t('notYours')
                : say(`trouble.${trouble.kind}`)}
          </AppText>
          {trouble.message === '' ? null : (
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {trouble.message}
            </AppText>
          )}
          <AppText size="sm">{say(`advice.${adviceFor(trouble)}`)}</AppText>
        </View>
      )}
    </View>
  );
}

type Palette = ReturnType<typeof useTokens>['colors'];

function inputStyle(colors: Palette) {
  return {
    minHeight: theme.size.touchTarget,
    borderRadius: theme.borderRadius.md,
    borderWidth: theme.size.borderWidth.thin,
    borderColor: colors.border.control,
    backgroundColor: colors.surface.base,
    color: colors.text.primary,
    padding: theme.spacing['3'],
    fontSize: theme.fontSize.base,
  } as const;
}

/** One of two answers to a yes/no finding. Both visible, one tap each, never a toggle. */
function Choice({
  testID,
  label,
  selected,
  onPress,
}: {
  testID: string;
  label: string;
  selected: boolean;
  onPress: () => void;
}) {
  const { colors } = useTokens();
  return (
    <Pressable
      testID={testID}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      onPress={onPress}
      style={{
        flex: 1,
        minHeight: theme.size.touchTarget,
        alignItems: 'center',
        justifyContent: 'center',
        borderRadius: theme.borderRadius.md,
        borderWidth: theme.size.borderWidth.thin,
        borderColor: selected ? colors.brand.border : colors.border.subtle,
        backgroundColor: selected ? colors.brand.subtle : colors.surface.base,
      }}
    >
      <AppText size="lg" weight={selected ? 'semibold' : 'regular'}>
        {label}
      </AppText>
    </Pressable>
  );
}

/** A button, sized for a thumb, that goes quiet while either act is in flight. */
function Act({
  testID,
  label,
  busy,
  primary,
  onPress,
}: {
  testID: string;
  label: string;
  busy: boolean;
  primary: boolean;
  onPress: () => void;
}) {
  const { colors } = useTokens();
  return (
    <Pressable
      testID={testID}
      accessibilityRole="button"
      accessibilityState={{ disabled: busy }}
      disabled={busy}
      onPress={onPress}
      style={({ pressed }) => ({
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
        alignItems: 'center',
        paddingHorizontal: theme.spacing['4'],
        borderRadius: theme.borderRadius.md,
        borderWidth: primary && !busy ? 0 : theme.size.borderWidth.thin,
        borderColor: busy ? colors.state.disabledBorder : colors.border.control,
        backgroundColor: busy
          ? colors.state.disabledSurface
          : primary
            ? colors.brand.solid
            : colors.surface.base,
        opacity: pressed ? 0.85 : 1,
      })}
    >
      <AppText
        size="lg"
        weight="semibold"
        style={{
          color: busy
            ? colors.state.disabledText
            : primary
              ? colors.text.onBrand
              : colors.text.primary,
        }}
      >
        {label}
      </AppText>
    </Pressable>
  );
}

/**
 * A quiet block for a sentence that is not a clinical value: an absence, or a refusal.
 *
 * Deliberately not a status tone. Nothing has gone wrong with the patient when a queue is
 * empty or a hat cannot read it, and this application keeps its status colours for things that
 * are about a clinical value.
 */
function Panel({ children }: { children: ReactNode }) {
  const { colors } = useTokens();
  return (
    <View
      style={{
        gap: theme.spacing['1'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: theme.size.borderWidth.thin,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      {children}
    </View>
  );
}
