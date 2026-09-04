'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';

import { ApiError, fieldMessages, queryKeys } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input, Select } from '@dthcms/ui';

import {
  checkDuplicates,
  newEventId,
  registerPatient,
  type DuplicateMatch,
  type Patient,
  type PatientRegistration,
} from '../api/patients';
import {
  isComplete,
  normalisePhone,
  readDate,
  requiredState,
  type DateParts,
} from '@dthcms/shared-schemas';
import { BirthDateField } from './BirthDateField';
import { DuplicateWarning } from './DuplicateWarning';

/**
 * The registration desk (CP32).
 *
 * Registration involves more typing than any other station, so this is the one screen where
 * a keyboard beats a tablet — and the target is a complete record in under ninety seconds
 * for somebody who does it fifty times a day. Everything here follows from that.
 *
 * **Sectioned, with the required set first.** The clinical minimum — a name, a sex, a date
 * of birth, one mobile, the consent record — is above the fold and can be submitted on its
 * own. Everything below it is prompted and skippable, because a desk that cannot finish a
 * record without an income band nobody present knows is a desk holding a queue.
 *
 * **The duplicate check runs while they type**, not on submit. Warning after the record
 * exists is warning too late; the whole point of CP30 is to catch it before creation.
 *
 * **Field errors come back bilingual and are shown where they happened.** The server sends
 * `fields` and `fields_bn`; a Bangla-reading officer sees Bangla.
 */

const EMPTY_DATE: DateParts = { day: '', month: '', year: '' };

interface FormState {
  nameEN: string;
  nameBN: string;
  sex: string;
  date: DateParts;
  dobSource: string;
  phone: string;
  phoneSecondary: string;
  division: string;
  district: string;
  upazila: string;
  addressLine: string;
  postcode: string;
  emergencyName: string;
  emergencyRelation: string;
  emergencyPhone: string;
  nationalID: string;
  education: string;
  occupation: string;
  income: string;
  household: string;
  residence: string;
  payer: string;
  consentReference: string;
}

const BLANK: FormState = {
  nameEN: '',
  nameBN: '',
  sex: '',
  date: EMPTY_DATE,
  dobSource: '',
  phone: '',
  phoneSecondary: '',
  division: '',
  district: '',
  upazila: '',
  addressLine: '',
  postcode: '',
  emergencyName: '',
  emergencyRelation: '',
  emergencyPhone: '',
  nationalID: '',
  education: '',
  occupation: '',
  income: '',
  household: '',
  residence: '',
  payer: '',
  consentReference: '',
};

