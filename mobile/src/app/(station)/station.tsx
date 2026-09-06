import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from 'react';
import { Pressable, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { ScreenShell } from '@/components/ScreenShell';
import { theme, useTokens } from '@/lib/tokens';
import {
  ALLERGEN_SYSTEM,
  AllergyStep,
  assertAllergyStatus,
  edit as editAllergyDraft,
  emptyDraft as emptyAllergyDraft,
  getAllergyState,
  listAllergyChanges,
  listReactions,
  recordAllergy,
  setCoding as setAllergyCoding,
  toAssertion,
  toRecording as toAllergyRecording,
  toWithdrawal,
  troubleOf as allergyTroubleOf,
  withdrawAllergy,
  withdrawAllergyAssertion,
  type AllergyChange,
  type AllergyDraft,
  type AllergyReaction,
  type AllergyState,
  type Answer as AllergyAnswer,
  type AssertionKind,
  type Trouble as AllergyTrouble,
  type WithdrawTarget,
} from '@/features/allergies';
import {
  AnthropometryStation,
  emptyForm,
  hasBlocking,
  previousFrom,
  previousMeasurementsFrom,
  previousSourcesFrom,
  toBatch,
  warningsFor,
  type Confirmations,
  type FieldKey,
  type FormState,
  type PreviousMeasurements,
  type PreviousSources,
  type PreviousValues,
} from '@/features/anthropometry';
import {
  CriticalAlertModal,
  prepareAlarm,
  releaseAlarm,
  type CriticalAlert,
} from '@/features/alerts';
import {
  CounselingGateStep,
  CounselingStation,
  attemptKey,
  choicesOf,
  completeCounselingSession,
  eventFor,
  getCounselingGate,
  getCounselingSession,
  holdEvent,
  keepsItsEvent,
  listCounselingChecklistsForVisit,
  listCounselingSessionsForVisit,
  readsBack,
  releaseEvent,
  resumeOf,
  startCounselingSession,
  tickCounselingItem,
  toCompletion,
  toStart,
  toTick,
  toUntick,
  troubleOf as counselingTroubleOf,
  untickCounselingItem,
  type Attempt as CounselingAttempt,
  type CounselingSession,
  type HeldEvents,
  type Trouble as CounselingTrouble,
} from '@/features/counseling';
import {
  ExaminationStation,
  emptyExam,
  footRiskFrom,
  footRiskSourcesFrom,
  markAllFelt,
  promptsFor,
  setCoded,
  setFlag,
  setNotTested,
  setNotTestedReason,
  setScore,
  tapSite,
  toExamBatch,
  type Answer,
  type ExamForm,
  type ExamGroup,
  type FootRisk,
  type RiskObservation,
  type HistoryRow,
  type Side,
} from '@/features/examination';
import {
  ExerciseStation,
  answerCondition,
  answerWalks,
  assessmentReading,
  chooseExercise,
  emptyAssessment,
  exclusionReading,
  issuePlan,
  keepOffered,
  listContraindications,
  namesOf,
  noAssessment,
  noTargets,
  offerRows,
  planReading,
  questionRows,
  questionsMissing,
  readExerciseRecord,
  readOptions,
  refusedRow,
  recordAssessment as recordExerciseAssessment,
  reviewFor,
  stepFor,
  targetProblems,
  toAssessmentBody,
  toPlanBody,
  troubleOf as exerciseTroubleOf,
  typeJointPain,
  typeMinutes as typeTargetMinutes,
  typeTimes as typeTargetTimes,
  typeWalkMinutes,
  unanswered,
  unapproved as unapprovedExercises,
  walkMinutesProblem,
  type Assessment as ExerciseAssessment,
  type AssessmentDraft as ExerciseDraft,
  type Chosen as ExerciseTargets,
  type ConditionAnswer,
  type Contraindication,
  type Options as ExerciseOptions,
  type Plan as ExercisePlan,
  type Review as ExerciseReview,
  type Trouble as ExerciseTrouble,
  type WalksAnswer,
} from '@/features/exercise';
import { PercentileCard, type CardPercentile, type CardWeightStatus } from '@/features/growth';
import {
  HistoryStation,
  amendItem,
  confirmItem,
  countUncoded,
  edit as editDraft,
  emptyDraft,
  lifestyleRows,
  listKinds,
  listMedicalHistory,
  recordItem,
  removeItem,
  setCoding,
  setKind,
  setOnset,
  toReactivation,
  toRecording,
  toRemoval,
  toResolution,
  troubleOf,
  type FamilyRelation,
  type HistoryDraft,
  type HistoryItem,
  type HistoryKind,
  type ObservationRow,
  type Trouble,
} from '@/features/history';
import {
  LifestyleStation,
  answerYesNo,
  catalogueRows,
  chooseOption,
  emptyNumbers,
  emptyRun,
  getLifestyleScoring,
  hasBlockingNumber,
  instrumentNamed,
  listInstruments,
  movedOn,
  numberProblems as lifestyleNumberProblems,
  numbersEmpty,
  outstandingOf,
  packYearsOnRecord,
  progressOf,
  readCatalogueVersion,
  recordAssessment,
  recordLifestyleNumbers,
  scoreLifestyle,
  scoreOnRecord,
  scorePanel,
  toAssessment,
  toNumbersBatch,
  troubleOf as lifestyleTroubleOf,
  typeNumber,
  warningsForNumbers,
  type Catalogue,
  type Confirmations as LifestyleConfirmations,
  type InstrumentResponse,
  type LifestyleFieldKey,
  type NumbersForm,
  type Observation as LifestyleObservation,
  type RunState,
  type Scoring as LifestyleScoring,
  type Trouble as LifestyleTrouble,
} from '@/features/lifestyle';
import {
  DEBOUNCE_MS as FOOD_DEBOUNCE_MS,
  NutritionStation,
  RECALL_REFRESH_MS,
  afterEntry,
  alreadyWithdrawn,
  applyAnswer,
  chooseFood,
  chooseMeal,
  chooseMeasure,
  ceilingFor,
  dayRelation,
  emptyDraft as emptyEntryDraft,
  emptyReference,
  entryIdFor,
  foodRows,
  getRecall,
  issueSearch,
  listRecallDays,
  listReference,
  mealGroups,
  measureNamed,
  measuresFor,
  missingFrom,
  openPicker as openFoodPicker,
  openWithdrawal,
  recordDietEntry,
  retrySearch as retryFoodSearch,
  runSearch as runFoodSearch,
  searchDue,
  shiftDay,
  toEntry,
  toWithdrawal as toDietWithdrawal,
  totalsOf,
  troubleOf as nutritionTroubleOf,
  typeQuantity,
  typeReason,
  typedQuery,
  unapproved,
  withdrawDietEntry,
  type DietRecall,
  type EntryDraft,
  type FoodRow,
  type PickerState as FoodPickerState,
  type RecallDay,
  type Reference,
  type Trouble as NutritionTrouble,
  type WithdrawalDraft,
} from '@/features/nutrition';
import {
  DEBOUNCE_MS,
  apply,
  clearSelection,
  due,
  issue,
  openPicker,
  retry,
  runSearch,
  select,
  typed,
  type PickerState,
} from '@/features/terminology';
import {
  VitalsStation,
  VITAL_FIELDS,
  emptyReading,
  flagsFor,
  previousVitalSourcesFrom,
  previousVitalsLocally,
  recordVitals,
  type PreviousVitalSources,
  type Range,
  type Reading,
  type Subject,
  type VitalKey,
} from '@/features/vitals';
import type { PlausibilityRule } from '@dthcms/clinical-calc';

import { type ObservationProvenance } from '@/features/attribution';
import { inServicePatient, readLocalPatient } from '@/features/queue';
import { useSyncEngine } from '@/features/sync';
import { cacheCatalogue, readCatalogue } from '@/lib/sync';

import { api } from '@/lib/api';
import { localStore } from '@/lib/local-store';
import { usePreferences } from '@/stores/preferences';
import { activePermissions, activeStation, useSession } from '@/stores/session';

/**
 * My station's capture screen (CP45, CP49).
 *
 * Which station is not a choice. It comes from the hat the operator is wearing — the same
 * rule the queue follows — because an operator working at anthropometry is at anthropometry,
 * and a screen that let them pick is a screen where a weight lands under a vitals encounter.
 *
 * Switching hats (CP41) therefore switches this screen, which is exactly what §3's "the same
 * assistant enters BP, then switches to anthropometry entry, from the same phone" describes.
 */
/**
 * The critical-value modal's state, shared by every station that records a value (CP50).
 *
 * The alarm is prepared when the screen mounts rather than when an alert arrives: the moment
 * an alert arrives is the worst possible moment to be loading an audio file, and a first
 * alarm that plays half a second late is a first alarm that plays after the operator has
 * looked away.
 */
function useCriticalAlerts() {
  const [alerts, setAlerts] = useState<CriticalAlert[]>([]);
  const [seen, setSeen] = useState(false);

  useEffect(() => {
    void prepareAlarm();
    return releaseAlarm;
  }, []);

  const raise = useCallback((raised: unknown) => {
    const list = Array.isArray(raised) ? (raised as CriticalAlert[]) : [];
    if (list.length === 0) return;
    setSeen(false);
    setAlerts(list);
  }, []);

  const dismiss = useCallback(() => {
    setSeen(true);
    setAlerts([]);
  }, []);

  return { alerts, seen, raise, dismiss };
}

function AnthropometryScreen({ station }: { station: string }) {
  const t = useTranslations('anthropometry');
  const critical = useCriticalAlerts();

  const [form, setForm] = useState<FormState>(emptyForm);
  const [previous, setPrevious] = useState<PreviousValues>({});
  const [previousTaken, setPreviousTaken] = useState<PreviousMeasurements>({});
  // The rows those numbers came off, so the comparison line can say who took them (CP61).
  const [previousSources, setPreviousSources] = useState<PreviousSources>({});
  // The patient's rows as they came back, so the growth card can find the measurement each
  // score was computed from and name whoever took it (CP61). Kept whole rather than reduced:
  // the card matches by code *and* moment, which a per-field map cannot answer.
  const [observations, setObservations] = useState<ObservationProvenance[]>([]);
  const [rules, setRules] = useState<PlausibilityRule[]>([]);
  const [confirmed, setConfirmed] = useState<Confirmations>({});
  const [patient, setPatient] = useState<{
    id: string;
    name: string;
    sex: 'male' | 'female' | 'other';
    ageYears: number;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  // The growth card, after a save. Not before: a card showing last visit's percentiles while
  // the operator types today's measurements is a card somebody reads as today's (CP48).
  const [growth, setGrowth] = useState<{
    age_days: number;
    applicable: boolean;
    note?: string;
    current: Partial<Record<'HFA' | 'WFA' | 'BFA', CardPercentile>>;
    weightStatus?: CardWeightStatus;
  } | null>(null);

  // The patient in service at this station. The queue owns who that is (CP39); this screen
  // only measures them.
  useEffect(() => {
    let live = true;
    if (station === '') return;
    void (async () => {
      const queue = await api.GET('/v1/stations/{station}/queue', {
        params: { path: { station } },
      });
      const inService = queue.data?.entries.find((entry) => entry.status === 'in_service');
      if (!live || inService?.patient_id === undefined) return;
      const [record, values] = await Promise.all([
        api.GET('/v1/patients/{id}', { params: { path: { id: inService.patient_id } } }),
        api.GET('/v1/patients/{id}/observations', {
          params: { path: { id: inService.patient_id } },
        }),
      ]);
      if (!live || record.data === undefined) return;
      setPatient({
        id: record.data.patient.id,
        name: record.data.patient.name_bn || record.data.patient.name_en,
        sex: record.data.patient.sex as 'male' | 'female' | 'other',
        // The server already computes the age from the validated date of birth.
        ageYears: record.data.patient.birth.age,
      });
      setPrevious(previousFrom(values.data?.observations));
      setPreviousTaken(previousMeasurementsFrom(values.data?.observations));
      setPreviousSources(previousSourcesFrom(values.data?.observations));
      setObservations((values.data?.observations ?? []) as ObservationProvenance[]);
    })();
    return () => {
      live = false;
    };
  }, [station]);

  // The plausibility rules, once. A tablet fetches them on arrival and warns the operator
  // for the rest of the clinic session, offline — which is the only way the warning arrives
  // while the patient is still standing there (CP46).
  useEffect(() => {
    let live = true;
    void api.GET('/v1/observations/plausibility').then((result) => {
      if (live && result.data !== undefined) setRules(result.data.rules as PlausibilityRule[]);
    });
    return () => {
      live = false;
    };
  }, []);

  const onChangeValue = useCallback((key: FieldKey, text: string) => {
    setSaved(false);
    // A changed number is a different number, so an earlier confirmation no longer applies.
    // Carrying it over would let an operator confirm 205, retype 15, and save it.
    setConfirmed((current) => ({ ...current, [key]: false }));
    setForm((current) => ({ ...current, [key]: { ...current[key], text } }));
  }, []);

  const onChangeUnit = useCallback((key: FieldKey, unit: string) => {
    setSaved(false);
    // The typed number stays. An operator who realises the scale reads pounds after typing
    // 154 means 154 lb; re-converting it to 69.9 would be the app deciding they meant
    // something they did not type.
    setConfirmed((current) => ({ ...current, [key]: false }));
    setForm((current) => ({ ...current, [key]: { ...current[key], unit } }));
  }, []);

  const warnings = useMemo(
    () =>
      warningsFor(
        form,
        rules,
        { sex: patient?.sex ?? 'other', ageYears: patient?.ageYears ?? 0 },
        previousTaken,
        confirmed,
      ),
    [form, rules, patient, previousTaken, confirmed],
  );

  const facts = useMemo(
    () => ({
      sex: (patient?.sex ?? 'other') as 'male' | 'female' | 'other',
      ageYears: patient?.ageYears ?? 0,
    }),
    [patient],
  );

  const onSave = useCallback(async () => {
    if (patient === null) return;
    setBusy(true);
    try {
      const perField = new Map<FieldKey, string>();
      const confirmedFields = new Set(
        Object.entries(confirmed)
          .filter(([, value]) => value === true)
          .map(([key]) => key as FieldKey),
      );
      const body = toBatch(form, {
        batch: crypto.randomUUID(),
        patient: patient.id,
        perField: (key) => {
          const existing = perField.get(key);
          if (existing !== undefined) return existing;
          const id = crypto.randomUUID();
          perField.set(key, id);
          return id;
        },
        confirmed: (key) => confirmedFields.has(key),
      });
      const written = await api.POST('/v1/observations/batch', {
        params: {
          header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id },
        },
        body,
      });
      // Before anything else on this path. A critical value has to reach the operator's eyes
      // and ears in the same instant the save returns — not after two more round trips for
      // the history and the growth card, which on a clinic connection is several seconds.
      critical.raise(written.data?.alerts);
      setSaved(true);
      const values = await api.GET('/v1/patients/{id}/observations', {
        params: { path: { id: patient.id } },
      });
      setPrevious(previousFrom(values.data?.observations));
      setPreviousTaken(previousMeasurementsFrom(values.data?.observations));
      setPreviousSources(previousSourcesFrom(values.data?.observations));
      setObservations((values.data?.observations ?? []) as ObservationProvenance[]);
      setForm(emptyForm());
      setConfirmed({});

      // [R-06]: the card appears immediately after entry for a patient under the paediatric
      // cut-off. For everybody else the server says "not applicable" and nothing is drawn —
      // which is why the check is the server's answer rather than an age comparison here.
      const scored = await api.GET('/v1/patients/{id}/growth', {
        params: { path: { id: patient.id } },
      });
      if (scored.data !== undefined) {
        setGrowth({
          age_days: scored.data.growth.age_days,
          applicable: scored.data.growth.applicable,
          note: scored.data.growth.note,
          current: (scored.data.growth.current ?? {}) as Partial<
            Record<'HFA' | 'WFA' | 'BFA', CardPercentile>
          >,
          weightStatus: scored.data.weight_status as CardWeightStatus | undefined,
        });
      }
    } finally {
      setBusy(false);
    }
  }, [confirmed, critical, form, patient]);

  return (
    <ScreenShell titleKey="screen.anthropometry">
      {patient === null ? (
        <AppText>{t('noPatient')}</AppText>
      ) : (
        <AnthropometryStation
          patientName={patient.name}
          patient={facts}
          form={form}
          previous={previous}
          previousSources={previousSources}
          busy={busy || hasBlocking(warnings)}
          saved={saved}
          warnings={warnings}
          onChangeValue={onChangeValue}
          onChangeUnit={onChangeUnit}
          onConfirm={(key) => setConfirmed((current) => ({ ...current, [key]: true }))}
          onSave={() => void onSave()}
        />
      )}
      {growth !== null && growth.applicable ? (
        <PercentileCard
          ageDays={growth.age_days}
          applicable={growth.applicable}
          note={growth.note}
          current={growth.current}
          weightStatus={growth.weightStatus}
          observations={observations}
        />
      ) : null}
      {/* Over everything, including the growth card. A critical value is the one thing on
          this screen that cannot wait for the operator to finish reading something else. */}
      <CriticalAlertModal alerts={critical.alerts} seen={critical.seen} onSeen={critical.dismiss} />
    </ScreenShell>
  );
}

/**
 * Station 5's vitals (CP49) — **and the offline-first worked example (CP64/CP66).**
 *
 * This is the one station converted end to end, so the other eleven have a pattern to follow.
 * Three things changed, and each is the checkpoint's own words:
 *
 *  1. **The screen reads from the local record, not from the network.** The queue, the patient
 *     and the last values all come from projections this device holds, so the form works with the
 *     Wi-Fi off — which is the moment an operator is most likely to be standing in a corridor
 *     with a cuff already on somebody's arm.
 *  2. **Saving writes locally and returns.** `recordVitals` appends the events, updates what the
 *     screen shows and queues them for the clinic in one transaction; there is no request on this
 *     path at all, and no online variant of it.
 *  3. **The sync engine delivers.** The save nudges it, and the sync screen — never this one —
 *     is where the operator is told what has and has not reached the clinic.
 *
 * What is knowingly lost offline, and must not be discovered in a clinic: **the critical-value
 * alert is the server's** (CP50). A dangerous reading recorded with no signal still shows this
 * screen's own out-of-range flag, which is computed from the cached reference ranges, but nobody
 * is paged until the event syncs. That is why the ranges are cached rather than fetched: the
 * flag beside the number is the only safety net a disconnected tablet has.
 *
 * `tabs` is the switch between station 5's two forms (CP51). It is rendered here, inside the
 * shell, rather than wrapped around this screen: a control above the safe-area header would
 * sit outside the frame every other screen in the app shares.
 */
/** Where the vitals screen keeps the reference ranges between sessions. */
const RANGES_CATALOGUE = 'observation-reference-ranges';

function VitalsScreen({ station, tabs }: { station: string; tabs?: ReactNode }) {
  const critical = useCriticalAlerts();
  const engine = useSyncEngine();
  const facilityId = useSession((state) => state.operator?.facilityId ?? '');
  const t = useTranslations('vitals');
  const [readings, setReadings] = useState<Reading[]>(() => [emptyReading()]);
  const [ranges, setRanges] = useState<Range[]>([]);
  const [previous, setPrevious] = useState<Partial<Record<VitalKey, number>>>({});
  // The rows those numbers came off, so the comparison line can say who took them (CP61).
  const [previousVitalSources, setPreviousVitalSources] = useState<PreviousVitalSources>({});
  const [patient, setPatient] = useState<{
    id: string;
    name: string;
    sex: 'male' | 'female' | 'other';
    ageYears: number;
    visitId: string | null;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  /** Everything this screen shows, from the local record. No request on this path. */
  const readLocally = useCallback(async () => {
    const store = localStore();
    if (store === null || station === '') return;
    const now = Date.now();
    const entry = await inServicePatient(store, station, now);
    if (entry === null) {
      setPatient(null);
      return;
    }
    const person = await readLocalPatient(store, entry.patient_id, now);
    setPatient(
      person === null
        ? null
        : { ...person, visitId: entry.visit_id === '' ? null : entry.visit_id },
    );
    const rows = await previousVitalsLocally(store, entry.patient_id);
    setPrevious(previousVitals(rows));
    setPreviousVitalSources(previousVitalSourcesFrom(rows));
  }, [station]);

  useEffect(() => {
    void readLocally();
  }, [readLocally]);

  /*
   * What is normal, from the reference cache first and the clinic second.
   *
   * The order is the point. These ranges are what draws the red flag beside a dangerous number,
   * and on a disconnected tablet they are the *only* warning an operator gets — the clinic's
   * critical-value alert cannot fire until the event reaches it. A screen that fetched them and
   * gave up when the fetch failed would be a screen with no safety net in exactly the session
   * that needs one.
   */
  useEffect(() => {
    let live = true;
    void (async () => {
      const store = localStore();
      const cached = store === null ? null : await readCatalogue(store, RANGES_CATALOGUE);
      if (live && Array.isArray(cached)) setRanges(cached as Range[]);
      const result = await api
        .GET('/v1/observations/reference-ranges')
        .catch(() => ({ data: undefined }));
      if (!live || result.data === undefined) return;
      setRanges(result.data.ranges as Range[]);
      if (store !== null) {
        await cacheCatalogue(store, RANGES_CATALOGUE, '', result.data.ranges, Date.now());
      }
    })();
    return () => {
      live = false;
    };
  }, []);

  const subject: Subject = useMemo(
    () => ({ sex: patient?.sex ?? 'other', ageYears: patient?.ageYears ?? 0 }),
    [patient],
  );
  const flags = useMemo(
    () => readings.map((reading) => flagsFor(reading, ranges, subject)),
    [readings, ranges, subject],
  );

  const editReading = useCallback((index: number, change: (reading: Reading) => Reading) => {
    setSaved(false);
    setReadings((current) =>
      current.map((reading, i) => (i === index ? change(reading) : reading)),
    );
  }, []);

  const onSave = useCallback(async () => {
    const store = localStore();
    if (patient === null || store === null) return;
    setBusy(true);
    try {
      const ids = new Map<string, string>();
      // Each reading gets its own effective time, a minute apart, because two blood
      // pressures taken in one sitting are two facts and a timeline that gave them the same
      // instant could not order them.
      const base = Date.now();
      await recordVitals(
        store,
        readings,
        {
          // Stable per (reading, code): a save the operator was told had failed is re-issued
          // with the same ids and is one measurement, not two.
          perValue: (index, code) => {
            const key = `${index}:${code}`;
            const existing = ids.get(key);
            if (existing !== undefined) return existing;
            const id = crypto.randomUUID();
            ids.set(key, id);
            return id;
          },
          takenAt: (index) => new Date(base + index * 60_000).toISOString(),
          newObservationId: () => crypto.randomUUID(),
        },
        { facilityId, patientId: patient.id, visitId: patient.visitId },
      );
      // Saved means saved *here*, which is the only thing this screen can honestly claim. The
      // sync screen says what has reached the clinic; this one never does.
      setSaved(true);
      void engine?.sync('command');
      await readLocally();
      setReadings([emptyReading()]);
    } finally {
      setBusy(false);
    }
  }, [engine, facilityId, patient, readLocally, readings]);

  return (
    <ScreenShell titleKey="screen.vitals">
      {tabs}
      <View style={{ flex: 1 }}>
        {patient === null ? (
          <AppText>{t('noPatient')}</AppText>
        ) : (
          <VitalsStation
            patientName={patient.name}
            readings={readings}
            flags={flags}
            previous={previous}
            previousSources={previousVitalSources}
            busy={busy}
            saved={saved}
            onChangeValue={(index, key, text) =>
              editReading(index, (reading) => ({
                ...reading,
                values: { ...reading.values, [key]: { ...reading.values[key], text } },
              }))
            }
            onChangeUnit={(index, key, unit) =>
              editReading(index, (reading) => ({
                ...reading,
                values: { ...reading.values, [key]: { ...reading.values[key], unit } },
              }))
            }
            onChangeContext={(index, field, value) =>
              editReading(index, (reading) => ({ ...reading, [field]: value }))
            }
            onAddReading={() => setReadings((current) => [...current, emptyReading()])}
            onSave={() => void onSave()}
          />
        )}
      </View>
      <CriticalAlertModal alerts={critical.alerts} seen={critical.seen} onSeen={critical.dismiss} />
    </ScreenShell>
  );
}

/**
 * Station 5's structured examination (CP51).
 *
 * The third screen in this file and deliberately the same shape as the other two: one
 * patient from the queue, one fetch of reference data on arrival, a batch on save, the
 * critical-value modal over the top. What is different is what it fetches — the whole
 * answer vocabulary, and the patient's own history, because criterion 4's prompts are
 * decided by what the record already says.
 */
function ExaminationScreen({ station, tabs }: { station: string; tabs?: ReactNode }) {
  const critical = useCriticalAlerts();
  const t = useTranslations('examination');

  const [form, setForm] = useState<ExamForm>(emptyExam);
  const [answers, setAnswers] = useState<Answer[]>([]);
  const [history, setHistory] = useState<HistoryRow[]>([]);
  const [risk, setRisk] = useState<Partial<Record<Side, FootRisk>>>({});
  // The rows those categories were read off, so the chip can say who derived them (CP61).
  const [riskSources, setRiskSources] = useState<Partial<Record<Side, RiskObservation>>>({});
  const [patient, setPatient] = useState<{ id: string; name: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let live = true;
    if (station === '') return;
    void (async () => {
      const queue = await api.GET('/v1/stations/{station}/queue', {
        params: { path: { station } },
      });
      const inService = queue.data?.entries.find((entry) => entry.status === 'in_service');
      if (!live || inService?.patient_id === undefined) return;
      const [record, values] = await Promise.all([
        api.GET('/v1/patients/{id}', { params: { path: { id: inService.patient_id } } }),
        // Every category, not just EXAM: the prompts read the retinopathy screening status
        // (SCREENING) and the risk category (DERIVED) as well as the findings themselves.
        api.GET('/v1/patients/{id}/observations', {
          params: { path: { id: inService.patient_id } },
        }),
      ]);
      if (!live || record.data === undefined) return;
      setPatient({
        id: record.data.patient.id,
        name: record.data.patient.name_bn || record.data.patient.name_en,
      });
      setHistory((values.data?.observations ?? []) as HistoryRow[]);
      setRisk(footRiskFrom(values.data?.observations));
      setRiskSources(footRiskSourcesFrom(values.data?.observations));
    })();
    return () => {
      live = false;
    };
  }, [station]);

  // The vocabulary, once. Eleven coded findings, and eleven round trips on a clinic
  // connection is the difference between a two-minute examination and a five-minute one.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/observations/answers').then((result) => {
      if (live && result.data !== undefined) setAnswers(result.data.answers as Answer[]);
    });
    return () => {
      live = false;
    };
  }, []);

  const prompts = useMemo(() => promptsFor(history), [history]);

  const edit = useCallback((change: (current: ExamForm) => ExamForm) => {
    setSaved(false);
    setForm(change);
  }, []);

  const onSave = useCallback(async () => {
    if (patient === null) return;
    setBusy(true);
    try {
      const perValue = new Map<string, string>();
      const perGroup = new Map<ExamGroup, string>();
      const stable = <K,>(store: Map<K, string>, key: K) => {
        const existing = store.get(key);
        if (existing !== undefined) return existing;
        const id = crypto.randomUUID();
        store.set(key, id);
        return id;
      };
      const batches = toExamBatch(form, {
        batch: (group) => stable(perGroup, group),
        patient: patient.id,
        perValue: (code) => stable(perValue, code),
      });

      // One after another rather than all at once. Each foot's batch asks the server to
      // derive that foot's risk category from what it has just written, and a queue of
      // writes racing each other is a race nobody can reason about six months later when a
      // category looks wrong.
      for (const body of batches) {
        const written = await api.POST('/v1/observations/batch', {
          params: {
            header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id },
          },
          body,
        });
        // Before the next batch, for the same reason as at the other two stations: a
        // critical value has to reach the operator in the instant the write returns.
        critical.raise(written.data?.alerts);
      }

      setSaved(true);
      const values = await api.GET('/v1/patients/{id}/observations', {
        params: { path: { id: patient.id } },
      });
      setHistory((values.data?.observations ?? []) as HistoryRow[]);
      // Read back rather than computed here. The category is the server's, and a screen that
      // worked one out for itself would be a second opinion nobody asked for.
      setRisk(footRiskFrom(values.data?.observations));
      setRiskSources(footRiskSourcesFrom(values.data?.observations));
      setForm(emptyExam());
    } finally {
      setBusy(false);
    }
  }, [critical, form, patient]);

  return (
    <ScreenShell titleKey="screen.examination">
      {tabs}
      <View style={{ flex: 1 }}>
        {patient === null ? (
          <AppText>{t('noPatient')}</AppText>
        ) : (
          <ExaminationStation
            patientName={patient.name}
            form={form}
            answers={answers}
            prompts={prompts}
            risk={risk}
            riskSources={riskSources}
            busy={busy}
            saved={saved}
            onTapSite={(side, site) => edit((current) => tapSite(current, side, site))}
            onMarkAllFelt={(side) => edit((current) => markAllFelt(current, side))}
            onSetNotTested={(side, notTested) =>
              edit((current) => setNotTested(current, side, notTested))
            }
            onChangeReason={(side, reason) =>
              edit((current) => setNotTestedReason(current, side, reason))
            }
            onChangeCoded={(code, valueCode) =>
              edit((current) => setCoded(current, code, valueCode))
            }
            onChangeFlag={(code, value) => edit((current) => setFlag(current, code, value))}
            onChangeScore={(text) => edit((current) => setScore(current, text))}
            onSave={() => void onSave()}
          />
        )}
      </View>
      <CriticalAlertModal alerts={critical.alerts} seen={critical.seen} onSeen={critical.dismiss} />
    </ScreenShell>
  );
}

