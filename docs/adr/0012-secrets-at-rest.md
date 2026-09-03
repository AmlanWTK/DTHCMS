# ADR-0012 · Small secrets are sealed with a configured key, with a key id beside every ciphertext

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP17
- **Supersedes:** —

## Context

CP17 stores a TOTP seed per enrolled user. A seed is a credential: whoever has it can
produce every code that account will ever accept. The plan says "encrypted secrets at rest
with a KMS key". There is no KMS — hosting is deferred (D-01) — and there will not be one
before the first physician enrols on a development server.

Three options were on the table: store the seed in the clear until a KMS exists; encrypt
under a key the process is configured with; or block CP17 on D-01.

## Decision

**Seeds are sealed with AES-256-GCM under a key the process reads from configuration, and
every ciphertext records the id of the key that sealed it.** `internal/platform/secretbox`
is the implementation; `DTHCMS_SECRET_KEY_ID`, `DTHCMS_SECRET_KEY` and
`DTHCMS_SECRET_PREVIOUS_KEYS` are the configuration.

The associated data is the user id, so a sealed row copied onto another user's record does
not decrypt there.

The key id is what makes this a stepping stone rather than a dead end. When a KMS exists, a
new key gets a new id; new seeds seal under it; an existing seed is re-sealed under the
current key on its next successful verification (`RecordTotpUse` writes the seed back), and
the old key retires from configuration when no row names it. Nothing has to happen all at
once, and no batch job has to touch every row.

A local development key is built in so `make up` works with nothing set. It is recognisable
on purpose, and configuration refuses it outside `local` and `test`.

## Consequences

**What it protects against.** A database backup, a `pg_dump` on a laptop, a stray copy of
the table, a read-only breach of the database: none of them yield a usable seed. Those are
the common ways a table leaks, and they are covered.

**What it does not protect against.** A compromise of the running server. The key is in the
process's environment, so a process that can read the table can also open it. A KMS does not
change that — it would decrypt for the compromised process just as readily — but it does
change _who can read the key_ (nobody, including operators), and _where the audit of its
use lives_ (outside the server). Those are real gains, and they are why this decision is
explicitly temporary in its key management and permanent in its data format.

**The assertion.** `core.assert_no_plaintext_second_factor()` refuses any column on the
second-factor tables whose name suggests a seed, code or token in the clear, so the rule
outlives the people who remember it.

**Recovery codes and short-lived tokens are not encrypted; they are hashed.** They are
high-entropy random strings, which is to say tokens, and the argument of ADR-0011 applies: a
digest of a thing that cannot be guessed is enough, and nothing needs to decrypt it.

## Compliance

`docs/identity.md` §8. `secretbox_test.go` proves round-trip, tamper detection, associated-data
binding and rotation; `secondfactor_test.go` proves the reseal-on-use; the configuration
test proves the local key is refused in production.
