# ADR-0035 · The physician's dashboard is a composition, served as one read, and owns no data

**Status:** Accepted · **Date:** 8 Sep 2026 · **Checkpoint:** CP73 ·
**Related:** §8, §4.1, §4.3, D-15, D-46, ADR-0002, ADR-0017, ADR-0021, ADR-0033

## Context

§8 asks for a three-panel screen: a patient snapshot, the AI's clinical summary, and an AI
assistant with accept, edit and reject against each suggestion. The checkpoint adds one
architectural requirement in a single line — **"aggregated dashboard endpoint (one request, not
twelve)"** — and one acceptance criterion that depends on it: the whole screen inside 1.5 seconds
at the ninety-fifth percentile, measured on a patient with a decade of history.

Every panel it asks for already exists somewhere. Values are `clinical`'s, allergies are
`allergy`'s, coded conditions are `history`'s, the counselling checkpoint is `counseling`'s, and
the summary is `synthesis`'s. Six modules, six stores, six endpoints — and a client that called
all of them would produce exactly the twelve round trips the checkpoint names.

Three questions had to be answered rather than transcribed.

1. **Where the aggregation lives.** A module that owns nothing is unusual here; every other
   module in `architecture.json` owns a table.
2. **Whether it is a read model.** The obvious alternative is a projection: one
   `read.physician_dashboard` row per patient, maintained by the projector, read in one query.
3. **What "one request" means for a client whose components already fetch for themselves.**

## Decision

### 1. A new module, `dashboard`, that owns no table and adds no query of its own

It is declared in `architecture.json` with the widest import list in the system — `platform`,
`eventstore`, `rbac`, `patient`, `visit`, `clinical`, `counseling`, `history`, `allergy`,
`synthesis` — and it writes nothing to any of them. It reads through each module's own store and
assembles one payload.

### 2. It is a fan-out, not a read model

The nine panel reads are issued **concurrently**, so the endpoint's wall time is the slowest read
rather than the sum. The fan-out is **fixed**: nine reads whatever the patient's record looks
like, never one per year and never one per code.

### 3. The client's own components are given what the aggregate already fetched

`AllergyBanner` — CP54's strip, rendered unchanged on this screen — takes an optional `state`
prop. Given one, it performs no request.

### 4. The read uses the caller's subject, not an event actor

`eventstore.ActorFrom` builds a _write_ envelope and D-46 requires one to name its device. A read
handler that reaches for it refuses every browser with `DEVICE_REQUIRED` (ADR-0021). This module's
read resolves the facility from the RBAC subject the route guard already verified; only the
decision write — which appends an event — takes an actor.

## Why, for each

### Why a module rather than a handler inside `patient`

`patient` may import `platform`, `eventstore` and `rbac` and nothing else. Putting the dashboard
there would mean widening the register's own import list to reach six clinical modules, which is
the boundary this system spends the most effort keeping narrow: `patient` is imported by almost
everything, and a cycle through it would be a cycle through most of the system.

A module whose whole content is a fan-out is also the honest description of what this is. Nobody
reading `architecture.json` should conclude that the dashboard owns clinical data, because the day
somebody believes that is the day a value gets written through it.

### Why not a read model, which is the obvious alternative

A `read.physician_dashboard` row would make the endpoint one query instead of nine, and it is the
wrong trade three times over.

**It would be a second copy of six modules' data**, with its own staleness, its own rebuild and its
own opportunity to disagree with the record. ADR-0017's projections exist to derive things that are
expensive to compute from events; a dashboard is not expensive to compute, it is merely _wide_.
The projection would be a cache of nine indexed lookups.

**It would have to be maintained by every module that writes.** A dashboard row that changes when
an allergy, an observation, a history item, a counselling tick or a synthesis run changes is a row
five modules have to remember to touch. The forgetting is silent: the screen simply shows
yesterday's value with today's timestamp, which is the worst failure this system can have.

**It would not be faster where it matters.** The measurement below is 17.7 ms at p95 against the
deepest record this system can hold. The criterion is 1.5 s. Buying a few milliseconds with a
duplicate of the clinical record is not a trade anybody should make.

The reasoning would change if the fan-out's cost grew with the record. It does not, and that is a
property this checkpoint had to _build_: see the fourth section.

### Why the fan-out is fixed, and the one place it was not

The reads must not multiply with the patient. Two of them were originally written in a way that
did:

