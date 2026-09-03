# The security audit log

CP22. What the system records about itself — who signed in, who was given which role, who
reset what, who took a copy of the trail, who broke the glass — how it is kept, how it is
read, and how a copy of it can be proven genuine. The clinical ledger is a different thing
(`docs/event-store.md`, CP23); this is the trail beside it (blueprint §4.5, ADR-0014).

## 1. What is recorded

Every kind in `internal/audit/sentences.go`, and nothing else: a kind without a sentence
cannot be recorded (`Recorder.Record` refuses it), and the registry test walks both ways.

| Kind                                                                         | Recorded by                              |
| ---------------------------------------------------------------------------- | ---------------------------------------- |
| `session.login`, `session.login_failed`, `session.logout`, `session.step_up` | auth (sessions, handlers, second factor) |
| `user.invited`, `user.status_changed`, `role.granted`, `role.revoked`        | the console (CP21)                       |
| `sessions.ended`, `password.set`, `second_factor.reset`                      | the console                              |
| `break_glass.opened`, `break_glass.acknowledged`, `break_glass.ended`        | this module                              |
| `audit.exported`, `audit.verified`, `audit.chain_broken`                     | this module                              |

A row names the actor by id and by employee code (copied, so the sentence survives a
renamed account), the target person where there is one, the patient, device and session
where relevant, a reason, and a small `details` object holding what the sentence needs —
the role code, the status before and after, a count. Never a secret, never a clinical
value, never a password typed into the wrong field: a refused sign-in for a code nobody
holds is recorded without the code.

## 2. How it is kept

`ledger.audit_event` (migration 00012), partitioned by month, in the schema where the
application role can INSERT and SELECT and nothing else. `core.assert_ledger_append_only()`
checks that on every start; `core.assert_audit_trail_kept()` checks that the table is
partitioned and that break-glass records and alerts cannot be deleted either.

Every row is a link: `prev_hash` is the previous row's hash (32 zero bytes for row 1) and
`hash` is SHA-256 over `prev_hash` and every field of the row in a fixed, length-prefixed
order (`hashOf` in `audit.go`). Details are hashed in canonical JSON — sorted keys, no
whitespace — so the JSON library's choices cannot make two verifiers disagree. The
sequence is assigned under `pg_advisory_xact_lock` so it is gapless and the chain is one
line; forty goroutines appending at once produce forty consecutive rows and an intact
chain (tested).

**Verification** (`Recorder.Verify`, `GET /v1/audit/chain`) walks from row 1 and recomputes
everything — linear in the log, deliberately. It reports the first row that does not agree:
a changed field ("does not hash"), a broken link, or a missing sequence. A failed
verification is itself recorded (`audit.chain_broken`, appended _after_ the break, which
stays where it is) and raises an alert. The test tampers three ways as the table owner —
edits a reason, edits a hash, deletes a row — and the verifier names the row each time.

**Partitions.** `ledger.ensure_audit_partitions(n)` creates the next _n_ months; the
migration creates fifteen. The application role cannot create tables in `ledger`, so this
is a migrator's job: a later migration, or the operator running the function. A row whose
month has no partition lands in `audit_event_default` rather than being refused, and the
verifier reports the count (`strays`) so it is noticed. Retention is D-05, open; when it is
decided, it is a per-partition operation.

## 3. How it is read