export function RegistrationForm({
  today = new Date(),
  onRegistered,
}: {
  today?: Date;
  onRegistered?: (patient: Patient) => void;
}) {
  const t = useTranslations('patients.register');
  const locale = useLocaleTag();
  const queryClient = useQueryClient();

  const [form, setForm] = useState<FormState>(BLANK);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<string | null>(null);
  const [match, setMatch] = useState<DuplicateMatch | null>(null);
  const [dismissedDuplicates, setDismissed] = useState(false);
  const [registered, setRegistered] = useState<Patient | null>(null);

  // The event id is minted once per attempt, not per submit. A double-click, a retried
  // request or a lost reply must create one patient, and the ledger's uniqueness on
  // event_id is what makes that true (CP29).
  const eventID = useRef(newEventId());

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((current) => ({ ...current, [key]: value }));

  const required = requiredState(form);
  const parsedDate = readDate(form.date);
  const ready = isComplete(required);

  const probe = useMemo(() => {
    if (form.nameEN.trim().length < 3 && !normalisePhone(form.phone)) return null;
    return buildRegistration(form, parsedDate, '');
  }, [form, parsedDate]);

  // Debounced: the desk types continuously, and one request per keystroke would be a
  // hundred round trips per registration and a 409 storm on the idempotency key.
  useEffect(() => {
    if (!probe) {
      setMatch(null);
      return;
    }
    const timer = setTimeout(() => {
      checkDuplicates({ ...probe, consent_reference: 'checking' })
        .then((result) => {
          setMatch(result);
          setDismissed(false);
        })
        // A duplicate check that fails must never stop a registration. The unique
        // constraint on the identifier digest still prevents the duplicate; what is lost
        // is the warning, and a desk that cannot register anybody is worse.
        .catch(() => setMatch(null));
    }, 450);
    return () => clearTimeout(timer);
  }, [probe]);

  const submit = useMutation({
    mutationFn: (registration: PatientRegistration) => registerPatient(registration),
    onSuccess: (result) => {
      setRegistered(result.patient);
      onRegistered?.(result.patient);
      void queryClient.invalidateQueries({ queryKey: queryKeys.patients() });
    },
    onError: (error) => {
      if (error instanceof ApiError) {
        // Bilingual, with an English fallback per field: silence is the worst outcome for
        // somebody standing at a desk trying to fix a form.
        const named = fieldMessages(error, locale);
        setFields(named);
        setRefusal(
          Object.keys(named).length > 0
            ? null
            : (locale === 'bn' ? error.messageBN : error.messageEN) || t('failed'),
        );
        return;
      }
      setRefusal(t('failed'));
    },
  });

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready || submit.isPending) return;
    setFields({});
    setRefusal(null);
    submit.mutate(buildRegistration(form, parsedDate, eventID.current));
  }

  if (registered) {
    return (
      <RegisteredCard
        patient={registered}
        onAnother={() => {
          eventID.current = newEventId();
          setForm(BLANK);
          setRegistered(null);
          setMatch(null);
        }}
      />
    );
  }

  const blocked = match?.verdict === 'blocked';

  return (
    <form className="app-register" onSubmit={onSubmit} noValidate>
      {refusal ? <AlertBanner tone="critical" title={refusal} /> : null}

      <Card className="app-register__section">
        <h2>{t('sections.identity')}</h2>
        <p className="app-register__note">{t('sections.identityNote')}</p>
        <div className="app-register__grid">
          <Input
            label={t('nameEN')}
            data-testid="name-en"
            required
            autoFocus
            autoComplete="off"
            value={form.nameEN}
            error={fields.name_en}
            onChange={(event) => set('nameEN', event.target.value)}
          />
          <Input
            label={t('nameBN')}
            data-testid="name-bn"
            autoComplete="off"
            lang="bn"
            value={form.nameBN}
            error={fields.name_bn}
            onChange={(event) => set('nameBN', event.target.value)}
          />
          <Select
            label={t('sex')}
            data-testid="sex"
            required
            value={form.sex}
            error={fields.sex}
            placeholder={t('sexPlaceholder')}
            onChange={(event) => set('sex', event.target.value)}
            options={[
              { value: 'female', label: t('sexes.female') },
              { value: 'male', label: t('sexes.male') },
              { value: 'other', label: t('sexes.other') },
            ]}
          />
        </div>

        <BirthDateField
          value={form.date}
          onChange={(date) => set('date', date)}
          source={form.dobSource}
          onSourceChange={(source) => set('dobSource', source)}
          today={today}
          serverError={fields.birth_date ?? fields.dob_precision}
        />
      </Card>

      <Card className="app-register__section">
        <h2>{t('sections.contact')}</h2>
        <div className="app-register__grid">
          <Input
            label={t('phone')}
            data-testid="phone"
            required
            inputMode="tel"
            autoComplete="off"
            value={form.phone}
            description={t('phoneHelp')}
            error={fields.phone_primary}
            onChange={(event) => set('phone', event.target.value)}
          />
          <Input
            label={t('phoneSecondary')}
            inputMode="tel"
            autoComplete="off"
            value={form.phoneSecondary}
            description={t('phoneSecondaryHelp')}
            error={fields.phone_secondary}
            onChange={(event) => set('phoneSecondary', event.target.value)}
          />
        </div>
        <div className="app-register__grid">
          <Input
            label={t('division')}
            value={form.division}
            autoComplete="off"
            onChange={(e) => set('division', e.target.value)}
          />
          <Input
            label={t('district')}
            value={form.district}
            autoComplete="off"
            onChange={(e) => set('district', e.target.value)}
          />
          <Input
            label={t('upazila')}
            value={form.upazila}
            autoComplete="off"
            onChange={(e) => set('upazila', e.target.value)}
          />
          <Input
            label={t('postcode')}
            value={form.postcode}
            inputMode="numeric"
            autoComplete="off"
            onChange={(e) => set('postcode', e.target.value)}
          />
        </div>
        <Input
          label={t('addressLine')}
          value={form.addressLine}
          autoComplete="off"
          onChange={(e) => set('addressLine', e.target.value)}
        />
      </Card>

      {/* Between the required set and the optional one, because it is the moment the
          warning is most useful: everything it needs has been typed, and nothing has been
          created. */}
      {match ? (
        <DuplicateWarning
          match={match}
          onDismiss={() => setDismissed(true)}
          dismissed={dismissedDuplicates}
        />
      ) : null}

      <Card className="app-register__section">
        <h2>{t('sections.identifiers')}</h2>
        <p className="app-register__note">{t('sections.identifiersNote')}</p>
        <Input
          label={t('nationalID')}
          data-testid="national-id"
          inputMode="numeric"
          autoComplete="off"
          value={form.nationalID}
          error={fields['identifiers.national_id'] ?? fields.identifiers}
          onChange={(event) => set('nationalID', event.target.value)}
        />
      </Card>

      <Card className="app-register__section">
        <h2>{t('sections.emergency')}</h2>
        <div className="app-register__grid">
          <Input
            label={t('emergencyName')}
            value={form.emergencyName}
            autoComplete="off"
            onChange={(e) => set('emergencyName', e.target.value)}
          />
          <Input
            label={t('emergencyRelation')}
            value={form.emergencyRelation}
            autoComplete="off"
            onChange={(e) => set('emergencyRelation', e.target.value)}
          />
          <Input
            label={t('emergencyPhone')}
            inputMode="tel"
            autoComplete="off"
            value={form.emergencyPhone}
            error={fields['emergency_contact.phone']}
            onChange={(e) => set('emergencyPhone', e.target.value)}
          />
        </div>
      </Card>

      <Card className="app-register__section">
        <h2>{t('sections.socio')}</h2>
        <p className="app-register__note">{t('sections.socioNote')}</p>
        <div className="app-register__grid">
          <Coded
            name="education"
            value={form.education}
            onChange={(v) => set('education', v)}
            options={[
              'none',
              'primary',
              'secondary',
              'higher_secondary',
              'graduate',
              'postgraduate',
              'madrasa',
              'unknown',
            ]}
            error={fields['socioeconomic.education_level']}
          />
          <Coded
            name="occupation"
            value={form.occupation}
            onChange={(v) => set('occupation', v)}
            options={[
              'agriculture',
              'day_labour',
              'factory_worker',
              'service_private',
              'service_government',
              'business',
              'homemaker',
              'student',
              'retired',
              'unemployed',
              'other',
              'unknown',
            ]}
            error={fields['socioeconomic.occupation_category']}
          />
          <Coded
            name="income"
            value={form.income}
            onChange={(v) => set('income', v)}
            options={['under_10k', '10k_25k', '25k_50k', '50k_100k', 'over_100k', 'unknown']}
            error={fields['socioeconomic.income_band']}
          />
          <Coded
            name="residence"
            value={form.residence}
            onChange={(v) => set('residence', v)}
            options={['urban', 'semi_urban', 'rural', 'unknown']}
            error={fields['socioeconomic.residence_type']}
          />
          <Coded
            name="payer"
            value={form.payer}
            onChange={(v) => set('payer', v)}
            options={['self', 'family', 'employer', 'ngo', 'government', 'unknown']}
            error={fields['socioeconomic.medicine_payer']}
          />
          <Input
            label={t('household')}
            inputMode="numeric"
            autoComplete="off"
            value={form.household}
            error={fields['socioeconomic.household_size']}
            onChange={(e) => set('household', e.target.value.replace(/[^0-9]/g, '').slice(0, 2))}
          />
        </div>
      </Card>

      <Card className="app-register__section">
        <h2>{t('sections.consent')}</h2>
        <p className="app-register__note">{t('sections.consentNote')}</p>
        <Input
          label={t('consent')}
          data-testid="consent"
          required
          autoComplete="off"
          value={form.consentReference}
          error={fields.consent_reference}
          description={t('consentHelp')}
          onChange={(event) => set('consentReference', event.target.value)}
        />
      </Card>

      <div className="app-register__submit">
        <Button type="submit" variant="primary" disabled={!ready || blocked || submit.isPending}>
          {submit.isPending ? t('saving') : t('save')}
        </Button>
        <MissingList required={required} blocked={blocked} />
      </div>
    </form>
  );
}

