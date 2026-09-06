import { Pressable, ScrollView, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { unitLabel } from '@/components/DualUnitValue';
import { MeasurementField } from '@/components/MeasurementField';
import { EnteredBy, ofInstrumentResponse, ofObservation } from '@/features/attribution';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import {
  LIFESTYLE_FIELDS,
  adviceFor,
  answerFor,
  answered,
  optionsInOrder,
  stillNeeded,
  troubleKey,
  wordingOf,
  type FieldWarning,
  type InstrumentItem,
  type InstrumentResponse,
  type InstrumentRow,
  type LifestyleDomain,
  type LifestyleFieldKey,
  type NumberProblem,
  type NumberWarnings,
  type NumbersForm,
  type Observation,
  type Progress,
  type RunState,
  type ScorePanel,
  type Trouble,
  type Wording,
} from './state';

/**
 * How many domains the composite has room for.
 *
 * Four today, and the only place this screen states it — beside a count the server sent, so
 * "3 of 4" reads as a fraction rather than as a bare number. It is not a copy of the formula's
 * floor: `minimum` comes off the payload, because a client that hard-coded the floor would go on
 * saying "below three" the day a clinician raised it.
 */
const DOMAIN_COUNT = 4;

/**
 * Station 3's lifestyle assessment (CP58, blueprint §3 step 3, §12).
 *
 * An operator sits beside a patient and works through short questionnaires and four plain
 * numbers, in one hand, on a cheap Android tablet, with a queue waiting. Everything below
 * follows from that sentence: no dropdowns, no modals, one column, and every control at least
 * `size.touchTarget` in both directions.
 *
 * # The score is at the top, and it is a readout rather than a result
 *
 * The same argument station 2's panel makes. Below the form it would be something you scroll to
 * after finishing; above it, it is what the operator glances at when they sit down — and on
 * this station the glance answers a question they can act on immediately, because "the sleep
 * domain is still missing" is a thing to ask the patient while they are still in the chair.
 *
 * # A score that does not exist is never drawn as a number
 *
 * `scoring.score: null` is a legitimate answer from the API — below `scoring.minimum` assessed
 * domains there is nothing honest to compute — and the two obvious renderings of it are both
 * wrong. A zero is a number somebody compares with a real one; a dash is a number missing from a
 * place a number belongs, which reads as a fault. So the card reads out the server's own account
 * instead: which domains it saw, which it wants, and how many more are needed.
 *
 * Every one of those is the server's. This screen briefly worked the missing list out for
 * itself — pack-years for smoking, the live AUDIT-C for alcohol — because the payload said only
 * `null`, and that copy would have gone silently wrong the day a fifth domain arrived. Nothing
 * in this feature knows which observation feeds which domain any more.
 *
 * The account arrives on the first render, from a read that computes it and stores nothing, so
 * the card can name what is still wanted before the operator has done anything. The one case it
 * cannot name is a tablet that could not reach the server, and it says that rather than leaving
 * a space an operator reads as "this patient has no lifestyle risk".
 *
 * # A score that does exist says what it is made of and that nobody has approved it
 *
 * Two lines that are not decoration. `inputs.domains` is on the value because a score from
 * three domains is a different number from one from four, and a reader with only the number
 * cannot tell; and the formula version ends in `-proposed` because D-26 is open and no
 * clinician has agreed the arithmetic. Both are drawn beside the figure rather than behind a
 * tap: a caveat somebody has to open is a caveat nobody reads.
 *
 * # The questionnaires this clinic may not run are on the list
 *
 * PSS-10, PHQ-9 and IPAQ are drawn as rows, greyed, not pressable, with the server's own
 * sentence saying what has to be confirmed. Leaving them out would make a decision look like an
 * oversight — a clinician who went looking for PHQ-9 and found nothing would report the app as
 * broken rather than the licence as missing.
 *
 * # The readiness question says whose question it is
 *
 * `READINESS_1` is one question written at this clinic because every validated alternative is
 * behind D-26. It sits on a list beside a WHO instrument, so it carries a line naming its
 * author. Without it the list would read as four published scales, and a number from it would
 * eventually be reported as though it came from one.
 */
export function LifestyleStation({
  patientName,
  rows,
  catalogueMoved,
  running,
  run,
  progress,
  outstanding,
  numberProblems,
  numbers,
  numberWarnings,
  packYears,
  score,
  scoreRow,
  lastResponse,
  loading,
  busy,
  savedNumbers,
  trouble,
  onOpen,
  onClose,
  onChooseOption,
  onTypeNumber,
  onAnswerYesNo,
  onSubmit,
  onChangeNumber,
  onChangeUnit,
  onConfirmNumber,
  onSaveNumbers,
  onReload,
}: {
  patientName: string;
  /** The whole catalogue, unusable rows included. Never filtered on the way in. */
  rows: InstrumentRow[];
  /**
   * The catalogue this tablet is holding has been overtaken by a republished version.
   *
   * Said here, with nothing half-answered, rather than met as a 422 on an item code at submit
   * after a patient has been asked every question on a form that no longer exists.
   */
  catalogueMoved?: boolean;
  /** The questionnaire currently open, or null on the list. */
  running: InstrumentRow | null;
  run: RunState;
  progress: Progress | null;
  outstanding: string[];
  numberProblems: Partial<Record<string, NumberProblem>>;
  numbers: NumbersForm;
  numberWarnings: NumberWarnings;
  /** The server's pack-years, once it has computed one. Never worked out here. */
  packYears: Observation | null;
  /**
   * The composite as it stands, and the server's account of the domains behind it.
   *
   * Built by `scorePanel` from the write's own answer and the patient's record. The component
   * chooses sentences from it and computes nothing.
   */
  score: ScorePanel;
  /**
   * The row that reading came off, so the score can say who produced it (CP61).
   *
   * A composite on the record when the operator sits down was produced by somebody else's
   * assessment earlier in the morning, and "whose questionnaire is this number from" is the
   * first thing anybody asks of a number they did not write.
   */
  scoreRow: Observation | null;
  /** The response the server stored, for the working it shows afterwards. */
  lastResponse: InstrumentResponse | null;
  loading?: boolean;
  busy?: boolean;
  savedNumbers?: boolean;
  trouble?: Trouble | null;
  onOpen: (code: string) => void;
  onClose: () => void;
  onChooseOption: (itemCode: string, optionCode: string) => void;
  onTypeNumber: (itemCode: string, text: string) => void;
  onAnswerYesNo: (itemCode: string, value: boolean) => void;
  onSubmit: () => void;
  onChangeNumber: (key: LifestyleFieldKey, text: string) => void;
  onChangeUnit: (key: LifestyleFieldKey, unit: string) => void;
  onConfirmNumber: (key: LifestyleFieldKey) => void;
  onSaveNumbers: () => void;
  onReload: () => void;
}) {
  const t = useTranslations('lifestyle');
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

      {trouble ? <TroubleBanner trouble={trouble} onReload={onReload} /> : null}

      {running === null ? (
        <>
          <ScoreCard panel={score} scoreRow={scoreRow} />
          {catalogueMoved === true ? <CatalogueMoved onReload={onReload} /> : null}
          {loading === true ? (
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('loadingCatalogue')}
            </AppText>
          ) : (
            <Catalogue rows={rows} onOpen={onOpen} />
          )}
          <Numbers
            numbers={numbers}
            warnings={numberWarnings}
            packYears={packYears}
            busy={busy}
            saved={savedNumbers}
            onChangeNumber={onChangeNumber}
            onChangeUnit={onChangeUnit}
            onConfirmNumber={onConfirmNumber}
            onSave={onSaveNumbers}
          />
        </>
      ) : (
        <Runner
          row={running}
          run={run}
          progress={progress}
          outstanding={outstanding}
          problems={numberProblems}
          busy={busy}
          onClose={onClose}
          onChooseOption={onChooseOption}
          onTypeNumber={onTypeNumber}
          onAnswerYesNo={onAnswerYesNo}
          onSubmit={onSubmit}
        />
      )}

      {running === null && lastResponse !== null ? (
        <Working
          response={lastResponse}
          totalMeansSomething={
            rows.find((row) => row.code === lastResponse.instrument_code)?.totalMeansSomething ??
            true
          }
        />
      ) : null}
    </ScrollView>
  );
}

