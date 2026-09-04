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
  toBatch,
  warningsFor,
  type Confirmations,
  type FieldKey,
  type FormState,
  type PreviousMeasurements,
  type PreviousValues,
} from '@/features/anthropometry';
import {
  CriticalAlertModal,
  prepareAlarm,
  releaseAlarm,
  type CriticalAlert,
} from '@/features/alerts';
import {
  ExaminationStation,
  emptyExam,
  footRiskFrom,
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
  type HistoryRow,
  type Side,
} from '@/features/examination';
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
  toVitalsBatch,
  type Range,
  type Reading,
  type Subject,
  type VitalKey,
} from '@/features/vitals';
import type { PlausibilityRule } from '@dthcms/clinical-calc';

import { api } from '@/lib/api';
import { usePreferences } from '@/stores/preferences';
import { activeStation, useSession } from '@/stores/session';

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
        />
      ) : null}
      {/* Over everything, including the growth card. A critical value is the one thing on
          this screen that cannot wait for the operator to finish reading something else. */}
      <CriticalAlertModal alerts={critical.alerts} seen={critical.seen} onSeen={critical.dismiss} />
    </ScreenShell>
  );
}

/**
 * Station 5's vitals (CP49).
 *
 * The same shape as the anthropometry screen above and deliberately so: one patient from the
 * queue, one fetch of what is normal, one batch on save. An operator who switches hats
 * mid-morning should not have to learn a second set of habits.
 *
 * `tabs` is the switch between station 5's two forms (CP51). It is rendered here, inside the
 * shell, rather than wrapped around this screen: a control above the safe-area header would
 * sit outside the frame every other screen in the app shares.
 */
function VitalsScreen({ station, tabs }: { station: string; tabs?: ReactNode }) {
  const critical = useCriticalAlerts();
  const t = useTranslations('vitals');
  const [readings, setReadings] = useState<Reading[]>(() => [emptyReading()]);
  const [ranges, setRanges] = useState<Range[]>([]);
  const [previous, setPrevious] = useState<Partial<Record<VitalKey, number>>>({});
  const [patient, setPatient] = useState<{
    id: string;
    name: string;
    sex: 'male' | 'female' | 'other';
    ageYears: number;
  } | null>(null);
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
        api.GET('/v1/patients/{id}/observations', {
          params: { path: { id: inService.patient_id }, query: { category: 'VITAL' } },
        }),
      ]);
      if (!live || record.data === undefined) return;
      setPatient({
        id: record.data.patient.id,
        name: record.data.patient.name_bn || record.data.patient.name_en,
        sex: record.data.patient.sex as 'male' | 'female' | 'other',
        ageYears: record.data.patient.birth.age,
      });
      setPrevious(previousVitals(values.data?.observations));
    })();
    return () => {
      live = false;
    };
  }, [station]);

  // What is normal, once. A tablet fetches it on arrival and flags offline for the rest of
  // the clinic session — the only way the flag arrives while the cuff is still on the arm.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/observations/reference-ranges').then((result) => {
      if (live && result.data !== undefined) setRanges(result.data.ranges as Range[]);
    });
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
    if (patient === null) return;
    setBusy(true);
    try {
      const ids = new Map<string, string>();
      // Each reading gets its own effective time, a minute apart, because two blood
      // pressures taken in one sitting are two facts and a timeline that gave them the same
      // instant could not order them.
      const base = Date.now();
      const body = toVitalsBatch(readings, {
        batch: crypto.randomUUID(),
        patient: patient.id,
        perValue: (index, code) => {
          const key = `${index}:${code}`;
          const existing = ids.get(key);
          if (existing !== undefined) return existing;
          const id = crypto.randomUUID();
          ids.set(key, id);
          return id;
        },
        takenAt: (index) => new Date(base + index * 60_000).toISOString(),
      });
      const written = await api.POST('/v1/observations/batch', {
        params: {
          header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id },
        },
        body,
      });
      // Before the history refetch, for the same reason as at anthropometry: the alarm has
      // to sound in the instant the save returns.
      critical.raise(written.data?.alerts);
      setSaved(true);
      const values = await api.GET('/v1/patients/{id}/observations', {
        params: { path: { id: patient.id }, query: { category: 'VITAL' } },
      });
      setPrevious(previousVitals(values.data?.observations));
      setReadings([emptyReading()]);
    } finally {
      setBusy(false);
    }
  }, [critical, patient, readings]);

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
  const tabs = <StationTabs value={form} onChange={setForm} />;
  return form === 'vitals' ? (
    <VitalsScreen station={station} tabs={tabs} />
  ) : (
    <ExaminationScreen station={station} tabs={tabs} />
  );
}

function StationTabs({
  value,
  onChange,
}: {
  value: 'vitals' | 'examination';
  onChange: (next: 'vitals' | 'examination') => void;
}) {
  const t = useTranslations('screen');
  const { colors } = useTokens();
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
      {(['vitals', 'examination'] as const).map((option) => {
        const selected = option === value;
        return (
          <Pressable
            key={option}
            testID={`station5-${option}`}
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
              {t(option)}
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
