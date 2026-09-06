import type { ReactNode } from 'react';
import { Pressable, ScrollView, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { EnteredBy, ofCounselingTick } from '@/features/attribution';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  adviceFor,
  alreadyCovered,
  clockTime,
  completionOf,
  attributionKey,
  coveredBy,
  mayTick as hatMayTick,
  noteOpenable,
  progressOf,
  roomsOf,
  rowsOf,
  sessionOpen,
  sessionTitle,
  startableOf,
  tapsToComplete,
  troubleKey,
  untickRefused,
  type Attribution,
  type ChecklistChoice,
  type Completion,
  type CounselingSession,
  type ItemRow,
  type Locale,
  type RoomGroup,
  type Trouble,
  type Wording,
} from './state';

/**
 * The counselling checklist, on the counsellor's phone (CP56, §5.3, [R-07]).
 *
 * # Everything here is arrangement; every decision is in `state.ts`
 *
 * The same split as every other station: this component cannot be rendered outside a device,
 * so anything it decided would be a decision nobody checks. What it holds is where things sit
 * — which matters here more than at most stations, because this list is read at arm's length,
 * in a room with a queue in it, by somebody who is also talking to a patient.
 *
 * # One list, with the rooms as headings
 *
 * §5.2 walks three rooms and the obvious design is three screens with a "next room" button.
 * That would turn a seven-tap session into seven taps plus four navigations, and it would hide
 * from the nutritionist what the counselling room already covered — which is precisely what
 * stops them asking the same questions again. So the rooms are headings on one scroll, the
 * whole checklist is always visible, and the only thing the operator's own station changes is
 * which heading is marked as theirs. Nothing is filtered by station: a room that is not yours
 * is still drawn, still ticked, still readable.
 *
 * # One tap covers one item, and there is no dialog in this file
 *
 * No confirmation, no "are you sure", no per-item modal. A dialog on the honest act doubles
 * its cost and teaches people to dismiss dialogs, which is a habit they carry to the dialog
 * that matters. `tapsToComplete` is the number that keeps this true, and it is asserted in the
 * tests rather than measured with a stopwatch in a busy clinic.
 *
 * The second step exists in exactly one place — taking a tick back — because that is the rarer
 * and more consequential act, and because criterion 3 requires a reason for it.
 *
 * # A covered item is not a toggle
 *
 * Tapping the tick control of a covered item does nothing. Making it a toggle would mean one
 * stray thumb on a phone held one-handed silently withdraws a tick with no reason — which the
 * server would refuse anyway, so all it would buy is a refusal in front of a patient. Taking a
 * tick back is its own control with its own reason box.
 *
 * # Every tick says whose it is
 *
 * Two counsellors and an insulin corner are involved in one session, so a covered row names
 * the person and the time. "Covered by you" is reachable only when the ids actually match; an
 * unknown reader reads as somebody else, never as you.
 *
 * # Finishing is never drawn as a failure
 *
 * The button changes its words when items are outstanding and names them above itself, and
 * that is all it changes. `Tone` has no failure value, so no row and no banner on this screen
 * can be red: a counsellor whose patient walked out is making an honest record, and a screen
 * that treated it as an error would make the dishonest record the cheaper one.
 *
 * # Starting is offered, and choosing is not
 *
 * The server answers which checklists this visit calls for, and this screen offers to open one
 * of **those** — with the coded condition that called for it printed on the control, so "why am
 * I being asked to do this" is answered where the question gets asked. There is no picker and no
 * search: assignment is a rule keyed on a coding (§5.1), and a list of every checklist in the
 * clinic would put that clinical decision in the hands of the person least placed to make it.
 * Where the answer names a session already open, the screen resumes it rather than offering to
 * start a second walk down the same list.
 *
 * A hat that may only read sees the same row without the control — a checklist this visit called
 * for and nobody opened, said as a fact. That question reads with `counseling.session.read` now,
 * and it is the half a gate refusal turns on: a reviewer shown nothing here would read a visit
 * whose counselling never happened as a visit that called for none.
 *
 * # A tick somebody else already made is not the counsellor's mistake
 *
 * The server refuses a tick on a covered item rather than quietly re-attributing it, and says so
 * with its own code. That refusal gets its own sentence and the colleague's name and time —
 * because the alternative reads as "you did something wrong" to somebody who did not. The name
 * comes from the record: the screen above reads the session again on that code, so what this
 * component draws is data rather than an inference about who is likely to have got there first.
 *
 * # The checkpoint is the last word on the screen
 *
 * `gate` is CP57's step, and it sits after the finish control in both branches — including the
 * one where no checklist has been opened, which is where it has the most to say. That is where
 * the question it answers actually gets asked: *and now, may this patient go on?* Above the
 * items it would be a loud panel at the top of every session from its first second, because a
 * checklist somebody is halfway down is a visit the gate is holding by definition — and a screen
 * that is loud for most of every session is a screen people stop reading.
 *
 * Nothing on this list consults it, and it consults nothing here. The checkpoint is the queue's
 * and this list is the counsellor's; they agree because they read the same outstanding list from
 * the same database function, not because either asks the other's opinion.
 */
