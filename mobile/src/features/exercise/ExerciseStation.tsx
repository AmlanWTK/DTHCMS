import { Pressable, ScrollView, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { EnteredBy, ofExerciseAssessment, ofExercisePlan } from '@/features/attribution';
import { theme, useTokens } from '@/lib/tokens';

import {
  MINUTES_PER_SESSION,
  TIMES_PER_WEEK,
  WALKS_ANSWERS,
  WALK_MINUTES,
  adviceFor,
  isChosen,
  readsAsFailure,
  serverSpoke,
  targetFor,
  troubleKey,
  type Assessment,
  type AssessmentDraft,
  type AssessmentReading,
  type ConditionAnswer,
  type Chosen,
  type ExclusionReading,
  type OfferRow,
  type Plan,
  type PlanReading,
  type QuestionRow,
  type Review,
  type SheetRow,
  type Step,
  type TargetProblem,
  type Trouble,
  type WalksAnswer,
  type Wording,
} from './state';

/**
 * Station 8's exercise assessment and plan (CP60, blueprint §3 step 8, §12.1).
 *
 * An exercise specialist sits with a patient, asks five questions, chooses from what is left, and
 * hands over a sheet. Everything below follows from one sentence in the design: **contraindicated
 * exercises are excluded, not warned about.**
 *
 * # This screen cannot show a contraindicated exercise, because it does not have one
 *
 * There is no filter in this component, no `contraindicated` flag styled in grey, no "show all",
 * and no list of exercises that did not arrive in the last response from
 * `GET /v1/patients/{id}/exercise/options`. The excluded rows never leave the server process.
 *
 * That is a stronger guarantee than a careful screen, and it is why the screen is allowed to be
 * simple: a list that hid part of itself would be a warning wearing a different colour, one bug
 * or one "show all" affordance away from offering a jumping routine to a patient with an
 * insensate foot.
 *
 * # What it does show is the exclusion, in a form somebody can argue with
 *
 * The count — *three of twelve are not shown* — and the reasons, **named by condition**. A
 * specialist looking at eight options needs to know the library holds twelve, or a short list is
 * indistinguishable from a table with rows missing. And a clinician who thinks an exclusion is
 * wrong needs a sentence to disagree with. The banner is above the list rather than under it,
 * because a caveat somebody has to scroll to is a caveat nobody reads.
 *
 * It names no exercise, and it could not: the excluded ones were never sent.
 *
 * # Three steps, in the order the station works
 *
 * The questions, the choosing, the sheet. They are steps rather than tabs because the second
 * cannot be drawn until the server has answered the first — there is no list before an assessment
 * — and a tab an operator can press into an empty list is a tab that teaches them the app is
 * broken.
 *
 * # Every decision is in `state.ts`
 *
 * This component cannot be rendered outside a device, so anything it decided would be a decision
 * nobody checks. It computes no weekly total: `minutes_per_week` is derived once on the server,
 * per item and per plan, and there is no arithmetic in this file at all.
 *
 * # A target is two numbers, and the button will not move without both
 *
 * `times_per_week` and `minutes_per_session`. §12.1's exercise–outcome correlation computes
 * adherence from those two integers, and "walk more" is unanalysable — so an incomplete target is
 * made impossible to submit rather than left for the server to refuse. The button says which
 * exercise is still missing a number, because a disabled button with no explanation is a bug
 * report.
 *
 * # No state is carried by colour alone
 *
 * A chosen exercise says "chosen", an answered question says which answer, a superseded plan says
 * it in words. Roughly one man in twelve who will work here cannot rely on the colour, and direct
 * sun through the clinic's windows flattens it for everybody else.
 */
export function ExerciseStation({
  patientName,
  step,
  questions,
  draft,
  outstanding,
  walkProblem,
  assessment,
  findings,
  offers,
  exclusion,
  unapprovedOffers,
  chosen,
  problems,
  plan,
  planRow,
  review,
  droppedNames,
  refusedExercise,
  refusedReason,
  busy,
  trouble,
  onAnswerCondition,
  onAnswerWalks,
  onTypeWalkMinutes,
  onTypeJointPain,
  onRecord,
  onChooseExercise,
  onTypeTimes,
  onTypeMinutes,
  onIssue,
  onBackToList,
  onAskAgain,
  onCancelQuestions,
  onRetry,
}: {
  patientName: string;
  step: Step;
  questions: QuestionRow[];
  draft: AssessmentDraft;
  /** The questions still open. The findings cannot be recorded while any of them is. */
  outstanding: string[];
  walkProblem: 'notANumber' | 'outOfRange' | null;
  /** The assessment on record, for the attribution beside the findings. */
  assessment: Assessment | null;
  /** The same assessment in words, or null before there is one. */
  findings: AssessmentReading | null;
  /**
   * The permitted set, exactly as the server sent it.
   *
   * The only list this screen has ever held. There is no second prop here holding the rest of
   * the library, and there must never be one.
   */
  offers: OfferRow[];
  /** How many are not shown and on account of what. Null when nothing was excluded. */
  exclusion: ExclusionReading | null;
  /** How many of the offered exercises nobody has approved. Today, all of them. */
  unapprovedOffers: number;
  chosen: Chosen;
  problems: Record<string, TargetProblem>;
  /** The plan on record, for the attribution on the sheet. */
  plan: Plan | null;
  /** The same plan, read for printing, with the server's own totals. */
  planRow: PlanReading | null;
  /** Why the operator is being asked to look at the list again, or null. */
  review: Review | null;
  /**
   * The choices the freshly read list no longer offers, by name.
   *
   * Named, not counted. The operator chose them and typed two numbers for each, so "one of your
   * choices went" leaves them comparing two lists by eye. Naming them is not what criterion 1
   * forbids: these are rows the server **gave** this screen and has since taken back, and it can
   * never name one it was not sent.
   */
  droppedNames: string[];
  /**
   * The row the refusal belongs on, from `state.refusedRow`, or empty.
   *
   * The screen puts the sentence on that row rather than above the list. Empty when the server
   * named no exercise, and empty when it named one the freshly read list no longer offers — a
   * retirement does exactly that, and its sentence has to stay in the banner or it is drawn
   * nowhere at all.
   */
  refusedExercise: string;
  /** The server's sentence about it — the exercise, the condition and the mapping's own reason. */
  refusedReason: string;
  busy?: boolean;
  trouble?: Trouble | null;
  onAnswerCondition: (code: string, answer: ConditionAnswer) => void;
  onAnswerWalks: (answer: WalksAnswer) => void;
  onTypeWalkMinutes: (text: string) => void;
  onTypeJointPain: (text: string) => void;
  onRecord: () => void;
  onChooseExercise: (code: string) => void;
  onTypeTimes: (code: string, text: string) => void;
  onTypeMinutes: (code: string, text: string) => void;
  onIssue: () => void;
  onBackToList: () => void;
  onAskAgain: () => void;
  /**
   * Leave the questions without recording, or null when there is nowhere to go back to.
   *
   * Null on a patient who has no assessment yet: the questions are the only screen there is, and
   * a button that went nowhere would be worse than none. Present whenever the operator opened the
   * questions themselves — because otherwise the only way out of a mis-tap is to answer five
   * questions and record, and **that writes a second assessment into the ledger** which supersedes
   * the first. A mis-tap must not be able to produce a clinical act nobody intended.
   */
  onCancelQuestions: (() => void) | null;
  onRetry: () => void;
}) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();

  return (
    <ScrollView
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
      </View>

      <StepLine step={step} />

      {trouble != null ? (
        <TroubleBanner
          trouble={trouble}
          /* The contraindication's sentence is drawn on the row it is about, so repeating it here
             would make the operator read the same paragraph twice and still have to work out
             which of nine exercises it names. The heading stays: something was refused, and that
             has to be visible from the top of the screen. */
          messageOnRow={step === 'choose' && refusedExercise !== ''}
          onRetry={onRetry}
        />
      ) : null}

      {step === 'questions' ? (
        <Questions
          questions={questions}
          draft={draft}
          outstanding={outstanding}
          walkProblem={walkProblem}
          busy={busy}
          onCancel={onCancelQuestions}
          onAnswerCondition={onAnswerCondition}
          onAnswerWalks={onAnswerWalks}
          onTypeWalkMinutes={onTypeWalkMinutes}
          onTypeJointPain={onTypeJointPain}
          onRecord={onRecord}
        />
      ) : null}

      {step === 'choose' ? (
        <Choosing
          findings={findings}
          assessment={assessment}
          offers={offers}
          exclusion={exclusion}
          unapprovedOffers={unapprovedOffers}
          chosen={chosen}
          problems={problems}
          review={review}
          droppedNames={droppedNames}
          refusedExercise={refusedExercise}
          refusedReason={refusedReason}
          busy={busy}
          onChooseExercise={onChooseExercise}
          onTypeTimes={onTypeTimes}
          onTypeMinutes={onTypeMinutes}
          onIssue={onIssue}
          onAskAgain={onAskAgain}
        />
      ) : null}

      {step === 'sheet' ? (
        <Sheet
          planRow={planRow}
          plan={plan}
          findings={findings}
          onBackToList={onBackToList}
          onAskAgain={onAskAgain}
        />
      ) : null}
    </ScrollView>
  );
}

