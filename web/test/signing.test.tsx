import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { PublicVerification, SignatureView } from '@/features/signing';

import { renderWithProviders, messages } from './render';

/**
 * What the signing screens and the public verification page tell the person in front of them
 * (CP84, CP85).
 *
 * The cryptography is proven in Go, against a real database, by altering a stored field and
 * watching verification fail. What can only be proven here is **what a person ends up believing**,
 * and the three ways that fails are all quiet:
 *
 *  - **A sign control handed to somebody the server will refuse.** This is the CP92 defect on the
 *    most consequential write in this system, and it is worse here than it was at station 11
 *    because the refusal costs a second factor to discover: the person finds their phone, types a
 *    code, and *then* learns they may not sign. Two separate versions of it are guarded below —
 *    the reader who may not sign at all, and the prescription that may not be signed yet — because
 *    they are different mistakes with the same symptom.
 *  - **A signed prescription that still looks editable.** Before CP84 the editor drew the entry
 *    row for a frozen sheet; the person typed a medicine and was told by a 409.
 *  - **Clinical detail on the public page.** One leak there is a patient record with a URL, and it
 *    would be reached by a stranger with a phone.
 *
 * Every test goes through the real `usePermission` against the real session store, because the
 * defect being guarded against is a screen that never asked the question — and a test that
 * answered it on the screen's behalf would not have caught it either.
 */

const readSignature = vi.hoisted(() => vi.fn());
const signPrescription = vi.hoisted(() => vi.fn());
const verifyPublicly = vi.hoisted(() => vi.fn());

vi.mock('@/features/signing/api/signing', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/signing/api/signing')>()),
  readSignature,
  signPrescription,
  verifyPublicly,
}));

const { SignaturePanel } = await import('@/features/signing/components/SignaturePanel');
const { PublicVerificationPanel } =
  await import('@/features/signing/components/PublicVerificationPanel');
const { useSessionStore } = await import('@/stores/session');
const { StepUpProvider } = await import('@/features/auth');

const SHEET = '0190a8f2-0000-7000-8000-0000000000c1';
const NAHID = '0190d820-0000-7000-8000-000000009912';
const SHIRIN = '0190d820-0000-7000-8000-000000009911';

const initialSession = useSessionStore.getInitialState();

/** The consultant: the only person in the clinic who may sign. */
const CONSULTANT = ['prescription.read', 'prescription.draft', 'prescription.sign', 'qa.clear'];
/** The QA officer: reads the same sheet, and may not sign it. */
const QA_OFFICER = ['prescription.read', 'qa.review', 'qa.clear', 'qa.bounce'];
/** A junior doctor: may write a prescription and may not sign one. */
const JUNIOR = ['prescription.read', 'prescription.draft'];

function holding(role: string, permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: role === 'PHYSICIAN' ? NAHID : SHIRIN,
      employeeCode: role === 'PHYSICIAN' ? 'E001' : 'E412',
      nameEN: role === 'PHYSICIAN' ? 'Dr K M Nahid Ul Haque' : 'Shirin Akhter',
      nameBN: role === 'PHYSICIAN' ? 'ডা. কে এম নাহিদ উল হক' : 'শিরীন আক্তার',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: [role],
      grants: { [role]: permissions },
      permissions,
      secondFactor: { required: true, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: role,
  });
}

function renderPanel(locale: 'en' | 'bn' = 'en') {
  return renderWithProviders(
    <StepUpProvider>
      <SignaturePanel prescriptionId={SHEET} itemCount={3} />
    </StepUpProvider>,
    { locale },
  );
}

const IMAGE_CAVEAT = {
  present_on_file: false,
  caveat_en:
    'This handwritten signature is a picture for readability. What proves this prescription has ' +
    'not been altered is the QR code, not the image.',
  caveat_bn:
    'হাতে লেখা এই স্বাক্ষরটি কেবল পড়ার সুবিধার জন্য একটি ছবি। এই ব্যবস্থাপত্র বদলানো হয়নি — তা ' +
    'প্রমাণ করে কিউআর কোড, ছবিটি নয়।',
};

/** Submitted, and station 10 has not decided. The server's own sentence. */
function uncleared(): SignatureView {
  return {
    signed: false,
    readiness: {
      may_sign: false,
      signed: false,
      cleared: false,
      status: 'QA_REVIEW',
      reason_en:
        'Station 10 has not cleared this prescription. It cannot be signed until the quality ' +
        'review clears it.',
      reason_bn:
        '১০ নম্বর কেন্দ্র এই ব্যবস্থাপত্রে ছাড়পত্র দেয়নি। মান যাচাইয়ের ছাড়পত্র না পাওয়া পর্যন্ত ' +
        'স্বাক্ষর করা যাবে না।',
    },
    signature_image: IMAGE_CAVEAT,
  };
}

