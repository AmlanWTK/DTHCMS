import { useEffect, useState } from 'react';
import { ScrollView, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { ageOn, readDate, type DateParts } from '@dthcms/shared-schemas';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { clearDraft, loadDraft, saveDraft } from '@/lib/registration-draft';
import { theme, useTokens } from '@/lib/tokens';

import {
  BLANK,
  STEPS,
  canAdvance,
  complete,
  required,
  toRegistration,
  type RegistrationValues,
} from './state';

/**
 * Registration on a phone (CP33): one section per screen.
 *
 * The step-by-step layout is not a stylistic choice. On a phone the keyboard covers half
 * the screen, so a long form means the operator scrolls with one thumb while the patient
 * waits — and every field they cannot see is a field they forget. One question at a time
 * with a large target is slower to read and faster to complete.
 *
 * Every keystroke is drafted. An interruption on a phone is not an edge case: a call comes
 * in, the screen locks, Android reclaims memory. Losing eight fields to any of those is how
 * an operator decides to use paper instead.
 */
export function RegistrationFlow({
  today = new Date(),
  onSubmit,
  newEventID,
}: {
  today?: Date;
  onSubmit: (body: ReturnType<typeof toRegistration>) => Promise<void>;
  newEventID: () => string;
}) {
  const t = useTranslations();
  const { colors } = useTokens();

  const [values, setValues] = useState<RegistrationValues>(BLANK);
  const [step, setStep] = useState(0);
  const [eventID, setEventID] = useState(() => newEventID());
  const [resumable, setResumable] = useState<{ name: string; step: number } | null>(null);
  const [busy, setBusy] = useState(false);

  // A draft found at start-up is offered, not applied. Silently restoring somebody else's
  // half-finished registration is how a phone passed between two registrars produces one
  // record with two people's details in it.
  useEffect(() => {
    void loadDraft<RegistrationValues>().then((draft) => {
      if (draft)
        setResumable({ name: draft.values.nameEN || t('registration.someone'), step: draft.step });
    });
  }, [t]);

  useEffect(() => {
    if (values === BLANK) return;
    void saveDraft({ eventID, step, savedAt: Date.now(), values });
  }, [values, step, eventID]);

  const set = <K extends keyof RegistrationValues>(key: K, value: RegistrationValues[K]) =>
    setValues((current) => ({ ...current, [key]: value }));

  // Clamped rather than indexed blind: a resumed draft carries a step number, and a step
  // number from an older build could point past the end of a flow that has since changed.
  const current = STEPS[Math.min(Math.max(step, 0), STEPS.length - 1)] ?? STEPS[0];
  const canGo = canAdvance(current.id, values);

  if (resumable) {
    return (
      <View style={{ gap: theme.spacing['4'], padding: theme.spacing['4'] }}>
        <AppText size="xl" weight="bold">
          {t('registration.resumeTitle')}
        </AppText>
        <AppText style={{ color: colors.text.muted }}>
          {t('registration.resumeBody', { name: resumable.name })}
        </AppText>
        <AppButton
          label={t('registration.resume')}
          onPress={() => {
            void loadDraft<RegistrationValues>().then((draft) => {
              if (draft) {
                setValues(draft.values);
                setStep(draft.step);
                setEventID(draft.eventID);
              }
              setResumable(null);
            });
          }}
        />
        <AppButton
          label={t('registration.startAgain')}
          variant="secondary"
          onPress={() => {
            void clearDraft();
            setEventID(newEventID());
            setResumable(null);
          }}
        />
      </View>
    );
  }

  return (
    <View style={{ flex: 1, gap: theme.spacing['3'] }}>
      <Progress step={step} total={STEPS.length} />

      <ScrollView
        contentContainerStyle={{ gap: theme.spacing['4'], paddingBottom: theme.spacing['8'] }}
      >
        <AppText size="lg" weight="bold">
          {t(`registration.steps.${current.id}` as never)}
        </AppText>

        {current.id === 'identity' ? (
          <>
            <Field
              label={t('registration.nameEN')}
              value={values.nameEN}
              onChange={(v) => set('nameEN', v)}
              required
            />
            <Field
              label={t('registration.nameBN')}
              value={values.nameBN}
              onChange={(v) => set('nameBN', v)}
            />
            <Choice
              label={t('registration.sex')}
              value={values.sex}
              onChange={(v) => set('sex', v)}
              options={['female', 'male', 'other'].map((id) => ({
                value: id,
                label: t(`registration.sexes.${id}` as never),
              }))}
            />
          </>
        ) : null}

        {current.id === 'birth' ? <BirthStep values={values} set={set} today={today} /> : null}

        {current.id === 'contact' ? (
          <>
            <Field
              label={t('registration.phone')}
              value={values.phone}
              onChange={(v) => set('phone', v)}
              keyboardType="phone-pad"
              hint={t('registration.phoneHelp')}
              required
            />
            <Field
              label={t('registration.phoneSecondary')}
              value={values.phoneSecondary}
              onChange={(v) => set('phoneSecondary', v)}
              keyboardType="phone-pad"
            />
          </>
        ) : null}

        {current.id === 'address' ? (
          <>
            <Field
              label={t('registration.division')}
              value={values.division}
              onChange={(v) => set('division', v)}
            />
            <Field
              label={t('registration.district')}
              value={values.district}
              onChange={(v) => set('district', v)}
            />
            <Field
              label={t('registration.upazila')}
              value={values.upazila}
              onChange={(v) => set('upazila', v)}
            />
            <Field
              label={t('registration.addressLine')}
              value={values.addressLine}
              onChange={(v) => set('addressLine', v)}
            />
          </>
        ) : null}

        {current.id === 'identifiers' ? (
          <Field
            label={t('registration.nationalID')}
            value={values.nationalID}
            onChange={(v) => set('nationalID', v)}
            keyboardType="number-pad"
            hint={t('registration.nationalIDHelp')}
          />
        ) : null}

        {current.id === 'emergency' ? (
          <>
            <Field
              label={t('registration.emergencyName')}
              value={values.emergencyName}
              onChange={(v) => set('emergencyName', v)}
            />
            <Field
              label={t('registration.emergencyRelation')}
              value={values.emergencyRelation}
              onChange={(v) => set('emergencyRelation', v)}
            />
            <Field
              label={t('registration.emergencyPhone')}
              value={values.emergencyPhone}
              onChange={(v) => set('emergencyPhone', v)}
              keyboardType="phone-pad"
            />
          </>
        ) : null}

        {current.id === 'background' ? (
          <>
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('registration.backgroundNote')}
            </AppText>
            <Choice
              label={t('registration.education')}
              value={values.education}
              onChange={(v) => set('education', v)}
              options={codedOptions(t, 'education', [
                'none',
                'primary',
                'secondary',
                'higher_secondary',
                'graduate',
                'postgraduate',
                'madrasa',
                'unknown',
              ])}
            />
            <Choice
              label={t('registration.income')}
              value={values.income}
              onChange={(v) => set('income', v)}
              options={codedOptions(t, 'income', [
                'under_10k',
                '10k_25k',
                '25k_50k',
                '50k_100k',
                'over_100k',
                'unknown',
              ])}
            />
            <Choice
              label={t('registration.residence')}
              value={values.residence}
              onChange={(v) => set('residence', v)}
              options={codedOptions(t, 'residence', ['urban', 'semi_urban', 'rural', 'unknown'])}
            />
          </>
        ) : null}

        {current.id === 'consent' ? (
          <>
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('registration.consentNote')}
            </AppText>
            <Field
              label={t('registration.consent')}
              value={values.consentReference}
              onChange={(v) => set('consentReference', v)}
              required
            />
          </>
        ) : null}

        {current.id === 'review' ? <Review values={values} today={today} /> : null}
      </ScrollView>

      <View style={{ flexDirection: 'row', gap: theme.spacing['3'] }}>
        {step > 0 ? (
          <View style={{ flex: 1 }}>
            <AppButton
              label={t('registration.back')}
              variant="secondary"
              onPress={() => setStep(step - 1)}
            />
          </View>
        ) : null}
        <View style={{ flex: 2 }}>
          {current.id === 'review' ? (
            <AppButton
              label={busy ? t('registration.saving') : t('registration.save')}
              disabled={!complete(values) || busy}
              onPress={() => {
                setBusy(true);
                void onSubmit(toRegistration(values, eventID))
                  .then(() => clearDraft())
                  .finally(() => setBusy(false));
              }}
            />
          ) : (
            <AppButton
              label={current.required ? t('registration.next') : t('registration.skip')}
              disabled={!canGo}
              onPress={() => setStep(step + 1)}
            />
          )}
        </View>
      </View>
    </View>
  );
}