/**
 * Where the operator is, in three words.
 *
 * Not a control. The steps cannot be jumped between — there is no list before an assessment and
 * no sheet before a plan — and drawing them as buttons would promise a move the station cannot
 * make.
 */
function StepLine({ step }: { step: Step }) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();
  const say = t as unknown as (key: string) => string;

  return (
    <View
      accessibilityRole="header"
      style={{ flexDirection: 'row', gap: theme.spacing['2'], flexWrap: 'wrap' }}
    >
      {(['questions', 'choose', 'sheet'] as const).map((one, index) => {
        const here = one === step;
        return (
          <View
            key={one}
            testID={`step-${one}`}
            style={{
              paddingVertical: theme.spacing['1'],
              paddingHorizontal: theme.spacing['3'],
              borderRadius: theme.borderRadius.full,
              borderWidth: here ? 2 : 1,
              borderColor: here ? colors.brand.border : colors.border.subtle,
              backgroundColor: here ? colors.brand.subtle : colors.surface.raised,
            }}
          >
            <AppText
              size="xs"
              weight={here ? 'semibold' : 'regular'}
              style={{ color: here ? colors.brand.text : colors.text.secondary }}
            >
              {t('step.numbered', { n: index + 1, name: say(`step.${one}`) })}
            </AppText>
          </View>
        );
      })}
    </View>
  );
}