export function CounselingStation({
  session,
  choices,
  gate,
  loading,
  patientName,
  station,
  me,
  permissions,
  noting,
  note,
  unticking,
  untickReason,
  busyItem,
  starting,
  finishing,
  trouble,
  troubleCode,
  onTick,
  onOpenNote,
  onChangeNote,
  onStartUntick,
  onChangeUntickReason,
  onUntick,
  onFinish,
  onReload,
  onChooseSession,
  onStartChecklist,
}: {
  /** The server's own answer, or null until it has arrived. Never assembled on this side. */
  session: CounselingSession | null;
  /**
   * What this visit calls for and what has been walked, merged by `choicesOf`.
   *
   * A patient with type 2 diabetes and hypothyroidism gets one per matching assignment rule, and
   * both are walked in the same rooms by the same people. Built in the screen above rather than
   * here because the same list decides which session is fetched at all.
   */
  choices: readonly ChecklistChoice[];
  /**
   * CP57's checkpoint, drawn last — see the note above. A slot rather than a component this
   * file builds, for the same reason station 4's allergy gate is one: what the gate says is a
   * read of its own, and a list that fetched it would be a list deciding when to ask.
   */
  gate?: ReactNode;
  /** True while the answer is still in flight. False and no session is a different fact. */
  loading: boolean;
  patientName: string;
  /** The station this hat works, so the operator's own room can be marked. */
  station: string;
  /** The operator's own id. Empty until the session store answers; then nothing reads as "you". */
  me: string;
  /** The hat's permissions — a courtesy for choosing a sentence, never a control. */
  permissions: readonly string[];
  /** The item whose note box is open, or null. Only ever one at a time. */
  noting: string | null;
  note: string;
  /** The item being taken back, or null. */
  unticking: string | null;
  untickReason: string;
  /** The item with a write in flight, so one row goes quiet rather than the whole screen. */
  busyItem: string | null;
  /** The template being opened, or null. One checklist at a time, for the same reason. */
  starting: string | null;
  finishing: boolean;
  trouble: Trouble | null;
  /** The item the trouble was about, so a colleague's tick can be named. Empty otherwise. */
  troubleCode: string;
  onTick: (code: string) => void;
  onOpenNote: (code: string | null) => void;
  onChangeNote: (text: string) => void;
  onStartUntick: (code: string | null) => void;
  onChangeUntickReason: (text: string) => void;
  onUntick: (code: string) => void;
  onFinish: () => void;
  onReload: () => void;
  onChooseSession: (sessionId: string) => void;
  onStartChecklist: (templateId: string) => void;
}) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();
  const locale = usePreferences((preference) => preference.language);

  // A message key that is a value rather than a literal — an advice, a trouble, an attribution
  // — cannot be typed by `useTranslations`. The same cast the other stations use, with the same
  // guarantee behind it: the test file asserts every key this code can produce exists in both
  // languages.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;

  const reader = { locale: locale as Locale, me };
  const allowed = hatMayTick(permissions);

  if (session === null) {
    return (
      // A scroll rather than a plain view, because the checkpoint below can name seven items in
      // three rooms and this is the branch where it names the most — a visit nobody has opened a
      // checklist for is the one the gate has most to say about.
      <ScrollView
        testID="counseling-station"
        contentContainerStyle={{
          gap: theme.spacing['4'],
          paddingBottom: theme.spacing['12'],
        }}
      >
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('station')}
        </AppText>
        {loading ? (
          /* Not an empty list. A screen that drew nothing here while the answer was in flight
             would read as "nothing has been covered" for as long as the clinic's link takes,
             which on this screen is a lie about a patient's counselling. */
          <AppText size="base">{t('loading')}</AppText>
        ) : choices.length > 0 ? (
          /* The visit calls for something, or something was walked and this phone has not read
             it back yet. Either way the row belongs here rather than behind a menu: a counsellor
             with a patient in front of them should not go looking for the checklist the record
             already asked for. */
          <>
            <Checklists
              choices={choices}
              chosen=""
              allowed={allowed}
              starting={starting}
              onChoose={onChooseSession}
              onStart={onStartChecklist}
            />
            {/* Said here as well as on an open session, because this is the branch a reviewer
                lands on: a checklist called for, nothing walked, and no control anywhere. The
                sentence is what turns that from a screen that looks broken into one that says
                whose writing it is. */}
            {!allowed ? <MayNotTick /> : null}
          </>
        ) : (
          <>
            {/* Two opposite facts, and they must never share a sentence: nobody has opened a
                checklist for this visit, and a checklist is open with nothing covered yet. */}
            <AppText size="base" weight="semibold">
              {t('noSession')}
            </AppText>
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('noSessionHint')}
            </AppText>
          </>
        )}

        {/* Last, as on an open session. Here it is the only thing that says whether the patient
            is being held, and by which items — which is exactly what a visit with no session
            opened cannot say for itself. */}
        {gate}
      </ScrollView>
    );
  }

  const rows = rowsOf(session, reader);
  const groups = roomsOf(rows, reader.locale, station);
  const progress = progressOf(session);
  const completion = completionOf(session, rows);

  return (
    <ScrollView
      testID="counseling-station"
      contentContainerStyle={{ gap: theme.spacing['5'], paddingBottom: theme.spacing['12'] }}
      keyboardShouldPersistTaps="handled"
    >
      <View style={{ gap: theme.spacing['0.5'] }}>
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('station')}
        </AppText>
        <AppText size="lg" weight="semibold">
          {patientName}
        </AppText>
        <AppText size="base">{sessionTitle(session, reader.locale)}</AppText>
        {/* The version, said out loud. This list is the one the session opened against, and a
            counsellor who has heard "the checklist changed this morning" needs to know that
            what is under their thumb did not. */}
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('frozen', { version: String(session.template_version) })}
        </AppText>
      </View>

      {/* Drawn when there is a choice to make or something still to open. One chip beside one
          open checklist is furniture that teaches an operator to look for a control that is not
          usually there. */}
      {choices.length > 1 || startableOf(choices).length > 0 ? (
        <Checklists
          choices={choices}
          chosen={session.id}
          allowed={allowed}
          starting={starting}
          onChoose={onChooseSession}
          onStart={onStartChecklist}
        />
      ) : null}

      <Progress
        covered={progress.covered}
        mandatory={progress.mandatory}
        optional={progress.optional}
        optionalCovered={progress.optionalCovered}
        complete={progress.complete}
        taps={tapsToComplete(session.items ?? [])}
        finishedAt={clockTime(session.completed_at ?? '')}
      />

      {!allowed ? <MayNotTick /> : null}

      {groups.map((group) => (
        <RoomSection key={group.room} group={group}>
          {group.rows.map((row) => (
            <Item
              key={row.code}
              row={row}
              say={say}
              open={sessionOpen(session)}
              allowed={allowed}
              busy={busyItem === row.code}
              noting={noting === row.code}
              note={note}
              unticking={unticking === row.code}
              untickReason={untickReason}
              onTick={() => onTick(row.code)}
              onOpenNote={onOpenNote}
              onChangeNote={onChangeNote}
              onStartUntick={onStartUntick}
              onChangeUntickReason={onChangeUntickReason}
              onUntick={() => onUntick(row.code)}
            />
          ))}
        </RoomSection>
      ))}

      <Finish
        completion={completion}
        say={say}
        allowed={allowed}
        busy={finishing}
        onFinish={onFinish}
      />

      {/* And now, may this patient go on? The question is asked here, beside the press that
          records the counsellor being done, rather than at the top of a screen somebody is
          halfway down. */}
      {gate}

      {trouble !== null ? (
        <TroubleNote
          trouble={trouble}
          say={say}
          /* Who covered it, from the session this phone is holding. At the moment the refusal
             lands it does not know — a phone that knew would not have offered the tick — and the
             screen above re-reads the session on that code, which is what fills this in. Until
             it arrives, and if it never does, the sentence omits the name rather than guessing. */
          covered={coveredBy(session, troubleCode, me)}
          onReload={onReload}
        />
      ) : null}
    </ScrollView>
  );
}

