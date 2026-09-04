import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { ScreenShell } from '@/components/ScreenShell';
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
import { PercentileCard, type CardPercentile, type CardWeightStatus } from '@/features/growth';
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
function AnthropometryScreen({
  station,
}: {
  station: string;
}) {
  const t = useTranslations('anthropometry');

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
      await api.POST('/v1/observations/batch', {
        params: {
          header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id },
        },
        body,
      });
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
  }, [confirmed, form, patient]);

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
    </ScreenShell>
  );
}


/**
 * Station 5's vitals (CP49).
 *
 * The same shape as the anthropometry screen above and deliberately so: one patient from the
 * queue, one fetch of what is normal, one batch on save. An operator who switches hats
 * mid-morning should not have to learn a second set of habits.
 */
function VitalsScreen({ station }: { station: string }) {
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
    setReadings((current) => current.map((reading, i) => (i === index ? change(reading) : reading)));
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
      await api.POST('/v1/observations/batch', {
        params: {
          header: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': body.event_id },
        },
        body,
      });
      setSaved(true);
      const values = await api.GET('/v1/patients/{id}/observations', {
        params: { path: { id: patient.id }, query: { category: 'VITAL' } },
      });
      setPrevious(previousVitals(values.data?.observations));
      setReadings([emptyReading()]);
    } finally {
      setBusy(false);
    }
  }, [patient, readings]);

  return (
    <ScreenShell titleKey="screen.vitals">
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
    </ScreenShell>
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
 */
export default function StationScreen() {
  const t = useTranslations('anthropometry');
  const station = useSession(activeStation);

  switch (station) {
    case 'STN_ANTHROPOMETRY':
      return <AnthropometryScreen station={station} />;
    case 'STN_EXAMINATION':
      return <VitalsScreen station={station} />;
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