// --- step 1 ---

function Questions({
  questions,
  draft,
  outstanding,
  walkProblem,
  busy,
  onCancel,
  onAnswerCondition,
  onAnswerWalks,
  onTypeWalkMinutes,
  onTypeJointPain,
  onRecord,
}: {
  questions: QuestionRow[];
  draft: AssessmentDraft;
  outstanding: string[];
  walkProblem: 'notANumber' | 'outOfRange' | null;
  busy?: boolean;
  onCancel: (() => void) | null;
  onAnswerCondition: (code: string, answer: ConditionAnswer) => void;
  onAnswerWalks: (answer: WalksAnswer) => void;
  onTypeWalkMinutes: (text: string) => void;
  onTypeJointPain: (text: string) => void;
  onRecord: () => void;
}) {
  const t = useTranslations('exercise');
  const sayWith = withValues(t);
  const { colors } = useTokens();

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText size="lg" weight="semibold">
          {t('questions.title')}
        </AppText>
        {/* The sentence that stops a 409 reading as a fault. An operator who meets an empty
            screen with a red banner learns to press "try again"; one who is told the list comes
            after the questions asks the questions. */}
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('questions.why')}
        </AppText>
      </View>

      {questions.length === 0 ? (
        <AppText testID="questions-empty" size="sm" style={{ color: colors.text.secondary }}>
          {t('questions.notRead')}
        </AppText>
      ) : null}

      {questions.map((question) => (
        <Question key={question.code} question={question} onAnswer={onAnswerCondition} />
      ))}

      <View style={{ gap: theme.spacing['2'] }}>
        <AppText weight="semibold">{t('mobility.title')}</AppText>

        <AppText weight="medium">{t('mobility.walks')}</AppText>
        <View style={{ flexDirection: 'row', gap: theme.spacing['2'], flexWrap: 'wrap' }}>
          {WALKS_ANSWERS.map((answer) => (
            <Choice
              key={answer}
              testID={`walks-${answer}`}
              label={t(`mobility.walksAnswer.${answer}` as never)}
              chosen={draft.walks === answer}
              onPress={() => onAnswerWalks(answer)}
            />
          ))}
        </View>
        {/* "Not asked" is the default and it is a different record from "no". The contract says
            so — absent is not false — and somebody will otherwise read the second meaning out of
            the first. */}
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('mobility.walksHint')}
        </AppText>

        <AppText weight="medium">{t('mobility.minutes')}</AppText>
        <TextInput
          testID="walk-minutes"
          accessibilityLabel={t('mobility.minutes')}
          value={draft.walkMinutes}
          onChangeText={onTypeWalkMinutes}
          keyboardType="number-pad"
          inputMode="numeric"
          placeholder={t('mobility.minutesPlaceholder')}
          placeholderTextColor={colors.text.muted}
          style={{
            minHeight: theme.size.touchTarget,
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: walkProblem === null ? colors.border.control : colors.border.strong,
            backgroundColor: colors.surface.raised,
            color: colors.text.primary,
            paddingHorizontal: theme.spacing['4'],
            fontSize: theme.fontSize.lg,
          }}
        />
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('mobility.minutesHint')}
        </AppText>
        {walkProblem !== null ? (
          <AppText testID="walk-minutes-problem" size="sm" style={{ color: colors.text.secondary }}>
            {sayWith(`mobility.problem.${walkProblem}`, {
              min: WALK_MINUTES.min,
              max: WALK_MINUTES.max,
            })}
          </AppText>
        ) : null}

        <AppText weight="medium">{t('mobility.jointPain')}</AppText>
        <TextInput
          testID="joint-pain"
          accessibilityLabel={t('mobility.jointPain')}
          value={draft.jointPain}
          onChangeText={onTypeJointPain}
          multiline
          placeholder={t('mobility.jointPainPlaceholder')}
          placeholderTextColor={colors.text.muted}
          style={{
            minHeight: theme.size.touchTarget,
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: colors.border.control,
            backgroundColor: colors.surface.raised,
            color: colors.text.primary,
            padding: theme.spacing['3'],
            fontSize: theme.fontSize.base,
          }}
        />
      </View>

      {outstanding.length > 0 ? (
        <AppText testID="questions-outstanding" size="sm" style={{ color: colors.text.secondary }}>
          {t('questions.outstanding', { n: outstanding.length })}
        </AppText>
      ) : null}

      <AppButton
        testID="record-assessment"
        label={t('questions.record')}
        disabled={
          busy === true || questions.length === 0 || outstanding.length > 0 || walkProblem !== null
        }
        onPress={onRecord}
      />
      {/* The way out of a mis-tap. Without it the only exit from this form is to answer every
          question and record — and a second assessment supersedes the first and is an act in the
          ledger, not a screen state. Absent on a patient who has no assessment yet, where there
          is genuinely nowhere to go back to. */}
      {onCancel !== null ? (
        <AppButton
          testID="cancel-questions"
          variant="secondary"
          label={t('questions.cancel')}
          onPress={onCancel}
        />
      ) : null}
    </View>
  );
}