// --- the composite ---

/**
 * The score, or the honest absence of one.
 *
 * There is no branch here that produces a figure from anything but `panel.reading.value`, and no
 * arithmetic at all: the number, the count of domains, the domains themselves and the floor are
 * the server's, and the only thing this component decides is which sentence goes around them.
 *
 * The three sources are drawn differently because only the first is a statement about what the
 * operator just did: a figure a write produced is theirs, a figure off the patient's chart is
 * somebody else's, and a patient with no composite anywhere has neither. What none of the three
 * decides is whether the domains can be named — that is the `Scoring` account, which arrives on
 * the first render from a read that writes nothing.
 */
function ScoreCard({ panel, scoreRow }: { panel: ScorePanel; scoreRow: Observation | null }) {
  const t = useTranslations('lifestyle');
  const { colors, status } = useTokens();
  const say = t as unknown as (key: string) => string;
  const list = (domains: readonly LifestyleDomain[]): string =>
    domains.map((one) => say(`domain.${one.toLowerCase()}`)).join(', ');

  return (
    <View
      testID="lifestyle-score"
      style={{
        backgroundColor: colors.surface.raised,
        borderRadius: theme.borderRadius.lg,
        borderWidth: theme.size.borderWidth.thin,
        borderColor: colors.border.subtle,
        padding: theme.spacing['4'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('score.title')}
      </AppText>

      {panel.reading === null ? (
        <View style={{ gap: theme.spacing['2'] }}>
          {/* Not a zero and not a dash. Both are marks a reader compares with a real score. */}
          <AppText testID="lifestyle-score-absent" weight="semibold">
            {t('score.none')}
          </AppText>
          {panel.scoring === null ? (
            // The account could not be read — this tablet is offline, or the read failed. The
            // one case left where the domains cannot be named, and saying so is better than an
            // empty space an operator reads as "this patient has no lifestyle risk".
            <AppText size="sm" style={{ color: colors.text.secondary }}>
              {t('score.notRead')}
            </AppText>
          ) : (
            <>
              <AppText size="sm" style={{ color: colors.text.secondary }}>
                {t('score.needMore', {
                  n: stillNeeded(panel.scoring),
                  minimum: panel.scoring.minimum,
                })}
              </AppText>
              {panel.scoring.missing.length > 0 ? (
                <AppText
                  testID="lifestyle-score-missing"
                  size="sm"
                  style={{ color: colors.text.muted }}
                >
                  {t('score.missing', { domains: list(panel.scoring.missing) })}
                </AppText>
              ) : null}
              {panel.scoring.assessed.length > 0 ? (
                <AppText size="sm" style={{ color: colors.text.muted }}>
                  {t('score.have', { domains: list(panel.scoring.assessed) })}
                </AppText>
              ) : null}
            </>
          )}
        </View>
      ) : (
        <View style={{ gap: theme.spacing['2'] }}>
          <AppText testID="lifestyle-score-value" size="4xl" weight="bold" variant="clinicalValue">
            {String(panel.reading.value)}
          </AppText>

          {/* What it is made of. A score from three domains is a different number from one
              from four, and a reader with only the figure cannot tell — so the count is on
              the card rather than in a tooltip nobody opens. */}
          <AppText
            testID="lifestyle-score-domains"
            size="sm"
            style={{ color: colors.text.secondary }}
          >
            {panel.reading.domains === null
              ? t('score.domainsUnknown')
              : t('score.domains', { n: panel.reading.domains, outOf: DOMAIN_COUNT })}
          </AppText>
          {panel.scoring !== null && panel.scoring.assessed.length > 0 ? (
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('score.from', { domains: list(panel.scoring.assessed) })}
            </AppText>
          ) : null}
          {/* And what is still open, beside a score that exists. A composite from three of four
              domains is not finished with — the fourth is a question somebody can still ask
              while the patient is in the chair. */}
          {panel.scoring !== null && panel.scoring.missing.length > 0 ? (
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('score.missing', { domains: list(panel.scoring.missing) })}
            </AppText>
          ) : null}
          {/* A figure read off the record rather than produced by anything the operator has
              just done. Saying so is the difference between "you have just scored 41.7" and
              "this patient's record holds 41.7", and only the second is true here. */}
          {panel.from === 'record' ? (
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('score.onRecord')}
            </AppText>
          ) : null}

          {/* D-26. The suffix is the whole point of the version string. */}
          {panel.reading.proposed ? (
            <View
              testID="lifestyle-score-proposed"
              style={{
                backgroundColor: status.unknown.surface,
                borderColor: status.unknown.border,
                borderWidth: theme.size.borderWidth.thin,
                borderRadius: theme.borderRadius.md,
                padding: theme.spacing['3'],
                gap: theme.spacing['1'],
              }}
            >
              <AppText size="sm" weight="semibold" style={{ color: status.unknown.text }}>
                {t('score.proposed')}
              </AppText>
              <AppText size="xs" style={{ color: status.unknown.text }}>
                {t('score.version', { version: panel.reading.version })}
              </AppText>
            </View>
          ) : null}

          {/* CP61. A derived value has an author — whoever's write produced it — and this one
              is very often not the person reading it: the score on screen when a counsellor
              sits down came out of an assessment somebody else recorded this morning. */}
          <EnteredBy
            compact
            testID="lifestyle-score-entered-by"
            provenance={ofObservation(scoreRow)}
          />
        </View>
      )}
    </View>
  );
}