/** Cleared by station 10, waiting for the consultant. */
function cleared(): SignatureView {
  return {
    signed: false,
    readiness: { may_sign: true, signed: false, cleared: true, status: 'QA_REVIEW' },
    signature_image: IMAGE_CAVEAT,
  };
}

function signature() {
  return {
    prescription_id: SHEET,
    facility_id: '11111111-1111-4111-8111-111111111111',
    canonical_version: 1,
    canonical_sha256: '9f2c1f0e5b7a6d4c3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d',
    algorithm: 'Ed25519' as const,
    signer_kind: 'LOCAL' as const,
    key_id: 'local-dev-1',
    public_key: '00'.repeat(32),
    signature: '01'.repeat(64),
    signed_at: '2026-09-14T09:45:00Z',
    signed_by: NAHID,
    signed_by_code: 'E001',
    signed_by_name_en: 'Dr K M Nahid Ul Haque',
    signed_by_name_bn: 'ডা. কে এম নাহিদ উল হক',
    device_assurance: 'NAMED' as const,
    // The clearance by value, both halves — what verification recomputes the canonical bytes
    // from, so that a rewritten `decided_at` at station 10 cannot make an untouched prescription
    // read as altered.
    qa_review_id: '0190a8f2-0000-7000-8000-0000000000d1',
    qa_cleared_at: '2026-09-14T09:35:00Z',
    non_exportable_key: false,
  };
}

function signed(verdict: 'VERIFIED' | 'NOT_VERIFIED' = 'VERIFIED'): SignatureView {
  return {
    signed: true,
    readiness: {
      may_sign: false,
      signed: true,
      cleared: true,
      status: 'SIGNED',
      reason_en: 'This prescription has already been signed.',
      reason_bn: 'এই ব্যবস্থাপত্রে ইতিমধ্যেই স্বাক্ষর করা হয়েছে।',
    },
    verification: {
      prescription_id: SHEET,
      verdict,
      signature: signature(),
      recomputed_canonical_sha256:
        verdict === 'VERIFIED'
          ? '9f2c1f0e5b7a6d4c3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d'
          : 'aa11bb22cc33dd44ee55ff6600778899aabbccddeeff00112233445566778899',
      ...(verdict === 'VERIFIED'
        ? {}
        : {
            reason_en: 'This prescription is not what it was when it was signed.',
            reason_bn: 'স্বাক্ষরের সময় এই ব্যবস্থাপত্র যেমন ছিল, এখন তেমন নেই।',
          }),
    },
    signature_image: IMAGE_CAVEAT,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  readSignature.mockResolvedValue(cleared());
});

/* ------------------------------------------------------------------------- */
/* Who is handed the sign control                                             */
/* ------------------------------------------------------------------------- */