/**
 * One condition, asked as the question rather than labelled as a tag.
 *
 * The question is the big text and the short name is under it. A checkbox saying "neuropathy"
 * gets ticked for tingling toes; one asking whether protective sensation is lost at a
 * monofilament site does not — and every exclusion downstream is only as good as the answer.
 */
function Question({
  question,
  onAnswer,
}: {
  question: QuestionRow;
  onAnswer: (code: string, answer: ConditionAnswer) => void;
}) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();

  return (
    <View
      testID={`question-${question.code}`}
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['3'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText weight="medium">
        <ServerText wording={question.question} />
      </AppText>
      <AppText size="xs" style={{ color: colors.text.muted }}>
        <ServerText wording={question.name} />
      </AppText>
      {/* Which of the five is the one the tablet had never seen. From `fields.missing_conditions`
          on the refusal that sent the operator back here — codes out of the payload, not parsed
          out of a sentence that gets rewritten. Said in words, not marked in colour. */}
      {question.newly ? (
        <AppText
          testID={`question-${question.code}-new`}
          size="xs"
          weight="semibold"
          style={{ color: colors.text.secondary }}
        >
          {t('questions.newlyAdded')}
        </AppText>
      ) : null}
      <View style={{ flexDirection: 'row', gap: theme.spacing['2'] }}>
        <Choice
          testID={`question-${question.code}-yes`}
          label={t('questions.yes')}
          chosen={question.answer === 'yes'}
          onPress={() => onAnswer(question.code, 'yes')}
        />
        <Choice
          testID={`question-${question.code}-no`}
          label={t('questions.no')}
          chosen={question.answer === 'no'}
          onPress={() => onAnswer(question.code, 'no')}
        />
      </View>
    </View>
  );
}

// --- step 2 ---

function Choosing({
  findings,
  assessment,
  offers,
  exclusion,
  unapprovedOffers,
  chosen,
  problems,
  review,
  droppedNames,
  refusedExercise,
  refusedReason,
  busy,
  onChooseExercise,
  onTypeTimes,
  onTypeMinutes,
  onIssue,
  onAskAgain,
}: {
  findings: AssessmentReading | null;
  assessment: Assessment | null;
  offers: OfferRow[];
  exclusion: ExclusionReading | null;
  unapprovedOffers: number;
  chosen: Chosen;
  problems: Record<string, TargetProblem>;
  review: Review | null;
  droppedNames: string[];
  refusedExercise: string;
  refusedReason: string;
  busy?: boolean;
  onChooseExercise: (code: string) => void;
  onTypeTimes: (code: string, text: string) => void;
  onTypeMinutes: (code: string, text: string) => void;
  onIssue: () => void;
  onAskAgain: () => void;
}) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();
  const stillNeeded = Object.keys(problems).length;
  const ready = chosen.length > 0 && stillNeeded === 0;

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      {review !== null ? (
        <View
          testID="review-notice"
          style={{
            gap: theme.spacing['1'],
            padding: theme.spacing['3'],
            borderRadius: theme.borderRadius.lg,
            borderWidth: 2,
            borderColor: colors.border.strong,
            backgroundColor: colors.surface.raised,
          }}
        >
          <AppText weight="semibold">{t(`review.${review}.title` as never)}</AppText>
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t(`review.${review}.body` as never)}
          </AppText>
          {droppedNames.length > 0 ? (
            <AppText testID="review-dropped" size="sm" style={{ color: colors.text.secondary }}>
              {t('review.dropped', {
                n: droppedNames.length,
                names: droppedNames.join(', '),
              })}
            </AppText>
          ) : null}
        </View>
      ) : null}

      <Findings findings={findings} assessment={assessment} onAskAgain={onAskAgain} />

      {/* Above the list, not under it. A list that is short on purpose is indistinguishable from
          a table with rows missing unless the screen says how many the library holds — and the
          reasons are what a clinician who disagrees with an exclusion argues with. */}
      {exclusion !== null ? <Exclusion exclusion={exclusion} /> : null}

      {unapprovedOffers > 0 ? (
        <View
          testID="unapproved-notice"
          style={{
            gap: theme.spacing['1'],
            padding: theme.spacing['3'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: colors.border.default,
            backgroundColor: colors.surface.sunken,
          }}
        >
          <AppText size="sm" weight="semibold">
            {t('library.unapproved')}
          </AppText>
          <AppText size="xs" style={{ color: colors.text.secondary }}>
            {t('library.unapprovedWhy', { n: unapprovedOffers })}
          </AppText>
        </View>
      ) : null}

      <AppText size="lg" weight="semibold">
        {t('offers.title')}
      </AppText>

      {offers.length === 0 ? (
        <AppText testID="offers-empty" size="sm" style={{ color: colors.text.secondary }}>
          {t('offers.none')}
        </AppText>
      ) : null}

      {offers.map((offer) => (
        <Offer
          key={offer.code}
          offer={offer}
          chosen={isChosen(chosen, offer.code)}
          times={targetFor(chosen, offer.code)?.times ?? ''}
          minutes={targetFor(chosen, offer.code)?.minutes ?? ''}
          problem={problems[offer.code]}
          refusal={refusedExercise === offer.code ? refusedReason : ''}
          onChoose={onChooseExercise}
          onTypeTimes={onTypeTimes}
          onTypeMinutes={onTypeMinutes}
        />
      ))}

      {chosen.length === 0 ? (
        <AppText testID="nothing-chosen" size="sm" style={{ color: colors.text.secondary }}>
          {t('offers.chooseSomething')}
        </AppText>
      ) : null}
      {stillNeeded > 0 ? (
        <AppText testID="targets-incomplete" size="sm" style={{ color: colors.text.secondary }}>
          {t('offers.incomplete', { n: stillNeeded })}
        </AppText>
      ) : null}

      <AppButton
        testID="issue-plan"
        label={t('offers.issue')}
        disabled={busy === true || !ready}
        onPress={onIssue}
      />
    </View>
  );
}