/**
 * A concept picker's clock (CP52).
 *
 * `search.ts` decides everything — when a request is due, which answer may replace what is on
 * screen — and this only turns the handle. The effect returns early once the query in the box
 * is the query already asked for, which is what stops the timer chasing itself.
 *
 * A hook rather than a copy per picker: station 4 now has two, the history item's and the
 * allergy substance's, and the staleness rule is exactly the sort of thing that survives in
 * the first copy and quietly rots in the second.
 */
function usePickerClock(
  picker: PickerState | null,
  setPicker: Dispatch<SetStateAction<PickerState | null>>,
  locale: 'en' | 'bn',
) {
  const [nudge, setNudge] = useState(0);

  useEffect(() => {
    if (picker === null) return;
    if (picker.issuedQuery !== null && picker.query === picker.issuedQuery) return;
    if (!due(picker, Date.now())) {
      const timer = setTimeout(() => setNudge((n) => n + 1), DEBOUNCE_MS);
      return () => clearTimeout(timer);
    }
    let live = true;
    const next = issue(picker, Date.now());
    setPicker(next.state);
    void runSearch(next.request, locale).then((answer) => {
      // Through the one door, so a slow answer cannot land over a newer list.
      if (live) setPicker((current) => (current === null ? current : apply(current, answer)));
    });
    return () => {
      live = false;
    };
  }, [picker, nudge, locale, setPicker]);
}

