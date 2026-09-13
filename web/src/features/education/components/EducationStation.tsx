'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useMemo, useState } from 'react';

import { Button } from '@dthcms/ui';

import { ApiError } from '@/lib/api';
import type { Locale } from '@/lib/i18n/config';

import {
  EDUCATION_REFERENCE_KEY,
  educationSessionKey,
  notApplicableReasonOf,
  readEducationReference,
  readEducationSession,
  recordAssessment,
  scoreOf,
  tally,
  type EducationAssessmentResult,
  type EducationCompetency,
  type EducationReference,
  type EducationSession,
  type EducationState,
} from '../api/education';

import { useRecordingCapability } from '../api/capability';

import { ComplianceQuestion } from './ComplianceQuestion';
import { ImprovementScoreSelector } from './ImprovementScoreSelector';
import { RecordedAssessment } from './RecordedAssessment';
import { ReeducationNotice } from './ReeducationNotice';
import { TechniqueChecklist } from './TechniqueChecklist';

/**
 * Station 11's screen (CP88, CP92).
 *
 * # The order of the screen is the order of the station
 *
 * The technique checklists first, because that is what the officer does with the patient in
 * front of them and the device in their hands. The compliance question second, because it is a
 * conversation and it follows naturally from having just watched them use the thing. The
 * improvement score **last**, and that is not a layout preference — §1 puts it at the end of the
 * journey, once the prescription is already written and cannot be changed by what the patient
 * says. A screen that asked it first would put the question back in the middle of a clinical
 * encounter, which is most of what CP88's decision is trying to move it out of.
 *
 * # One save, and why there is no per-item save
 *
 * CP56's counselling checklist saves one tick at a time, deliberately: twenty topics covered has
 * to be twenty acts by whoever covered them. This is the opposite case and the difference is
 * real. One officer watches one demonstration, once, and scores ten lines about the one thing
 * they just watched. Ten saves would record ten timestamps for a single event, and — worse —
 * would let a half-written assessment sit in the record looking complete.
 *
 * # Two modes, and the second is not the first with its buttons off
 *
 * The nav entry is on `education.read`, which the physician holds so that CP92's second
 * acceptance criterion — competency visible at the next consultation — is reachable. That means
 * this screen has two readers with opposite relationships to it, and it had better know which one
 * it has: the officer records, and the consultant reads.
 *
 * `useRecordingCapability` is the one place that decides. It returns a token, not a boolean, and
 * every writable control in this feature takes that token as a required prop — so the read branch
 * below cannot accidentally render one, because the value it would have to pass is `null` and the
 * compiler says so. The physician's branch mounts `RecordedAssessment`, which has no controls in
 * it at all.
 *
 * This existed as a defect first: the feature shipped with no permission check anywhere, and a
 * consultant opening the station was handed the whole form — ten live state buttons, the
 * missed-dose row, and the improvement score selector. Every one of them answers 403. The score
 * is the part that matters: CP88 §1 moves that question away from the consultation so that the
 * person whose treatment it grades is not in the loop, and a screen letting him hover over an 8
 * is that failure arriving through the interface instead of through the ledger.
 *
 * # Nothing on this screen decides the flag
 *
 * The tally is shown live, because an officer wants to know where they are. Whether the flag
 * fires is the server's and is shown only after the save, from what the server stored. The two
 * are deliberately not the same component: a screen that computed the flag itself would
 * eventually disagree with the record, and the disagreement would be invisible.
 */

export interface EducationStationProps {
  patientId: string;
  visitId: string;
}