type Say = (key: string, values?: Record<string, string>) => string;

/**
 * The sentence a hat that reads counselling without writing it gets, before the first tap.
 *
 * A courtesy and never a control: the server decides, and this is only ever a sentence — a
 * client that treated its own copy of a grant as the answer would be a second, staler account of
 * a rule it does not own. Said rather than discovered as a 403 in front of a patient.
 *
 * It goes to a reviewer and a physician's panel, and to nobody who counsels: `counseling.tick`
 * belongs to the nutritionist and the exercise and prescription-education officers as well as
 * the counsellor, because §5.2 walks three rooms and only the counsellor holding it would have
 * meant every other room's items ticked by proxy. So the words say what the hat is for and send
 * nobody off to change roles.
 */
function MayNotTick() {
  const t = useTranslations('counseling');
  const { colors } = useTokens();
  return (
    <AppText testID="counseling-may-not-tick" size="sm" style={{ color: colors.text.secondary }}>
      {t('mayNotTick')}
    </AppText>
  );
}

/**
 * Progress, as two numbers and never a percentage.
 *
 * "Five of seven" is what a counsellor says out loud when somebody asks how far they have got.
 * A percentage rounds away the difference between finished and nearly finished, and on a list
 * whose last item is insulin technique that is the difference that matters. There is no bar
 * here either: a bar is a percentage drawn sideways.
 *
 * Optional items are reported on their own line and are not folded into the figure, because
 * the gate does not count them and a screen that did would disagree with the gate.
 */