describe('who is offered a signature', () => {
  it('offers the control to the consultant on a cleared prescription', async () => {
    holding('PHYSICIAN', CONSULTANT);

    renderPanel();

    expect(await screen.findByTestId('sign-prescription')).toBeInTheDocument();
    // And it says what pressing it will cost, before the press.
    expect(screen.getByText(/asked for your authenticator code/i)).toBeInTheDocument();
    expect(screen.getByText(/Signing freezes the prescription/i)).toBeInTheDocument();
  });

  it('offers a QA officer who may read the prescription no sign control at all', async () => {
    // The CP92 defect. She holds `prescription.read` and reaches the same sheet; she does not
    // hold `prescription.sign` and the server would refuse her — *after* she had found her phone
    // and typed a code, which is the part that makes a disabled button worse than none.
    holding('QA', QA_OFFICER);

    renderPanel();

    expect(await screen.findByTestId('signing-not-yours')).toBeInTheDocument();
    expect(screen.queryByTestId('sign-prescription')).not.toBeInTheDocument();
    expect(screen.queryByTestId('sign-card')).not.toBeInTheDocument();
    expect(screen.getByText(/prescribing consultant's act/i)).toBeInTheDocument();
  });

  it('offers a junior doctor who may draft a prescription no sign control', async () => {
    // `prescription.draft` is not `prescription.sign`, and a screen that keyed the control off
    // "may prescribe" would hand it to exactly the person the separation exists for.
    holding('JUNIOR_DOCTOR', JUNIOR);

    renderPanel();

    expect(await screen.findByTestId('signing-not-yours')).toBeInTheDocument();
    expect(screen.queryByTestId('sign-prescription')).not.toBeInTheDocument();
  });

  it('offers no sign control on an uncleared prescription, and names station 10', async () => {
    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(uncleared());

    renderPanel();

    expect(await screen.findByTestId('signing-blocked')).toBeInTheDocument();
    expect(screen.queryByTestId('sign-prescription')).not.toBeInTheDocument();
    // The reason says **which** gate is shut. "You may not sign this" would send the consultant
    // looking for the wrong person.
    expect(screen.getByTestId('signing-blocked-reason')).toHaveTextContent(/Station 10/);
  });

  it('tells the consultant which gate is shut in his own language', async () => {
    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(uncleared());

    renderPanel('bn');

    expect(await screen.findByTestId('signing-blocked-reason')).toHaveTextContent(
      /১০ নম্বর কেন্দ্র/,
    );
  });

  it('draws the two no-control states differently', async () => {
    // Both end in "no sign control", and a screen that drew one greyed-out button for both would
    // tell a QA officer that signing is part of her job and tell a consultant that his uncleared
    // sheet is his own fault.
    holding('QA', QA_OFFICER);
    const { unmount } = renderPanel();
    expect(await screen.findByTestId('signing-not-yours')).toBeInTheDocument();
    expect(screen.queryByTestId('signing-blocked')).not.toBeInTheDocument();
    unmount();

    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(uncleared());
    renderPanel();
    expect(await screen.findByTestId('signing-blocked')).toBeInTheDocument();
    expect(screen.queryByTestId('signing-not-yours')).not.toBeInTheDocument();
  });
});

/* ------------------------------------------------------------------------- */
/* A signed prescription                                                      */
/* ------------------------------------------------------------------------- */

describe('a signed prescription', () => {
  it('says it cannot be changed, rather than waiting to refuse', async () => {
    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(signed());

    renderPanel();

    expect(await screen.findByTestId('immutable-note')).toBeInTheDocument();
    expect(screen.getByText(/can no longer be changed/i)).toBeInTheDocument();
    // And that there is no way back, which is the question a physician who spots a mistake asks
    // next.
    expect(screen.getByText(/There is no unsigning/i)).toBeInTheDocument();
    expect(screen.queryByTestId('sign-prescription')).not.toBeInTheDocument();
  });

  it('is honest that a development key is not a non-exportable one', async () => {
    // docs/signing.md §2. The acceptance criterion "the signing key is non-exportable" is not met
    // by the local signer and cannot be. A screen that stayed quiet would be the clinic believing
    // something about its own prescriptions that is not true.
    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(signed());

    renderPanel();

    expect(await screen.findByTestId('weak-key')).toBeInTheDocument();
    expect(screen.getByText(/anybody who can read that configuration can copy it/i)).toBeVisible();
  });

  it('draws a prescription that no longer verifies as critical, with the reason', async () => {
    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(signed('NOT_VERIFIED'));

    renderPanel();

    expect(await screen.findByTestId('signature-not-verified')).toBeInTheDocument();
    expect(screen.queryByTestId('signature-verified')).not.toBeInTheDocument();
    expect(screen.getByText(/not what it was when it was signed/i)).toBeInTheDocument();
  });

  it('keeps the handwritten image and the signature apart, and says which proves anything', async () => {
    // docs/signing.md §6. A pasted image is trivially forged; the signature is not. The caveat is
    // the server's sentence, so the screen and the paper cannot say different things.
    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(signed());

    renderPanel();

    const note = await screen.findByTestId('signature-image-note');
    expect(note).toHaveTextContent(/a picture for readability/i);
    expect(note).toHaveTextContent(/the QR code, not the image/i);
    // It is drawn outside the verdict banner, so nothing about the picture reads as the verdict.
    expect(screen.getByTestId('signature-verified')).not.toContainElement(note);
  });

  it('says the verification code is gone rather than leaving a blank space', async () => {
    holding('PHYSICIAN', CONSULTANT);
    readSignature.mockResolvedValue(signed());

    renderPanel();

    expect(await screen.findByTestId('token-gone')).toHaveTextContent(/shown once/i);
    expect(screen.queryByTestId('verification-code')).not.toBeInTheDocument();
  });
});

/* ------------------------------------------------------------------------- */
/* The public page                                                            */
/* ------------------------------------------------------------------------- */

/**
 * The whole of what a stranger may be shown, as a **positive** assertion.
 *
 * A blacklist — "no patient name, no drug" — passes the day somebody adds a field nobody thought
 * to forbid. This is the other direction: everything the page renders must be accounted for by
 * either the page's own translated words or one of these values, and anything else fails the test
 * until a person adds it here and, in doing so, decides in a diff that a stranger may see it.
 *
 * The Go suite asserts the same thing about the *response* (`TestThePublicResponseCarriesOnlyWhat
 * ItIsAllowedTo`). This asserts it about the *page*, which is the half a server test cannot reach:
 * a component that received an allowed response and rendered something else — a token echoed back
 * under the heading, a physician's name drawn beside an unverified verdict — would pass there and
 * fail here.
 */
const PUBLIC_PAGE_MAY_SHOW = {
  facility_name_en: 'Diabetes, Thyroid & Hormone Clinic, Faridpur',
  facility_name_bn: 'ডায়াবেটিস, থাইরয়েড ও হরমোন ক্লিনিক, ফরিদপুর',
  physician_name_en: 'Dr K M Nahid Ul Haque',
  physician_name_bn: 'ডা. কে এম নাহিদ উল হক',
  // A date in words, in both scripts — `internal/clinicalterm`'s rendering, not ISO. The Bengali
  // one carries Bengali digits, which is what stops a Bengali page rendering a Latin numeral
  // beside a Bengali sentence.
  issued_on_en: '14 Sep 2026',
  issued_on_bn: '১৪ সেপ্ট ২০২৬',
  id: '0190a8f2-0000-7000-8000-0000000000c1',
  item_count: 3,
};

function genuine(): PublicVerification {
  return {
    verdict: 'VERIFIED',
    prescription: { ...PUBLIC_PAGE_MAY_SHOW },
    message_en:
      'This prescription was issued by this clinic and has not been altered since it was signed.',
    message_bn:
      'এই ব্যবস্থাপত্রটি এই ক্লিনিক থেকে দেওয়া হয়েছে এবং স্বাক্ষরের পর এতে কোনো পরিবর্তন করা হয়নি।',
  };
}

function tampered(): PublicVerification {
  return {
    verdict: 'NOT_VERIFIED',
    message_en:
      'This code could not be verified. It may have been mistyped or damaged, or the ' +
      'prescription may not be genuine. Ask the clinic before acting on it.',
    message_bn:
      'এই কোডটি যাচাই করা যায়নি। এটি ভুল উঠে থাকতে পারে বা নষ্ট হয়ে থাকতে পারে, অথবা ' +
      'ব্যবস্থাপত্রটি আসল না-ও হতে পারে। এটি অনুযায়ী কিছু করার আগে ক্লিনিকে জিজ্ঞাসা করুন।',
  };
}

const TOKEN = 'MFRGGZDFMZTWQ2LKNNWG23TPOJZA4YTB';

/**
 * Every word the page is allowed to draw of its own accord: the `verify` namespace in both
 * languages, ICU braces stripped.
 *
 * Taken from the message files rather than listed here, so that rewording the page does not
 * require editing this test — and so that the test stays about *data* rather than about prose.
 */
function furniture(): Set<string> {
  const words = new Set<string>();
  for (const locale of ['en', 'bn'] as const) {
    const namespace = messages[locale].verify as Record<string, string>;
    for (const value of Object.values(namespace)) {
      // Only the ICU punctuation is stripped, never the arms: `other {# medicines}` renders the
      // word "medicines", and a scan that threw the arms away would report the page's own noun as
      // unaccounted-for data.
      for (const word of tokenise(String(value).replace(/[{}#]/g, ' '))) words.add(word);
    }
  }
  return words;
}

/** Every word actually drawn, taken one text node at a time. */
function renderedWords(): string[] {
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const words: string[] = [];
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    words.push(...tokenise(node.textContent ?? ''));
  }
  return words;
}

function tokenise(text: string): string[] {
  return text
    .replace(/[.,;:·—–()[\]{}"'“”‘’!?/\\|=&#]/g, ' ')
    .split(/\s+/)
    .map((word) => word.trim())
    .filter(Boolean);
}

function renderPublic(answer: PublicVerification, locale: 'en' | 'bn' = 'en') {
  verifyPublicly.mockResolvedValue(answer);
  return renderWithProviders(<PublicVerificationPanel token={TOKEN} />, { locale });
}

describe('the public verification page', () => {
  it('shows a pharmacist the five things they came for, and says which clinic', async () => {
    renderPublic(genuine());

    expect(await screen.findByTestId('verify-genuine')).toBeInTheDocument();
    const facts = screen.getByTestId('verify-facts');
    expect(facts).toHaveTextContent(PUBLIC_PAGE_MAY_SHOW.physician_name_en);
    expect(facts).toHaveTextContent(PUBLIC_PAGE_MAY_SHOW.facility_name_en);
    expect(facts).toHaveTextContent(PUBLIC_PAGE_MAY_SHOW.issued_on_en);
    expect(facts).toHaveTextContent(PUBLIC_PAGE_MAY_SHOW.issued_on_bn);
    // The date a person says aloud, never the one a database stores. This is CP83's defect in
    // the one place a stranger reads.
    expect(facts).not.toHaveTextContent('2026-09-14');
    expect(facts).toHaveTextContent(PUBLIC_PAGE_MAY_SHOW.id);
    expect(facts).toHaveTextContent(/3 medicines/i);
  });

  it("renders nothing but the page's own words and the allowed fields", async () => {
    renderPublic(genuine());
    await screen.findByTestId('verify-genuine');

    const allowed = furniture();
    for (const value of Object.values(PUBLIC_PAGE_MAY_SHOW)) {
      for (const word of tokenise(String(value))) allowed.add(word);
    }
    // The verdict sentences are the server's, composed there so that one wording reaches every
    // reader. They are data on this page, not prose, so they are allowed the same way.
    const answer = genuine();
    for (const word of tokenise(`${answer.message_en} ${answer.message_bn}`)) allowed.add(word);
    // The count renders through an ICU plural whose braces the furniture scan stripped.
    allowed.add(String(PUBLIC_PAGE_MAY_SHOW.item_count));

    // Text node by text node, not `body.textContent`: that concatenates adjacent elements with
    // no separator, so "…unaltered" followed by "This clinic…" becomes one word nobody wrote.
    const unaccounted = renderedWords().filter((word) => !allowed.has(word));

    expect(
      unaccounted,
      `The public page rendered text that is neither its own words nor an allowed field: ` +
        `${unaccounted.join(', ')}. If a stranger with no account may see it, add it to ` +
        `PUBLIC_PAGE_MAY_SHOW and say so in the diff.`,
    ).toEqual([]);
  });

  it('does not echo the token back onto the screen', async () => {
    // It arrives from a camera and sits in the address bar; printing it under the heading turns a
    // shoulder-surfed screen into a working code. The prescription id is what a person names when
    // they ring the clinic, and it is already on their paper.
    renderPublic(genuine());
    await screen.findByTestId('verify-genuine');

    expect(document.body.textContent).not.toContain(TOKEN);
  });

  it('shows a tampered prescription as unverified and offers nothing else', async () => {
    renderPublic(tampered());

    expect(await screen.findByTestId('verify-not-verified')).toBeInTheDocument();
    expect(screen.queryByTestId('verify-genuine')).not.toBeInTheDocument();
    // **No block, and above all no physician's name.** Printing a named colleague beside "this has
    // been altered" would publish an accusation about them to anybody holding a forged sheet.
    expect(screen.queryByTestId('verify-facts')).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain(PUBLIC_PAGE_MAY_SHOW.physician_name_en);
    expect(document.body.textContent).not.toContain(PUBLIC_PAGE_MAY_SHOW.id);
    expect(screen.getByText(/Ask the clinic before dispensing/i)).toBeInTheDocument();
  });

  it('says the same thing in both languages on the verdict, whatever the interface is set to', async () => {
    // A stranger has no stored preference, so this page opens in the default for exactly the
    // reader most likely to want the other language. The sentence they act on carries both.
    renderPublic(genuine());

    expect(await screen.findByTestId('verify-message')).toHaveTextContent(
      /has not been altered since it was signed/i,
    );
    expect(screen.getByTestId('verify-message-bn')).toHaveTextContent(/স্বাক্ষরের পর/);
  });

  it('states the limit on both verdicts, rather than only when there is nothing to show', async () => {
    renderPublic(genuine());
    expect(await screen.findByTestId('verify-limit')).toHaveTextContent(/never show the patient/i);
  });

  it('does not turn an unreachable service into a verdict', async () => {
    // The endpoint answers 200 for both verdicts; anything else is our problem or the network's,
    // and inventing NOT_VERIFIED from it would put an accusation on the screen because a phone
    // lost signal.
    verifyPublicly.mockRejectedValue(new Error('offline'));

    renderWithProviders(<PublicVerificationPanel token={TOKEN} />);

    expect(await screen.findByTestId('verify-unreachable')).toBeInTheDocument();
    expect(screen.queryByTestId('verify-not-verified')).not.toBeInTheDocument();
    expect(screen.queryByTestId('verify-genuine')).not.toBeInTheDocument();
  });
});