export function EducationStation({ patientId, visitId }: EducationStationProps) {
  const t = useTranslations('education');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';
  const queryClient = useQueryClient();
  const capability = useRecordingCapability();

  const reference = useQuery({
    queryKey: EDUCATION_REFERENCE_KEY,
    queryFn: readEducationReference,
    // Reference data with no patient in it. A station app fetches it once and keeps working on
    // it for the rest of the clinic session, which is the point of it being its own endpoint.
    staleTime: 60 * 60 * 1000,
  });

  const session = useQuery({
    queryKey: educationSessionKey(patientId, visitId),
    queryFn: () => readEducationSession(patientId, visitId),
  });

  const [answers, setAnswers] = useState<Record<string, EducationState>>({});
  const [missed, setMissed] = useState<number | null>(null);
  const [reasons, setReasons] = useState<string[]>([]);
  const [score, setScore] = useState<number | null>(null);
  const [notApplicable, setNotApplicable] = useState<string | null>(null);
  const [result, setResult] = useState<EducationAssessmentResult | null>(null);

  const save = useMutation({
    mutationFn: () =>
      recordAssessment(patientId, {
        // A fresh id per attempt, minted here so that a retry of *this* attempt is the same
        // assessment. The server derives every value's ledger id from it, which is what makes a
        // lost reply safe to press save on again.
        event_id: crypto.randomUUID(),
        visit_id: visitId,
        items: Object.entries(answers).map(([code, state]) => ({ code, state })),
        compliance: { missed_doses: missed, reasons },
        improvement:
          score !== null
            ? { score }
            : notApplicable !== null
              ? { not_applicable_reason: notApplicable }
              : null,
      }),
    onSuccess: (saved) => {
      setResult(saved);
      void queryClient.invalidateQueries({ queryKey: educationSessionKey(patientId, visitId) });
    },
  });

  // What the last officer saw, by item. A map rather than a search per row: a patient on two
  // devices is twenty rows, and twenty linear scans of a list is the kind of thing that is fine
  // until somebody adds a third checklist.
  const previous = useMemo(() => {
    const index = new Map<string, EducationCompetency>();
    for (const entry of session.data?.prior_competency ?? []) index.set(entry.code, entry);
    return index;
  }, [session.data]);

  if (reference.isPending || session.isPending) {
    return <p className="app-page__description">{t('loading')}</p>;
  }
  if (reference.isError || session.isError || !reference.data || !session.data) {
    return (
      <p className="app-page__description" role="alert">
        {t('unavailable')}
      </p>
    );
  }

  const view = session.data;

  // The read branch. Taken before anything writable is built, so there is one line in this file
  // where the two audiences part and it is not buried inside a render.
  if (capability === null) {
    return (
      <div className="edu-station app-stack" data-testid="education-station" data-mode="read">
        <DeviceSummary view={view} reference={reference.data} bn={bn} />
        {view.reeducation_flagged && (
          <ReeducationNotice
            raised
            standing
            unable={0}
            correctedToday={0}
            threshold={reference.data.reeducation_policy.corrected_today_threshold}
          />
        )}
        <RecordedAssessment session={view} reference={reference.data} />
      </div>
    );
  }

  const counts = tally(answers);
  const alreadyAnswered = scoreOf(view.improvement) ?? null;
  const alreadyNotApplicable = notApplicableReasonOf(view.improvement);

  return (
    <div className="edu-station app-stack" data-testid="education-station" data-mode="record">
      <DeviceSummary view={view} reference={reference.data} bn={bn} />

      {view.checklists.map((checklist) => (
        <TechniqueChecklist
          key={checklist.code}
          capability={capability}
          checklist={checklist}
          states={reference.data.states}
          answers={answers}
          previous={previous}
          disabled={save.isPending}
          onAnswer={(code, state) => setAnswers((current) => ({ ...current, [code]: state }))}
        />
      ))}

      {view.checklists.length > 0 && (
        <p className="edu-tally" data-testid="tally">
          {t('tally', {
            demonstrated: counts.demonstrated,
            correctedToday: counts.correctedToday,
            unable: counts.unable,
          })}
        </p>
      )}

      <ComplianceQuestion
        capability={capability}
        reference={reference.data}
        missed={missed}
        reasons={reasons}
        disabled={save.isPending}
        onMissed={(count) => {
          setMissed(count);
          // Clearing the count clears the reasons with it. A coded reason left behind on a
          // patient who turns out to have missed none is a row that means nothing and will be
          // counted by something.
          if (count === null || count === 0) setReasons([]);
        }}
        onToggleReason={(code) =>
          setReasons((current) =>
            current.includes(code) ? current.filter((one) => one !== code) : [...current, code],
          )
        }
      />

      <ImprovementScoreSelector
        capability={capability}
        scale={reference.data.score_scale}
        reasons={reference.data.not_applicable_reasons}
        value={score ?? alreadyAnswered}
        notApplicable={notApplicable ?? alreadyNotApplicable}
        firstVisit={view.first_visit}
        disabled={save.isPending}
        onPick={(value) => {
          setScore(value);
          setNotApplicable(null);
        }}
        onNotApplicable={(reason) => {
          setNotApplicable(reason);
          setScore(null);
        }}
      />

      {result ? (
        <ReeducationNotice
          raised={result.reeducation_flagged}
          unable={result.unable}
          correctedToday={result.corrected_today}
          threshold={reference.data.reeducation_policy.corrected_today_threshold}
        />
      ) : (
        view.reeducation_flagged && (
          <ReeducationNotice
            raised
            standing
            unable={0}
            correctedToday={0}
            threshold={reference.data.reeducation_policy.corrected_today_threshold}
          />
        )
      )}

      <div className="edu-save">
        <Button
          variant="primary"
          size="lg"
          data-testid="education-save"
          loading={save.isPending}
          loadingLabel={t('saving')}
          onClick={() => save.mutate()}
        >
          {t('save')}
        </Button>
        {save.isError && (
          <p className="edu-save__error" role="alert" data-testid="education-error">
            {save.error instanceof ApiError ? save.error.message : t('saveFailed')}
          </p>
        )}
        {result && (
          <p className="edu-save__done" data-testid="education-saved">
            {t('saved', { count: result.observations.length })}
          </p>
        )}
      </div>
    </div>
  );
}