/** The date of birth, with the same age echo the web desk uses and for the same reason. */
function BirthStep({
  values,
  set,
  today,
}: {
  values: RegistrationValues;
  set: <K extends keyof RegistrationValues>(key: K, value: RegistrationValues[K]) => void;
  today: Date;
}) {
  const t = useTranslations();
  const { colors, status } = useTokens();

  const parsed = readDate(values.date);
  const age = parsed ? ageOn(parsed.iso, parsed.precision, today) : null;
  const tooOld = age !== null && age.years > 130;
  const future = age !== null && age.years < 0;

  const part = (key: keyof DateParts, max: number) => (raw: string) =>
    set('date', { ...values.date, [key]: raw.replace(/[^0-9]/g, '').slice(0, max) });

  return (
    <View style={{ gap: theme.spacing['3'] }}>
      <View style={{ flexDirection: 'row', gap: theme.spacing['3'] }}>
        <View style={{ flex: 1 }}>
          <Field
            label={t('registration.day')}
            value={values.date.day}
            onChange={part('day', 2)}
            keyboardType="number-pad"
          />
        </View>
        <View style={{ flex: 1 }}>
          <Field
            label={t('registration.month')}
            value={values.date.month}
            onChange={part('month', 2)}
            keyboardType="number-pad"
          />
        </View>
        <View style={{ flex: 1.4 }}>
          <Field
            label={t('registration.year')}
            value={values.date.year}
            onChange={part('year', 4)}
            keyboardType="number-pad"
            required
          />
        </View>
      </View>

      <AppText
        size="lg"
        weight="bold"
        accessibilityLiveRegion="polite"
        style={{
          color:
            tooOld || future ? status.critical.text : age ? status.normal.text : colors.text.muted,
        }}
      >
        {future
          ? t('registration.future')
          : tooOld
            ? t('registration.implausible')
            : age
              ? age.approximate
                ? t('registration.ageApproximate', { years: age.years })
                : t('registration.age', { years: age.years, months: age.months })
              : t('registration.birthHint')}
      </AppText>

      <Choice
        label={t('registration.source')}
        value={values.dobSource}
        onChange={(v) => set('dobSource', v)}
        options={[
          'birth_certificate',
          'national_id',
          'passport',
          'immunisation_card',
          'patient_stated',
          'guardian_stated',
          'estimated',
        ].map((id) => ({ value: id, label: t(`registration.sources.${id}` as never) }))}
      />
    </View>
  );
}