function Progress({
  covered,
  mandatory,
  optional,
  optionalCovered,
  complete,
  taps,
  finishedAt,
}: {
  covered: number;
  mandatory: number;
  optional: number;
  optionalCovered: number;
  complete: boolean;
  taps: number;
  finishedAt: string;
}) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();

  return (
    <View
      testID="counseling-progress"
      style={{
        gap: theme.spacing['1'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      {/* Two numbers. Not `clinicalValue` — that pins the Latin face, and this sentence is
          words as well as figures, which in Bangla would come out as empty boxes. */}
      <AppText testID="counseling-covered-of" size="2xl" weight="bold">
        {t('progress', { covered: String(covered), mandatory: String(mandatory) })}
      </AppText>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('progressHint')}
      </AppText>
      {optional > 0 ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('optionalCovered', {
            covered: String(optionalCovered),
            optional: String(optional),
          })}
        </AppText>
      ) : null}
      {complete ? (
        <AppText size="sm" weight="semibold">
          {finishedAt === '' ? t('finished') : t('finishedAt', { when: finishedAt })}
        </AppText>
      ) : (
        // The cost of the whole session, before it starts. It is here because it is the promise
        // this screen makes: one tap an item, one to finish, and nothing else in the way.
        <AppText size="sm" style={{ color: colors.text.muted }}>
          {t('taps', { n: String(taps) })}
        </AppText>
      )}
    </View>
  );
}

/**
 * The visit's checklists: the ones being walked, and the ones nothing has opened yet.
 *
 * A patient with type 2 diabetes and hypothyroidism gets one per matching assignment rule, and
 * both are walked in the same three rooms by the same two people. Choosing between them is a
 * real choice — unlike the rooms, which are one list — so it is a row of names, all visible, one
 * tap each, with the finished ones saying so.
 *
 * A checklist the record calls for and nobody has opened is the same row with a different
 * control: it names the checklist, names the coded condition that called for it, and opens it.
 * There is no picker beside it and no way to reach one — which checklist a patient gets is a
 * rule keyed on a coding (§5.1), and this screen only ever offers what the server named.
 *
 * To a hat that may read and not tick it is that row without the control, and it is drawn rather
 * than hidden: what a visit *should* have been walked through is what explains a patient being
 * held, and it is the one thing an empty screen cannot say.
 */