/** What was answered, so that the list above it can be disagreed with. */
function Findings({
  findings,
  assessment,
  onAskAgain,
}: {
  findings: AssessmentReading | null;
  assessment: Assessment | null;
  onAskAgain: () => void;
}) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();
  if (findings === null) return null;

  return (
    <View
      testID="findings"
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['3'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText weight="semibold">{t('findings.title')}</AppText>
      {findings.conditions.length === 0 ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('findings.none')}
        </AppText>
      ) : (
        findings.conditions.map((condition) => (
          <AppText key={condition.code} size="sm">
            <ServerText wording={condition.name} />
          </AppText>
        ))
      )}
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t(`findings.walks.${findings.walks}` as never)}
      </AppText>
      {findings.walkMinutes !== null ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('findings.walkMinutes', { n: findings.walkMinutes })}
        </AppText>
      ) : null}
      {findings.jointPain !== '' ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {t('findings.jointPain', { words: findings.jointPain })}
        </AppText>
      ) : null}
      {/* How many questions were put. It is what makes "none apply" mean anything, and it is what
          explains a `NOT_ASKED` exclusion: an assessment that asked five questions is
          complete-for-its-time even after a sixth condition joins the catalogue. */}
      <AppText testID="findings-asked" size="xs" style={{ color: colors.text.muted }}>
        {t('findings.asked', { n: findings.askedCount })}
      </AppText>
      <EnteredBy compact provenance={ofExerciseAssessment(assessment)} />
      <AppButton
        testID="ask-again"
        variant="secondary"
        label={t('findings.askAgain')}
        onPress={onAskAgain}
      />
    </View>
  );
}

/**
 * How many are not shown, and on account of what.
 *
 * By condition, never by exercise, and the count is the server's. The per-condition figures
 * overlap — one exercise can be excluded by two conditions at once — so they are drawn as
 * separate true statements rather than added up into a total that would disagree with the one
 * above them.
 *
 * **The two kinds of reason get two different sentences, and that is the point of the status.**
 * `APPLIES` is a finding about this patient — *not shown because of severe peripheral
 * neuropathy* — and the operator's next act, if they disagree, is a conversation with a
 * physician. `NOT_ASKED` is a question added to the catalogue since the assessment was taken —
 * *not shown until the question about X has been asked* — and the next act is asking the patient
 * something. Drawing the second in the first's words would put a diagnosis on the screen that
 * nobody made.
 */
function Exclusion({ exclusion }: { exclusion: ExclusionReading }) {
  const t = useTranslations('exercise');
  const sayWith = withValues(t);
  const { colors } = useTokens();

  return (
    <View
      testID="exclusion"
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['3'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 2,
        borderColor: colors.border.default,
        backgroundColor: colors.surface.sunken,
      }}
    >
      <AppText weight="semibold">
        {t('exclusion.title', { excluded: exclusion.excluded, library: exclusion.librarySize })}
      </AppText>
      {exclusion.reasons.map((reason) => (
        <AppText
          key={reason.code}
          testID={`exclusion-${reason.code}`}
          size="sm"
          weight={reason.status === 'NOT_ASKED' ? 'medium' : 'regular'}
        >
          {sayWith(reason.status === 'NOT_ASKED' ? 'exclusion.notAsked' : 'exclusion.applies', {
            n: reason.excluded,
            condition: reason.name.text,
          })}
        </AppText>
      ))}
      {/* One line for the whole set of them, because one act answers all of them: take the
          assessment again. An operator who has to notice it by reading each sentence carefully is
          an operator who will not notice on a busy morning. */}
      {exclusion.unasked > 0 ? (
        <AppText testID="exclusion-unasked" size="xs" style={{ color: colors.text.secondary }}>
          {t('exclusion.unasked', { n: exclusion.unasked })}
        </AppText>
      ) : null}
      {/* The sentence that stops the banner reading as a list somebody hid. */}
      <AppText size="xs" style={{ color: colors.text.secondary }}>
        {t('exclusion.why')}
      </AppText>
      {exclusion.reasons.length > 1 ? (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('exclusion.overlap')}
        </AppText>
      ) : null}
    </View>
  );
}

/**
 * One permitted exercise, its two numbers once it is chosen, and the server's refusal if this is
 * the row that was refused.
 *
 * `refusal` is the sentence `EXERCISE_CONTRAINDICATED` carries — the exercise, the condition and
 * the reason from the mapping table — put **on this row** rather than at the top of a list of
 * nine. That is what the codes in `fields` are for. A banner above the list would make an operator
 * work out which of their choices it was about, and the whole argument for a reason rather than a
 * status is that the operator reads it and thinks about it.
 */
