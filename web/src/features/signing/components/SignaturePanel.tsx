'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useState } from 'react';

import { AlertBanner, Badge, Button, Card, Icon } from '@dthcms/ui';

import { StepUpCancelled, useStepUp } from '@/features/auth';
import { ApiError } from '@/lib/api';
import type { Locale } from '@/lib/i18n/config';

import { useSignCapability, type SignCapability } from '../api/capability';
import {
  readSignature,
  signPrescription,
  signatureKey,
  type SignatureView,
  type SigningResult,
} from '../api/signing';
import { SignatureImageNote } from './SignatureImageNote';
import { VerificationCode } from './VerificationCode';

/**
 * The signature on a prescription: making one, and reading the one that is there (CP84).
 *
 * # Five states, and the two that must not look alike
 *
 *  1. **Signed and verifying.** The sheet is what it was when it was signed.
 *  2. **Signed and not verifying.** Something covered by the signature has changed since. This is
 *     the loudest thing this component can draw, and it is drawn in `critical` rather than as a
 *     warning: the prescription in front of the physician is not the prescription he signed.
 *  3. **Ready to sign, by somebody who may.** The control, and what pressing it will cost.
 *  4. **Ready to sign, by somebody who may not.** No control, and a sentence saying whose act it
 *     is. Not a disabled button — see below.
 *  5. **Not ready.** No control at all, and the server's own sentence about *which* gate is shut.
 *
 * States 4 and 5 are the two the interface gets wrong. Both end in "no sign control", and a screen
 * that drew the same greyed-out button for them would be telling a QA officer that clearing her own
 * file is part of her job, and telling a consultant that his uncleared sheet is his own fault. They
 * are different sentences because they are different problems.
 *
 * # Absent, never disabled
 *
 * `prescription.sign` is PHYSICIAN's alone. The QA officer, the pharmacist and the prescription
 * education officer all reach the same sheet through `prescription.read` and not one of them may
 * sign it. A disabled button is still the interface saying *this act is yours and you cannot do it
 * right now*, which for them is false. The capability token makes the absence structural:
 * [SignControl] takes a `SignCapability` as a required prop, so a reader who does not hold the
 * permission cannot be passed one and the control cannot be rendered. This is CP83's mechanism and
 * CP92's defect, and signing is the sharpest place in the system for it — the refusal costs a
 * second factor to discover.
 *
 * # Why the reason comes from the server
 *
 * Three gates stand in front of a signature: the status machine, station 10's clearance, and the
 * fact that there is no re-signing. The prescriber can read none of them —
 * `GET /v1/prescriptions/{id}/qa` is behind `qa.review`, which PHYSICIAN does not hold. So the
 * server answers, in both languages, and this component renders the sentence rather than composing
 * one. A client-side copy of the rule would be a third implementation of a gate that already has
 * two, and the copy in the browser would be the one that disagreed.
 *
 * # The picture and the signature
 *
 * `docs/signing.md` §6. The handwritten image is a picture; the signature is a cryptographic fact.
 * They are drawn in separate blocks with separate headings and the picture's block says what it is
 * — see `SignatureImageNote.tsx`, where the reasoning belongs.
 */

export interface SignaturePanelProps {
  prescriptionId: string;
  /** How many live medicines are on the sheet, for the sentence before the press. */
  itemCount?: number;
}

export function SignaturePanel({ prescriptionId, itemCount }: SignaturePanelProps) {
  const t = useTranslations('signing');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';
  const maySign = useSignCapability();

  const view = useQuery({
    queryKey: signatureKey(prescriptionId),
    queryFn: () => readSignature(prescriptionId),
  });

  /**
   * The verification token, held in memory for this one render and nowhere else.
   *
   * The server returns it exactly once, at signing; what the database holds is its digest. It is
   * not written to `localStorage`, not put in a query cache key, and not fetched again — a
   * physician who navigates away before printing has to correct the prescription, which is the
   * right cost for a value that must not be re-derivable.
   */
  const [minted, setMinted] = useState<SigningResult | null>(null);

  if (view.isPending) return <p className="app-sign__note">{t('loading')}</p>;
  if (view.isError || !view.data) {
    return <AlertBanner tone="critical" title={t('unavailable')} />;
  }

  const data: SignatureView = view.data;

  return (
    <section className="app-sign" data-testid="signature-panel">
      {data.signed && data.verification ? (
        <SignedBlock view={data} minted={minted} />
      ) : (
        <UnsignedBlock
          prescriptionId={prescriptionId}
          view={data}
          itemCount={itemCount}
          maySign={maySign}
          onSigned={setMinted}
        />
      )}

      {/* Drawn in both states. A screen that showed the caveat only once there was a signature
          would be teaching whoever adds the picture at CP89 that the caveat belongs to the
          signature, when it belongs to the picture. */}
      <SignatureImageNote caveat={data.signature_image} bn={bn} />
    </section>
  );
}