function Checklists({
  choices,
  chosen,
  allowed,
  starting,
  onChoose,
  onStart,
}: {
  choices: readonly ChecklistChoice[];
  chosen: string;
  allowed: boolean;
  starting: string | null;
  onChoose: (sessionId: string) => void;
  onStart: (templateId: string) => void;
}) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();

  return (
    <View testID="counseling-checklists" style={{ gap: theme.spacing['2'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('checklists', { n: String(choices.length) })}
      </AppText>

      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
        {choices
          .filter((choice) => choice.started)
          .map((choice) => {
            const selected = choice.sessionId === chosen;
            return (
              <Pressable
                key={choice.sessionId}
                testID={`counseling-checklist-${choice.sessionId}`}
                accessibilityRole="button"
                accessibilityState={{ selected }}
                onPress={() => onChoose(choice.sessionId)}
                style={{
                  minHeight: theme.size.touchTarget,
                  justifyContent: 'center',
                  paddingHorizontal: theme.spacing['4'],
                  borderRadius: theme.borderRadius.md,
                  borderWidth: 1,
                  borderColor: selected ? colors.brand.border : colors.border.subtle,
                  backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
                }}
              >
                <AppText size="sm" weight={selected ? 'semibold' : 'regular'}>
                  {choice.title}
                </AppText>
                {/* Said in words rather than greyed out. A finished checklist is a fact about
                    this visit, not a disabled control. */}
                {choice.finished ? (
                  <AppText size="xs" style={{ color: colors.text.muted }}>
                    {t('alreadyFinished')}
                  </AppText>
                ) : null}
              </Pressable>
            );
          })}
      </View>

      {/* Drawn for everybody; only the control is behind the permission. The checklist answer
          reads with `counseling.session.read` now, so a reviewer asking why a patient was held
          can see what the visit called for — and a row hidden from them would have said the
          visit called for nothing, which is the opposite of what they are looking at. */}
      {startableOf(choices).map((choice) =>
        allowed ? (
          <StartChecklist
            key={choice.templateId}
            choice={choice}
            busy={starting === choice.templateId}
            onStart={() => onStart(choice.templateId)}
          />
        ) : (
          <UnwalkedChecklist key={choice.templateId} choice={choice} />
        ),
      )}
    </View>
  );
}

/**
 * Opening a checklist the record calls for.
 *
 * One control, and it says three things in the order they are asked: which checklist, which
 * recorded condition called for it, and that pressing opens it. The condition is on the control
 * rather than in a policy document because a counsellor handed a list of seven questions
 * deserves to know why this patient is getting them — and because a checklist that arrived from
 * a mis-coded condition is caught by the person holding the phone or by nobody.
 */
function StartChecklist({
  choice,
  busy,
  onStart,
}: {
  choice: ChecklistChoice;
  busy: boolean;
  onStart: () => void;
}) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();

  return (
    <Pressable
      testID={`counseling-start-${choice.templateId}`}
      accessibilityRole="button"
      accessibilityState={{ disabled: busy }}
      disabled={busy}
      onPress={onStart}
      style={({ pressed }) => ({
        gap: theme.spacing['1'],
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 2,
        borderColor: busy ? colors.state.disabledBorder : colors.border.control,
        backgroundColor: busy ? colors.state.disabledSurface : colors.surface.base,
        opacity: pressed ? 0.85 : 1,
      })}
    >
      <AppText size="lg" weight="semibold">
        {choice.title}
      </AppText>
      {/* Absent for a session the server could no longer match to a rule — a rule retired at
          lunchtime does not make a half-ticked session disappear — and left unsaid rather than
          filled with a guess at which condition it was. */}
      {choice.matched !== '' ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('calledFor', { code: choice.matched })}
        </AppText>
      ) : null}
      <AppText size="base" weight="semibold" style={{ color: colors.text.link }}>
        {busy ? t('startingChecklist') : t('startChecklist')}
      </AppText>
    </Pressable>
  );
}

/**
 * The same checklist, to somebody who cannot open it.
 *
 * A reviewer or a physician's panel asking why this patient was held needs what the visit
 * *should* have been walked through, and this is the only place it appears. Until the checklist
 * answer began reading with `counseling.session.read` they were shown nothing here and had to
 * read it as a visit that called for no counselling.
 *
 * Not a Pressable and not a greyed-out button. It says the same three things the control says —
 * which checklist, which coded condition called for it, and where it got to — as a statement,
 * because a control that looks disabled invites the press that produces the 403, and because
 * "nobody opened this" is a fact about the visit rather than a thing waiting on the reader.
 */
function UnwalkedChecklist({ choice }: { choice: ChecklistChoice }) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();

  return (
    <View
      testID={`counseling-unwalked-${choice.templateId}`}
      style={{
        gap: theme.spacing['1'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText size="lg" weight="semibold">
        {choice.title}
      </AppText>
      {choice.matched !== '' ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('calledFor', { code: choice.matched })}
        </AppText>
      ) : null}
      <AppText size="base">{t('unwalked')}</AppText>
    </View>
  );
}

/**
 * A room, as a heading.
 *
 * Not a screen and not a step. The counsellor scrolls; they do not navigate. "Your room" is a
 * marking on the heading rather than a filter on the list, so a nutritionist can see that the
 * counselling room already covered diet — and not ask again.
 */
function RoomSection({ group, children }: { group: RoomGroup; children: ReactNode }) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();

  return (
    <View testID={`counseling-room-${group.room}`} style={{ gap: theme.spacing['2'] }}>
      <View style={{ flexDirection: 'row', alignItems: 'baseline', gap: theme.spacing['2'] }}>
        <AppText size="lg" weight="semibold" style={{ flex: 1 }}>
          {group.heading.text}
        </AppText>
        {group.yours ? (
          <AppText size="xs" weight="semibold" style={{ color: colors.brand.text }}>
            {t('yourRoom')}
          </AppText>
        ) : null}
      </View>
      {children}
    </View>
  );
}

/**
 * One item of the checklist.
 *
 * The words come before the control: what to cover, then what to say about it, then the tick.
 * A counsellor reading a row aloud to a patient is reading the top of it, and a screen that
 * led with a checkbox would be a screen where the guidance is the thing scrolled past.
 */