`GET /v1/audit/events` (needs `audit.read`): newest first, filtered by `person` (actor or
target), `actor`, `kind`, `patient`, `from`/`to` (days on the clinic's clock, Asia/Dhaka),
paged by `before`. Every entry carries `sentence_en` and `sentence_bn`, rendered from the
row by the registry — never stored, so a template corrected later corrects the whole
history. Empty placeholders render as "—", never as a blank.

The web viewer is **Administration → Audit trail** (`/admin/audit`): the four filters, the
sentences in the interface language, verify, export.

## 4. Break-glass

The emergency door (D-70; blueprint §11). `POST /v1/audit/break-glass` needs a clinical
role (`patient.read.clinical` or `patient.read.demographics`) **and** a step-up minted for
`break_glass`; the permission is decided first, so a pharmacist gets the same `FORBIDDEN`
as for any other door and never learns this one exists. The body names a scope (a patient
id, or something else in words) and a justification of at least twenty characters
(runes — Bengali is not shorter). The default is four hours; the most is twenty-four; the
database CHECK agrees.

Opening it does three things in one call, and refuses the access if the second cannot be
done: stores `core.break_glass_access`, appends `break_glass.opened` to the chain, raises a
`core.admin_alert`. Every administrator's console polls `GET /v1/audit/alerts` every thirty
seconds and shows the alert, in the reader's language, until one of them presses "I have
seen this" — which acknowledges the access too, on the record. The clinician sees their
open doors at `/break-glass` and can close one early; an administrator can close anyone's.

What the access _unlocks_ is the clinical checkpoints' business: they call
`BreakGlass.ForUser` and widen the RBAC subject for the named patient. Until then the door
exists, is loud, and is on the record, which is the part that had to exist first.

## 5. Export and signature

`GET /v1/audit/export` (needs `audit.read`) renders the filtered trail — at most 500
entries, oldest first, English sentences, each row's hash beside it, and the chain
verification the exporter ran first printed on the face of the report — as a PDF, and
signs it: Ed25519 over the SHA-256 of the bytes. The signature rides in three headers
(`X-Audit-Signature`, `X-Audit-Key-Id`, `X-Audit-Digest`); the viewer saves them as
`<file>.pdf.sig.json` beside the PDF. The export is recorded (`audit.exported`, with the
count) _before_ it is produced, so it appears in the trail even if the download is
abandoned.

The PDF writer is in the repository (`pdf.go`) rather than a dependency, because the file
must render to the same bytes every time for a signature over it to mean anything; the
test renders twice and compares. It uses the standard Courier and Helvetica faces, which
cannot shape Bengali — the export is English, and D-73 is the Bengali PDF.

**Verifying** needs the PDF, the sidecar, and the public key — from
`GET /v1/audit/signing-key` or the operations guide — and nothing else:

```
go run ./tools/auditverify -key <base64 public key> report.pdf report.pdf.sig.json
```

Exit 0 means the file is, byte for byte, the one the system signed. The key is
`DTHCMS_AUDIT_SIGNING_SEED` (32 bytes, base64) with `DTHCMS_AUDIT_SIGNING_KEY_ID`; the local
seed is refused outside `local` and `test`. Rotating the key is a configuration change;
old exports keep verifying under the id they carry, so keep every public key ever used.

## 6. Acceptance criteria, and where each is proven

| Criterion                                                                                      | Test                                                                                                                  |
| ---------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| (1) Audit rows cannot be updated or deleted by the application role                            | `TestTheApplicationRoleCannotRewriteTheTrail`; `assert_ledger_append_only`, `assert_audit_trail_kept` on every start  |
| (2) Every audited action renders as a correct sentence in both languages                       | `TestEveryRecordedKindHasASentenceInBothLanguages`, `TestSentencesReadAsTheBlueprintAsks`                             |
| (3) Break-glass requires a typed justification and notifies an administrator within one minute | `TestBreakGlassNeedsAJustificationAndRaisesTheAlarm` (the alert exists in the same call; the console polls at 30 s)   |
| (4) The export PDF verifies against its signature                                              | `TestAnExportVerifiesAndATamperedOneDoesNot`, `TestTheExportedPDFVerifiesAgainstThePublishedKey`, `tools/auditverify` |
| Hash chain detects tampering                                                                   | `TestTheVerifierDetectsTampering` (three tampers, as the owner)                                                       |
| Gapless sequence under concurrency                                                             | `TestTheSequenceIsGaplessUnderConcurrency`                                                                            |

## 7. Open

- **D-05 retention.** Partitioned from the first day so that the answer, when it comes, is
  a per-partition operation.
- **D-73 Bengali in the export PDF.** Needs an embedded Bengali font with shaping (or a
  rendering path through a browser engine). Until then the export is English; the viewer
  is bilingual.
- **A notifier beyond the console.** SMS or e-mail when the clinic has a gateway — a second
  notifier behind the same alert row.
- **The nightly verifier job.** `Recorder.Verify` exists and the console runs it on demand;
  the scheduled run arrives with the jobs framework, and calls the same `chainBroken`.