function Offer({
  offer,
  chosen,
  times,
  minutes,
  problem,
  refusal,
  onChoose,
  onTypeTimes,
  onTypeMinutes,
}: {
  offer: OfferRow;
  chosen: boolean;
  times: string;
  minutes: string;
  problem?: TargetProblem;
  /** The server's sentence about this exercise, or empty. */
  refusal?: string;
  onChoose: (code: string) => void;
  onTypeTimes: (code: string, text: string) => void;
  onTypeMinutes: (code: string, text: string) => void;
}) {
  const t = useTranslations('exercise');
  const say = t as unknown as (key: string) => string;
  const sayWith = withValues(t);
  const { colors } = useTokens();
  const refused = (refusal ?? '') !== '';

  return (
    <View
      testID={`offer-${offer.code}`}
      style={{
        gap: theme.spacing['2'],
        padding: theme.spacing['3'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: refused || chosen ? 2 : 1,
        borderColor: refused
          ? colors.border.strong
          : chosen
            ? colors.brand.border
            : colors.border.subtle,
        backgroundColor: chosen && !refused ? colors.brand.subtle : colors.surface.raised,
      }}
    >
      {/* Above the name, because it is the reason this row is worth looking at. Said in words
          rather than only in a heavier border: roughly one man in twelve who will work here
          cannot rely on the colour. */}
      {refused ? (
        <View testID={`offer-${offer.code}-refused`} style={{ gap: theme.spacing['0.5'] }}>
          <AppText size="xs" weight="semibold" style={{ color: colors.text.secondary }}>
            {t('offers.refused')}
          </AppText>
          <AppText size="sm">{refusal}</AppText>
        </View>
      ) : null}
      <Pressable
        testID={`offer-${offer.code}-choose`}
        accessibilityRole="checkbox"
        accessibilityState={{ checked: chosen }}
        onPress={() => onChoose(offer.code)}
        style={{ minHeight: theme.size.touchTarget, justifyContent: 'center' }}
      >
        <AppText weight="semibold" style={chosen ? { color: colors.brand.text } : undefined}>
          <ServerText wording={offer.name} />
        </AppText>
        {/* Said in words as well as drawn in colour. */}
        <AppText
          size="xs"
          weight={chosen ? 'semibold' : 'regular'}
          style={{ color: colors.text.secondary }}
        >
          {chosen ? t('offers.chosen') : t('offers.choose')}
        </AppText>
      </Pressable>

      <AppText size="sm" style={{ color: colors.text.secondary }}>
        <ServerText wording={offer.how} />
      </AppText>

      <View style={{ flexDirection: 'row', gap: theme.spacing['2'], flexWrap: 'wrap' }}>
        <Tag label={say(`kind.${offer.kind}`)} />
        <Tag label={say(`intensity.${offer.intensity}`)} />
        <Tag label={offer.canDoAtHome ? t('offers.atHome') : t('offers.notAtHome')} />
        {offer.needsEquipment ? <Tag label={t('offers.needsEquipment')} /> : null}
      </View>

      {chosen ? (
        <View style={{ gap: theme.spacing['2'] }}>
          <NumberField
            testID={`offer-${offer.code}-times`}
            label={t('target.times')}
            hint={t('target.timesHint', { min: TIMES_PER_WEEK.min, max: TIMES_PER_WEEK.max })}
            value={times}
            wrong={problem === 'times' || problem === 'timesRange'}
            onChangeText={(text) => onTypeTimes(offer.code, text)}
          />
          <NumberField
            testID={`offer-${offer.code}-minutes`}
            label={t('target.minutes')}
            hint={t('target.minutesHint', {
              min: MINUTES_PER_SESSION.min,
              max: MINUTES_PER_SESSION.max,
            })}
            value={minutes}
            wrong={problem === 'minutes' || problem === 'minutesRange'}
            onChangeText={(text) => onTypeMinutes(offer.code, text)}
          />
          {problem !== undefined ? (
            <AppText
              testID={`offer-${offer.code}-problem`}
              size="sm"
              style={{ color: colors.text.secondary }}
            >
              {sayWith(`target.problem.${problem}`, {
                min: problem === 'timesRange' ? TIMES_PER_WEEK.min : MINUTES_PER_SESSION.min,
                max: problem === 'timesRange' ? TIMES_PER_WEEK.max : MINUTES_PER_SESSION.max,
              })}
            </AppText>
          ) : null}
        </View>
      ) : null}
    </View>
  );
}

// --- step 3 ---

/**
 * The sheet, in the shape the patient is handed.
 *
 * The total is at the top, because that is the number a follow-up visit compares against last
 * month's; the instruction is the largest text on each line, because it is what the patient reads
 * at home with nobody to ask. Both figures are the server's.
 */
function Sheet({
  planRow,
  plan,
  findings,
  onBackToList,
  onAskAgain,
}: {
  planRow: PlanReading | null;
  plan: Plan | null;
  findings: AssessmentReading | null;
  onBackToList: () => void;
  onAskAgain: () => void;
}) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();
  if (planRow === null) return null;

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText size="lg" weight="semibold">
          {t('sheet.title')}
        </AppText>
        {/* The figure and its unit, never one sentence with a number in it. `clinicalValue` pins
            the Latin face and tabular figures whatever the interface language is, and a
            translated sentence rendered in that face would be a Bangla sentence drawn in a font
            with no Bengali glyphs. The same split every station's readouts use. */}
        <View style={{ flexDirection: 'row', alignItems: 'baseline', gap: theme.spacing['2'] }}>
          <AppText testID="plan-total" size="4xl" weight="bold" variant="clinicalValue">
            {String(planRow.minutesPerWeek)}
          </AppText>
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('sheet.perWeek')}
          </AppText>
        </View>
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('sheet.totalWhy')}
        </AppText>
        {planRow.superseded ? (
          <AppText testID="plan-superseded" size="sm" weight="semibold">
            {t('sheet.superseded')}
          </AppText>
        ) : null}
      </View>

      {planRow.rows.map((row) => (
        <SheetLine key={row.code} row={row} />
      ))}

      {planRow.unapproved > 0 ? (
        <AppText size="xs" style={{ color: colors.text.secondary }}>
          {t('sheet.unapproved', { n: planRow.unapproved })}
        </AppText>
      ) : null}

      {/* The findings this plan was filtered against, frozen at issue. Without them, "why was she
          given stair climbing in June" has no answer once her cardiac symptom is recorded in
          July: the plan would look like a mistake rather than a decision that was right on the
          evidence of the day. */}
      {findings !== null ? (
        <AppText testID="plan-assessment" size="xs" style={{ color: colors.text.muted }}>
          {t('sheet.builtFrom')}
        </AppText>
      ) : null}

      <EnteredBy provenance={ofExercisePlan(plan)} />

      <AppButton testID="change-plan" label={t('sheet.change')} onPress={onBackToList} />
      <AppButton
        testID="sheet-ask-again"
        variant="secondary"
        label={t('findings.askAgain')}
        onPress={onAskAgain}
      />
    </View>
  );
}