/** A closed value list, labelled from the message catalogue so both languages stay in step. */
function Coded({
  name,
  value,
  options,
  onChange,
  error,
}: {
  name: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
  error?: string;
}) {
  const t = useTranslations('patients.register');
  return (
    <Select
      label={t(`socio.${name}`)}
      value={value}
      error={error}
      placeholder={t('notAsked')}
      onChange={(event) => onChange(event.target.value)}
      options={options.map((option) => ({ value: option, label: t(`values.${name}.${option}`) }))}
    />
  );
}

/**
 * What is still missing, named.
 *
 * A disabled button with no explanation is the commonest way a form wastes an operator's
 * time — they cannot tell whether the system is thinking or waiting for them.
 */
function MissingList({
  required,
  blocked,
}: {
  required: ReturnType<typeof requiredState>;
  blocked: boolean;
}) {
  const t = useTranslations('patients.register');
  if (blocked) return <p className="app-register__missing">{t('blocked')}</p>;

  const missing = Object.entries(required)
    .filter(([, done]) => !done)
    .map(([key]) => t(`missing.${key}`));
  if (missing.length === 0) return null;
  return (
    <p className="app-register__missing">{t('stillNeeded', { fields: missing.join(', ') })}</p>
  );
}

/**
 * What the desk does next: read the clinical id aloud, write it on the card, send the
 * patient to the next station. Printable on its own, because the card is printed.
 */
