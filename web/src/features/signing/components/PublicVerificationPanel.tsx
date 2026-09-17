'use client';

import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';

import { AlertBanner, Card, Icon } from '@dthcms/ui';

import { verifyPublicly, type PublicVerification } from '../api/signing';

/**
 * The public verification page (CP85).
 *
 * # Who is reading this, and what they came to find out
 *
 * A pharmacist in Faridpur, holding a printed sheet, who has just pointed a phone camera at a QR
 * code. They have no account, no session and no reason to trust us. They came to answer one
 * question — *did this clinic issue this, and is it the same paper it issued* — and they need the
 * answer in four seconds, from across a counter, in whichever of two languages they read.
 *
 * Everything below follows from that, and from the other fact: **this is the only unauthenticated
 * surface in the system besides login**, so a fraction of the people reaching it are looking for a
 * way in.
 *
 * # What may appear on this page
 *
 * The issuing physician, the issuing clinic, the date, the prescription id, the verdict and the
 * number of medicines. That is the whole list, it is decided by the server, and this component
 * renders the fields of one object rather than picking from a larger one — there is no clinical
 * detail *available* here to leak. No patient in any part, no diagnosis, no medicine, no dose, no
 * visit. The item **count** is a count; "does the paper in my hand have this many lines" is
 * answerable from it and nothing about what they are is.
 *
 * Five things that are not clinical detail are also absent, and they were the harder decision: the
 * signature bytes, the public key, the key id, the signer kind and the device assurance. A public
 * page that published which prescriptions were signed with a development key would be publishing a
 * target list.
 *
 * # An unverified answer says one thing and offers nothing
 *
 * An unknown token, a mistyped one, a rate-limited request and a prescription that has been altered
 * are **one screen**. That is a deliberate loss of helpfulness: a page that said "this exists but
 * has been altered" for one and "no such code" for another would be a membership oracle over the
 * token space, and would tell somebody forging a prescription that they had the token right and
 * only the content wrong.
 *
 * It also shows **no physician's name** on that screen, which is the reason the block is withheld
 * rather than drawn with a warning across it: printing a named colleague's name beside "this has
 * been altered" would publish an accusation about them to anybody holding a forged piece of paper.
 *
 * # Trustworthy, and not reassuring
 *
 * The verified state is deliberately plain — the clinic's name, the physician's, the date, the
 * count, and a sentence the server wrote. No badge, no seal, no lock icon promising more than a
 * signature check delivers. The unverified state does not accuse; it says the code could not be
 * confirmed and tells the reader to ask the clinic, which is the only action a pharmacist at a
 * counter can actually take.
 */
export function PublicVerificationPanel({ token }: { token: string }) {
  const t = useTranslations('verify');

  const answer = useQuery({
    // The token is in the query key because it is what the query is about, and this cache lives
    // for the life of one page in one stranger's browser. It is not persisted anywhere.
    queryKey: ['verify', token],
    queryFn: () => verifyPublicly(token),
    // One attempt. A page that retried would multiply a stranger's load on the one endpoint that
    // is rate-limited by address, and would make a refused request look like a slow one.
    retry: false,
    refetchOnWindowFocus: false,
  });

  if (answer.isPending) {
    return (
      <div className="app-verify__state" data-testid="verify-checking">
        <p>{t('checking')}</p>
      </div>
    );
  }

  if (answer.isError) {
    // **Not a verdict.** The endpoint answers 200 for both verdicts; anything else is our problem
    // or the network's, and inventing NOT_VERIFIED from it would put an accusation on the screen
    // because a phone lost signal.
    return (
      <div data-testid="verify-unreachable">
        <AlertBanner tone="unknown" title={t('unreachableTitle')}>
          <p style={{ margin: 0 }}>{t('unreachableBody')}</p>
        </AlertBanner>
      </div>
    );
  }

  const body: PublicVerification = answer.data;
  const verified = body.verdict === 'VERIFIED';

  return (
    <div className="app-stack">
      <div
        className={verified ? 'app-verify__verdict app-verify__verdict--ok' : 'app-verify__verdict'}
        data-testid={verified ? 'verify-genuine' : 'verify-not-verified'}
      >
        <AlertBanner
          tone={verified ? 'normal' : 'critical'}
          title={verified ? t('genuineTitle') : t('notVerifiedTitle')}
        >
          {/* **Both languages, always**, and not the one the interface happens to be in. A
              stranger has no stored preference — this page opens in the default for exactly the
              reader most likely to read the other one — and the sentence a pharmacist acts on is
              the one thing on the page that must not depend on that. The print preview shows both
              for the same reason: the paper is read by two people. */}
          <p style={{ margin: 0 }} data-testid="verify-message">
            {body.message_en}
          </p>
          <p style={{ margin: '0.4rem 0 0' }} lang="bn" data-testid="verify-message-bn">
            {body.message_bn}
          </p>
        </AlertBanner>
      </div>

      {verified && body.prescription ? (
        <Card header={<strong>{t('detailsTitle')}</strong>}>
          <dl className="app-verify__facts" data-testid="verify-facts">
            {/* A name in both scripts, for the same reason the message is: whoever is holding
                the paper reads one of them, and which one is not knowable from here. */}
            <Row label={t('clinic')}>
              <Bilingual
                en={body.prescription.facility_name_en}
                bn={body.prescription.facility_name_bn}
              />
            </Row>
            <Row label={t('physician')}>
              <Bilingual
                en={body.prescription.physician_name_en}
                bn={body.prescription.physician_name_bn}
              />
            </Row>
            {/* The date in words, in both scripts, exactly as the names are — and **not** in the
                monospace face the prescription reference uses. The reference is an identifier a
                pharmacist reads off a sheet character by character; the date is a sentence. */}
            <Row label={t('issuedOn')}>
              <Bilingual en={body.prescription.issued_on_en} bn={body.prescription.issued_on_bn} />
            </Row>
            <Row label={t('itemCount')}>
              {t('itemCountValue', { count: body.prescription.item_count })}
            </Row>
            <Row label={t('prescriptionId')}>
              <span className="dthc-mono app-verify__id">{body.prescription.id}</span>
            </Row>
          </dl>
        </Card>
      ) : null}

      {/* Said on both verdicts, and said as a *limit* rather than as an apology. A pharmacist who
          expected to see the medicines needs to know this page will never show them, so that a page
          which does show them is recognisable as not being this one. */}
      <p className="app-verify__limit" data-testid="verify-limit">
        <Icon name="help-circle" size={16} /> {t('noClinicalDetail')}
      </p>

      {!verified ? <p className="app-verify__ask">{t('askClinic')}</p> : null}
    </div>
  );
}

/** A name as both scripts carry it, or as whichever one the clinic recorded. */
function Bilingual({ en, bn }: { en: string; bn: string }) {
  if (!en) return <span lang="bn">{bn}</span>;
  if (!bn || bn === en) return <span>{en}</span>;
  return (
    <>
      <span>{en}</span>
      <span className="app-verify__alt" lang="bn">
        {bn}
      </span>
    </>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="app-verify__fact">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}