function Item({
  row,
  say,
  open,
  allowed,
  busy,
  noting,
  note,
  unticking,
  untickReason,
  onTick,
  onOpenNote,
  onChangeNote,
  onStartUntick,
  onChangeUntickReason,
  onUntick,
}: {
  row: ItemRow;
  say: Say;
  open: boolean;
  allowed: boolean;
  busy: boolean;
  noting: boolean;
  note: string;
  unticking: boolean;
  untickReason: string;
  onTick: () => void;
  onOpenNote: (code: string | null) => void;
  onChangeNote: (text: string) => void;
  onStartUntick: (code: string | null) => void;
  onChangeUntickReason: (text: string) => void;
  onUntick: () => void;
}) {
  const t = useTranslations('counseling');
  const { colors, status } = useTokens();
  // `Tone` has no failure value, so this map has no red in it and cannot grow one without a
  // change in `state.ts`, next to the sentence saying why there is not one.
  const tone = row.tone === 'quiet' ? null : status[row.tone];

  return (
    <View
      testID={`counseling-item-${row.code}`}
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: tone?.border ?? colors.border.subtle,
        backgroundColor: tone?.surface ?? colors.surface.raised,
      }}
    >
      <Words wording={row.text} say={say} missingKey="noText" size="base" weight="semibold" />

      {row.guidance.text !== '' ? (
        <Words wording={row.guidance} say={say} missingKey="noText" size="sm" muted />
      ) : null}

      <AppText size="xs" style={{ color: colors.text.muted }}>
        {row.mandatory ? t('mandatory') : t('optional')}
      </AppText>

      {/* The tick, and it is the largest thing on the row. Read at arm's length in a busy
          room, tapped by somebody holding the phone in one hand. */}
      {row.covered ? (
        <View
          testID={`counseling-covered-${row.code}`}
          style={{
            minHeight: theme.size.touchTarget,
            justifyContent: 'center',
            paddingHorizontal: theme.spacing['4'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: status.normal.border,
            backgroundColor: status.normal.surface,
          }}
        >
          {/* A state, not a button. A toggle here would let a stray thumb withdraw a tick with
              no reason, which the server refuses anyway — so all it would buy is a refusal in
              front of a patient. */}
          <AppText size="base" weight="semibold" style={{ color: status.normal.text }}>
            {t('covered')}
          </AppText>
        </View>
      ) : (
        <Pressable
          testID={`counseling-tick-${row.code}`}
          accessibilityRole="button"
          accessibilityState={{ disabled: !open || !allowed || busy }}
          disabled={!open || !allowed || busy}
          onPress={onTick}
          style={({ pressed }) => ({
            // The token, and then some. `size.touchTarget` is CP09's 48 — a safety
            // requirement rather than a style choice — and the padding on top of it is
            // because this one is read at arm's length and tapped in a hurry, sometimes
            // gloved, by somebody who is also talking to a patient.
            minHeight: theme.size.touchTarget,
            paddingVertical: theme.spacing['3'],
            justifyContent: 'center',
            paddingHorizontal: theme.spacing['4'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 2,
            borderColor: !open || !allowed ? colors.state.disabledBorder : colors.border.control,
            backgroundColor: !open || !allowed ? colors.state.disabledSurface : colors.surface.base,
            opacity: pressed ? 0.85 : 1,
          })}
        >
          <AppText size="lg" weight="semibold">
            {busy ? t('ticking') : t('tick')}
          </AppText>
        </Pressable>
      )}

      {/* Whose act it was, and when (CP56 criterion 6, CP61 §4.2).
          The row used to say "covered at 11:02 by COUNSELOR", which names a hat and not a
          person — and §5.4's physician asks the patient who taught them, which is a question
          about a person. The tick has carried the counsellor's own name since CP56; this is
          the screen finally using it, and one tap gives the rest. */}
      {row.history !== null ? (
        <View testID={`counseling-history-${row.code}`} style={{ gap: theme.spacing['0.5'] }}>
          <EnteredBy
            compact
            testID={`counseling-entered-by-${row.code}`}
            provenance={ofCounselingTick(row.tick)}
          />

          {row.history.withdrawn !== null ? (
            // Criterion 7. The tick and the withdrawal, both, with both times — because an
            // item ticked at 11:02 and taken back at 11:04 is an answer to "what was covered",
            // not the absence of one.
            <>
              <AppText size="sm" weight="semibold">
                {say(`withdrawn.${attributionKey(row.history.withdrawn)}`, {
                  when: clockTime(row.history.withdrawn.at),
                })}
              </AppText>
              {/* The server requires the reason, so this is only ever drawn with one in it.
                  Guarded rather than trusted: "Reason given:" with nothing after it reads as a
                  reason nobody could be bothered to write, which is the opposite of the truth. */}
              {row.history.withdrawn.reason === '' ? null : (
                <AppText size="sm" style={{ color: colors.text.secondary }}>
                  {t('withdrawnWhy', { reason: row.history.withdrawn.reason })}
                </AppText>
              )}
            </>
          ) : null}

          {row.history.undoCount > 0 ? (
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('undoCount', { n: String(row.history.undoCount) })}
            </AppText>
          ) : null}
        </View>
      ) : null}

      {row.note !== '' ? (
        <View style={{ gap: theme.spacing['0.5'] }}>
          <AppText size="xs" weight="semibold" style={{ color: colors.text.secondary }}>
            {t('note')}
          </AppText>
          <AppText size="sm">{row.note}</AppText>
        </View>
      ) : null}

      {/* The optional note, behind a tap, before the tick and never blocking it. It goes into
          the tick request: attaching one afterwards would re-tick the item and rewrite who
          covered it and when. */}
      {open && allowed && noteOpenable(row) ? (
        noting ? (
          <View style={{ gap: theme.spacing['1'] }}>
            <AppText size="xs" style={{ color: colors.text.secondary }}>
              {t('noteHint')}
            </AppText>
            <TextInput
              testID={`counseling-note-${row.code}`}
              value={note}
              onChangeText={onChangeNote}
              multiline
              placeholder={t('notePlaceholder')}
              placeholderTextColor={colors.text.muted}
              style={{
                minHeight: theme.size.touchTarget,
                padding: theme.spacing['3'],
                borderRadius: theme.borderRadius.md,
                borderWidth: 1,
                borderColor: colors.border.control,
                backgroundColor: colors.surface.base,
                color: colors.text.primary,
              }}
            />
          </View>
        ) : (
          <Quiet
            testID={`counseling-add-note-${row.code}`}
            label={t('noteAdd')}
            onPress={() => onOpenNote(row.code)}
          />
        )
      ) : null}

      {/* Taking it back: the one act on this screen with a second step, because it is the one
          that needs a reason. */}
      {open && allowed && row.covered ? (
        unticking ? (
          <View style={{ gap: theme.spacing['1'] }}>
            <AppText size="sm" weight="semibold">
              {t('untickReasonLabel')}
            </AppText>
            <AppText size="xs" style={{ color: colors.text.secondary }}>
              {t('untickHint')}
            </AppText>
            <TextInput
              testID={`counseling-untick-reason-${row.code}`}
              value={untickReason}
              onChangeText={onChangeUntickReason}
              multiline
              placeholder={t('untickReasonPlaceholder')}
              placeholderTextColor={colors.text.muted}
              style={{
                minHeight: theme.size.touchTarget,
                padding: theme.spacing['3'],
                borderRadius: theme.borderRadius.md,
                borderWidth: 1,
                borderColor: colors.border.control,
                backgroundColor: colors.surface.base,
                color: colors.text.primary,
              }}
            />
            {untickRefused(untickReason) ? (
              // The sentence a person reads, not the enforcement. The button is dead as well,
              // and the server refuses it a third time.
              <AppText size="sm" style={{ color: colors.text.secondary }}>
                {say('problem.needsReason')}
              </AppText>
            ) : null}
            <View style={{ flexDirection: 'row', gap: theme.spacing['2'] }}>
              <View style={{ flex: 1 }}>
                <AppButton
                  testID={`counseling-untick-${row.code}`}
                  label={t('untick')}
                  disabled={busy || untickRefused(untickReason)}
                  onPress={onUntick}
                />
              </View>
              <View style={{ flex: 1 }}>
                <AppButton
                  testID={`counseling-untick-cancel-${row.code}`}
                  label={t('untickCancel')}
                  variant="secondary"
                  onPress={() => onStartUntick(null)}
                />
              </View>
            </View>
          </View>
        ) : (
          <Quiet
            testID={`counseling-untick-open-${row.code}`}
            label={t('untickOpen')}
            onPress={() => onStartUntick(row.code)}
          />
        )
      ) : null}
    </View>
  );
}