function SheetLine({ row }: { row: SheetRow }) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();

  return (
    <View
      testID={`sheet-${row.code}`}
      style={{
        gap: theme.spacing['1'],
        padding: theme.spacing['3'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.raised,
      }}
    >
      <AppText size="lg" weight="semibold">
        <ServerText wording={row.name} />
      </AppText>
      {/* The three figures, each with its unit under it. Pinned to the Latin face for the reason
          CP09 gives — a number somebody copies onto a paper follow-up card must look identical in
          both interfaces — while every number that lives inside a *sentence* on this screen, the
          exclusion count and the outstanding count, stays in the reader's own numerals. */}
      <View style={{ flexDirection: 'row', gap: theme.spacing['5'], flexWrap: 'wrap' }}>
        <Figure value={row.timesPerWeek} label={t('sheet.timesUnit')} />
        <Figure value={row.minutesPerSession} label={t('sheet.minutesUnit')} />
        <Figure
          testID={`sheet-${row.code}-weekly`}
          value={row.minutesPerWeek}
          label={t('sheet.perWeek')}
        />
      </View>
      {/* The instruction the patient is handed. Joined from the library by the server rather than
          copied onto the row, so a correction to the Bangla reaches a sheet reprinted tomorrow. */}
      <AppText>
        <ServerText wording={row.how} />
      </AppText>
      {row.needsEquipment ? (
        <AppText size="xs" style={{ color: colors.text.secondary }}>
          {t('offers.needsEquipment')}
        </AppText>
      ) : null}
      {row.note !== '' ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          {row.note}
        </AppText>
      ) : null}
    </View>
  );
}

// --- the small pieces ---

function Choice({
  testID,
  label,
  chosen,
  onPress,
}: {
  testID: string;
  label: string;
  chosen: boolean;
  onPress: () => void;
}) {
  const { colors } = useTokens();
  return (
    <Pressable
      testID={testID}
      accessibilityRole="radio"
      accessibilityState={{ selected: chosen }}
      onPress={onPress}
      style={{
        minHeight: theme.size.touchTarget,
        paddingHorizontal: theme.spacing['4'],
        justifyContent: 'center',
        borderRadius: theme.borderRadius.md,
        borderWidth: chosen ? 2 : 1,
        borderColor: chosen ? colors.brand.border : colors.border.control,
        backgroundColor: chosen ? colors.brand.subtle : colors.surface.raised,
      }}
    >
      <AppText
        weight={chosen ? 'semibold' : 'regular'}
        style={{ color: chosen ? colors.brand.text : colors.text.primary }}
      >
        {label}
      </AppText>
    </Pressable>
  );
}

/**
 * One clinical figure and the unit under it.
 *
 * The number alone in `clinicalValue`, never a translated sentence with a number in it. That
 * variant pins the Latin face and tabular figures whatever the interface language is (CP09), and
 * a Bangla sentence drawn in a face with no Bengali glyphs is worse than either language on its
 * own. So the figure is Latin and the unit beside it is the reader's, which is also how the
 * anthropometry and nutrition readouts are built.
 */
function Figure({ value, label, testID }: { value: number; label: string; testID?: string }) {
  const { colors } = useTokens();
  return (
    <View>
      <AppText testID={testID} size="xl" weight="semibold" variant="clinicalValue">
        {String(value)}
      </AppText>
      <AppText size="xs" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
    </View>
  );
}