/** The last screen: everything that will be written, before it is written. */
function Review({ values, today }: { values: RegistrationValues; today: Date }) {
  const t = useTranslations();
  const { colors } = useTokens();
  const parsed = readDate(values.date);
  const age = parsed ? ageOn(parsed.iso, parsed.precision, today) : null;
  const missing = Object.entries(required(values))
    .filter(([, done]) => !done)
    .map(([key]) => t(`registration.missing.${key}` as never));

  const rows: Array<[string, string]> = [
    [t('registration.nameEN'), values.nameEN],
    [t('registration.nameBN'), values.nameBN],
    [t('registration.sex'), values.sex ? t(`registration.sexes.${values.sex}` as never) : ''],
    [t('registration.birth'), parsed ? `${parsed.iso} · ${age?.years ?? '—'}` : ''],
    [t('registration.phone'), values.phone],
    [t('registration.consent'), values.consentReference],
  ];

  return (
    <View style={{ gap: theme.spacing['3'] }}>
      {rows.map(([label, value]) => (
        <View key={label} style={{ gap: theme.spacing['0.5'] }}>
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {label}
          </AppText>
          <AppText>{value || '—'}</AppText>
        </View>
      ))}
      {missing.length > 0 ? (
        <AppText size="sm" style={{ color: colors.text.muted }}>
          {t('registration.stillNeeded', { fields: missing.join(', ') })}
        </AppText>
      ) : null}
    </View>
  );
}

function Progress({ step, total }: { step: number; total: number }) {
  const t = useTranslations();
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['1'] }}>
      <AppText size="xs" style={{ color: colors.text.muted }}>
        {t('registration.progress', { step: step + 1, total })}
      </AppText>
      <View style={{ height: 4, backgroundColor: colors.surface.sunken, borderRadius: 2 }}>
        <View
          style={{
            height: 4,
            width: `${((step + 1) / total) * 100}%`,
            backgroundColor: colors.brand.solid,
            borderRadius: 2,
          }}
        />
      </View>
    </View>
  );
}

function Field({
  label,
  value,
  onChange,
  hint,
  required: isRequired,
  keyboardType,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  hint?: string;
  required?: boolean;
  keyboardType?: 'default' | 'phone-pad' | 'number-pad';
}) {
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['1'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {label}
        {isRequired ? ' *' : ''}
      </AppText>
      <TextInput
        accessibilityLabel={label}
        value={value}
        onChangeText={onChange}
        keyboardType={keyboardType ?? 'default'}
        autoCorrect={false}
        style={{
          minHeight: theme.size.touchTarget,
          borderWidth: 1,
          borderColor: colors.border.control,
          borderRadius: theme.borderRadius.md,
          paddingHorizontal: theme.spacing['3'],
          color: colors.text.primary,
          backgroundColor: colors.surface.base,
          fontSize: theme.fontSize.base,
        }}
      />
      {hint ? (
        <AppText size="xs" style={{ color: colors.text.muted }}>
          {hint}
        </AppText>
      ) : null}
    </View>
  );
}

/**
 * A closed list as a row of large targets rather than a picker.
 *
 * Three to eight options, each a thumb's width: on a phone that is one tap, where a picker
 * is a tap, a scroll and a tap. Anything longer stays a picker.
 */
function Choice({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: Array<{ value: string; label: string }>;
  onChange: (value: string) => void;
}) {
  const { colors } = useTokens();
  return (
    <View style={{ gap: theme.spacing['2'] }}>
      <AppText size="sm" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
        {options.map((option) => {
          const chosen = option.value === value;
          return (
            <AppButton
              key={option.value}
              label={option.label}
              variant={chosen ? 'primary' : 'secondary'}
              accessibilityState={{ selected: chosen }}
              onPress={() => onChange(option.value)}
            />
          );
        })}
      </View>
    </View>
  );
}

function codedOptions(
  t: ReturnType<typeof useTranslations>,
  group: string,
  ids: string[],
): Array<{ value: string; label: string }> {
  return ids.map((id) => ({ value: id, label: t(`registration.values.${group}.${id}` as never) }));
}