/**
 * A piece of an item's text, with the honest line when it is not in the reader's language.
 *
 * Publishing already refuses a version missing either language, so this is an edge case rather
 * than a shape to design around. It gets one line because a Bangla-reading counsellor handed
 * English with no explanation is one who thinks the app switched languages — and who may read
 * an injection-technique instruction aloud rather than admit they cannot read it.
 */
function Words({
  wording,
  say,
  missingKey,
  size,
  weight,
  muted,
}: {
  wording: Wording;
  say: Say;
  missingKey: string;
  size: 'sm' | 'base';
  weight?: 'semibold';
  muted?: boolean;
}) {
  const { colors } = useTokens();

  if (wording.text === '') {
    return (
      <AppText size={size} style={{ color: colors.text.secondary }}>
        {say(missingKey)}
      </AppText>
    );
  }

  return (
    <View style={{ gap: theme.spacing['0.5'] }}>
      <AppText
        size={size}
        weight={weight ?? 'regular'}
        style={muted === true ? { color: colors.text.secondary } : undefined}
      >
        {wording.text}
      </AppText>
      {!wording.ownLanguage ? (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {say(`inLanguage.${wording.language ?? 'en'}`)}
        </AppText>
      ) : null}
    </View>
  );
}

/**
 * Finishing the session.
 *
 * The button says what it is about to record. With everything covered it finishes; with items
 * outstanding it says how many and the list above it says which — by name, in the reader's
 * language, before the press rather than in a report afterwards.
 *
 * Nothing here is styled as an alarm and nothing refuses the press. A counsellor whose patient
 * walked out is making an honest record; a screen that made that expensive would get the other
 * kind.
 */
