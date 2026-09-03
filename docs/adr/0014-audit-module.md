# ADR-0014 · The security audit log is its own module, depending on platform alone, with a hash chain and an offline-verifiable export signature

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP22
- **Supersedes:** —

## Context

The blueprint asks for two trails (§4.5): the clinical ledger, which is the source of truth
for patients (CP23), and a security audit log — sign-ins, role changes, credential resets,
exports, break-glass — that must be append-only and readable as sentences in both
languages. `architecture.json` had no home for the second one. Putting it in `auth` would
make auth the module that records exports and break-glass, which are not authentication;
putting it in `platform` would give every module a way to write to a table nobody reviews.

Three questions had to be settled at once: where the module lives and what it may import;
how "append-only" is made true rather than promised; and what "verifies against its
signature" means for a PDF a regulator opens on a machine that has never seen the server.

## Decision

1. **A module `audit` that imports `platform` only.** `auth` declares an `AuditRecorder`
   interface and `audit` never imports `auth`; the composition root (`cmd/api`) joins the
   two with a small bridge. Anything that wants to record goes through the same door.
   `architecture.json` gains `"audit": ["platform"]`.

2. **Append-only by grant, linear by chain.** `ledger.audit_event` is in the `ledger`
   schema, where the application role has INSERT and SELECT and nothing else — the same
   rule as the clinical ledger (ADR-0008), checked on every start by
   `core.assert_ledger_append_only()`. Every row carries `prev_hash` and `hash` (SHA-256
   over a length-prefixed canonical form of every field, never JSON), and the sequence is
   assigned under a Postgres advisory lock so it is gapless and the chain has one line. The
   verifier recomputes the whole chain; sampling would be a verifier that could be fooled.
   The table is partitioned by month from the first day (§9.4), with a default partition
   so a forgotten monthly chore loses nothing — the verifier reports how many rows landed
   there.

3. **Sentences are rendered, never stored.** A registry maps each event kind to one
   template per language. A kind without a sentence cannot be recorded, and a test walks
   both directions: every recorded kind has both languages; every sentence has a recorder.

4. **Exports are signed with Ed25519 over SHA-256, verifiable offline.** The seed is
   configuration (`DTHCMS_AUDIT_SIGNING_SEED`); the public key is published by the API and
   printed in the operations guide. The signature travels in headers beside the PDF, the
   browser saves it as a sidecar file, and `tools/auditverify` checks the pair without the
   server. The PDF is written by hand, without dates, ids or compression, so the same trail
   renders to the same bytes — a signature over bytes that vary would mean nothing.

5. **Break-glass is loud by construction.** Opening the door writes the justification,
   appends the chain row and raises an administrator alert in one call; if the row cannot
   be written, the door is closed again. The alert is a row every administrator's console
   polls every thirty seconds — inside the criterion's minute — because the realtime
   gateway is CP26 and an emergency cannot wait for it.

## Consequences

- The clinical ledger (CP23) will follow the same shape — advisory-locked sequence, hash
  chain, partitioning, verifier — and can reuse the canonical-hash discipline established
  here, with per-aggregate rather than global sequences.
- The export prints English sentences: the standard PDF fonts cannot shape Bengali, and
  embedding a Bengali font with correct shaping is a piece of work of its own (D-73). The
  Bengali sentence is in the viewer, and the row it renders from is the same row.
- Rotating the signing key is a configuration change; old exports keep verifying under the
  key id they carry, so the operations guide must keep every public key ever used.
- The SMS or e-mail notification of break-glass, when the clinic has a gateway, is a second
  notifier behind the same alert; nothing in the door changes.