/**
 * Which checklists this prescription brought up, and which line brought each.
 *
 * Shared by both modes deliberately. The officer needs it because criterion 1 is that they chose
 * none of this and are owed the reason it is on their screen; the physician needs the same list
 * for the opposite reason — to see which device the patient was actually assessed against before
 * reading how they did. A second copy for the read mode is how the two would come to disagree
 * about what "brings up" means.
 */
function DeviceSummary({
  view,
  reference,
  bn,
}: {
  view: EducationSession;
  reference: EducationReference;
  bn: boolean;
}) {
  const t = useTranslations('education');

  return (
    <section className="edu-devices" data-testid="selected-devices">
      <h2 className="edu-devices__title">{t('devices.title')}</h2>
      {view.selected_devices.length === 0 ? (
        // "Nothing here is a device" is only said when that is the whole truth. A line the
        // station could not classify is a device — one nobody has told it about — and printing
        // the reassuring sentence above the warning would contradict it in the same card,
        // which is worse than printing neither.
        view.unclassified_devices.length === 0 && (
          <p className="edu-devices__none" data-testid="no-devices">
            {t('devices.none')}
          </p>
        )
      ) : (
        <ul className="edu-devices__list">
          {view.selected_devices.map((device) => (
            <li key={`${device.checklist_code}:${device.product_id}`}>
              <span className="edu-devices__product">{device.product_label}</span>
              <span className="edu-devices__because">
                {t('devices.because', {
                  checklist: titleOf(reference, device.checklist_code, bn),
                })}
              </span>
            </li>
          ))}
        </ul>
      )}

      {/*
            A line this station ought to recognise and does not.

            Drawn as a warning rather than left out, because the alternative is the failure the
            server refuses to commit: a GLP-1 with no rule of its own inherits no checklist — a
            checklist teaches a dosing rhythm, and semaglutide's would tell a liraglutide patient
            to inject on Fridays. Silence is the safe answer only if somebody is told, and the
            officer with the patient in front of them is the only person who can act on it today.
          */}
      {view.unclassified_devices.length > 0 && (
        <div className="edu-devices__unclassified" role="status" data-testid="unclassified-devices">
          <p className="edu-devices__unclassified-lead">{t('devices.unrecognised')}</p>
          <ul className="edu-devices__list">
            {view.unclassified_devices.map((device) => (
              <li key={device.product_id}>
                <span className="edu-devices__product">{device.generic_name}</span>
                <span className="edu-devices__because">
                  {t('devices.unrecognisedClass', {
                    className: bn ? device.class_name_bn : device.class_name_en,
                  })}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

function titleOf(
  reference: { checklists: { code: string; title_en: string; title_bn: string }[] },
  code: string,
  bn: boolean,
): string {
  const found = reference.checklists.find((checklist) => checklist.code === code);
  if (!found) return code;
  return bn ? found.title_bn : found.title_en;
}