function Finish({
  completion,
  say,
  allowed,
  busy,
  onFinish,
}: {
  completion: Completion;
  say: Say;
  allowed: boolean;
  busy: boolean;
  onFinish: () => void;
}) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();

  if (!completion.open) {
    return (
      <AppText testID="counseling-closed" size="base" weight="semibold">
        {t('alreadyFinished')}
      </AppText>
    );
  }

  return (
    <View testID="counseling-finish" style={{ gap: theme.spacing['2'] }}>
      {completion.missing.length > 0 ? (
        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="base" weight="semibold">
            {t('missingHeading', { n: String(completion.missing.length) })}
          </AppText>
          {/* By name. A count alone would send the counsellor back up the list to work out
              which ones, which is the moment they stop reading it. */}
          {completion.missing.map((row) => (
            <AppText key={row.code} testID={`counseling-missing-${row.code}`} size="sm">
              {row.text.text === '' ? row.code : row.text.text}
            </AppText>
          ))}
        </View>
      ) : (
        <AppText size="base">{t('nothingOutstanding')}</AppText>
      )}

      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {say(completion.hint)}
      </AppText>

      <AppButton
        testID="counseling-finish-button"
        label={say(completion.label, { n: String(completion.missing.length) })}
        disabled={busy || !allowed}
        onPress={onFinish}
      />
    </View>
  );
}

/**
 * What went wrong, and the one thing that would help.
 *
 * Three shapes and three answers, and never more than one button — a screen that offered "try
 * again" for everything would train people to press it at the failure where pressing it cannot
 * help. A stale checklist gets **reload** and says so in words, because that is the sentence
 * criterion 8 owes the person holding the old list.
 */
function TroubleNote({
  trouble,
  say,
  covered,
  onReload,
}: {
  trouble: Trouble;
  say: Say;
  /** Who covered the item, once the re-read has answered. Null until then, and if it never does. */
  covered: Attribution | null;
  onReload: () => void;
}) {
  const t = useTranslations('counseling');
  const { colors } = useTokens();
  const advice = adviceFor(trouble);

  return (
    <View
      testID="counseling-trouble"
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['4'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.default,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText size="base" weight="semibold">
        {say(troubleKey(trouble))}
      </AppText>
      {/* A colleague got there first, and this is who — from the record, because the screen
          above went back for the session the moment the code said to. The server's own sentence
          tells the counsellor to open the session again to see who, and this is the phone having
          done it for them rather than a name inferred from which press met the conflict. */}
      {alreadyCovered(trouble) && covered !== null ? (
        <AppText testID="counseling-covered-by" size="sm">
          {say(`ticked.${attributionKey(covered)}`, {
            when: clockTime(covered.at),
            role: covered.role === '' ? t('unknownRole') : covered.role,
          })}
        </AppText>
      ) : null}
      {/* The server's own words. The rules behind them are the database's, and paraphrasing
          them here would be a second, staler account of a clinical rule this app does not own. */}
      {trouble.message !== '' ? <AppText size="sm">{trouble.message}</AppText> : null}
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {say(`advice.${advice}`)}
      </AppText>
      {advice === 'none' ? null : (
        <AppButton
          testID="counseling-reload"
          label={advice === 'reload' ? t('reload') : t('retry')}
          variant="secondary"
          onPress={onReload}
        />
      )}
    </View>
  );
}

/** A small secondary control: a text button that is still a full touch target. */
function Quiet({ testID, label, onPress }: { testID: string; label: string; onPress: () => void }) {
  const { colors } = useTokens();
  return (
    <Pressable
      testID={testID}
      accessibilityRole="button"
      onPress={onPress}
      style={{
        minHeight: theme.size.touchTarget,
        justifyContent: 'center',
      }}
    >
      <AppText size="sm" weight="semibold" style={{ color: colors.text.link }}>
        {label}
      </AppText>
    </Pressable>
  );
}
