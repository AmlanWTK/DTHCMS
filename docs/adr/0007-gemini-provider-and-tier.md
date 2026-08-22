# ADR-0007: Google Gemini as the AI provider, with a fail-closed tier guard

- **Status:** Accepted
- **Date:** 2026-08-22
- **Blueprint reference:** §7 (AI framework), §16.5 (open decision)
- **Related decision:** D-07

## Context

The blueprint left the AI provider open. Dr. Nahid has chosen **Google Gemini**, which fits
well: it is available in Bangladesh, and using Google for both cloud and AI keeps one
contract and one data-residency conversation.

The choice of **tier**, however, is not a preference — it is a constraint. Google's Gemini
API Terms distinguish sharply between the free tier ("Unpaid Services") and paid usage:

> "Google uses the content you submit to the Services and any generated responses to
> provide, improve, and develop Google products and services."
> "Human reviewers may read, annotate, and process your API input and output."
> **"Do not submit sensitive, confidential, or personal information to the Unpaid Services."**

Paid usage carries the opposite commitment: no training on submitted content, and a data
processing addendum.

Everything DTHCMS would send to a model is health data: pre-consultation summaries, scanned
lab reports carrying names and national ID numbers, dictated notes. This also engages the
Bangladesh Personal Data Protection Act, 2026 (D-01).

## Decision

**Gemini is the provider.** The tier is decided by what the data is, and enforced in code:

- **Development, CI, prompt iteration, demos and evaluation run on the free tier**, against
  synthetic patients only. This is a legitimate use of the free tier and costs nothing.
- **Anything derived from a real patient uses paid Gemini or Vertex AI.** The AI gateway
  (CP70) carries a tier guard: a request flagged as real-patient data cannot be sent with a
  free-tier credential. It **fails closed** — it does not quietly downgrade.
- Model versions are pinned explicitly; free-tier and preview aliases are retired quickly.
- PHI minimisation (D-08) applies on the paid tier too. Belt and braces.

## Alternatives considered

**Free tier everywhere.** Would breach Google's own terms and expose patients' clinical data
to human review outside Bangladesh. Not available to us. If paid usage is ultimately not
approved, the consequence is that AI features run on synthetic data only and stay dark for
real patients — the clinic still runs, because the deterministic parts of DTHCMS are most
of it.

**Self-hosted open-weights model.** Full data control, no tier question; materially weaker
clinical synthesis quality, real GPU cost, and MLOps burden a two-person team cannot carry
today. Kept as the fallback if D-01 forces it.

## Consequences

**Good**

- One vendor for cloud, AI and embeddings; one residency conversation.
- The gateway keeps the provider a configuration value, so this decision is reversible in
  days rather than months.
- Free-tier development means the entire build period costs nothing in AI spend.

**Bad — and we accept these knowingly**

- A production dependency on a third-party API with rate limits and model deprecations.
  Mitigated by pinned versions, the evaluation set (CP72), and a degraded mode that keeps
  the clinic running when AI is unavailable (D-15).
- Paid usage introduces a per-encounter cost that must be metered from CP70 and measured at
  CP71 rather than assumed.

**Revisit when** D-01's legal opinion lands, if it prohibits cross-border processing of
health data; or if measured cost or quality makes a self-hosted model preferable.
