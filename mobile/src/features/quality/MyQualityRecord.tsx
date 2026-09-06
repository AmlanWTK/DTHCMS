import { useRouter } from 'expo-router';
import { useMemo, useState, type ReactNode } from 'react';
import { Pressable, View } from 'react-native';
import { useFormatter, useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { CORRECTIONS_HREF } from '@/features/corrections';
import { useRealtimeTopics } from '@/lib/realtime';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';
import { useSession } from '@/stores/session';

import { troubleOf, useMyQualityRecord, useQualityThresholds } from './api';
import {
  WINDOW_DAYS,
  adviceFor,
  answeredOf,
  byCodeOf,
  byHourOf,
  byReasonOf,
  myTopic,
  notesOf,
  questionsOf,
  rateOf,
  retentionOf,
  thresholdReadingsOf,
  windowOf,
  workDone,
  type Counted,
  type FlagReading,
  type Locale,
  type PatternRow,
  type ThresholdReading,
} from './state';

/**
 * My own record (CP63, §4.3, ADR-0029).
 *
 * # Everything here is arrangement; every decision is in `state.ts`
 *
 * The same split as every other feature on this surface. What is left here is where things
 * sit — which, on this screen more than any other, *is* the substance. The plan's stated risk
 * is that a metric which feels punitive makes staff hide their work rather than correct it,
 * and on a five-inch screen at the end of a shift, tone is almost entirely typography and
 * ordering. So the ordering is a constant with a test on it (`SECTIONS`), the counts arrive as
 * `Counted` values that cannot exist without a denominator, and this file adds no figure of
 * its own.
 *
 * # The first thing read is the work
 *
 * The largest thing on the screen is the number of values this operator entered. Not the
 * corrections — the work. That is the whole ordering criterion, and it is not decoration: a
 * screen that opens with "3" and reaches "412" halfway down has told somebody what it thinks
 * of them before they have read a word.
 *
 * # `Figure` is the only way a count is drawn, and it cannot draw one alone
 *
 * It takes a `Counted`, which `state.ts` will not mint without a denominator, and there is no
 * other component in this file that renders a number about this person's work. `quality.test.ts`
 * asserts that this file never reads `corrections`, `upheld`, `rejected`, `open`,
 * `observed_count` or `entries_count` off a record — every one of them reaches the glass
 * through a `Counted` or not at all.
 *
 * # `rejected` is drawn beside `upheld`, at the same size, in the same tone
 *
 * An operator who read the tape again and said the value stands did the job. Rendering that in
 * a quieter colour, or folding it into a total, teaches them to accept every flag without
 * looking — and the moment that happens the physician's flag has stopped meaning anything.
 *
 * # A note says on its face that nobody has approved it
 *
 * Every threshold this system ships with has `approved_at` null. The line saying so is inside
 * the note's own card, above the pattern it names, in the same size as the pattern — not a
 * footnote, not a badge, not a tooltip. A note raised on numbers nobody has agreed to,
 * presented as though it were policy, is the single most punitive thing this screen could do.
 *
 * # No red anywhere, and no status tone at all
 *
 * The one thing on this tablet allowed to be drawn in an alarm colour is a critical value in a
 * patient. A note about a colleague's month is a conversation somebody should have with them,
 * and the cards wear the ordinary brand edge — the same treatment CP62's correction panel
 * settled on, for the same reason.
 */
export function MyQualityRecord() {
  const t = useTranslations('quality');
  const format = useFormatter();
  const { colors } = useTokens();
  const locale = usePreferences((state) => state.language) as Locale;
  const me = useSession((state) => state.operator?.id ?? '');
  const router = useRouter();

  // A trouble kind and an advice each name their own sentence, so the key is a value rather
  // than a literal and `useTranslations` cannot type it — the same cast the other features
  // use, with the same guarantee behind it: `quality.test.ts` asserts every key this feature
  // can produce exists in both languages.
  const say = t as unknown as (key: string, values?: Record<string, string | number>) => string;

  /*
   * The operator's own channel, and nothing else.
   *
   * There is no message listener here any more. `realtimeInvalidations` in
   * `@dthcms/api-client` now turns every `quality.*` message — raised and resolved — into
   * `queryKeys.quality()`, and `gapInvalidations` does the same for a `user:` topic after a
   * dropped connection, which is the ordinary way an operator would otherwise hear about a
   * pattern from somebody else first. This feature's keys sit under that prefix, so a
   * listener of its own would be a second, quieter copy of a rule that now lives in one place
   * for both surfaces.
   *
   * The subscription stays, because a topic nobody asked for is a topic the gateway does not
   * send. Subscribed here as well as in the shell's line: the client reference-counts them,
   * and a screen that relied on another component's subscription would stop updating on the
   * day that component was removed.
   */
  useRealtimeTopics(useMemo(() => (myTopic(me) === '' ? [] : [myTopic(me)]), [me]));

  const signedIn = me !== '';
  const record = useMyQualityRecord(signedIn);
  const thresholds = useQualityThresholds(signedIn);

  const window = windowOf(record.data);
  const days = window.known ? window.days : WINDOW_DAYS;
  const entries = workDone(record.data);
  const questions = questionsOf(record.data);
  const answered = answeredOf(record.data);
  const rate = rateOf(record.data);
  const kept = retentionOf(record.data);
  const notes = useMemo(() => notesOf(record.data, locale), [record.data, locale]);
  const byReason = useMemo(() => byReasonOf(record.data, locale), [record.data, locale]);
  const byCode = useMemo(() => byCodeOf(record.data, locale), [record.data, locale]);
  const byHour = useMemo(() => byHourOf(record.data), [record.data]);
  const rules = useMemo(
    () => thresholdReadingsOf(thresholds.data, locale),
    [thresholds.data, locale],
  );

  const [showRules, setShowRules] = useState(false);

  const failure = record.error;
  const trouble = failure === null ? null : troubleOf(failure, locale);

  if (trouble !== null) {
    return (
      <View testID="quality-record" style={{ gap: theme.spacing['4'] }}>
        <Card>
          <AppText testID="quality-read-failed" size="base">
            {say(`trouble.${trouble.kind}`)}
          </AppText>
          {trouble.message === '' ? null : (
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {trouble.message}
            </AppText>
          )}
          <AppText size="sm">{say(`advice.${adviceFor(trouble)}`)}</AppText>
        </Card>
      </View>
    );
  }

  if (record.isPending) {
    return (
      <View testID="quality-record" style={{ gap: theme.spacing['4'] }}>
        <AppText size="base">{t('loading')}</AppText>
      </View>
    );
  }

  return (
    <View testID="quality-record" style={{ gap: theme.spacing['4'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('intro')}
      </AppText>

      <AppText testID="quality-window" size="xs" style={{ color: colors.text.muted }}>
        {window.known
          ? t('window', { days, from: window.from, to: window.to })
          : t('windowUnknown')}
      </AppText>

      {/* SECTIONS[0] — the work. The largest thing on the screen, and the only bare number on
          it: this is the denominator everything below is shown against, so it has nothing of
          its own to be shown out of. */}
      <Card testID="quality-work">
        {/* Deliberately *not* `variant="clinicalValue"`. That variant pins the Latin face for
            clinical values, and this is not one — it is a count of somebody's own work, which
            the Bangla interface renders in Bengali numerals. Inter carries no Bengali digit
            glyphs, so pinning it here would draw an operator's morning as a row of boxes. */}
        <AppText size="4xl" weight="bold">
          {format.number(entries)}
        </AppText>
        <AppText size="base">{t('work', { days })}</AppText>
        {entries === 0 ? (
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('workNone')}
          </AppText>
        ) : null}
      </Card>

      {/* SECTIONS[1] — how many of those a colleague asked about, out of that same total, and
          the rate or the sentence that stands where a rate would be misleading. */}
      {questions === null ? null : (
        <Card testID="quality-questions">
          <Figure figure={questions} label={t('questionsLabel')} />
          {/* The rate, or the arithmetic that says how many more values it needs. "Eight more
              values this month and a rate will appear" and "too few" are the same fact; only
              one of them reads as a number rather than as an opinion about the reader. The
              floor comes off the payload, so this sentence cannot promise the old one after a
              clinic changes it. */}
          <AppText testID="quality-rate" size="sm" style={{ color: colors.text.secondary }}>
            {rate.known
              ? t('rateKnown', { rate: rate.perHundred })
              : rate.needed > 0
                ? t('rateSoon', { needed: rate.needed, floor: rate.floor })
                : t('rateNotYet')}
          </AppText>
          {questions.count === 0 ? (
            <AppText testID="quality-nothing-questioned" size="sm">
              {t('nothingQuestioned')}
            </AppText>
          ) : null}
        </Card>
      )}

      {/* SECTIONS[2] — the four answers. Same size, same tone, no colour between them.
          `rejected` sits immediately after `upheld` because `OUTCOMES` says so and a test says
          `OUTCOMES` says so: those are the operator's own two answers and they belong beside
          each other. `overridden` — a correction a supervisor applied instead — is drawn apart
          from `upheld` rather than inside it, which is the whole reason CP62 made it a
          different event in the first place. */}
      {answered.length === 0 || questions === null || questions.count === 0 ? null : (
        <Card testID="quality-answered">
          <AppText size="sm" weight="semibold">
            {t('answeredLabel')}
          </AppText>
          <View
            style={{
              flexDirection: 'row',
              flexWrap: 'wrap',
              gap: theme.spacing['4'],
            }}
          >
            {answered.map((row) => (
              <View key={row.outcome} testID={`quality-outcome-${row.outcome}`} style={{ flex: 1 }}>
                <Figure figure={row.figure} label={say(`outcome.${row.outcome}`)} />
              </View>
            ))}
          </View>
          <AppText size="xs" style={{ color: colors.text.secondary }}>
            {t('defending')}
          </AppText>
          {/* The one act on this screen, and it belongs to the other one: a request still
              waiting is answered at the station, on CP62's queue. */}
          <Pressable
            testID="quality-open-corrections"
            accessibilityRole="button"
            accessibilityHint={t('openHint')}
            onPress={() => router.push(CORRECTIONS_HREF)}
            style={({ pressed }) => ({
              minHeight: theme.size.touchTarget,
              justifyContent: 'center',
              opacity: pressed ? 0.85 : 1,
            })}
          >
            <AppText size="sm" weight="semibold" style={{ color: colors.text.link }}>
              {t('openAction')}
            </AppText>
          </Pressable>
        </Card>
      )}

      {/* SECTIONS[3] — the notes. Above the detail and below the counts: never the first thing
          read, and never something to scroll for. */}
      {notes.map((note) => (
        <Note key={note.id} note={note} keptDays={kept} />
      ))}

      {/* SECTIONS[4] — where the questions fell. Only when there were any: three empty lists
          under three headings is a screen that looks like it is hiding something. */}
      {questions !== null && questions.count > 0 ? (
        <Card testID="quality-patterns">
          <AppText size="sm" weight="semibold">
            {t('patternsLabel')}
          </AppText>
          {/* Each grouping against its own denominator, and each saying which one. A reason
              has no denominator of its own — nobody does a number of "transcriptions" — so it
              is counted against the questions asked. A measurement and an hour do: how many
              weights this operator took, how many values they entered at four in the
              afternoon. Labelling all three the same way would have been the mistake the
              server's own comment names, where a rota reads as a person. */}
          <Pattern
            rows={byReason}
            heading={t('patternReason')}
            note={t('patternReasonNote')}
            testID="quality-pattern-reason"
          />
          <Pattern
            rows={byCode}
            heading={t('patternCode')}
            note={t('patternCodeNote')}
            testID="quality-pattern-code"
          />
          <Pattern
            rows={byHour}
            heading={t('patternHour')}
            note={t('patternHourNote')}
            testID="quality-pattern-hour"
            latin
          />
        </Card>
      ) : null}

      {/* SECTIONS[5] — the rules, readable before one is ever applied. Collapsed by default
          because it is reference rather than news, and one press from open because a rule
          somebody has to ask a supervisor to explain is a rule that arrives as an ambush. */}
      <Card testID="quality-rules">
        <Pressable
          testID="quality-rules-toggle"
          accessibilityRole="button"
          accessibilityState={{ expanded: showRules }}
          onPress={() => setShowRules((was) => !was)}
          style={({ pressed }) => ({
            minHeight: theme.size.touchTarget,
            justifyContent: 'center',
            opacity: pressed ? 0.85 : 1,
          })}
        >
          <AppText size="sm" weight="semibold" style={{ color: colors.text.link }}>
            {showRules ? t('rulesHide') : t('rulesShow')}
          </AppText>
        </Pressable>

        {showRules ? (
          <View style={{ gap: theme.spacing['3'] }}>
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('rulesIntro')}
            </AppText>
            {rules.length === 0 ? (
              <AppText size="sm">{t('rulesMissing')}</AppText>
            ) : (
              rules.map((rule) => <Rule key={rule.code} rule={rule} />)
            )}
          </View>
        ) : null}
      </Card>

      {/* Said out loud, at the bottom, every time — exactly as CP62's queue does. The cheapest
          defence against a mechanism that starts to feel punitive is to keep saying what it is
          for, and the cost of saying it is one line. */}
      <AppText testID="quality-purpose" size="xs" style={{ color: colors.text.muted }}>
        {t('notBlame')}
      </AppText>
    </View>
  );
}

/**
 * A count, and the number it came out of, in one sentence.
 *
 * The only component in this file that draws a number about a person's work, and it takes a
 * `Counted` — which `state.ts` will not produce without both halves. There is no variant of
 * this that takes a bare number, and adding one would mean adding a way to mint a `Counted`,
 * which is a line somebody would have to write on purpose and a reviewer would see.
 *
 * The figure sits *below* its label rather than above it. A column of large numerals with
 * small words under them reads as a scoreboard; the label first reads as a sentence.
 */
function Figure({ figure, label }: { figure: Counted; label: string }) {
  const t = useTranslations('quality');
  const { colors } = useTokens();

  return (
    <View style={{ gap: theme.spacing['1'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
      <AppText size="lg" weight="semibold">
        {t('figure', { count: figure.count, outOf: figure.outOf })}
      </AppText>
    </View>
  );
}

/**
 * One note on this record, open or answered.
 *
 * # Open
 *
 * Five things, in this order: that nobody has approved the numbers, what the pattern is, what
 * was counted and out of how many, the corrections it was raised on, and what it suggests
 * somebody does. The approval line is first because it is the one that changes how everything
 * under it should be read, and a reader who meets it last has already read the rest as a
 * finding.
 *
 * # Answered
 *
 * The decision comes first instead — what was decided, by whom, and in their own words why —
 * and the pattern and its figure sit underneath as the thing that was decided about. A note
 * that appeared on somebody's device and then silently vanished when a supervisor closed it
 * would teach them that things are decided about them out of sight, which is the same ambush
 * the raise-time message exists to prevent arriving from the other end. So it stays, and it
 * says what happened.
 *
 * A dismissal is not drawn as a defeat and an acknowledgement is not drawn as a verdict:
 * neither wears a status colour, and the difference between them is a sentence.
 *
 * # The evidence
 *
 * The corrections behind the note, with no patient and no value in any of them — a supervisor
 * reading a patient's measurements through their staff's record would be reading clinical data
 * through a side door, and a database invariant refuses a row that carries either. It is here
 * because a number somebody can see is a number they can argue with, which ADR-0029 names as
 * the real defence against the cultural risk. Each row says what it *was* when the note was
 * written and the day that was true, because a frozen record that reads as current is a record
 * that misleads quietly.
 *
 * The pattern, the action, the reasons and the measurement names are all the server's own
 * sentences in the reader's language. None is restated in the message files: they come off
 * rows a clinician or a registry owns, and a copy here would be a second, staler account of a
 * vocabulary this application does not own.
 */
function Note({ note, keptDays }: { note: FlagReading; keptDays: number }) {
  const t = useTranslations('quality');
  const { colors } = useTokens();
  const say = t as unknown as (key: string, values?: Record<string, string | number>) => string;

  return (
    <View
      testID={`quality-note-${note.id}`}
      style={{
        gap: theme.spacing['3'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: theme.size.borderWidth.thin,
        // The brand edge while it is open, the ordinary one once it has been answered — and
        // no status tone in either case. A red border here would be this screen's loudest
        // statement, and what it would be stating is that somebody is in trouble.
        borderColor: note.state === 'open' ? colors.brand.border : colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      {/* Answered: the decision, first. Open: that nobody has approved the numbers, first.
          Whichever it is, it is the line that decides how the rest should be read. */}
      {note.state === 'open' ? (
        <AppText
          testID={`quality-note-${note.id}-approval`}
          size="sm"
          weight="semibold"
          style={{ color: colors.brand.text }}
        >
          {note.approved ? t('noteApproved') : t('noteNotApproved')}
        </AppText>
      ) : (
        <View testID={`quality-note-${note.id}-decision`} style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm" weight="semibold" style={{ color: colors.brand.text }}>
            {say(`noteState.${note.state}`)}
          </AppText>
          <AppText size="xs" style={{ color: colors.text.secondary }}>
            {note.decidedBy === ''
              ? t('noteDecidedUnnamed', { date: note.decidedOn })
              : t('noteDecidedBy', { who: note.decidedBy, date: note.decidedOn })}
          </AppText>
          {note.reason === '' ? null : (
            <AppText size="sm">{t('noteReason', { reason: note.reason })}</AppText>
          )}
          {/* How long it will go on being here, in the server's own number. A decision that
              disappears one day without warning is the same silent vanishing keeping it was
              meant to prevent — and a retention promised by a sentence in a message file goes
              on being promised after somebody changes the rule. Nothing is said when the
              server sent no number: a wrong promise is worse than none. */}
          {keptDays === 0 ? null : (
            <AppText size="2xs" style={{ color: colors.text.muted }}>
              {t('noteKept', { days: keptDays })}
            </AppText>
          )}
        </View>
      )}

      <AppText size="base" weight="semibold">
        {note.threshold === '' ? t('noteUnnamed', { code: note.code }) : note.threshold}
      </AppText>

      {note.figure === null ? null : (
        <Figure
          figure={note.figure}
          label={note.windowDays > 0 ? t('noteOver', { days: note.windowDays }) : t('noteCounted')}
        />
      )}

      {note.raised === '' ? null : (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('noteRaised', { date: note.raised })}
        </AppText>
      )}

      {note.evidence.length === 0 ? null : (
        <View testID={`quality-note-${note.id}-evidence`} style={{ gap: theme.spacing['2'] }}>
          <AppText size="xs" weight="semibold" style={{ color: colors.text.secondary }}>
            {t('noteEvidenceLabel')}
          </AppText>
          {note.evidence.map((item) => (
            <AppText key={item.id} size="sm">
              {t('noteEvidenceLine', {
                measurement: item.measurement,
                reason: item.reason,
                date: item.day,
                time: item.hour,
              })}
            </AppText>
          ))}
          {/* Once, beneath the rows, not under each one. `status_as_of` is the moment the note
              was written and is the same on every row of it; repeating it per line implied it
              varied, which is the reading that sends somebody looking for the fresher row. */}
          {note.frozenOn === '' ? null : (
            <AppText size="2xs" style={{ color: colors.text.muted }}>
              {t('noteEvidenceAsOf', { date: note.frozenOn })}
            </AppText>
          )}
        </View>
      )}

      {/* Only while it is still open. Once somebody has answered it, what a supervisor was
          asked to do has been overtaken by what they actually did, and printing the suggestion
          underneath their decision would read as a second opinion about their judgement. */}
      {note.action === '' || note.state !== 'open' ? null : (
        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="xs" style={{ color: colors.text.secondary }}>
            {t('noteActionLabel')}
          </AppText>
          <AppText size="sm">{note.action}</AppText>
        </View>
      )}
    </View>
  );
}

/**
 * One grouping — by reason, by measurement, by hour.
 *
 * Every row carries its own denominator, because a list of bare counts under a heading is a
 * ranking, and a ranking of the ways one person's values were questioned is the scorecard this
 * screen exists not to be. Which denominator differs by grouping — the questions asked, that
 * measurement's own entries, that hour's own entries — so `note` says which, above the rows
 * rather than under them.
 *
 * Nothing is sorted here. The payload now comes back in stable code order rather than
 * count-descending, which is the order this screen wants: a phone that re-sorted, or a server
 * that ranked, would be putting the biggest number about somebody's month at the top of a list
 * about them.
 *
 * `latin` pins the Latin face for the clock times, which are the same characters in either
 * interface language. The measurement names are no longer among them — they arrive as a
 * bilingual pair now, so a Bangla reader gets words rather than `BODY_WEIGHT`.
 */
function Pattern({
  rows,
  heading,
  note,
  testID,
  latin = false,
}: {
  rows: readonly PatternRow[];
  heading: string;
  /** What this grouping's figures are counted against. Never left for the reader to infer. */
  note: string;
  testID: string;
  latin?: boolean;
}) {
  const t = useTranslations('quality');
  const { colors } = useTokens();

  if (rows.length === 0) return null;

  return (
    <View testID={testID} style={{ gap: theme.spacing['2'] }}>
      <AppText size="xs" weight="semibold" style={{ color: colors.text.secondary }}>
        {heading}
      </AppText>
      <AppText size="2xs" style={{ color: colors.text.muted }}>
        {note}
      </AppText>
      {rows.map((row) => (
        <View
          key={row.label}
          style={{
            flexDirection: 'row',
            flexWrap: 'wrap',
            justifyContent: 'space-between',
            gap: theme.spacing['2'],
            alignItems: 'center',
          }}
        >
          <AppText
            size="sm"
            variant={latin ? 'clinicalValue' : 'ui'}
            style={{ flexShrink: 1, flexGrow: 1 }}
          >
            {row.label}
          </AppText>
          <AppText size="sm" weight="semibold">
            {t('figure', { count: row.figure.count, outOf: row.figure.outOf })}
          </AppText>
        </View>
      ))}
    </View>
  );
}

/**
 * One rule, as an operator may read it before it is ever applied to them.
 *
 * The sentence is the server's own — `looks_for_en` / `looks_for_bn`, rendered from the live
 * row with Bengali numerals in the Bangla — rather than one this feature composes from the
 * row's numbers. The server renders it that way precisely so a threshold whose count changes
 * cannot leave a description of the old one behind, and a client that rebuilt it would put the
 * staleness straight back in two languages instead of one. The version that lived in the
 * message files while this field was English only is deleted.
 */
function Rule({ rule }: { rule: ThresholdReading }) {
  const t = useTranslations('quality');
  const { colors } = useTokens();

  return (
    <View testID={`quality-rule-${rule.code}`} style={{ gap: theme.spacing['1'] }}>
      <AppText size="sm" weight="semibold">
        {rule.display === '' ? rule.code : rule.display}
      </AppText>
      {rule.looksFor === '' ? null : (
        <AppText size="xs" style={{ color: colors.text.secondary }}>
          {rule.looksFor}
        </AppText>
      )}
      {rule.action === '' ? null : (
        <AppText size="xs" style={{ color: colors.text.secondary }}>
          {rule.action}
        </AppText>
      )}
      <AppText size="xs" style={{ color: colors.brand.text }}>
        {rule.approved ? t('ruleApproved') : t('ruleNotApproved')}
      </AppText>
    </View>
  );
}

/** The ordinary card, so every panel on this screen wears one edge and one ground. */
function Card({ children, testID }: { children: ReactNode; testID?: string }) {
  const { colors } = useTokens();

  return (
    <View
      testID={testID}
      style={{
        gap: theme.spacing['3'],
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