/**
 * Station 4's medical history, and the allergy checkpoint above it (CP53, CP54).
 *
 * The fourth screen in this file and the same shape as the other three: one patient from the
 * queue, reference data on arrival, and a write per action. What is different is what an
 * action *is*. The other stations save a form; this one makes one assertion at a time —
 * a confirmation, an amendment, a removal, a new item — each its own request with a person
 * behind it, because that is what a medical history is made of.
 *
 * The confirmations are pressed one at a time and sent one at a time. There is no loop over
 * this screen's `onConfirm`, and there is no endpoint one could be pointed at: twenty items
 * carried forward is twenty presses. See `features/history` for why.
 *
 * The allergy checkpoint (CP54) is drawn above the history, as this station's `gate`. It is
 * not another kind of history: it is the hard stop, and no patient leaves station 4 without an
 * answer to it. Nothing on this screen can get past it — the gate is a trigger on the queue
 * table — so what the screen does is let an officer answer it in three ways and say plainly
 * why the patient cannot be sent on until they have.
 */
function HistoryScreen({ station }: { station: string }) {
  const t = useTranslations('history');
  const locale = usePreferences((state) => state.language);

  const [patient, setPatient] = useState<{ id: string; name: string } | null>(null);
  const [visitID, setVisitID] = useState('');
  // When this visit opened, from the visit itself. "Confirmed this visit" is a question about
  // the visit, and the queue's own timestamps answer a narrower one.
  const [since, setSince] = useState('');
  const [kinds, setKinds] = useState<HistoryKind[]>([]);
  const [relations, setRelations] = useState<FamilyRelation[]>([]);
  const [lifestyleCodes, setLifestyleCodes] = useState<string[]>([]);
  const [observations, setObservations] = useState<ObservationRow[]>([]);
  const [items, setItems] = useState<HistoryItem[]>([]);
  const [uncoded, setUncoded] = useState<Record<string, number>>({});

  const [draft, setDraft] = useState<HistoryDraft>(emptyDraft);
  const [draftKind, setDraftKind] = useState<HistoryKind | null>(null);
  const [picker, setPicker] = useState<PickerState | null>(null);

  // CP54's checkpoint. The state is the server's answer and is never assembled here: every
  // write below replaces it wholesale with what the write itself returned, because a
  // withdrawal can re-close the gate and a screen that patched its own copy would be guessing.
  const [allergies, setAllergies] = useState<AllergyState | null>(null);
  const [reactions, setReactions] = useState<AllergyReaction[]>([]);
  const [changes, setChanges] = useState<AllergyChange[]>([]);
  const [answering, setAnswering] = useState<AllergyAnswer | null>(null);
  const [allergyDraft, setAllergyDraft] = useState<AllergyDraft>(emptyAllergyDraft);
  const [allergyPicker, setAllergyPicker] = useState<PickerState | null>(null);
  const [assertReason, setAssertReason] = useState('');
  const [withdrawing, setWithdrawing] = useState<WithdrawTarget | null>(null);
  const [withdrawReason, setWithdrawReason] = useState('');
  const [allergyBusy, setAllergyBusy] = useState(false);
  const [allergyWrote, setAllergyWrote] = useState(false);
  const [allergyTrouble, setAllergyTrouble] = useState<AllergyTrouble | null>(null);

  const [removingId, setRemovingId] = useState<string | null>(null);
  const [removeReason, setRemoveReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [savedId, setSavedId] = useState<string | null>(null);
  const [justRecorded, setJustRecorded] = useState(false);
  const [trouble, setTrouble] = useState<Trouble | null>(null);

  // The six kinds and their rules, once. Everything this screen asks for is derived from
  // them, so a clinic that changes a rule changes the form without changing this build.
  useEffect(() => {
    let live = true;
    void listKinds()
      .then((reference) => {
        if (!live) return;
        setKinds(reference.kinds);
        setRelations(reference.relations);
        setLifestyleCodes(reference.from_lifestyle_station);
      })
      .catch((error: unknown) => {
        if (live) setTrouble(troubleOf(error, locale));
      });
    return () => {
      live = false;
    };
  }, [locale]);

  // The reaction vocabulary, once. Eight chips fetched on arrival is what makes recording an
  // allergy a matter of taps rather than a round trip in the middle of the question.
  useEffect(() => {
    let live = true;
    void listReactions()
      .then((vocabulary) => {
        if (live) setReactions(vocabulary);
      })
      .catch((error: unknown) => {
        if (live) setAllergyTrouble(allergyTroubleOf(error, locale));
      });
    return () => {
      live = false;
    };
  }, [locale]);

  const load = useCallback(async (patientID: string) => {
    const [history, counts] = await Promise.all([listMedicalHistory(patientID), countUncoded()]);
    setItems(history);
    setUncoded(counts);
  }, []);

  const loadAllergies = useCallback(async (patientID: string) => {
    const [state, said] = await Promise.all([
      getAllergyState(patientID),
      listAllergyChanges(patientID),
    ]);
    setAllergies(state);
    setChanges(said);
  }, []);

  useEffect(() => {
    let live = true;
    if (station === '') return;
    void (async () => {
      const queue = await api.GET('/v1/stations/{station}/queue', {
        params: { path: { station } },
      });
      const inService = queue.data?.entries.find((entry) => entry.status === 'in_service');
      if (!live || inService?.patient_id === undefined) return;
      const [record, visit, values] = await Promise.all([
        api.GET('/v1/patients/{id}', { params: { path: { id: inService.patient_id } } }),
        api.GET('/v1/visits/{id}', { params: { path: { id: inService.visit_id } } }),
        // The lifestyle station's answers, to be shown and never asked for again.
        api.GET('/v1/patients/{id}/observations', {
          params: { path: { id: inService.patient_id } },
        }),
      ]);
      if (!live || record.data === undefined) return;
      setPatient({
        id: record.data.patient.id,
        name: record.data.patient.name_bn || record.data.patient.name_en,
      });
      setVisitID(inService.visit_id);
      setSince(visit.data?.visit.opened_at ?? inService.entered_at);
      setObservations((values.data?.observations ?? []) as ObservationRow[]);
      await Promise.all([load(inService.patient_id), loadAllergies(inService.patient_id)]);
    })();
    return () => {
      live = false;
    };
  }, [station, load, loadAllergies]);

  // One clock each. Two pickers, one rule, and the rule lives in `search.ts`.
  usePickerClock(picker, setPicker, locale);
  usePickerClock(allergyPicker, setAllergyPicker, locale);

  const act = useCallback(
    async (run: () => Promise<void>) => {
      if (patient === null) return;
      setBusy(true);
      setTrouble(null);
      try {
        await run();
        await load(patient.id);
      } catch (error: unknown) {
        setTrouble(troubleOf(error, locale));
      } finally {
        setBusy(false);
      }
    },
    [load, locale, patient],
  );

  const onChooseKind = useCallback((kind: HistoryKind) => {
    setJustRecorded(false);
    setDraftKind(kind);
    setDraft((current) => setKind(current, kind));
    // Each kind draws on its own catalogue, so choosing a kind opens a different picker.
    setPicker(openPicker(kind.code_system));
  }, []);

  const onConfirm = useCallback(
    (itemId: string) =>
      void act(async () => {
        // One item. One press by one person, one assertion in the record.
        await confirmItem(itemId, { event: crypto.randomUUID(), visit: visitID });
        setSavedId(itemId);
      }),
    [act, visitID],
  );

  const onSetResolved = useCallback(
    (itemId: string, resolved: boolean) =>
      void act(async () => {
        const ids = { event: crypto.randomUUID(), visit: visitID };
        await amendItem(itemId, resolved ? toResolution(ids) : toReactivation(ids));
      }),
    [act, visitID],
  );

  const onRemove = useCallback(
    (itemId: string) => {
      const body = toRemoval(removeReason, { event: crypto.randomUUID(), visit: visitID });
      // Never without a reason. The screen disables the button too; this is the second lock,
      // because a removal with no reason is a correction nobody can read afterwards.
      if (body === null) return;
      void act(async () => {
        await removeItem(itemId, body);
        setRemovingId(null);
        setRemoveReason('');
      });
    },
    [act, removeReason, visitID],
  );

  const onRecord = useCallback(() => {
    if (patient === null || draftKind === null) return;
    const body = toRecording(draft, draftKind, { event: crypto.randomUUID(), visit: visitID });
    if (body === null) return;
    void act(async () => {
      await recordItem(patient.id, body);
      setJustRecorded(true);
      // The kind stays: an officer taking a history writes several complaints in a row, and
      // making them choose the kind again each time is how the fourth one gets skipped.
      setDraft(setKind(emptyDraft(), draftKind));
      setPicker(openPicker(draftKind.code_system));
    });
  }, [act, draft, draftKind, patient, visitID]);

  /**
   * One allergy write.
   *
   * The state is replaced with what the write itself answered rather than refetched or
   * patched: withdrawing the last allergy can drop the patient back to whatever assertion
   * stands behind it — or to nothing, which re-closes the gate — and the endpoint returns the
   * resulting status for exactly that reason.
   */
  const writeAllergy = useCallback(
    (write: () => Promise<AllergyState>, after?: () => void) => {
      if (patient === null) return;
      setAllergyBusy(true);
      setAllergyTrouble(null);
      void (async () => {
        try {
          setAllergies(await write());
          setAllergyWrote(true);
          after?.();
          setChanges(await listAllergyChanges(patient.id));
        } catch (error: unknown) {
          setAllergyTrouble(allergyTroubleOf(error, locale));
        } finally {
          setAllergyBusy(false);
        }
      })();
    },
    [locale, patient],
  );

  const onRecordAllergy = useCallback(() => {
    if (patient === null) return;
    const body = toAllergyRecording(allergyDraft, reactions, {
      event: crypto.randomUUID(),
      visit: visitID,
    });
    // Never a draft the server would refuse, and never a partial coding: `toRecording` will
    // not build a body for either.
    if (body === null) return;
    writeAllergy(
      () => recordAllergy(patient.id, body),
      () => {
        setAllergyDraft(emptyAllergyDraft());
        setAllergyPicker(openPicker(ALLERGEN_SYSTEM));
      },
    );
  }, [allergyDraft, patient, reactions, visitID, writeAllergy]);

  const onAssert = useCallback(
    (kind: AssertionKind) => {
      if (patient === null) return;
      // `toAssertion` is the only thing in this app that builds one, it knows two kinds, and
      // it refuses an "unable to assess" with no reason. There is no other path to this call.
      const body = toAssertion(kind, assertReason, {
        event: crypto.randomUUID(),
        visit: visitID,
      });
      if (body === null) return;
      writeAllergy(
        () => assertAllergyStatus(patient.id, body),
        () => {
          setAssertReason('');
          setAnswering(null);
        },
      );
    },
    [assertReason, patient, visitID, writeAllergy],
  );

  const onWithdraw = useCallback(
    (target: WithdrawTarget) => {
      const body = toWithdrawal(withdrawReason, { event: crypto.randomUUID(), visit: visitID });
      // Never without a reason. The screen disables the button too; this is the second lock,
      // because an entry taken back with no reason is a correction nobody can read afterwards.
      if (body === null) return;
      writeAllergy(
        () =>
          target.kind === 'allergy'
            ? withdrawAllergy(target.id, body)
            : withdrawAllergyAssertion(target.id, body),
        () => {
          setWithdrawing(null);
          setWithdrawReason('');
        },
      );
    },
    [visitID, withdrawReason, writeAllergy],
  );

  const lifestyle = useMemo(
    () => lifestyleRows(lifestyleCodes, observations),
    [lifestyleCodes, observations],
  );

  return (
    <ScreenShell titleKey="screen.history">
      <View style={{ flex: 1 }}>
        {patient === null ? (
          <AppText>{t('noPatient')}</AppText>
        ) : (
          <HistoryStation
            patientName={patient.name}
            gate={
              <AllergyStep
                state={allergies}
                reactions={reactions}
                changes={changes}
                answering={answering}
                draft={allergyDraft}
                picker={allergyPicker}
                reason={assertReason}
                withdrawing={withdrawing}
                withdrawReason={withdrawReason}
                busy={allergyBusy}
                justWrote={allergyWrote}
                trouble={allergyTrouble}
                onChooseAnswer={(answer) => {
                  setAllergyWrote(false);
                  setAnswering(answer);
                  // The substance comes from the clinic's own dictionary, and the picker opens
                  // on its favourites — so the common allergens cost no keystrokes at all.
                  setAllergyPicker(answer === 'ALLERGY' ? openPicker(ALLERGEN_SYSTEM) : null);
                }}
                onEditDraft={(patch) => {
                  setAllergyWrote(false);
                  setAllergyDraft((current) => editAllergyDraft(current, patch));
                }}
                onPickerQuery={(text) =>
                  setAllergyPicker((current) =>
                    current === null ? current : typed(current, text, Date.now()),
                  )
                }
                onPickerSelect={(concept) => {
                  if (allergyPicker === null) return;
                  const next = select(allergyPicker, concept);
                  setAllergyPicker(next);
                  // All three parts of the coding, or none. `setCoding` refuses anything else.
                  setAllergyDraft((current) => setAllergyCoding(current, next.selected));
                }}
                onPickerClear={() => {
                  if (allergyPicker === null) return;
                  setAllergyPicker(clearSelection(allergyPicker));
                  setAllergyDraft((current) => setAllergyCoding(current, null));
                }}
                onPickerRetry={() =>
                  setAllergyPicker((current) =>
                    current === null ? current : retry(current, Date.now()),
                  )
                }
                onRecord={onRecordAllergy}
                onChangeReason={(text) => {
                  setAllergyWrote(false);
                  setAssertReason(text);
                }}
                onAssert={onAssert}
                onStartWithdraw={(target) => {
                  setWithdrawing(target);
                  setWithdrawReason('');
                }}
                onChangeWithdrawReason={setWithdrawReason}
                onWithdraw={onWithdraw}
                onRetry={() => {
                  setAllergyTrouble(null);
                  if (patient !== null) void loadAllergies(patient.id);
                }}
              />
            }
            kinds={kinds}
            relations={relations}
            lifestyle={lifestyle}
            items={items}
            since={since}
            uncoded={uncoded}
            draft={draft}
            draftKind={draftKind}
            picker={picker}
            removingId={removingId}
            removeReason={removeReason}
            busy={busy}
            savedId={savedId}
            justRecorded={justRecorded}
            trouble={trouble}
            onChooseKind={onChooseKind}
            onEditDraft={(patch) => {
              setJustRecorded(false);
              setDraft((current) => editDraft(current, patch));
            }}
            onSetOnset={(onsetOn, precision) =>
              setDraft((current) => setOnset(current, onsetOn, precision))
            }
            onPickerQuery={(text) =>
              setPicker((current) =>
                current === null ? current : typed(current, text, Date.now()),
              )
            }
            onPickerSelect={(concept) => {
              if (picker === null || draftKind === null) return;
              const next = select(picker, concept);
              setPicker(next);
              // The coding, all three parts of it, and only if it belongs to this kind's
              // catalogue. `setCoding` is what refuses the rest.
              setDraft((current) => setCoding(current, draftKind, next.selected));
            }}
            onPickerClear={() => {
              if (picker === null || draftKind === null) return;
              setPicker(clearSelection(picker));
              setDraft((current) => setCoding(current, draftKind, null));
            }}
            onPickerRetry={() =>
              setPicker((current) => (current === null ? current : retry(current, Date.now())))
            }
            onRecord={onRecord}
            onConfirm={onConfirm}
            onSetResolved={onSetResolved}
            onStartRemoving={(itemId) => {
              setRemovingId(itemId);
              setRemoveReason('');
            }}
            onChangeRemoveReason={setRemoveReason}
            onRemove={onRemove}
            onRetry={() => {
              setTrouble(null);
              if (patient !== null) void load(patient.id);
            }}
          />
        )}
      </View>
    </ScreenShell>
  );
}

/**
 * The counselling checklist, at stations 3 and 7 (CP56, §5.3).
 *
 * # Two stations, one checklist, one list on screen
 *
 * A counsellor works `STN_COUNSELING` and a nutritionist works `STN_NUTRITION`, and they walk
 * **the same session**: §5.2's three rooms are rooms in a corridor, not three checklists. So
 * this screen is the same at both stations, and the only thing the station changes is which
 * room heading is marked as the operator's own. Nothing is filtered — the nutritionist seeing
 * that the counselling room already covered diet is exactly what stops them covering it again.
 *
 * # The loading is here and the decisions are not
 *
 * React Query owns the five reads: who is in service, what this visit calls for, what it has
 * been walked through, the chosen session with its frozen list, and what the checkpoint says.
 * Every write answers with the whole session, and that answer is written straight into the cache
 * rather than merged — ticking changes the outstanding list, which comes from the same database
 * function CP57's gate reads, and a screen that patched its own copy would be keeping a second,
 * staler account of what is still missing.
 *
 * There is no sixth read for the room catalogue. `room_station` arrives on a session's item, so
 * the one fact the catalogue was fetched for — that the insulin corner is worked from the
 * counselling station rather than being a station of its own — comes with the session; and the
 * gate's own missing items carry the room's words, so the checkpoint names its rooms too.
 *
 * # This screen offers to start a checklist, and it still does not choose one
 *
 * Which checklist a patient gets is an assignment rule keyed on a **coded condition** (CP55), and
 * the server answers it: `/v1/counseling/visits/{id}/checklists` matches the patient's live coded
 * comorbidities and names both the checklists this visit calls for and any session already open
 * for them. So this screen offers to open **those** and nothing else — there is no template
 * picker and no search, because a counsellor choosing from a catalogue of every checklist in the
 * clinic would be making that clinical assignment by hand.
 *
 * That answer reads with either `counseling.tick` or `counseling.session.read`, so it is asked
 * for every hat that gets this far — a reviewer or a physician's panel has to see what a visit
 * *should* have been walked through to explain why a patient was held, and while it needed the
 * tick permission they got a 403 and an empty screen. Only the control that opens a checklist is
 * a counsellor's. The visit's session index is read beside it for a narrower reason: the
 * checklist answer carries one row per checklist, so a list walked, finished and opened again
 * names only the later session, and `choicesOf` merges what the index adds.
 *
 * # An event id belongs to an attempt, not to a press
 *
 * Every write carries a client-generated event id which is also its idempotency key, and the
 * server answers a repeat of a tick it already stored **successfully** when the id matches. So
 * the id is minted once per attempt and held until that attempt is settled: a retry after a
 * dropped connection re-sends the same one, and a fresh id on the second press would turn this
 * phone's own successful tick into "somebody has already covered this" against itself.
 *
 * # The checkpoint is read here, and the routes back land here (CP57, §5.5)
 *
 * One more read, and only one: what the gate says about this visit. Its missing items carry the
 * room's own words and the checklist's own title beside their codes, so the blocked panel needs
 * no room catalogue and no merge with the checklist answer to draw itself. The gate is
 * invalidated by every write, for the same reason the indexes are — covering an item is the
 * thing that lifts it, and it is the server's answer for the **whole visit**, which this session
 * may not be the only checklist of.
 *
 * The blocked panel's two routes are the two the server distinguishes, and both are already
 * controls this screen owns: resuming is `setChosen`, which puts a half-walked session under the
 * counsellor's thumb, and starting is the same `onStartChecklist` the checklist row uses. That is
 * why the panel is drawn here rather than on a screen of its own — a route back that navigated
 * somewhere else would be a route to a screen that would then have to be told which session.
 *
 * There is no override anywhere on this screen. It needs `counseling.gate.override`, which a
 * station operator does not hold, so the panel names who can and offers nothing.
 */
function CounselingScreen({ station, tabs }: { station: string; tabs?: ReactNode }) {
  const t = useTranslations('anthropometry');
  const locale = usePreferences((state) => state.language);
  const operator = useSession((state) => state.operator);
  const activeRole = useSession((state) => state.activeRole);
  const queryClient = useQueryClient();

  // The two stable references the store hands out, turned into the hat's own grants here
  // rather than in a selector: a selector that built the array would return a new one on every
  // render, and zustand compares by identity.
  const permissions = useMemo(
    () => activePermissions({ operator, activeRole }),
    [operator, activeRole],
  );
  const me = operator?.id ?? '';

  const [chosen, setChosen] = useState<string | null>(null);
  const [noting, setNoting] = useState<string | null>(null);
  const [note, setNote] = useState('');
  const [unticking, setUnticking] = useState<string | null>(null);
  const [untickReason, setUntickReason] = useState('');
  const [busyItem, setBusyItem] = useState<string | null>(null);
  const [starting, setStarting] = useState<string | null>(null);
  const [trouble, setTrouble] = useState<CounselingTrouble | null>(null);
  // The item a refusal was about, so a colleague's tick can be named beside it.
  const [troubleCode, setTroubleCode] = useState('');
  // The event id of every attempt whose fate is still unknown. Emptied as each one settles.
  const [events, setEvents] = useState<HeldEvents>({});

  // There is no `mayTick` here any more, and that absence is the point. It used to decide
  // whether the checklist question was asked at all; both reads accept a hat that may only read
  // counselling now, so gating one on the client's copy of a grant would be this screen refusing
  // itself an answer the server is willing to give — and leaving a reviewer with a blank page.
  // The permissions go to the component, which uses them to choose sentences and controls.

  // The patient in service at this station. The queue owns who that is (CP39); this screen
  // only records what they were told.
  const patient = useQuery({
    queryKey: ['counseling', 'patient', station],
    enabled: station !== '',
    queryFn: async () => {
      const queue = await api.GET('/v1/stations/{station}/queue', {
        params: { path: { station } },
      });
      const inService = queue.data?.entries.find((entry) => entry.status === 'in_service');
      if (inService?.patient_id === undefined) return null;
      const record = await api.GET('/v1/patients/{id}', {
        params: { path: { id: inService.patient_id } },
      });
      if (record.data === undefined) return null;
      return {
        id: record.data.patient.id,
        visitId: inService.visit_id,
        name: record.data.patient.name_bn || record.data.patient.name_en,
      };
    },
  });

  const visitId = patient.data?.visitId ?? '';

  // What this visit calls for, matched from the patient's coded comorbidities, with any session
  // already open named on it. This is how a phone resumes the walk a colleague started instead
  // of opening a second half-ticked copy of the same list.
  //
  // Asked for every hat, not only one that may tick. It reads with `counseling.session.read` as
  // well now, and it is the only answer that names a checklist nobody opened — so a reviewer
  // gated out of it would be looking at a visit whose counselling never happened and reading it
  // as a visit that called for none.
  const checklists = useQuery({
    queryKey: ['counseling', 'checklists', visitId],
    enabled: visitId !== '',
    queryFn: () => listCounselingChecklistsForVisit(visitId),
  });

  // What the visit has actually been walked through, every session there is. The checklist
  // answer collapses to one row per checklist, so this is what names the earlier of two walks
  // down the same list; `choicesOf` merges them and this one only ever adds.
  const sessions = useQuery({
    queryKey: ['counseling', 'sessions', visitId],
    enabled: visitId !== '',
    queryFn: () => listCounselingSessionsForVisit(visitId),
  });

  /*
   * What the checkpoint says about this visit (CP57). Asked here rather than discovered as a
   * refusal at the queue: a counsellor who has just finished should be able to see whether the
   * patient is now clear without trying the write and being told no in front of them.
   *
   * It is the server's answer and it is never assembled on this side. Every write below
   * invalidates it, because covering an item is exactly what changes it — and a screen that
   * patched its own copy of "what is still missing" would be the second implementation the whole
   * of CP56 and CP57 is arranged to avoid.
   */
  const gate = useQuery({
    queryKey: ['counseling', 'gate', visitId],
    enabled: visitId !== '',
    queryFn: () => getCounselingGate(visitId),
  });

  const choices = useMemo(
    () => choicesOf(checklists.data ?? [], sessions.data ?? [], locale),
    [checklists.data, sessions.data, locale],
  );

  /*
   * Which of them is on screen: the operator's own choice, then the one still being walked,
   * then the last one that was started. Never "the first" alone — a visit whose diabetes
   * checklist was finished this morning would open on the finished one.
   */
  const chosenId = useMemo(() => chosen ?? resumeOf(choices), [chosen, choices]);

  // The patient in the room changed. A session chosen for the last one is not a choice about
  // this one, and leaving it would put a stranger's checklist under somebody's thumb.
  useEffect(() => setChosen(null), [visitId]);

  const session = useQuery({
    queryKey: ['counseling', 'session', chosenId],
    enabled: chosenId !== '',
    queryFn: () => getCounselingSession(chosenId),
  });

  /**
   * One write, settled.
   *
   * The answer replaces the cached session wholesale. Withdrawing a tick changes what the gate
   * reads, and the endpoint returns the resulting session for exactly that reason; a caller
   * that patched its own list would be guessing at an answer the database already gave it.
   *
   * The attempt's event id is released here: it existed only so that a retry of an unanswered
   * request would be recognised as the same act, and this request has now been answered.
   */
  const settle = useCallback(
    (after: CounselingSession, key: string) => {
      queryClient.setQueryData(['counseling', 'session', after.id], after);
      // Both indexes carry `completed_at`, so finishing changes them — and starting adds a row.
      void queryClient.invalidateQueries({ queryKey: ['counseling', 'sessions', visitId] });
      void queryClient.invalidateQueries({ queryKey: ['counseling', 'checklists', visitId] });
      // And the checkpoint, because covering an item is the thing that lifts it. Re-read rather
      // than recomputed from the session that has just come back: `blocked` is the server's
      // answer for the whole visit, and this session may not be the only checklist holding it.
      void queryClient.invalidateQueries({ queryKey: ['counseling', 'gate', visitId] });
      setEvents((held) => releaseEvent(held, key));
      setTrouble(null);
      setTroubleCode('');
    },
    [queryClient, visitId],
  );

  /**
   * One write, refused or lost.
   *
   * The server's own code travels into the trouble, because a 409 is four different facts and a
   * code is the only part of a refusal a client may branch on.
   *
   * A refusal forgets the attempt's event id — nothing was written and whatever the counsellor
   * does next is a new act. Anything else keeps it, because a write that may have landed must
   * be retried as itself rather than as a second tick.
   *
   * `readsBack` is the one refusal that fetches: an item a colleague has already covered. The
   * server's own sentence says to open the session again to see who, and this does that instead
   * of asking somebody mid-consultation to press a button. Without it the phone keeps a copy it
   * has just been told is stale — the item still reads as uncovered, the tick control is still
   * live, and the next press meets the same refusal.
   */
  const failed = useCallback(
    (error: unknown, attempt: CounselingAttempt, key: string, code: string) => {
      const trouble = counselingTroubleOf(error, locale, attempt);
      setTrouble(trouble);
      setTroubleCode(code);
      if (!keepsItsEvent(trouble)) setEvents((held) => releaseEvent(held, key));
      // The banner stays up. What the read changes is what is under it: the row now says who
      // covered the item and when, from the record rather than from a guess.
      if (readsBack(trouble)) void session.refetch();
    },
    [locale, session],
  );

  /** The id this attempt goes out with: the one it was first made with, where there is one. */
  const eventOf = useCallback(
    (key: string) => {
      const event = eventFor(events, key, crypto.randomUUID());
      setEvents((held) => holdEvent(held, key, event));
      return event;
    },
    [events],
  );

  // Opening the checklist the server named for this visit. Idempotent there too, so a phone
  // that lost the reply and pressed again lands back in the session it already has.
  const start = useMutation({
    mutationFn: (input: { templateId: string; key: string; event: string }) => {
      const body = toStart(patient.data?.id ?? '', visitId, input.templateId, {
        event: input.event,
      });
      if (body === null) return Promise.reject(new Error('counseling: start refused before send'));
      return startCounselingSession(body);
    },
    onSuccess: (after, input) => {
      settle(after, input.key);
      // Straight into the session that was just opened, rather than waiting for the indexes to
      // come back — the counsellor pressed this with the patient in front of them.
      setChosen(after.id);
    },
    onError: (error, input) => failed(error, 'start', input.key, ''),
    onSettled: () => setStarting(null),
  });

  // One item, one request, one event id — which is also the idempotency key, so a retry over a
  // stuttering clinic link is one tick in the ledger rather than two people claiming to have
  // covered the same item.
  const tick = useMutation({
    mutationFn: (input: { open: CounselingSession; code: string; note: string; event: string }) => {
      const body = toTick(input.open, input.code, input.note, { event: input.event });
      // Refused before it is a request: a closed session, or a code the frozen list does not
      // contain. Null cannot reach here from the screen — those controls are already dead —
      // and a rejected promise is what keeps it from silently becoming a no-op if one ever does.
      if (body === null) return Promise.reject(new Error('counseling: item refused before send'));
      return tickCounselingItem(input.open.id, body);
    },
    onSuccess: (after, input) => {
      settle(after, attemptKey('tick', input.open.id, input.code));
      setNoting(null);
      setNote('');
    },
    onError: (error, input) =>
      failed(error, 'tick', attemptKey('tick', input.open.id, input.code), input.code),
    onSettled: () => setBusyItem(null),
  });

  const untick = useMutation({
    mutationFn: (input: {
      open: CounselingSession;
      code: string;
      reason: string;
      event: string;
    }) => {
      const body = toUntick(input.open, input.code, input.reason, { event: input.event });
      if (body === null) return Promise.reject(new Error('counseling: untick refused before send'));
      return untickCounselingItem(input.open.id, body);
    },
    onSuccess: (after, input) => {
      settle(after, attemptKey('untick', input.open.id, input.code));
      setUnticking(null);
      setUntickReason('');
    },
    onError: (error, input) =>
      failed(error, 'untick', attemptKey('untick', input.open.id, input.code), input.code),
    onSettled: () => setBusyItem(null),
  });

  const finish = useMutation({
    mutationFn: (input: { open: CounselingSession; event: string }) => {
      const body = toCompletion(input.open, { event: input.event });
      if (body === null) return Promise.reject(new Error('counseling: session already finished'));
      return completeCounselingSession(input.open.id, body);
    },
    onSuccess: (after, input) => settle(after, attemptKey('finish', input.open.id, '')),
    onError: (error, input) => failed(error, 'finish', attemptKey('finish', input.open.id, ''), ''),
  });

  const onStartChecklist = useCallback(
    (templateId: string) => {
      const key = attemptKey('start', visitId, templateId);
      setStarting(templateId);
      start.mutate({ templateId, key, event: eventOf(key) });
    },
    [eventOf, start, visitId],
  );

  const onTick = useCallback(
    (code: string) => {
      const open = session.data;
      if (open === undefined) return;
      setBusyItem(code);
      // The note that is open belongs to this item or to nothing. A note typed against one
      // item must never travel with another one's tick.
      tick.mutate({
        open,
        code,
        note: noting === code ? note : '',
        event: eventOf(attemptKey('tick', open.id, code)),
      });
    },
    [eventOf, note, noting, session.data, tick],
  );

  const onUntick = useCallback(
    (code: string) => {
      const open = session.data;
      if (open === undefined) return;
      setBusyItem(code);
      untick.mutate({
        open,
        code,
        reason: untickReason,
        event: eventOf(attemptKey('untick', open.id, code)),
      });
    },
    [eventOf, session.data, untick, untickReason],
  );

  const onFinish = useCallback(() => {
    const open = session.data;
    if (open === undefined) return;
    finish.mutate({ open, event: eventOf(attemptKey('finish', open.id, '')) });
  }, [eventOf, finish, session.data]);

  // What the refusal for a stale item code asks for, and the only thing that fixes it: read
  // the session again. The list this phone is holding is the one that is wrong.
  const onReload = useCallback(() => {
    setTrouble(null);
    setTroubleCode('');
    void checklists.refetch();
    void sessions.refetch();
    void session.refetch();
    void gate.refetch();
  }, [checklists, gate, session, sessions]);

  const onOpenNote = useCallback((code: string | null) => {
    setNoting(code);
    setNote('');
  }, []);

  const onStartUntick = useCallback((code: string | null) => {
    setUnticking(code);
    setUntickReason('');
  }, []);

  // The title is the station's, not the checklist's: a nutritionist is at nutrition even
  // though the list they are walking says "counselling" at the top of it.
  return (
    <ScreenShell titleKey={station === 'STN_NUTRITION' ? 'screen.nutrition' : 'screen.counseling'}>
      {/* Station 3's switch, when there is one. Rendered here, inside the shell, for the reason
          station 5's is: a control above the safe-area header would sit outside the frame every
          other screen in the app shares. */}
      {tabs}
      {patient.data === null || patient.data === undefined ? (
        <AppText>{t('noPatient')}</AppText>
      ) : (
        <CounselingStation
          session={session.data ?? null}
          choices={choices}
          /* CP57's checkpoint, drawn last on the list. The two routes back are the two the
             server distinguishes: into the session somebody left half-walked, and into opening
             a checklist nobody has. Both land on this screen — `setChosen` puts the session
             under the counsellor's thumb, and `onStartChecklist` opens the one that was never
             started — which is why the panel lives here rather than on a screen of its own. */
          gate={
            <CounselingGateStep
              gate={gate.data ?? null}
              permissions={permissions}
              /* A disabled query reports as pending, so a visit this screen has no id for would
                 say "reading the checkpoint…" for ever instead of saying plainly that it does
                 not know — which on a checkpoint is the one silence that must not be mistaken
                 for "nothing is holding this patient". */
              loading={visitId !== '' && gate.isPending}
              starting={starting}
              onResume={setChosen}
              onStart={onStartChecklist}
            />
          }
          /* React Query reports a *disabled* query as pending, so the session read counts only
             once there is a session to read. Without that, a visit with no checklist would say
             "reading the checklist…" for ever instead of saying plainly that nobody has opened
             one. Both index reads are asked for every hat now, so neither needs the guard. */
          loading={
            sessions.isPending || checklists.isPending || (chosenId !== '' && session.isPending)
          }
          patientName={patient.data.name}
          station={station}
          me={me}
          permissions={permissions}
          noting={noting}
          note={note}
          unticking={unticking}
          untickReason={untickReason}
          busyItem={busyItem}
          starting={starting}
          finishing={finish.isPending}
          trouble={trouble}
          troubleCode={troubleCode}
          onChooseSession={setChosen}
          onStartChecklist={onStartChecklist}
          onTick={onTick}
          onOpenNote={onOpenNote}
          onChangeNote={setNote}
          onStartUntick={onStartUntick}
          onChangeUntickReason={setUntickReason}
          onUntick={onUntick}
          onFinish={onFinish}
          onReload={onReload}
        />
      )}
    </ScreenShell>
  );
}

/**
 * Station 3's lifestyle assessment (CP58, §3 step 3, §12).
 *
 * The same shape as every other capture screen on this file and deliberately so: one patient
 * from the queue, one fetch of the reference data at the start of the session, one write per
 * act. An operator who switches hats mid-morning should not have to learn a second set of
 * habits.
 *
 * # Two writes, because they are two facts
 *
 * A questionnaire goes to `POST /v1/assessments`; the four plain numbers go through
 * `/v1/observations/batch`, the path every station value uses. They are not one save with one
 * button, and joining them would be wrong twice over: an operator who has only the AUDIT-C
 * answers should be able to record them without inventing a sleep figure, and a count of
 * cigarettes belongs in `read.observation` where the correction cascade, the plausibility
 * bands and the research extract can see it.
 *
 * # The catalogue is fetched once and then the morning is offline
 *
 * `GET /v1/assessments/instruments` returns every question and option in one response for
 * exactly this reason (ADR-0004): a questionnaire that needed a round trip per item stalls
 * halfway through with a patient in the chair, and the clinic's link drops for seconds at a
 * time.
 *
 * # The score is the server's, in every direction
 *
 * The figure is read off the patient's current values — a `LIFESTYLE_RISK` observation is an
 * ordinary derived value — or, after a write, is whatever that write produced, including a
 * **null**. The account beside it — which domains are assessed, which are missing, and the floor
 * — comes from `GET /v1/patients/{id}/lifestyle-scoring`, which computes and stores nothing and
 * is therefore asked on arrival and after every save. Nothing here computes a score, predicts
 * one, or knows which observation feeds which domain.
 *
 * `POST /v1/assessments/score` is the other one, and it **writes**: it is asked after the four
 * numbers save, because that save is what most often makes a score possible, and never on
 * arrival, because a derived observation appended every time somebody opens a tab is ledger
 * noise nobody asked for.
 */
function LifestyleScreen({ station, tabs }: { station: string; tabs?: ReactNode }) {
  const t = useTranslations('lifestyle');
  const locale = usePreferences((state) => state.language);
  const critical = useCriticalAlerts();

  const [patient, setPatient] = useState<{
    id: string;
    name: string;
    visitId: string;
    sex: 'male' | 'female' | 'other';
    ageYears: number;
  } | null>(null);
  const [catalogue, setCatalogue] = useState<Catalogue>({ instruments: [], version: '' });
  const [loadingCatalogue, setLoadingCatalogue] = useState(true);
  // The catalogue in hand has been overtaken by a republished version. Held rather than acted
  // on: reloading under a counsellor's thumb would swap a questionnaire mid-conversation.
  const [catalogueMoved, setCatalogueMoved] = useState(false);
  const [openCode, setOpenCode] = useState('');
  const [run, setRun] = useState<RunState>({});
  const [numbers, setNumbers] = useState<NumbersForm>(emptyNumbers);
  const [confirmed, setConfirmed] = useState<LifestyleConfirmations>({});
  const [rules, setRules] = useState<PlausibilityRule[]>([]);
  const [observations, setObservations] = useState<LifestyleObservation[]>([]);
  // The response the last write stored, kept apart from the record: the record answers "what
  // does this patient's chart hold", and this answers "what did the thing I just pressed do".
  const [written, setWritten] = useState<InstrumentResponse | null>(null);
  /*
   * The score a write produced this session.
   *
   * `undefined` while there has been no write, and **null when a write produced no score** —
   * which is why it is not simply `Observation | null`. A null a write has just sent is a fact
   * about the assessment just recorded, and filling that space from the chart would put an
   * older score under a questionnaire that did not produce it.
   */
  const [freshScore, setFreshScore] = useState<LifestyleObservation | null | undefined>(undefined);
  /*
   * The server's account of the composite: which domains it has, which it wants, and the floor.
   *
   * Read on arrival, because there is a read that computes it and stores nothing. An operator
   * sitting down beside a patient needs the missing list before they have done anything — it is
   * what tells them which of the four things to ask about — and the writing endpoint could not
   * answer that without appending a derived observation every time somebody opened a tab.
   */
  const [scoring, setScoring] = useState<LifestyleScoring | null>(null);
  const [busy, setBusy] = useState(false);
  const [savedNumbers, setSavedNumbers] = useState(false);
  const [trouble, setTrouble] = useState<LifestyleTrouble | null>(null);

  // The patient in service at this station. The queue owns who that is (CP39); this screen
  // only asks them the questions.
  const loadPatient = useCallback(async () => {
    const queue = await api.GET('/v1/stations/{station}/queue', {
      params: { path: { station } },
    });
    const inService = queue.data?.entries.find((entry) => entry.status === 'in_service');
    if (inService?.patient_id === undefined) return null;
    const record = await api.GET('/v1/patients/{id}', {
      params: { path: { id: inService.patient_id } },
    });
    if (record.data === undefined) return null;
    return {
      id: record.data.patient.id,
      name: record.data.patient.name_bn || record.data.patient.name_en,
      visitId: inService.visit_id ?? '',
      sex: record.data.patient.sex as 'male' | 'female' | 'other',
      ageYears: record.data.patient.birth.age,
    };
  }, [station]);

  /*
   * What the record already holds, in one read.
   *
   * Every current value rather than one category: the composite is DERIVED, the pack-years is
   * DERIVED, and the three numbers it is computed from are SCREENING — and the observations
   * endpoint filters by a single category, so asking per category would be three requests to
   * answer one question.
   *
   * The patient's previous questionnaire responses are deliberately **not** fetched. They were,
   * while this screen had to work out for itself which domains the record could answer; the
   * server says that now, and a read kept for a question nothing asks any more is a read that
   * goes stale without anybody noticing.
   *
   * Beside them, where the patient stands — the domains assessed, the ones missing and the
   * floor. A read rather than the recompute, because this runs on arrival and after every save
   * and the recompute writes.
   */
  const loadRecord = useCallback(async (patientId: string) => {
    const [values, account] = await Promise.all([
      api.GET('/v1/patients/{id}/observations', { params: { path: { id: patientId } } }),
      // Computed and not stored, so it is safe to ask on arrival and after every save. Its
      // `score` is null by construction; the figure is the `LIFESTYLE_RISK` above.
      getLifestyleScoring(patientId).catch(() => null),
    ]);
    setObservations((values.data?.observations ?? []) as LifestyleObservation[]);
    // Only when it answered. A failed read leaves whatever account the screen already had
    // rather than blanking a list the operator is working from.
    if (account !== null) setScoring(account);
  }, []);

  useEffect(() => {
    let live = true;
    if (station === '') return;
    void (async () => {
      const found = await loadPatient();
      if (!live || found === null) return;
      setPatient(found);
      await loadRecord(found.id);
    })();
    return () => {
      live = false;
    };
  }, [loadPatient, loadRecord, station]);

  // The catalogue, once. A tablet fetches it on arrival and then runs questionnaires for the
  // rest of the clinic session without asking again.
  const loadCatalogue = useCallback(async () => {
    setLoadingCatalogue(true);
    try {
      setCatalogue(await listInstruments());
      setCatalogueMoved(false);
      setTrouble(null);
    } catch (error) {
      setTrouble(lifestyleTroubleOf(error, locale, 'catalogue'));
    } finally {
      setLoadingCatalogue(false);
    }
  }, [locale]);

  useEffect(() => {
    void loadCatalogue();
  }, [loadCatalogue]);

  /*
   * The cheap re-check, once per patient (CP58's `catalogue_version`).
   *
   * A patient being called is the one moment nothing is half-answered, which makes it the only
   * safe moment to find out the wording has moved. Asked with `version_only`, so it costs one
   * timestamp rather than five instruments' names, purposes and licence notes — and it only
   * *reports*: the reload is a control the counsellor presses, because swapping a questionnaire
   * under somebody mid-conversation is worse than the stale copy it fixes.
   *
   * Without it the only signal is a 422 on an item code at submit, after a patient has been
   * asked every question on a form the record no longer accepts.
   */
  const heldVersion = catalogue.version;
  useEffect(() => {
    let live = true;
    if (patient === null || heldVersion === '') return;
    void readCatalogueVersion()
      .then((latest) => {
        if (live && movedOn(heldVersion, latest)) setCatalogueMoved(true);
      })
      // A failed re-check is not a failed anything: the copy in hand still works, and a banner
      // raised because one small request did not answer would train people to ignore it.
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [heldVersion, patient]);

  // The plausibility bands, once, for the same reason: the warning has to arrive while the
  // patient can still be asked the question again (CP46).
  useEffect(() => {
    let live = true;
    void api.GET('/v1/observations/plausibility').then((result) => {
      if (live && result.data !== undefined) setRules(result.data.rules as PlausibilityRule[]);
    });
    return () => {
      live = false;
    };
  }, []);

  const rows = useMemo(() => catalogueRows(catalogue.instruments, locale), [catalogue, locale]);
  const open = useMemo(
    () => instrumentNamed(catalogue.instruments, openCode),
    [catalogue, openCode],
  );
  const openRow = useMemo(
    () => rows.find((row) => row.code === openCode) ?? null,
    [rows, openCode],
  );

  const subject = useMemo(
    () => ({ sex: patient?.sex ?? 'other', ageYears: patient?.ageYears ?? 0 }),
    [patient],
  );
  const warnings = useMemo(
    () => warningsForNumbers(numbers, rules, subject, confirmed),
    [numbers, rules, subject, confirmed],
  );

  /*
   * The score, and which of the three it is.
   *
   * `scorePanel` prefers the server's own account whenever a write this session produced one —
   * including a null score, which is a fact about the assessment just recorded rather than a gap
   * to fill from the record. Reading the chart in place of a null the server has just sent would
   * put an older score under a questionnaire that did not produce it.
   */
  const score = useMemo(
    () => scorePanel(scoring, freshScore, scoreOnRecord(observations)),
    [scoring, freshScore, observations],
  );
  // The row the figure came off, for CP61's attribution: a score a write just produced, or the
  // one the chart already held.
  const scoreRow = useMemo(
    () => freshScore ?? scoreOnRecord(observations),
    [freshScore, observations],
  );
  const packYears = useMemo(() => packYearsOnRecord(observations), [observations]);

  const onOpen = useCallback(
    (code: string) => {
      const instrument = instrumentNamed(catalogue.instruments, code);
      if (instrument === null) return;
      setOpenCode(code);
      setRun(emptyRun(instrument));
      setWritten(null);
      // Back to "whatever the chart holds" until this run produces its own answer. The domain
      // account stays: it is about the patient, not about the last thing pressed.
      setFreshScore(undefined);
      setTrouble(null);
    },
    [catalogue],
  );

  const onClose = useCallback(() => {
    setOpenCode('');
    setRun({});
  }, []);

  const onSubmit = useCallback(async () => {
    if (open === null || patient === null) return;
    const body = toAssessment(open, run, {
      event: crypto.randomUUID(),
      patient: patient.id,
      visit: patient.visitId,
    });
    // Null means the run is not a request yet — a required item blank, a number outside the
    // item's own range, or a questionnaire this clinic may not run. The screen has already
    // said which; sending it anyway would only turn that into a 422 the operator meets after
    // the patient has stood up.
    if (body === null) return;
    setBusy(true);
    try {
      const result = await recordAssessment(body);
      setWritten(result.response);
      // Both halves of the write's answer. `score` may be null and that is a fact about this
      // assessment; `assessed`, `missing` and `minimum` beside it are what the card reads out
      // instead of a number.
      setFreshScore(result.scoring.score);
      setScoring(result.scoring);
      setTrouble(null);
      setOpenCode('');
      setRun({});
      await loadRecord(patient.id);
    } catch (error) {
      setTrouble(lifestyleTroubleOf(error, locale, 'assessment'));
    } finally {
      setBusy(false);
    }
  }, [loadRecord, locale, open, patient, run]);

  const onChangeNumber = useCallback((key: LifestyleFieldKey, text: string) => {
    setSavedNumbers(false);
    // A changed number is a different number, so an earlier confirmation no longer applies.
    // Carrying it over would let an operator confirm 80 a day, retype 8, and save it.
    setConfirmed((current) => ({ ...current, [key]: false }));
    setNumbers((current) => ({ ...current, [key]: { ...current[key], text } }));
  }, []);

  const onChangeUnit = useCallback((key: LifestyleFieldKey, unit: string) => {
    setSavedNumbers(false);
    // The typed number stays. An operator who realises they have been given minutes after
    // typing 8 means eight minutes; re-converting it to 480 would be the app deciding they
    // meant something they did not type.
    setConfirmed((current) => ({ ...current, [key]: false }));
    setNumbers((current) => ({ ...current, [key]: { ...current[key], unit } }));
  }, []);

  const onSaveNumbers = useCallback(async () => {
    if (patient === null || numbersEmpty(numbers)) return;
    const perField = new Map<LifestyleFieldKey, string>();
    const body = toNumbersBatch(numbers, {
      batch: crypto.randomUUID(),
      patient: patient.id,
      visit: patient.visitId,
      perField: (key) => {
        const existing = perField.get(key);
        if (existing !== undefined) return existing;
        const id = crypto.randomUUID();
        perField.set(key, id);
        return id;
      },
      confirmed: (key) => confirmed[key] === true,
    });
    if (body === null) return;
    setBusy(true);
    try {
      const result = await recordLifestyleNumbers(body);
      // Before anything else on this path. A critical value has to reach the operator's eyes
      // and ears in the same instant the save returns, not after a second read of the record.
      critical.raise(result.alerts);
      setSavedNumbers(true);
      setNumbers(emptyNumbers());
      setConfirmed({});
      setTrouble(null);
      /*
       * And then ask for the composite, because this is the save that most often makes one
       * possible. "Type the four numbers, save" can take a patient from two assessed domains to
       * four without a questionnaire being answered at all, and until this call existed the
       * score stayed stale exactly when the operator had just finished making it computable.
       *
       * Its own event id, which is also its idempotency key: a retry of this save re-sends it
       * unchanged rather than appending a second identical score.
       */
      const recomputed = await scoreLifestyle(patient.id, crypto.randomUUID(), patient.visitId);
      setFreshScore(recomputed.score);
      setScoring(recomputed);
      await loadRecord(patient.id);
    } catch (error) {
      setTrouble(lifestyleTroubleOf(error, locale, 'numbers'));
    } finally {
      setBusy(false);
    }
  }, [confirmed, critical, loadRecord, locale, numbers, patient]);

  return (
    <ScreenShell titleKey="screen.lifestyle">
      {tabs}
      {patient === null ? (
        <AppText>{t('noPatient')}</AppText>
      ) : (
        <LifestyleStation
          patientName={patient.name}
          rows={rows}
          catalogueMoved={catalogueMoved}
          running={openRow}
          run={run}
          progress={open === null ? null : progressOf(open, run)}
          outstanding={open === null ? [] : outstandingOf(open, run)}
          numberProblems={open === null ? {} : lifestyleNumberProblems(open, run)}
          numbers={numbers}
          numberWarnings={warnings}
          packYears={packYears}
          score={score}
          scoreRow={scoreRow}
          lastResponse={written}
          loading={loadingCatalogue}
          busy={busy || hasBlockingNumber(warnings)}
          savedNumbers={savedNumbers}
          trouble={trouble}
          onOpen={onOpen}
          onClose={onClose}
          onChooseOption={(item, option) =>
            setRun((current) => chooseOption(current, item, option))
          }
          onTypeNumber={(item, text) => setRun((current) => typeNumber(current, item, text))}
          onAnswerYesNo={(item, value) => setRun((current) => answerYesNo(current, item, value))}
          onSubmit={() => void onSubmit()}
          onChangeNumber={onChangeNumber}
          onChangeUnit={onChangeUnit}
          onConfirmNumber={(key) => setConfirmed((current) => ({ ...current, [key]: true }))}
          onSaveNumbers={() => void onSaveNumbers()}
          onReload={() => void loadCatalogue()}
        />
      )}
      {/* Over everything. A critical value is the one thing on this screen that cannot wait
          for the operator to finish reading something else. */}
      <CriticalAlertModal alerts={critical.alerts} seen={critical.seen} onSeen={critical.dismiss} />
    </ScreenShell>
  );
}

/**
 * Station 7's 24-hour dietary recall (CP59, blueprint §5.2, §12.1, [R-01]).
 *
 * The sixth capture screen in this file and the same shape as the others: one patient from the
 * queue, reference data on arrival, and a write per action. Three things are particular to it,
 * and all three come from the fact that **a second assistant may be recording the same recall
 * from another tablet at the same time**.
 *
 * **Every write replaces the whole day.** `POST /v1/diet` answers with the entire recall, not
 * the entry it wrote, so the operator who just added rice sees whatever their colleague added
 * while they were typing. That is the duplicate-prevention mechanism, and it is why nothing
 * here appends to a local list.
 *
 * **The day is re-read on a timer as well.** An operator who has not written anything for two
 * minutes has been shown nothing for two minutes, and that is exactly the operator about to
 * record the rice somebody else already entered. The timed read is quiet: it replaces the day
 * and never the half-filled form, and a failed one raises nothing, because a banner produced by
 * a poll is a banner people learn to ignore.
 *
 * **The recall date is the server's and is sent back on every write.** The first read asks with
 * no date at all so that the endpoint applies its own default — yesterday — and every write
 * afterwards names the date that answer carried. Nothing on this side computes which day is
 * being recalled; what it does is check it, because the server's "yesterday" is computed in UTC
 * and this clinic runs six hours ahead of that.
 */
function NutritionScreen({ station, tabs }: { station: string; tabs?: ReactNode }) {
  const t = useTranslations('nutrition');
  const locale = usePreferences((state) => state.language);

  const [patient, setPatient] = useState<{ id: string; name: string; visitId: string } | null>(
    null,
  );
  /*
   * The station's vocabulary and the clinic's calendar, fetched once.
   *
   * The measures, the meals **with their names**, the two quantity ceilings, and the day the
   * server thinks a recall taken now is about. Every one of those was invented on this side for
   * one checkpoint, and the two dates were the dangerous pair: a tablet's clock is whatever
   * somebody set it to.
   */
  const [reference, setReference] = useState<Reference>(emptyReference);
  const [picker, setPicker] = useState<FoodPickerState>(openFoodPicker);
  const [draft, setDraft] = useState<EntryDraft>(emptyEntryDraft);
  /*
   * The chosen food, held apart from the results.
   *
   * Deriving it from the current result list looked simpler and was wrong: the operator types a
   * new query while a food is chosen, the row leaves the list, and the measures underneath it
   * vanish with the entry half built. What is chosen is chosen until something else is.
   */
  const [chosenFood, setChosenFood] = useState<FoodRow | null>(null);
  const [recall, setRecall] = useState<DietRecall | null>(null);
  /*
   * The day being recalled — empty until the server has answered once.
   *
   * There is deliberately no default here. `recall_date` defaults to yesterday server-side, and
   * a client that computed that default would be a client asserting the clinic's calendar from
   * a tablet whose time zone nobody has checked.
   */
  const [day, setDay] = useState('');
  /** Which days this patient already has a recall for, with their counts. */
  const [days, setDays] = useState<RecallDay[]>([]);
  /*
   * The entry this tablet's last write produced, derived from the event id it sent.
   *
   * Cleared when the operator moves the day, because a row highlighted on Tuesday means nothing
   * on Monday's list.
   */
  const [justAdded, setJustAdded] = useState('');
  const [withdrawal, setWithdrawal] = useState<WithdrawalDraft | null>(null);
  const [loadingRecall, setLoadingRecall] = useState(true);
  const [busy, setBusy] = useState(false);
  const [trouble, setTrouble] = useState<NutritionTrouble | null>(null);
  const [nudge, setNudge] = useState(0);
  /*
   * The clinic's own today, from whichever answer spoke last.
   *
   * Seeded from the reference payload for the first render and replaced by **every** recall
   * answer — the read, the write and the withdrawal all carry one. The reference is fetched once
   * and held for a whole clinic session, so a session crossing midnight would otherwise be
   * checking a date against yesterday's idea of today and would quietly stop warning about a
   * recall dated in the future.
   */
  const [clinicToday, setClinicToday] = useState('');

  /*
   * The day on screen: the one the recall answered with, or the clinic's own default until it
   * has. Both are the server's — nothing here computes a date, and the tablet's clock is never
   * read. It was, for one checkpoint, and the server's default was a UTC yesterday, which in
   * Faridpur between midnight and six is the day before the one anybody means.
   */
  const shownDate = day === '' ? reference.recallDateDefault : day;

  // The patient in service at this station. The queue owns who that is (CP39); this screen only
  // asks them what they ate.
  const loadPatient = useCallback(async () => {
    const queue = await api.GET('/v1/stations/{station}/queue', {
      params: { path: { station } },
    });
    const inService = queue.data?.entries.find((entry) => entry.status === 'in_service');
    if (inService?.patient_id === undefined) return null;
    const record = await api.GET('/v1/patients/{id}', {
      params: { path: { id: inService.patient_id } },
    });
    if (record.data === undefined) return null;
    return {
      id: record.data.patient.id,
      name: record.data.patient.name_bn || record.data.patient.name_en,
      visitId: inService.visit_id ?? '',
    };
  }, [station]);

  /**
   * One day's eating, as the server holds it.
   *
   * `quiet` is the timed re-read: it replaces the day on success and does nothing at all on
   * failure. A poll that raised a banner every time the clinic's link dropped for its usual few
   * seconds (ADR-0004) would teach the operator to ignore the banner that matters.
   */
  const readDay = useCallback(
    async (patientId: string, date: string, quiet = false) => {
      if (!quiet) setLoadingRecall(true);
      try {
        const answered = await getRecall(patientId, date);
        setRecall(answered.recall);
        // The date the server answered with, always — including the one it chose itself.
        setDay(answered.recall.recall_date);
        // And the clinic's own today, off this answer rather than off the reference this tablet
        // fetched at the start of the morning.
        setClinicToday(answered.clinicToday);
        if (!quiet) setTrouble(null);
      } catch (error) {
        if (!quiet) setTrouble(nutritionTroubleOf(error, locale, 'recall'));
      } finally {
        if (!quiet) setLoadingRecall(false);
      }
    },
    [locale],
  );

  /**
   * Which days this patient has anything on, with the counts.
   *
   * Read on arrival and after every write, because the day the operator is working on may not
   * have been on the list a moment ago. Failures are silent: the list is a way to *reach*
   * another day, and a banner about a navigation aid would sit over the recall itself.
   */
  const readDays = useCallback(async (patientId: string) => {
    try {
      setDays(await listRecallDays(patientId));
    } catch {
      // Nothing. The step controls still move the day.
    }
  }, []);

  useEffect(() => {
    let live = true;
    if (station === '') return;
    void (async () => {
      const found = await loadPatient();
      if (!live || found === null) return;
      setPatient(found);
      // No date on the first read. The server's default is the whole point of it.
      await readDay(found.id, '');
      await readDays(found.id);
    })();
    return () => {
      live = false;
    };
  }, [loadPatient, readDay, readDays, station]);

  // The measures and the meals, once per clinic session. Reference data with no patient in it.
  useEffect(() => {
    let live = true;
    void listReference()
      .then((answered) => {
        if (!live) return;
        setReference(answered);
        // Only until a recall answers. A recall's own is the fresher of the two, always.
        setClinicToday((current) => (current === '' ? answered.clinicToday : current));
      })
      .catch((error) => {
        if (live) setTrouble(nutritionTroubleOf(error, locale, 'measures'));
      });
    return () => {
      live = false;
    };
  }, [locale]);

  /*
   * The picker's clock.
   *
   * `state.ts` decides everything — when a request is due, and which answer may replace what is
   * on screen — and this only turns the handle. Its own copy rather than station 4's
   * `usePickerClock`, which is typed to the terminology picker: two pickers searching two
   * catalogues should not have to agree about anything but the shape of the rule.
   */
  useEffect(() => {
    if (picker.issuedQuery !== null && picker.query === picker.issuedQuery) return;
    if (!searchDue(picker, Date.now())) {
      const timer = setTimeout(() => setNudge((n) => n + 1), FOOD_DEBOUNCE_MS);
      return () => clearTimeout(timer);
    }
    let live = true;
    const next = issueSearch(picker, Date.now());
    setPicker(next.state);
    void runFoodSearch(next.request, locale).then((answer) => {
      // Through the one door, so a slow answer cannot land over a newer list.
      if (live) setPicker((current) => applyAnswer(current, answer));
    });
    return () => {
      live = false;
    };
  }, [picker, nudge, locale]);

  // The other tablet's entries, without this one having to write something to see them.
  useEffect(() => {
    if (patient === null || shownDate === '') return;
    const timer = setInterval(() => {
      void readDay(patient.id, shownDate, true);
    }, RECALL_REFRESH_MS);
    return () => clearInterval(timer);
  }, [shownDate, patient, readDay]);

  const rows = useMemo(
    () => foodRows(picker.foods, reference.measures, locale),
    [picker.foods, reference.measures, locale],
  );
  // Recomputed against the measures currently in hand rather than read off the row that was
  // tapped: the measures list may have arrived after the food was chosen, and grams appearing
  // late is better than grams never appearing.
  const choices = useMemo(
    () => measuresFor(chosenFood?.food ?? null, reference.measures, locale),
    [chosenFood, reference.measures, locale],
  );
  // The most of the chosen measure one entry may carry, off the measure's own row.
  const ceiling = useMemo(
    () => ceilingFor(measureNamed(choices, draft.measureCode)),
    [choices, draft.measureCode],
  );
  const missing = useMemo(() => missingFrom(draft, choices), [draft, choices]);
  const groups = useMemo(
    () => mealGroups(recall, reference.meals, locale),
    [recall, reference.meals, locale],
  );
  const totals = useMemo(() => totalsOf(recall), [recall]);
  const relation = useMemo(() => dayRelation(shownDate, clinicToday), [shownDate, clinicToday]);
  const unapprovedFoods = useMemo(() => unapproved(picker.foods), [picker.foods]);

  const onAdd = useCallback(async () => {
    if (patient === null) return;
    const event = crypto.randomUUID();
    const body = toEntry(draft, choices, {
      event,
      patient: patient.id,
      visit: patient.visitId,
      recallDate: shownDate,
    });
    // Null means the draft is not a request yet, and the form has already said which part is
    // missing. Sending it anyway would turn that into a 422 the operator meets mid-sentence.
    if (body === null) return;
    setBusy(true);
    try {
      const answered = await recordDietEntry(body);
      // The whole day, including whatever the other tablet added while this one was typing.
      setRecall(answered.recall);
      setDay(answered.recall.recall_date);
      setClinicToday(answered.clinicToday);
      // Which row of it is this one. The server derives the entry id from the event id, so the
      // operator can find what they just added in a list grouped by meal — and tell it from the
      // other tablet's identical rice recorded a second earlier.
      setJustAdded(entryIdFor(event));
      // The meal stays; the food and the count do not. See `afterEntry`.
      setDraft(afterEntry(draft));
      setChosenFood(null);
      // And the box empties, so the next food starts from a clean list rather than from three
      // letters the operator has to delete first.
      setPicker((current) => typedQuery(current, '', Date.now()));
      setTrouble(null);
      // The day may not have been on the list before this write.
      await readDays(patient.id);
    } catch (error) {
      setTrouble(nutritionTroubleOf(error, locale, 'entry'));
    } finally {
      setBusy(false);
    }
  }, [choices, shownDate, draft, locale, patient, readDays]);

  const onWithdraw = useCallback(async () => {
    if (patient === null || withdrawal === null) return;
    const request = toDietWithdrawal(withdrawal, crypto.randomUUID());
    if (request === null) return;
    setBusy(true);
    try {
      const answered = await withdrawDietEntry(request.entryId, request.body);
      setRecall(answered.recall);
      setDay(answered.recall.recall_date);
      setClinicToday(answered.clinicToday);
      setWithdrawal(null);
      setTrouble(null);
    } catch (error) {
      const failure = nutritionTroubleOf(error, locale, 'withdrawal');
      setTrouble(failure);
      /*
       * Both assistants noticed the same duplicate and both pressed. The entry is already back,
       * which is what each of them wanted, so the form closes and the day is read again — a
       * dead control left open over an entry that no longer stands is worse than the 409.
       */
      if (alreadyWithdrawn(failure)) {
        setWithdrawal(null);
        await readDay(patient.id, shownDate, true);
      }
    } finally {
      setBusy(false);
    }
  }, [shownDate, locale, patient, readDay, withdrawal]);

  /**
   * Move to another day.
   *
   * The only date arithmetic left on this screen, and it does not decide *which* day a recall is
   * about — the server does that, and `shownDate` starts from its answer. This moves the day
   * already on screen to the one beside it, which has no endpoint of its own.
   */
  const onChooseDay = useCallback(
    (date: string) => {
      if (patient === null || date === '') return;
      setDay(date);
      // A withdrawal half-written against yesterday's entry means nothing on another day, and a
      // row highlighted as "you added this" is not on this list at all.
      setWithdrawal(null);
      setJustAdded('');
      void readDay(patient.id, date);
    },
    [patient, readDay],
  );

  const onStepDay = useCallback(
    (step: number) => {
      if (shownDate === '') return;
      onChooseDay(shiftDay(shownDate, step));
    },
    [onChooseDay, shownDate],
  );

  return (
    <ScreenShell titleKey="screen.nutrition">
      {tabs}
      {patient === null ? (
        <AppText>{t('noPatient')}</AppText>
      ) : (
        <NutritionStation
          patientName={patient.name}
          recallDate={shownDate}
          relation={relation}
          totals={totals}
          unapprovedFoods={unapprovedFoods}
          picker={picker}
          rows={rows}
          draft={draft}
          meals={reference.meals}
          choices={choices}
          ceiling={ceiling}
          missing={missing}
          chosenFood={chosenFood}
          groups={groups}
          days={days}
          justAdded={justAdded}
          withdrawal={withdrawal}
          loadingRecall={loadingRecall}
          busy={busy}
          trouble={trouble}
          onStepDay={onStepDay}
          onChooseDay={onChooseDay}
          onRefresh={() => void readDay(patient.id, shownDate)}
          onTypeQuery={(text) => setPicker((current) => typedQuery(current, text, Date.now()))}
          onRetrySearch={() => setPicker((current) => retryFoodSearch(current, Date.now()))}
          onChooseMeal={(meal) => setDraft((current) => chooseMeal(current, meal))}
          onChooseFood={(row) => {
            setChosenFood(row);
            setDraft((current) => chooseFood(current, row));
          }}
          onChooseMeasure={(code) => setDraft((current) => chooseMeasure(current, code))}
          onTypeQuantity={(text) => setDraft((current) => typeQuantity(current, text))}
          onAdd={() => void onAdd()}
          onOpenWithdrawal={(entryId) => setWithdrawal(openWithdrawal(entryId))}
          onTypeReason={(text) =>
            setWithdrawal((current) => (current === null ? current : typeReason(current, text)))
          }
          onWithdraw={() => void onWithdraw()}
          onCancelWithdrawal={() => setWithdrawal(null)}
        />
      )}
    </ScreenShell>
  );
}

/** Station 7's own permission (§4.4). A recall is nutrition data. */
const PERM_WRITE_NUTRITION = 'observation.write.nutrition';

/**
 * Station 7 has two forms, and this is how the nutritionist moves between them (CP56, CP59).
 *
 * The same arrangement stations 3 and 5 have, for the same reason. §5.2's rooms are rooms in a
 * corridor: the nutritionist walks the same counselling checklist the counsellor does — a
 * nutritionist who could not see what the counselling room covered would cover it again — and
 * takes the 24-hour recall, which is station 7's own work. Both are done by whoever is sitting
 * with the patient, so this is a view rather than a routing decision.
 *
 * The checklist opens first because the queue's gate reads it (CP57) and the recall can be
 * picked up whenever the patient is ready to talk about food.
 *
 * The recall appears only for a hat that holds `observation.write.nutrition`. A tab that
 * answered 403 in front of a patient would be a control that teaches an operator the app is
 * broken.
 */
function Station7({ station }: { station: string }) {
  const operator = useSession((state) => state.operator);
  const activeRole = useSession((state) => state.activeRole);
  const permissions = useMemo(
    () => activePermissions({ operator, activeRole }),
    [operator, activeRole],
  );
  const [form, setForm] = useState<'counseling' | 'nutrition'>('counseling');

  if (!permissions.includes(PERM_WRITE_NUTRITION)) return <CounselingScreen station={station} />;

  const tabs = (
    <StationTabs
      prefix="station7"
      value={form}
      options={['counseling', 'nutrition'] as const}
      onChange={setForm}
    />
  );
  return form === 'counseling' ? (
    <CounselingScreen station={station} tabs={tabs} />
  ) : (
    <NutritionScreen station={station} tabs={tabs} />
  );
}

/**
 * Station 3 has two forms, and this is how the operator moves between them (CP56, CP58).
 *
 * The same arrangement station 5 has, for the same reason and with the same rule: the station
 * is not a choice, and what is chosen here is which of station 3's own forms is open. §5.2's
 * room is "Counseling & Lifestyle" — one counsellor, one patient, a checklist to walk and a
 * questionnaire to run — and splitting that across two roles would mean the two halves of one
 * conversation could not be recorded by whoever is sitting in the room.
 *
 * The checklist opens first because it is the longer act and the one the queue's gate reads
 * (CP57); the assessment is the shorter and can be picked up whenever the patient is ready.
 *
 * The lifestyle form appears only for a hat that holds `observation.write.lifestyle`. A tab
 * that answered 403 in front of a patient would be a control that teaches an operator the app
 * is broken, and station 7's nutritionist walks the same checklist without that grant.
 */
function Station3({ station }: { station: string }) {
  const operator = useSession((state) => state.operator);
  const activeRole = useSession((state) => state.activeRole);
  const permissions = useMemo(
    () => activePermissions({ operator, activeRole }),
    [operator, activeRole],
  );
  const [form, setForm] = useState<'counseling' | 'lifestyle'>('counseling');

  if (!permissions.includes(PERM_WRITE_LIFESTYLE)) return <CounselingScreen station={station} />;

  const tabs = (
    <StationTabs
      prefix="station3"
      value={form}
      options={['counseling', 'lifestyle'] as const}
      onChange={setForm}
    />
  );
  return form === 'counseling' ? (
    <CounselingScreen station={station} tabs={tabs} />
  ) : (
    <LifestyleScreen station={station} tabs={tabs} />
  );
}

/**
 * Station 8's exercise assessment and plan (CP60, §3 step 8, §12.1).
 *
 * An exercise specialist asks five questions, chooses from what is left, and hands over a sheet.
 *
 * # The whole screen is arranged so that a contraindicated exercise cannot be on it
 *
 * There are exactly two places a list of exercises enters this component: the response to
 * `GET /v1/patients/{id}/exercise/options`, and the `options` half of the response to
 * `POST /v1/exercise/assessments`. Both are the permitted set, computed on the server from the
 * assessment actually recorded, with the excluded rows never leaving the server process.
 *
 * There is no third source and there must never be one. `setOptions` is the only writer of the
 * one piece of state that holds exercises, and every call to it is a server answer.
 *
 * # A 409 is not always a failure, and one of them is a step
 *
 * `EXERCISE_NO_ASSESSMENT` means nobody has answered the questions for this patient. The screen
 * shows the questions — which is the operator's next act anyway — rather than an empty list or a
 * red banner over a form they are about to fill in. `stepFor` makes that decision and a test
 * pins it.
 *
 * `EXERCISE_ASSESSMENT_SUPERSEDED` means a colleague recorded newer findings while this operator
 * was choosing. The options are read again immediately, any choice the fresh list no longer
 * offers is taken off the plan, and the operator is told to look through the list before issuing.
 * Anything else would leave a plan half built against findings that are no longer true.
 *
 * # The questions are fetched once and worked from
 *
 * The catalogue is five rows with no patient in it, so it is read when the screen opens and held
 * for the clinic session (ADR-0004). The options are not: they are per patient and per
 * assessment, and are read on arrival and after every write that could move them.
 */
function ExerciseScreen({ station }: { station: string }) {
  const t = useTranslations('exercise');
  const locale = usePreferences((state) => state.language);

  const [patient, setPatient] = useState<{ id: string; name: string; visitId: string } | null>(
    null,
  );
  /** The conditions and their questions. No patient in it, so it is fetched once. */
  const [catalogue, setCatalogue] = useState<Contraindication[]>([]);
  const [draft, setDraft] = useState<ExerciseDraft>(emptyAssessment);
  const [assessment, setAssessment] = useState<ExerciseAssessment | null>(null);
  /*
   * The permitted set, and the only state in this component that holds exercises.
   *
   * Written from a server answer and from nothing else. Null is the honest value before the
   * server has answered and after a 409 saying the questions have not been asked — there is
   * deliberately no unfiltered library for it to fall back to, because there is no route that
   * would return one.
   */
  const [options, setOptions] = useState<ExerciseOptions | null>(null);
  const [plan, setPlan] = useState<ExercisePlan | null>(null);
  const [chosen, setChosen] = useState<ExerciseTargets>(noTargets);
  /** True while the operator has deliberately reopened the questions on a patient who has some. */
  const [asking, setAsking] = useState(false);
  const [review, setReview] = useState<ExerciseReview | null>(null);
  /** The choices a freshly read list no longer offers, by name. Resolved from the list they came from. */
  const [dropped, setDropped] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [trouble, setTrouble] = useState<ExerciseTrouble | null>(null);

  const loadPatient = useCallback(async () => {
    const queue = await api.GET('/v1/stations/{station}/queue', {
      params: { path: { station } },
    });
    const inService = queue.data?.entries.find((entry) => entry.status === 'in_service');
    if (inService?.patient_id === undefined) return null;
    const record = await api.GET('/v1/patients/{id}', {
      params: { path: { id: inService.patient_id } },
    });
    if (record.data === undefined) return null;
    return {
      id: record.data.patient.id,
      name: record.data.patient.name_bn || record.data.patient.name_en,
      visitId: inService.visit_id ?? '',
    };
  }, [station]);

  /**
   * The permitted set, read again.
   *
   * The 409 saying no assessment has been recorded is kept as a trouble rather than swallowed,
   * because the sentence above the questions explains why there is no list — but `stepFor` reads
   * it as a step and the banner draws it as a note rather than as a refusal.
   */
  const readList = useCallback(
    async (patientId: string) => {
      try {
        const answered = await readOptions(patientId);
        setOptions(answered);
        setTrouble(null);
        return answered;
      } catch (error) {
        const failure = exerciseTroubleOf(error, locale, 'options');
        setTrouble(failure);
        // No assessment, no list. Anything else — an unreachable server, a refused hat — leaves
        // whatever list was already in hand, because throwing it away would lose the operator's
        // half-built plan over a connection that drops for its usual few seconds.
        if (noAssessment(failure)) setOptions(null);
        return null;
      }
    },
    [locale],
  );

  /**
   * The questions, read again.
   *
   * Called on arrival, and again when the server refuses an assessment for leaving a live
   * condition unanswered — which from this screen means only one thing: a condition was added to
   * the catalogue after this tablet fetched it, on an offline morning or through a rolling
   * deploy. Reading it again puts the new question on the form, which is the operator's way on.
   */
  const readQuestions = useCallback(async () => {
    const conditions = await listContraindications().catch(() => null);
    if (conditions !== null) setCatalogue(conditions);
    return conditions;
  }, []);

  useEffect(() => {
    let live = true;
    void (async () => {
      const [conditions, whoever] = await Promise.all([
        listContraindications().catch(() => [] as Contraindication[]),
        loadPatient().catch(() => null),
      ]);
      if (!live) return;
      setCatalogue(conditions);
      setPatient(whoever);
      if (whoever === null) return;

      // What the record already says, so a patient who came back after lunch does not have the
      // questions asked again.
      const standing = await readExerciseRecord(whoever.id).catch(() => null);
      if (!live) return;
      if (standing !== null) {
        setAssessment(standing.assessment);
        setPlan(standing.plan);
      }
      await readList(whoever.id);
    })();
    return () => {
      live = false;
    };
  }, [loadPatient, readList]);

  /*
   * The questions, with the ones this tablet had never seen marked.
   *
   * `trouble.missing` is `fields.missing_conditions` off the refusal that sent the operator back
   * here — the codes, out of the prose beside them. It survives the catalogue being read again,
   * because the refusal is what explains why they are looking at the form a second time.
   */
  const questions = useMemo(
    () => questionRows(catalogue, draft, locale, trouble?.missing ?? []),
    [catalogue, draft, locale, trouble],
  );
  const outstanding = useMemo(() => unanswered(draft, catalogue), [draft, catalogue]);
  const walkProblem = useMemo(() => walkMinutesProblem(draft), [draft]);
  const findings = useMemo(
    () => assessmentReading(assessment, catalogue, locale),
    [assessment, catalogue, locale],
  );
  const offers = useMemo(() => offerRows(options, locale), [options, locale]);
  const exclusion = useMemo(() => exclusionReading(options, locale), [options, locale]);
  const unapprovedOffers = useMemo(() => unapprovedExercises(options), [options]);
  const problems = useMemo(() => targetProblems(chosen), [chosen]);
  const planRow = useMemo(() => planReading(plan, locale), [plan, locale]);
  const step = useMemo(
    () => stepFor({ options, plan, asking }, trouble),
    [options, plan, asking, trouble],
  );

  const onRecord = useCallback(async () => {
    if (patient === null) return;
    const body = toAssessmentBody(draft, catalogue, {
      event: crypto.randomUUID(),
      patient: patient.id,
      visit: patient.visitId,
    });
    // Null means the questions are not finished, and the form has already said which are open.
    // Sending it anyway would turn that into a 422 met mid-sentence.
    if (body === null) return;
    setBusy(true);
    try {
      const written = await recordExerciseAssessment(body);
      setAssessment(written.assessment);
      // The options come back on the same response, which is why they are not fetched here:
      // between a write and a refetch there is a window in which a station holds findings and no
      // list, and the natural thing for a screen to do in that window is show one it already has.
      setOptions(written.options);
      // A new assessment supersedes the old one, so the plan built against the old one is no
      // longer the plan this operator is working on, and a selection made against the old list
      // may hold an exercise the new one excludes.
      setPlan(null);
      setChosen(noTargets());
      setDropped([]);
      setReview(null);
      setAsking(false);
      setTrouble(null);
    } catch (error) {
      const failure = exerciseTroubleOf(error, locale, 'assessment');
      setTrouble(failure);
      /*
       * The server refused because a live condition was left unanswered — which cannot be the
       * operator's doing, since `toAssessmentBody` will not build a body until every question
       * this tablet knows about is answered. It means the catalogue moved. Reading it again puts
       * the new question on the form; the operator answers it and records, and the answers they
       * have already given are still in the draft.
       */
      if (questionsMissing(failure)) await readQuestions();
    } finally {
      setBusy(false);
    }
  }, [catalogue, draft, locale, patient, readQuestions]);

  const onIssue = useCallback(async () => {
    if (patient === null) return;
    const body = toPlanBody(chosen, options, {
      event: crypto.randomUUID(),
      patient: patient.id,
      visit: patient.visitId,
    });
    // Null means a target is missing one of its two numbers, or nothing is chosen, or a choice
    // is not on the list the server sent. The form has already said which.
    if (body === null) return;
    setBusy(true);
    try {
      const issued = await issuePlan(body);
      setPlan(issued);
      setReview(null);
      setDropped([]);
      setTrouble(null);
    } catch (error) {
      const failure = exerciseTroubleOf(error, locale, 'plan');
      setTrouble(failure);
      /*
       * A newer assessment, a target refused as unsafe, or an exercise that left the library
       * between the list being drawn and the plan being sent. All three mean the list this
       * operator was looking at is not the list that is true now, so it is read again here and
       * the selection is carried across it — anything the fresh list does not offer comes off the
       * plan, and the notice says how many went.
       */
      const why = reviewFor(failure);
      if (why !== null) {
        setReview(why);
        const fresh = await readList(patient.id);
        // The refusal is what the operator has to read; `readList` clearing it on success would
        // leave a fresh list and no explanation of why they are looking at it again.
        setTrouble(failure);
        if (fresh !== null) {
          const kept = keepOffered(chosen, fresh);
          setChosen(kept.chosen);
          // Named against the list the operator was choosing from — `options` here is still the
          // payload they were looking at, because `fresh` has only just replaced it in state.
          // Resolving against the new one would name nothing, since the dropped rows are exactly
          // what it no longer has.
          setDropped(namesOf(kept.dropped, options, locale));
        }
      }
    } finally {
      setBusy(false);
    }
  }, [chosen, locale, options, patient, readList]);

  return (
    <ScreenShell titleKey="screen.exercise">
      {patient === null ? (
        <AppText>{t('noPatient')}</AppText>
      ) : (
        <ExerciseStation
          patientName={patient.name}
          step={step}
          questions={questions}
          draft={draft}
          outstanding={outstanding}
          walkProblem={walkProblem}
          assessment={assessment}
          findings={findings}
          offers={offers}
          exclusion={exclusion}
          unapprovedOffers={unapprovedOffers}
          chosen={chosen}
          problems={problems}
          plan={plan}
          planRow={planRow}
          review={review}
          droppedNames={dropped}
          /* Empty unless the server named an exercise that is still on the list being drawn.
             A retirement names one too, and by the time the options have been read again it is
             gone — so its sentence stays in the banner, where there is something to read it. */
          refusedExercise={refusedRow(trouble, options)}
          refusedReason={trouble?.message ?? ''}
          busy={busy}
          trouble={trouble}
          onAnswerCondition={(code: string, answer: ConditionAnswer) =>
            setDraft((current) => answerCondition(current, code, answer))
          }
          onAnswerWalks={(answer: WalksAnswer) =>
            setDraft((current) => answerWalks(current, answer))
          }
          onTypeWalkMinutes={(text) => setDraft((current) => typeWalkMinutes(current, text))}
          onTypeJointPain={(text) => setDraft((current) => typeJointPain(current, text))}
          onRecord={() => void onRecord()}
          onChooseExercise={(code) => setChosen((current) => chooseExercise(current, code))}
          onTypeTimes={(code, text) => setChosen((current) => typeTargetTimes(current, code, text))}
          onTypeMinutes={(code, text) =>
            setChosen((current) => typeTargetMinutes(current, code, text))
          }
          onIssue={() => void onIssue()}
          onBackToList={() => setPlan(null)}
          /*
           * The way back out of the questions, and null when there is nowhere to go.
           *
           * Offered only when the operator opened them on a patient who already has a list —
           * because otherwise the only exit from a mis-tapped "ask the questions again" is to
           * answer all of them and record, and that writes a second assessment which supersedes
           * the first. `docs/exercise.md` is explicit that a second assessment is a new answer
           * rather than a corrected one; a screen that made one easy to produce by accident would
           * be putting a clinical act behind a stray thumb.
           */
          onCancelQuestions={
            asking && options !== null
              ? () => {
                  setAsking(false);
                  setDraft(emptyAssessment());
                  setTrouble(null);
                }
              : null
          }
          onAskAgain={() => {
            // The questions start empty rather than pre-filled from the assessment on record. A
            // second assessment is a new answer, not a corrected one — a patient whose foot ulcer
            // healed has a different foot today — and pre-ticking last month's conditions is how
            // a healed ulcer stays on the record for a year.
            setDraft(emptyAssessment());
            setAsking(true);
          }}
          onRetry={() => void readList(patient.id)}
        />
      )}
    </ScreenShell>
  );
}

/** Station 3's own permission (§4.4). An assessment is lifestyle data in another shape. */
const PERM_WRITE_LIFESTYLE = 'observation.write.lifestyle';

/**
 * Station 5 has two forms, and this is how the operator moves between them (CP51).
 *
 * The station is still not a choice — it comes from the hat, exactly as before. What is
 * chosen here is which of station 5's own forms is open: the vitals the assistant takes when
 * the patient sits down, and the examination the junior doctor performs afterwards. Both
 * write under the same station and the same encounter, so this is a view, not a routing
 * decision, and putting them behind two roles would mean a patient's blood pressure and
 * their foot examination could not be entered by whoever is free.
 *
 * A segmented control rather than a tab bar or a dropdown: two options, both visible, one
 * tap each, and the one you are on is unmistakable. The examination is the longer form and
 * the vitals the more frequent, so vitals opens first.
 */
function Station5({ station }: { station: string }) {
  const [form, setForm] = useState<'vitals' | 'examination'>('vitals');
  const tabs = (
    <StationTabs
      prefix="station5"
      value={form}
      options={['vitals', 'examination'] as const}
      onChange={setForm}
    />
  );
  return form === 'vitals' ? (
    <VitalsScreen station={station} tabs={tabs} />
  ) : (
    <ExaminationScreen station={station} tabs={tabs} />
  );
}

/**
 * The switch between one station's own forms.
 *
 * Generic over the options because two stations now have two forms each — station 5's vitals
 * and examination (CP51), and station 3's counselling checklist and lifestyle assessment
 * (CP58) — and a second copy of this control is a second place the touch target, the selected
 * state and the tab role would drift.
 *
 * `prefix` keeps each station's test ids its own, so a flow written against station 5 cannot
 * accidentally match station 3's control.
 */
function StationTabs<Option extends string>({
  prefix,
  value,
  options,
  onChange,
}: {
  prefix: string;
  value: Option;
  options: readonly Option[];
  onChange: (next: Option) => void;
}) {
  const t = useTranslations('screen');
  const { colors } = useTokens();
  const say = t as unknown as (key: string) => string;
  return (
    <View
      accessibilityRole="tablist"
      style={{
        flexDirection: 'row',
        gap: theme.spacing['2'],
        padding: theme.spacing['1'],
        borderRadius: theme.borderRadius.md,
        backgroundColor: colors.surface.sunken,
      }}
    >
      {options.map((option) => {
        const selected = option === value;
        return (
          <Pressable
            key={option}
            testID={`${prefix}-${option}`}
            accessibilityRole="tab"
            accessibilityState={{ selected }}
            onPress={() => onChange(option)}
            style={{
              flex: 1,
              minHeight: theme.size.touchTarget,
              alignItems: 'center',
              justifyContent: 'center',
              borderRadius: theme.borderRadius.md,
              borderWidth: 1,
              borderColor: selected ? colors.brand.border : colors.border.subtle,
              backgroundColor: selected ? colors.brand.subtle : colors.surface.raised,
            }}
          >
            <AppText
              size="sm"
              weight={selected ? 'semibold' : 'regular'}
              style={{ color: selected ? colors.brand.text : colors.text.secondary }}
            >
              {say(option)}
            </AppText>
          </Pressable>
        );
      })}
    </View>
  );
}

/** The patient's last value per vital, canonical, for the comparison line. */
function previousVitals(
  rows: { code: string; value?: number | null }[] | undefined,
): Partial<Record<VitalKey, number>> {
  const out: Partial<Record<VitalKey, number>> = {};
  if (rows === undefined) return out;
  const seen = new Map<string, number>();
  for (const row of rows) {
    if (row.value === null || row.value === undefined) continue;
    if (!seen.has(row.code)) seen.set(row.code, row.value);
  }
  for (const field of VITAL_FIELDS) {
    const value = seen.get(field.code);
    if (value !== undefined) out[field.key] = value;
  }
  return out;
}

/**
 * Which capture screen this operator gets.
 *
 * Not a choice. The station comes from the hat they are wearing — the same rule the queue
 * follows — because an operator working at anthropometry is at anthropometry, and a screen
 * that let them pick is a screen where a weight lands under a vitals encounter.
 *
 * Switching hats (CP41) therefore switches this screen, which is exactly what §3's "the same
 * assistant enters BP, then switches to anthropometry entry, from the same phone" describes.
 *
 * Station 5 is the one station that records two different things — the vitals and the
 * structured examination (CP51) — so it gets a switch between its own two forms. That is not
 * a choice of station: both write under station 5, and which of them is open is a view.
 */
export default function StationScreen() {
  const t = useTranslations('anthropometry');
  const station = useSession(activeStation);

  switch (station) {
    case 'STN_ANTHROPOMETRY':
      return <AnthropometryScreen station={station} />;
    case 'STN_HISTORY':
      return <HistoryScreen station={station} />;
    case 'STN_EXAMINATION':
      // One station, two forms. The vitals and the examination are both taken at station 5,
      // by whoever is free, so they are one screen with a switch rather than two stations.
      return <Station5 station={station} />;
    case 'STN_COUNSELING':
      // §5.2's "Counseling & Lifestyle" room, and it records two different things: the
      // checklist somebody walks (CP56) and the lifestyle assessment somebody runs (CP58).
      // One screen with a switch rather than two stations, on station 5's argument — both
      // write under station 3, by whoever is sitting with the patient.
      return <Station3 station={station} />;
    case 'STN_EXERCISE':
      // §5.2's exercise room, and station 8 records one thing: the assessment and the routine
      // that follows from it. One form, so no switch — the counselling checklist is not offered
      // here because station 8 does not hold station 3's grant, and a tab that answered 403 in
      // front of a patient would teach an operator the app is broken.
      return <ExerciseScreen station={station} />;
    case 'STN_NUTRITION':
      // §5.2's nutrition room, and it records two different things: the same counselling
      // checklist the counsellor walks (CP56) and the 24-hour dietary recall that is station
      // 7's own work (CP59). One screen with a switch rather than two stations, on station 5's
      // argument — the rooms are rooms in a corridor rather than separate lists, and a
      // nutritionist who could not see what the counselling room covered would cover it again.
      // The lifestyle form is not offered here: station 7 does not hold station 3's grant.
      return <Station7 station={station} />;
    default:
      // Every other station's capture screen arrives at its own checkpoint. Saying so is
      // better than showing an anthropometry form to somebody at the pharmacy.
      return (
        <ScreenShell titleKey="screen.station">
          <AppText>{t('notThisStation')}</AppText>
        </ScreenShell>
      );
  }
}