function Tag({ label }: { label: string }) {
  const { colors } = useTokens();
  return (
    <View
      style={{
        paddingVertical: theme.spacing['0.5'],
        paddingHorizontal: theme.spacing['2'],
        borderRadius: theme.borderRadius.full,
        borderWidth: 1,
        borderColor: colors.border.subtle,
        backgroundColor: colors.surface.sunken,
      }}
    >
      <AppText size="xs" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
    </View>
  );
}

function NumberField({
  testID,
  label,
  hint,
  value,
  wrong,
  onChangeText,
}: {
  testID: string;
  label: string;
  hint: string;
  value: string;
  wrong: boolean;
  onChangeText: (text: string) => void;
}) {
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['1'] }}>
      <AppText size="sm" weight="medium">
        {label}
      </AppText>
      <TextInput
        testID={testID}
        accessibilityLabel={label}
        value={value}
        onChangeText={onChangeText}
        keyboardType="number-pad"
        inputMode="numeric"
        style={{
          minHeight: theme.size.touchTarget,
          borderRadius: theme.borderRadius.md,
          borderWidth: wrong ? 2 : 1,
          borderColor: wrong ? colors.border.strong : colors.border.control,
          backgroundColor: colors.surface.raised,
          color: colors.text.primary,
          paddingHorizontal: theme.spacing['4'],
          fontSize: theme.fontSize.xl,
          fontVariant: ['tabular-nums'],
        }}
      />
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {hint}
      </AppText>
    </View>
  );
}

/**
 * `t` with a key this file computed and arguments to go in it.
 *
 * `use-intl` types a key as one of the literals in the message file, which is what stops a typo
 * reaching a screen — but a message chosen by a value (`target.problem.timesRange`) is not a
 * literal at the call site. The cast is narrowed to exactly this shape rather than spread over
 * every call, and `i18n.test.ts` walks the message files for the keys these produce.
 */
function withValues(
  t: ReturnType<typeof useTranslations<'exercise'>>,
): (key: string, values: Record<string, unknown>) => string {
  return t as unknown as (key: string, values: Record<string, unknown>) => string;
}

/**
 * The server's own words, with an honest line when they are in the other language.
 *
 * Invariant 88 refuses a library row or an exclusion reason that reads in only one language, so
 * this is an edge case rather than a shape to design around. It is still worth saying: a
 * Bangla-reading specialist handed English with no explanation is one who thinks the app has
 * switched languages on them.
 */
function ServerText({ wording }: { wording: Wording }) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();

  if (wording.language === null) {
    return <AppText style={{ color: colors.text.muted }}>{t('noWording')}</AppText>;
  }
  if (wording.ownLanguage) return <AppText>{wording.text}</AppText>;
  return (
    <AppText>
      {wording.text}
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {` (${t('inOtherLanguage')})`}
      </AppText>
    </AppText>
  );
}

function TroubleBanner({
  trouble,
  messageOnRow,
  onRetry,
}: {
  trouble: Trouble;
  messageOnRow?: boolean;
  onRetry: () => void;
}) {
  const t = useTranslations('exercise');
  const { colors } = useTokens();
  const say = t as unknown as (key: string) => string;
  const advice = adviceFor(trouble);
  // `EXERCISE_NO_ASSESSMENT` is a step, not a fault, and is drawn as a quiet note above the form
  // the operator is about to fill in rather than as a refusal they have to dismiss.
  const failure = readsAsFailure(trouble);

  return (
    <View
      testID="trouble"
      style={{
        gap: theme.spacing['1'],
        padding: theme.spacing['3'],
        borderRadius: theme.borderRadius.lg,
        borderWidth: failure ? 2 : 1,
        borderColor: failure ? colors.border.strong : colors.border.subtle,
        backgroundColor: failure ? colors.surface.raised : colors.surface.sunken,
      }}
    >
      {/*
       * The server's own sentence, and never a paraphrase of it.
       *
       * This screen writes no version of what was refused. The refusal that matters here — the
       * exercise, the condition, and the reason from `core.exercise_contraindication` — is the
       * sentence a clinician is meant to argue with, and a client-side rewording of it would be a
       * second source of truth that drifts the first time somebody improves the server's. So
       * there are exactly three things this can draw: a pointer to the row the sentence is on,
       * the server's sentence, or — only when there was no server to write one — the screen's own
       * description of the shape of the failure.
       */}
      {messageOnRow === true ? (
        <AppText testID="trouble-onrow" weight="semibold">
          {t('trouble.onRow')}
        </AppText>
      ) : serverSpoke(trouble) ? (
        <AppText testID="trouble-message" weight="semibold">
          {trouble.message}
        </AppText>
      ) : (
        <AppText testID="trouble-own" weight="semibold">
          {say(troubleKey(trouble))}
        </AppText>
      )}
      {/* What the screen has already done, and what to do next — the one thing the server cannot
          know, and the only thing this component adds to a refusal. */}
      {advice !== 'none' ? (
        <AppText testID="trouble-advice" size="sm" style={{ color: colors.text.secondary }}>
          {say(`advice.${advice}`)}
        </AppText>
      ) : null}
      {advice === 'retry' ? (
        <AppButton
          testID="trouble-retry"
          variant="secondary"
          label={t('retry')}
          onPress={onRetry}
        />
      ) : null}
    </View>
  );
}