function RegisteredCard({ patient, onAnother }: { patient: Patient; onAnother: () => void }) {
  const t = useTranslations('patients.register');
  return (
    <section className="app-register__done" aria-live="polite">
      <AlertBanner tone="normal" title={t('done', { name: patient.name_en })} />
      <Card className="app-register__card">
        <p className="app-register__card-label">{t('clinicalId')}</p>
        <p className="app-register__card-id">{patient.clinical_id}</p>
        <p className="app-register__card-name">{patient.name_bn || patient.name_en}</p>
        <p className="app-register__card-meta">
          {t('cardMeta', { age: patient.birth.age, sex: t(`sexes.${patient.sex}`) })}
        </p>
      </Card>
      <div className="app-register__after">
        <Button variant="primary" onClick={() => window.print()}>
          {t('print')}
        </Button>
        <Button variant="secondary" onClick={onAnother}>
          {t('another')}
        </Button>
      </div>
      <p className="app-register__note">{t('nextStation')}</p>
    </section>
  );
}

function buildRegistration(
  form: FormState,
  parsed: ReturnType<typeof readDate>,
  eventID: string,
): PatientRegistration {
  const identifiers = form.nationalID.trim() ? { national_id: form.nationalID.trim() } : undefined;
  return {
    event_id: eventID,
    name_en: form.nameEN.trim(),
    name_bn: form.nameBN.trim(),
    sex: form.sex as PatientRegistration['sex'],
    birth_date: parsed?.iso ?? '',
    dob_precision: (parsed?.precision ?? 'day') as PatientRegistration['dob_precision'],
    dob_source: form.dobSource as PatientRegistration['dob_source'],
    phone_primary: form.phone.trim(),
    phone_secondary: form.phoneSecondary.trim(),
    division: form.division.trim(),
    district: form.district.trim(),
    upazila: form.upazila.trim(),
    address_line: form.addressLine.trim(),
    postcode: form.postcode.trim(),
    emergency_name: form.emergencyName.trim(),
    emergency_relation: form.emergencyRelation.trim(),
    emergency_phone: form.emergencyPhone.trim(),
    education_level: form.education as PatientRegistration['education_level'],
    occupation_category: form.occupation as PatientRegistration['occupation_category'],
    income_band: form.income as PatientRegistration['income_band'],
    household_size: form.household ? Number(form.household) : undefined,
    residence_type: form.residence as PatientRegistration['residence_type'],
    medicine_payer: form.payer as PatientRegistration['medicine_payer'],
    identifiers,
    consent_reference: form.consentReference.trim(),
  };
}

/**
 * The reader's language, read from the document rather than from next-intl.
 *
 * The root layout puts `lang` on `<html>`, so this is the same value the whole page is
 * rendered in — and it is what decides which of the server's two field-error languages to
 * show. Read here rather than passed in, because every caller would otherwise have to
 * remember to thread it through.
 */
function useLocaleTag(): string {
  return typeof document !== 'undefined' ? document.documentElement.lang || 'en' : 'en';
}