// --- the catalogue ---

/**
 * The questionnaires have moved on since this tablet fetched them.
 *
 * Drawn above the list and not over it: the copy in hand is still answerable, and a modal that
 * blocked the screen would stop a counsellor mid-conversation over a wording change that may not
 * touch the questionnaire they are about to run. What it must not do is stay silent — the
 * alternative signal is a 422 on an item code at submit, with the patient already asked.
 */
function CatalogueMoved({ onReload }: { onReload: () => void }) {
  const t = useTranslations('lifestyle');
  const { status } = useTokens();
  return (
    <View
      testID="lifestyle-catalogue-moved"
      style={{
        backgroundColor: status.unknown.surface,
        borderColor: status.unknown.border,
        borderWidth: theme.size.borderWidth.thin,
        borderRadius: theme.borderRadius.md,
        padding: theme.spacing['3'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText size="sm" weight="semibold" style={{ color: status.unknown.text }}>
        {t('catalogueMoved')}
      </AppText>
      <AppButton
        testID="lifestyle-catalogue-reload"
        variant="secondary"
        label={t('reload')}
        onPress={onReload}
      />
    </View>
  );
}

function Catalogue({ rows, onOpen }: { rows: InstrumentRow[]; onOpen: (code: string) => void }) {
  const t = useTranslations('lifestyle');
  const { colors } = useTokens();

  return (
    <View style={{ gap: theme.spacing['3'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('catalogue')}
      </AppText>
      {rows.length === 0 ? (
        <AppText size="sm" style={{ color: colors.text.muted }}>
          {t('noInstruments')}
        </AppText>
      ) : null}
      {rows.map((row) => (
        <InstrumentCard key={row.code} row={row} onOpen={onOpen} />
      ))}
    </View>
  );
}

/**
 * One instrument on the list — including the ones that cannot be opened.
 *
 * An unavailable row is a `View` and not a `Pressable`: not a disabled button, which on Android
 * still takes the press and still feels like a control that is failing, but a row that was never
 * a control. It carries its reason in words, so nothing about it depends on the grey.
 */
function InstrumentCard({ row, onOpen }: { row: InstrumentRow; onOpen: (code: string) => void }) {
  const t = useTranslations('lifestyle');
  const { colors, status } = useTokens();

  const body = (
    <View style={{ gap: theme.spacing['1'] }}>
      <AppText
        weight="semibold"
        style={{ color: row.open ? colors.text.primary : colors.text.secondary }}
      >
        <ServerText wording={row.name} />
      </AppText>
      {row.purpose.text !== '' ? (
        <AppText size="sm" style={{ color: colors.text.secondary }}>
          <ServerText wording={row.purpose} />
        </AppText>
      ) : null}

      <AppText size="xs" style={{ color: colors.text.muted }}>
        {t(`domain.${row.domain.toLowerCase()}`)}
      </AppText>

      {/* Whose question this is. On a list where every other row is published literature, the
          clinic's own single question has to say so — otherwise a number from it is eventually
          reported as though it came from a validated scale. */}
      {row.ownWriting ? (
        <View style={{ gap: theme.spacing['0.5'] }}>
          <AppText
            testID={`instrument-${row.code}-own`}
            size="xs"
            weight="semibold"
            style={{ color: colors.text.secondary }}
          >
            {t('ownWriting')}
          </AppText>
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('ownWritingWhy')}
          </AppText>
        </View>
      ) : row.copyrightHolder !== '' ? (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('copyright', { holder: row.copyrightHolder })}
        </AppText>
      ) : null}

      {row.availability !== 'ready' ? (
        <View
          testID={`instrument-${row.code}-unavailable`}
          style={{
            marginTop: theme.spacing['1'],
            backgroundColor: status.unknown.surface,
            borderColor: status.unknown.border,
            borderWidth: theme.size.borderWidth.thin,
            borderRadius: theme.borderRadius.md,
            padding: theme.spacing['3'],
            gap: theme.spacing['1'],
          }}
        >
          <AppText size="sm" weight="semibold" style={{ color: status.unknown.text }}>
            {t(`unavailable.${row.availability}`)}
          </AppText>
          {/* The server's own sentence, in the reader's language. An invariant refuses an
              instrument that says why it may or may not be used in only one language, so this
              is a bilingual pair like every other piece of text on the row. */}
          {row.licenceNote.text !== '' ? (
            <AppText size="xs" style={{ color: status.unknown.text }}>
              <ServerText wording={row.licenceNote} />
            </AppText>
          ) : null}
        </View>
      ) : null}
    </View>
  );

  const frame = {
    backgroundColor: row.open ? colors.surface.raised : colors.surface.sunken,
    borderRadius: theme.borderRadius.lg,
    borderWidth: theme.size.borderWidth.thin,
    borderColor: row.open ? colors.border.control : colors.border.subtle,
    padding: theme.spacing['4'],
    minHeight: theme.size.touchTarget,
    justifyContent: 'center' as const,
  };

  if (!row.open) {
    return (
      <View testID={`instrument-${row.code}`} accessibilityRole="summary" style={frame}>
        {body}
      </View>
    );
  }

  return (
    <Pressable
      testID={`instrument-${row.code}`}
      accessibilityRole="button"
      accessibilityLabel={row.name.text}
      onPress={() => onOpen(row.code)}
      style={frame}
    >
      {body}
    </Pressable>
  );
}

// --- one questionnaire, being answered ---

function Runner({
  row,
  run,
  progress,
  outstanding,
  problems,
  busy,
  onClose,
  onChooseOption,
  onTypeNumber,
  onAnswerYesNo,
  onSubmit,
}: {
  row: InstrumentRow;
  run: RunState;
  progress: Progress | null;
  outstanding: string[];
  problems: Partial<Record<string, NumberProblem>>;
  busy?: boolean;
  onClose: () => void;
  onChooseOption: (itemCode: string, optionCode: string) => void;
  onTypeNumber: (itemCode: string, text: string) => void;
  onAnswerYesNo: (itemCode: string, value: boolean) => void;
  onSubmit: () => void;
}) {
  const t = useTranslations('lifestyle');
  const { colors } = useTokens();
  // Already in the instrument's own order: `catalogueRows` sorted them on the way in, so the
  // component never re-sorts and cannot disagree with the module that decided the order.
  const items = row.items;

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      <View style={{ gap: theme.spacing['1'] }}>
        <AppText size="lg" weight="semibold">
          <ServerText wording={row.name} />
        </AppText>
        {/* Two integers and never a percentage: "two of three" is what an operator says out
            loud, and a percentage rounds away the difference between finished and nearly. */}
        {progress !== null ? (
          <AppText testID="runner-progress" size="sm" style={{ color: colors.text.secondary }}>
            {t('progress', { answered: progress.answered, total: progress.total })}
          </AppText>
        ) : null}
        {row.version !== null ? (
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('version', { version: row.version })}
          </AppText>
        ) : null}
        {row.ownWriting ? (
          <AppText size="xs" weight="semibold" style={{ color: colors.text.secondary }}>
            {t('ownWriting')}
          </AppText>
        ) : null}
      </View>

      {items.map((item) => (
        <Item
          key={item.item_code}
          item={item}
          run={run}
          problem={problems[item.item_code]}
          onChooseOption={onChooseOption}
          onTypeNumber={onTypeNumber}
          onAnswerYesNo={onAnswerYesNo}
        />
      ))}

      {outstanding.length > 0 ? (
        <AppText testID="runner-outstanding" size="sm" style={{ color: colors.text.secondary }}>
          {t('outstanding', { n: outstanding.length })}
        </AppText>
      ) : null}

      <AppButton
        testID="runner-submit"
        label={t('submit')}
        disabled={busy === true || outstanding.length > 0 || Object.keys(problems).length > 0}
        onPress={onSubmit}
      />
      <AppButton testID="runner-close" variant="secondary" label={t('back')} onPress={onClose} />
    </View>
  );
}

/** One question, in whichever of the three shapes it asks for. */
function Item({
  item,
  run,
  problem,
  onChooseOption,
  onTypeNumber,
  onAnswerYesNo,
}: {
  item: InstrumentItem;
  run: RunState;
  problem?: NumberProblem;
  onChooseOption: (itemCode: string, optionCode: string) => void;
  onTypeNumber: (itemCode: string, text: string) => void;
  onAnswerYesNo: (itemCode: string, value: boolean) => void;
}) {
  const t = useTranslations('lifestyle');
  const { colors, status } = useTokens();
  const locale = usePreferences((state) => state.language);
  const answer = answerFor(run, item.item_code);
  const prompt = wordingOf(item.prompt_en, item.prompt_bn, locale);

  return (
    <View testID={`item-${item.item_code}`} style={{ gap: theme.spacing['2'] }}>
      <AppText weight="medium">
        <ServerText wording={prompt} />
      </AppText>
      {!item.required ? (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {t('optional')}
        </AppText>
      ) : null}

      {item.answer_type === 'coded' ? (
        <View style={{ gap: theme.spacing['2'] }}>
          {optionsInOrder(item).map((option) => {
            const selected = answer.option === option.option_code;
            return (
              <Pressable
                key={option.option_code}
                testID={`option-${item.item_code}-${option.option_code}`}
                accessibilityRole="radio"
                accessibilityState={{ selected }}
                onPress={() => onChooseOption(item.item_code, option.option_code)}
                style={{
                  // The whole row, not a small circle beside a label. A tap target the width
                  // of the screen is one an operator hits without looking down.
                  minHeight: theme.size.touchTarget,
                  justifyContent: 'center',
                  paddingHorizontal: theme.spacing['4'],
                  paddingVertical: theme.spacing['2'],
                  borderRadius: theme.borderRadius.md,
                  borderWidth: selected
                    ? theme.size.borderWidth.thick
                    : theme.size.borderWidth.thin,
                  borderColor: selected ? colors.brand.border : colors.border.control,
                  backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
                }}
              >
                {/* The chosen answer says so in a word as well as in a border: roughly one man
                    in twelve who will work here cannot rely on the colour, and a tablet in
                    direct sun flattens it for everybody. */}
                <AppText
                  weight={selected ? 'semibold' : 'regular'}
                  style={{ color: selected ? colors.brand.text : colors.text.primary }}
                >
                  <ServerText wording={wordingOf(option.label_en, option.label_bn, locale)} />
                  {selected ? ` · ${t('chosen')}` : ''}
                </AppText>
              </Pressable>
            );
          })}
        </View>
      ) : null}

      {item.answer_type === 'numeric' ? (
        <MeasurementField
          testID={`number-${item.item_code}`}
          label={prompt.text}
          value={answer.text}
          unit={item.unit ?? '1'}
          units={[item.unit ?? '1']}
          onChangeValue={(text) => onTypeNumber(item.item_code, text)}
          onChangeUnit={() => undefined}
          warning={
            problem === undefined
              ? null
              : {
                  severity: 'warn',
                  text: t(`numberProblem.${problem}`, {
                    min: item.min_value ?? 0,
                    max: item.max_value ?? 0,
                  }),
                }
          }
        />
      ) : null}

      {item.answer_type === 'boolean' ? (
        <View style={{ flexDirection: 'row', gap: theme.spacing['2'] }}>
          {[true, false].map((value) => {
            const selected = answer.yesNo === value;
            return (
              <Pressable
                key={String(value)}
                testID={`yesno-${item.item_code}-${value ? 'yes' : 'no'}`}
                accessibilityRole="radio"
                accessibilityState={{ selected }}
                onPress={() => onAnswerYesNo(item.item_code, value)}
                style={{
                  flex: 1,
                  minHeight: theme.size.touchTarget,
                  alignItems: 'center',
                  justifyContent: 'center',
                  borderRadius: theme.borderRadius.md,
                  borderWidth: selected
                    ? theme.size.borderWidth.thick
                    : theme.size.borderWidth.thin,
                  borderColor: selected ? colors.brand.border : colors.border.control,
                  backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
                }}
              >
                <AppText weight={selected ? 'semibold' : 'regular'}>
                  {t(value ? 'yes' : 'no')}
                </AppText>
              </Pressable>
            );
          })}
        </View>
      ) : null}

      {answered(run, item) ? null : (
        <AppText size="xs" style={{ color: status.unknown.text }}>
          {item.required ? t('notAnsweredYet') : t('optionalNotAnswered')}
        </AppText>
      )}
    </View>
  );
}

// --- §3 step 3's four plain numbers ---

function Numbers({
  numbers,
  warnings,
  packYears,
  busy,
  saved,
  onChangeNumber,
  onChangeUnit,
  onConfirmNumber,
  onSave,
}: {
  numbers: NumbersForm;
  warnings: NumberWarnings;
  packYears: Observation | null;
  busy?: boolean;
  saved?: boolean;
  onChangeNumber: (key: LifestyleFieldKey, text: string) => void;
  onChangeUnit: (key: LifestyleFieldKey, unit: string) => void;
  onConfirmNumber: (key: LifestyleFieldKey) => void;
  onSave: () => void;
}) {
  const t = useTranslations('lifestyle');
  const { colors } = useTokens();
  const language = usePreferences((state) => state.language);
  const blocked = Object.values(warnings).some((warning) => warning?.severity === 'stop');

  return (
    <View style={{ gap: theme.spacing['4'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('numbers')}
      </AppText>

      {LIFESTYLE_FIELDS.map((field) => (
        <MeasurementField
          key={field.key}
          testID={`lifestyle-${field.key}`}
          label={t(`field.${field.key}`)}
          value={numbers[field.key].text}
          unit={numbers[field.key].unit}
          units={field.units}
          onChangeValue={(text) => onChangeNumber(field.key, text)}
          onChangeUnit={(unit) => onChangeUnit(field.key, unit)}
          warning={warningFor(warnings[field.key], numbers[field.key].unit, t, language)}
          onConfirm={() => onConfirmNumber(field.key)}
          confirmLabel={t('confirmValue')}
        />
      ))}

      {/* Pack-years, computed by the server from the two counts above. Declared since CP43 and
          unreachable until this checkpoint gave it a smoking history to read. Shown as a
          readout and never as a field: a client that could type a derived value would be a
          client asserting a clinical number nobody measured. */}
      {packYears !== null && packYears.value !== undefined ? (
        <View
          testID="lifestyle-pack-years"
          style={{
            backgroundColor: colors.surface.raised,
            borderRadius: theme.borderRadius.md,
            borderWidth: theme.size.borderWidth.thin,
            borderColor: colors.border.subtle,
            padding: theme.spacing['3'],
            gap: theme.spacing['0.5'],
          }}
        >
          <AppText size="sm" style={{ color: colors.text.secondary }}>
            {t('packYears')}
          </AppText>
          <AppText size="2xl" weight="semibold" variant="clinicalValue">
            {String(packYears.value)}
          </AppText>
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('packYearsBy', { version: packYears.formula_version ?? '' })}
          </AppText>
          {/* The derivation's own author, for the same reason: a pack-years figure that has
              been on the record since a previous visit is not this operator's number. */}
          <EnteredBy
            compact
            testID="lifestyle-pack-years-entered-by"
            provenance={ofObservation(packYears)}
          />
        </View>
      ) : null}

      <AppButton
        testID="lifestyle-save-numbers"
        label={saved === true ? t('savedNumbers') : t('saveNumbers')}
        disabled={busy === true || blocked}
        onPress={onSave}
      />
    </View>
  );
}

// --- what the server stored, afterwards ---

/**
 * The response as the record holds it, with each answer's points.
 *
 * Shown **after** the write and read off the stored response, which is the difference between
 * showing the working and doing the arithmetic. The points beside an option are deliberately
 * absent from the questionnaire itself: an operator reading "4 points" aloud beside "daily or
 * almost daily" is an operator teaching the patient which answer to give.
 */
function Working({
  response,
  totalMeansSomething,
}: {
  response: InstrumentResponse;
  /** Whether this instrument's answers add up to anything. The server says; this only draws. */
  totalMeansSomething: boolean;
}) {
  const t = useTranslations('lifestyle');
  const { colors } = useTokens();
  const locale = usePreferences((state) => state.language);

  return (
    <View
      testID="lifestyle-working"
      style={{
        backgroundColor: colors.surface.raised,
        borderRadius: theme.borderRadius.lg,
        borderWidth: theme.size.borderWidth.thin,
        borderColor: colors.border.subtle,
        padding: theme.spacing['4'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {t('recorded', { version: response.instrument_version })}
      </AppText>
      {/* The server's total, computed from the item rows on the way out and stored nowhere.
          A sentence with a figure in it rather than a bare clinical value, so the figure is the
          reader's own numerals — `clinicalValue` pins the Latin face, which is right for a
          measurement standing alone and wrong for a number inside a Bangla sentence.

          Drawn only where the instrument is summed. `READINESS_1`'s own published note says its
          score means nothing on its own, and "Total 3" under a single question is a screen
          inventing a finding out of an answer. */}
      {totalMeansSomething ? (
        <AppText testID="lifestyle-total" size="2xl" weight="semibold">
          {t('total', { total: response.total })}
        </AppText>
      ) : (
        <AppText testID="lifestyle-no-total" size="sm" style={{ color: colors.text.secondary }}>
          {t('noTotal')}
        </AppText>
      )}
      {/* CP61, on the response as a whole rather than on each answer: a questionnaire is
          answered by one person at one moment, and the item rows are that one act. */}
      <EnteredBy
        compact
        testID="lifestyle-response-entered-by"
        provenance={ofInstrumentResponse(response)}
      />
      {response.answers.map((answer) => (
        <View key={answer.item_code} style={{ gap: theme.spacing['0.5'] }}>
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {locale === 'bn'
              ? (answer.prompt_bn ?? '').trim() || (answer.prompt_en ?? '').trim()
              : (answer.prompt_en ?? '').trim() || (answer.prompt_bn ?? '').trim()}
          </AppText>
          <AppText size="sm">
            {locale === 'bn'
              ? (answer.option_bn ?? '').trim() || (answer.option_en ?? '').trim()
              : (answer.option_en ?? '').trim() || (answer.option_bn ?? '').trim()}
            {answer.value_num === undefined ? '' : ` ${String(answer.value_num)}`}
            {` · ${t('points', { points: answer.score })}`}
          </AppText>
        </View>
      ))}
    </View>
  );
}

// --- what went wrong ---

function TroubleBanner({ trouble, onReload }: { trouble: Trouble; onReload: () => void }) {
  const t = useTranslations('lifestyle');
  // The same cast the counselling screen uses: `troubleKey` returns a key this component does
  // not enumerate, and use-intl's key type cannot follow a value chosen at runtime.
  const say = t as unknown as (key: string, values?: Record<string, string>) => string;
  const { status } = useTokens();
  const advice = adviceFor(trouble);

  return (
    <View
      testID="lifestyle-trouble"
      style={{
        backgroundColor: status.borderline.surface,
        borderColor: status.borderline.border,
        borderWidth: theme.size.borderWidth.thin,
        borderRadius: theme.borderRadius.md,
        padding: theme.spacing['3'],
        gap: theme.spacing['2'],
      }}
    >
      <AppText size="sm" weight="semibold" style={{ color: status.borderline.text }}>
        {say(troubleKey(trouble))}
      </AppText>
      {trouble.message !== '' ? (
        <AppText size="sm" style={{ color: status.borderline.text }}>
          {trouble.message}
        </AppText>
      ) : null}
      {advice === 'reload' ? (
        <AppButton
          testID="lifestyle-reload"
          variant="secondary"
          label={t('reload')}
          onPress={onReload}
        />
      ) : null}
    </View>
  );
}

// --- shared pieces ---

/**
 * Text the server wrote, with a line when it is not in the reader's language.
 *
 * The fallback happens **and says so**. A Bangla-reading operator handed English with no
 * explanation is one who thinks the app switched languages on them, and who may read a question
 * aloud wrongly rather than admit they could not read it.
 */
function ServerText({ wording }: { wording: Wording }) {
  const t = useTranslations('lifestyle');
  if (wording.text === '') return <>{t('noWording')}</>;
  if (wording.ownLanguage) return <>{wording.text}</>;
  return <>{`${wording.text} · ${t('inOtherLanguage')}`}</>;
}

/**
 * A plausibility verdict, as a sentence.
 *
 * Composed here rather than sent by the server, the same rule station 2 follows: the operator
 * may be reading Bangla, and a message assembled in English on a server is a message half the
 * staff cannot act on. The rule sends the numbers; the screen writes the words.
 */
function warningFor(
  verdict: FieldWarning | undefined,
  unit: string,
  t: ReturnType<typeof useTranslations<'lifestyle'>>,
  language: 'en' | 'bn',
): { text: string; severity: 'warn' | 'stop' } | null {
  if (verdict === undefined) return null;
  const note = language === 'bn' ? verdict.note_bn : verdict.note_en;
  // The limit is in the canonical unit, which for sleep and activity is minutes whatever the
  // operator is typing in. Saying so is the difference between "over 720" and "over 720 min".
  const canonical = unit === 'h' ? 'min' : unit;
  const words = { limit: verdict.limit, unit: unitLabel(canonical, language) };
  let text: string;
  switch (verdict.kind) {
    case 'low':
      text = t('warnLow', words);
      break;
    case 'high':
      text = t('warnHigh', words);
      break;
    case 'rose':
      text = t('warnRose', words);
      break;
    default:
      text = t('warnFell', words);
  }
  if (note !== undefined && note !== '') text = `${text} ${note}`;
  return { text, severity: verdict.severity };
}