/* ------------------------------------------------------------------------- */
/* Signed                                                                     */
/* ------------------------------------------------------------------------- */

function SignedBlock({ view, minted }: { view: SignatureView; minted: SigningResult | null }) {
  const t = useTranslations('signing');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';
  const verification = view.verification!;
  const signature = verification.signature;
  const verified = verification.verdict === 'VERIFIED';

  return (
    <div className="app-stack">
      <div data-testid={verified ? 'signature-verified' : 'signature-not-verified'}>
        <AlertBanner
          tone={verified ? 'normal' : 'critical'}
          title={verified ? t('signed.verifiedTitle') : t('signed.notVerifiedTitle')}
        >
          <p style={{ margin: 0 }}>
            {verified
              ? t('signed.verifiedBody')
              : (bn ? verification.reason_bn : verification.reason_en) ||
                t('signed.notVerifiedBody')}
          </p>
        </AlertBanner>
      </div>

      {/* Criterion: a signed prescription is **visibly** immutable. Said in words, before
          anybody reaches for a control, rather than discovered by pressing one and being
          refused — which is what the editor did before CP84 and what a database trigger does
          underneath it still. */}
      <Card compact header={<strong>{t('signed.immutableTitle')}</strong>}>
        <p style={{ margin: 0 }} data-testid="immutable-note">
          {t('signed.immutableBody')}
        </p>
        <p style={{ margin: '0.4rem 0 0', opacity: 0.8 }}>{t('signed.correctionNote')}</p>
      </Card>

      <Card compact header={<strong>{t('signed.factsTitle')}</strong>}>
        <dl className="app-sign__facts">
          <Fact label={t('signed.by')}>
            {(bn ? signature.signed_by_name_bn : signature.signed_by_name_en) ||
              signature.signed_by_code ||
              ''}
          </Fact>
          <Fact label={t('signed.on')}>{new Date(signature.signed_at).toLocaleString(locale)}</Fact>
          <Fact label={t('signed.algorithm')}>
            <span className="dthc-mono">{signature.algorithm}</span>
          </Fact>
          <Fact label={t('signed.keyId')}>
            <span className="dthc-mono">{signature.key_id}</span>
          </Fact>
          <Fact label={t('signed.assurance')}>{t(`assurance.${signature.device_assurance}`)}</Fact>
        </dl>
      </Card>

      {/*
        docs/signing.md §2, on the screen a physician reads rather than in a commit message.
        The local signer's key is a file in this deployment's configuration; anybody who can
        read the configuration can copy it, and a copied key signs. The acceptance criterion
        "the signing key is non-exportable" is **not met** by it and cannot be, and a screen that
        stayed quiet about that would be the clinic believing something about its own
        prescriptions that is not true.
      */}
      {!signature.non_exportable_key && (
        <div data-testid="weak-key">
          <AlertBanner tone="high" title={t('signed.weakKeyTitle')}>
            <p style={{ margin: 0 }}>{t('signed.weakKeyBody')}</p>
          </AlertBanner>
        </div>
      )}

      {minted ? (
        <VerificationCode token={minted.verification_token} path={minted.verification_path} />
      ) : (
        // Honest about why the code is not on this screen: it is not retrievable. A blank space
        // would read as a loading failure.
        <p className="app-sign__note" data-testid="token-gone">
          {t('signed.tokenGone')}
        </p>
      )}
    </div>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="app-sign__fact">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

/* ------------------------------------------------------------------------- */
/* Not signed                                                                 */
/* ------------------------------------------------------------------------- */

function UnsignedBlock({
  prescriptionId,
  view,
  itemCount,
  maySign,
  onSigned,
}: {
  prescriptionId: string;
  view: SignatureView;
  itemCount?: number;
  maySign: SignCapability | null;
  onSigned: (result: SigningResult) => void;
}) {
  const t = useTranslations('signing');
  const locale = useLocale() as Locale;
  const bn = locale === 'bn';
  const readiness = view.readiness;

  // The gate is shut. No control, and the server's own sentence about which one.
  if (!readiness.may_sign) {
    return (
      <div data-testid="signing-blocked">
        <AlertBanner tone="unknown" title={t('blocked.title')}>
          <p style={{ margin: 0 }} data-testid="signing-blocked-reason">
            {(bn ? readiness.reason_bn : readiness.reason_en) ?? ''}
          </p>
        </AlertBanner>
      </div>
    );
  }

  // The gate is open and this reader is not the one it opens for. A different sentence, because
  // it is a different problem: nothing is wrong with the prescription.
  if (!maySign) {
    return (
      <div data-testid="signing-not-yours">
        <AlertBanner tone="info" title={t('notYours.title')}>
          <p style={{ margin: 0 }}>{t('notYours.body')}</p>
        </AlertBanner>
      </div>
    );
  }

  return (
    <SignControl
      prescriptionId={prescriptionId}
      itemCount={itemCount}
      maySign={maySign}
      onSigned={onSigned}
    />
  );
}

/* ------------------------------------------------------------------------- */
/* The control                                                                */
/* ------------------------------------------------------------------------- */

/**
 * The act itself.
 *
 * # `maySign` is required and unused, and that is the point
 *
 * The token carries no data. What it does is make this component unrenderable by a caller who has
 * not asked the permission question — `tsc` refuses the call site, rather than a reviewer noticing
 * it. Reading the permission inside here instead would put the check somewhere a future caller
 * could bypass by rendering the control in another screen.
 *
 * # The second factor is announced before it is asked for
 *
 * Signing is the one act in this system that creates a medico-legal document, and the step-up is
 * there to re-prove the person at that moment. A control that silently opened an authenticator
 * dialog would make the second factor feel like an obstacle the software put in the way; a control
 * that says, before the press, that it will ask makes it part of the act. The sentence above the
 * button is not decoration.
 *
 * # What is being signed is on the screen before the press
 *
 * The count of medicines, and the statement that it cannot be undone. Not an attestation tick —
 * a tick this screen would not send anywhere is a ceremony, and a ceremony nobody records is worse
 * than no ceremony, because it looks like evidence.
 */
function SignControl({
  prescriptionId,
  itemCount,
  maySign,
  onSigned,
}: {
  prescriptionId: string;
  itemCount?: number;
  maySign: SignCapability;
  onSigned: (result: SigningResult) => void;
}) {
  void maySign;
  const t = useTranslations('signing');
  const queryClient = useQueryClient();
  const requestStepUp = useStepUp();
  const [failure, setFailure] = useState<string | null>(null);

  const sign = useMutation({
    mutationFn: async () => {
      // Asked for **before** the request, and the token is a required argument to the call: a
      // caller who has not minted one does not compile. The server refuses without it too, and
      // consumes it on use, so a replayed token is refused by its second presentation.
      const token = await requestStepUp('prescription.sign', t('ready.stepUpPurpose'));
      return signPrescription(prescriptionId, crypto.randomUUID(), token);
    },
    onSuccess: async (result) => {
      setFailure(null);
      onSigned(result);
      await queryClient.invalidateQueries({ queryKey: signatureKey(prescriptionId) });
      await queryClient.invalidateQueries({ queryKey: ['prescriptions', prescriptionId] });
    },
    onError: (error) => {
      // A physician who closed the authenticator dialog has decided not to sign. That is not a
      // failure and must not be reported as one.
      if (error instanceof StepUpCancelled) return;
      setFailure(error instanceof ApiError && error.message ? error.message : t('ready.failed'));
    },
  });

  return (
    <Card compact header={<strong>{t('ready.title')}</strong>}>
      <div className="app-stack" data-testid="sign-card">
        <p style={{ margin: 0 }}>
          {itemCount === undefined ? t('ready.bodyNoCount') : t('ready.body', { count: itemCount })}
        </p>
        <p style={{ margin: 0 }}>{t('ready.irreversible')}</p>
        <p style={{ margin: 0, opacity: 0.8 }}>
          <Icon name="alert-triangle" size={14} /> {t('ready.stepUpNote')}
        </p>
        {failure && <AlertBanner tone="critical" title={failure} />}
        <div>
          <Button
            variant="primary"
            data-testid="sign-prescription"
            disabled={sign.isPending}
            onClick={() => sign.mutate()}
          >
            {sign.isPending ? t('ready.saving') : t('ready.action')}
          </Button>
        </div>
        <Badge tone="neutral">{t('ready.whoCanSign')}</Badge>
      </div>
    </Card>
  );
}