- **The current values.** `clinical.Store.ForPatient` answers _what has been recorded lately_ —
  every live value, newest first, with a limit. For a patient with six years of quarterly visits
  that is forty weights, thirty-nine of them history, and a code last recorded before the limit's
  window missing altogether. The snapshot asks a different question — _what is true now_ — and
  CP73 added `clinical.Store.Current`, a `DISTINCT ON (code)` read whose answer is bounded by the
  registry. Measured: 200 rows and 151 KB became 14 rows and 26 KB for the same patient.
- **The trends.** Four codes, five points each, and both numbers are constants. A trend list
  derived from the patient's own codes would make the widest patient the most expensive one.

### Why the panels are read concurrently, and what it costs

The cost is that one dashboard load holds as many pooled connections as it has panels, for the
length of the slowest one. At sixteen concurrent readers the p95 is 158 ms and throughput is flat
at about 110 requests per second, which is the pool saturating rather than the database working
hard. That is a number to watch, and it is the reason the fan-out is fixed: a dashboard whose
connection cost grew with the record would fail first on exactly the patients it matters most for.

### Why a failed panel does not fail the screen

D-15 — _fail visible, never fail silent, never fail invented_. A dashboard that answered 500
because the counselling checklist was slow would be a screen a physician cannot open during an
incident that has nothing to do with the patient in front of them. A panel that could not be read
is **absent with its reason named**, in both languages, in the same `omitted` list that names a
panel the caller may not see.

The two exceptions are the patient and the allergy state. A patient that cannot be read is no
screen. An allergy state that cannot be read is the one panel where absence is dangerous rather
than incomplete: a header with no allergy line reads as _no allergies_, which is CP54's whole
argument, so the request fails rather than rendering a payload that looks complete.

### Why the assembled synthesis context is not on the payload

`synthesis.Run` carries the entire [Context] the model was shown, and D-15's degraded screen is
built from it. It is deliberately not carried here. The context is a **de-identified snapshot with
no operator names in it by construction** (ADR-0033); the dashboard's left panel is the **live
record with attribution on every value**. Sending both would put the same numbers on one screen
twice, from two moments, with provenance on one copy and not the other — and a physician reading
the wrong copy would be reading a number nobody's name is against.

### Why the decisions are events with no read model

Accepting, editing or rejecting a suggestion appends `AI_SUGGESTION_DECIDED` to the visit's
stream, and the panel folds that stream on read. §4.1 says the event log and not the
current-state table is the source of truth; here that is literally how it is read.

The stream is bounded by one journey through the clinic. A table, a projection, a migration and an
invariant for data whose only reader is the screen that wrote it would be machinery without a
question to answer.

The cost is stated rather than hidden: _"how often does the physician reject a drafted diagnosis,
across the clinic, this quarter"_ cannot be answered by a query today. It needs a projection, and
CP81 will want one anyway when a decision starts meaning something to a prescription. Building it
now would be building it twice.

## Consequences

- **`dashboard` has the widest import list in the system.** That is legible in
  `architecture.json` and is the price of the aggregation being in one place rather than six.
- **`clinical` gained one query and one type.** `Store.Current` and `Service.WeightStatus`; the
  latter replaced an inline `map[string]any` in the growth handler, so [R-06]'s obesity flag now
  has one producer instead of two.
- **A future panel is a field on one struct and one more concurrent read.** A future panel that
  needs a _twelfth_ module means widening this module's import list, which is an ADR — deliberately.
- **The web must not add a `useQuery` to a panel.** The acceptance criterion is a property of the
  client as much as of the server, and it is enforced by a test that counts calls rather than by a
  convention.
- **Cross-visit questions about AI suggestions are not answerable.** See above.
- **A decision cannot be recorded from a browser** until D-71 is decided, because it is a clinical
  write and D-46 requires a device. The same wall CP32's registration desk met.

## Alternatives considered

**A GraphQL-style field selection**, so a client asks for the panels it wants. Rejected: it makes
the server's cost a function of a string the client sends, and the security argument against it is
stronger — the RBAC filtering here is decided per panel against the caller's subject, and a query
language would put the shape of a clinical response into the caller's hands.

**Twelve endpoints and HTTP/2 multiplexing.** Rejected on the checkpoint's own terms. It is not
the round trips alone: twelve responses is twelve authorisation decisions, twelve audit questions
and twelve chances for a client to render a screen half-composed from a patient it has stopped
looking at.

**A read model maintained by the projector.** Rejected, at length, above.

**Server-rendering the dashboard.** Rejected because the screen is a live surface: it holds a
socket subscription, it re-reads on realtime invalidation, and its panels collapse and remember.
A server render would produce a first paint sooner and a screen that has to become interactive
anyway, and the measurement says the first paint is not the problem.
