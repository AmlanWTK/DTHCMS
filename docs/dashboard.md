# The physician's dashboard

CP73. Blueprint §8. The screen Dr. Nahid spends the working day in, and the one place where the
blueprint's central promise — cognitive load reduced to what a consultation actually needs — is
either kept or not.

> Zero raw forms. Three panels plus the timeline.

The timeline is CP74. This is the three panels.

---

## What is on it

**Left — the snapshot.** Demographics and the visit; unanswered critical values; the current value
of every code, with the BMI banded; sparklines of the last five values of four codes; the
paediatric percentile card where a child is in front of you; the active coded conditions; the
counselling checkpoint.

**Centre — the clinical summary.** The pre-consultation narrative (CP71), its key points, §6.4's
red lines as the model expressed them, and — one interaction away — what produced it: the
generation, the trigger, the model and prompt versions, the grounding verdict and the model's own
confidence.

**Right — the AI assistant.** Suggested ICD-coded diagnoses, missing-data alerts, drafted
investigations and drafted doses, each with **accept / edit / reject**.

**Above all three — the patient header**, with CP54's allergy strip on it. Above the header, when
it applies, a banner saying that this record is open through the emergency door.

---

## One request

`GET /v1/patients/{id}/dashboard` returns all of it.

That is the checkpoint's own requirement and its measurable one. Twelve round trips on a clinic's
shared connection is a second and a half of spinner in front of a patient who is already sitting
down, and a physician who waits through that twice learns to keep the screen open on the previous
patient — which is how a dashboard stops being read.

The endpoint is a **fan-out, not a read model**. Nine reads, issued concurrently, each through the
store of the module that owns the data. Nothing is copied and nothing is projected. The argument,
including why a `read.physician_dashboard` row was rejected, is ADR-0035.

The fan-out is **fixed**: nine reads whatever the patient's record looks like. That property had
to be built rather than assumed, and one query was added to make it true —
`clinical.Store.Current`, a `DISTINCT ON (code)` read that answers _what is true now_ rather than
_what has been recorded lately_. For the deepest patient in the local corpus that changed the
payload from 200 rows and 151 KB to 14 rows and 26 KB, and closed a hole: a code last measured
before the previous query's limit simply did not appear on the screen.

### The client half, which is the fragile half

Several components on this screen fetch for themselves. `AllergyBanner` is CP54's and is rendered
here unchanged; the critical-value strip is CP50's. Rendering them inside a screen that has
already fetched the same data would produce the twelve requests the endpoint exists to replace,
plus one.

So the aggregate hands `AllergyBanner` its state as a prop, and the component performs no request
when it has one. The dashboard _also_ primes the same cache key, which makes the next screen
instant; but the prop is what makes the criterion structural. A cache prime alone would depend on
the application's `staleTime` — a number in another file — and the day somebody tunes it down the
criterion would stop holding with nothing on screen to show it.

Measured in a real browser against the production build, one dashboard load makes these API calls:

```
GET /v1/auth/me                      the session, from the shell
GET /v1/audit/alerts                 the administrator alert poller, from the shell
GET /v1/patients/{id}/dashboard      the whole clinical screen
GET /v1/directory                    the staff directory (CP61), once per session
```

One request for the patient. `TestTheWholeScreenIsOneRequest` in `web/test/dashboard.test.tsx`
asserts the call count and — separately — that the strip renders the _aggregate's_ allergy status
rather than one it fetched, because a call count alone would pass against a component that fetched
and threw the answer away.

---

## Attribution on every value

§4.3, and acceptance criterion 5: _attribution is one interaction away on every value._

The payload therefore carries **whole observations** — `recorded_by`, `recorded_role`,
`station_code`, `device_id`, `source`, `status` — rather than formatted numbers. A
`{"systolic": 140}` shape would have been smaller and would have thrown the criterion away.

Every clinical number on the screen is drawn through CP61's `ValueWithAttribution` and CP44's
`DualUnitValue`. That includes the five points inside each sparkline, which is where the promise
was most likely to be quietly dropped: a chart is a picture, and a step in an HbA1c series is
exactly the thing that makes a physician ask who typed it.

The sparkline points use the **disclosure** variant rather than the compact one. That is the only
density decision on this screen that is not a safety decision: compact puts the operator's name on
screen with no interaction, which is right for the allergy strip and wrong for twenty points,
because a column of names is a column a physician reads past.

---

## Marking what a machine wrote

Acceptance criterion 3: _AI-generated content is unmistakably marked._

The obvious implementation is a chip above the narrative. It fails in four ordinary situations,
all of which happen here every week:

1. **A scrolled panel.** The narrative is a page of prose; a physician reading the bottom of it
   has scrolled the chip away, and what is in front of them is unmarked clinical text.
2. **A photograph of the screen**, which is how a second opinion is asked for. The crop rarely
   includes the top of a panel.
3. **Colour that is not there.** Roughly one man in twelve working in this clinic cannot use hue,
   a tablet near a window flattens it for everybody, and the printed summary has none.
4. **Habituation.** A chip in the same place on every screen stops being read within a week.

So a machine-written region is **enclosed**, with four signals and no hue among them:

- its own ground and a heavy left edge, so the region is visibly a different kind of surface —
  this is the signal that survives a photograph, because the boundary is where the machine's words
  start;
- a header that **sticks to the top of the panel** while the prose scrolls under it, so there is
  no scroll position at which the mark is off screen;
- a **repeating gutter** of the word "AI" down the full height of the region, so a crop showing
  three lines shows the mark beside them;
- the words themselves, **in both languages at once** — a summary is read over a shoulder by
  whoever is in the room, and the person who most needs to know a machine wrote it may not be the
  one who chose the interface language.

It is loud. That is the trade the criterion asks for, and this is the one place on the screen where
visual weight is a safety control rather than a style choice.

### The right panel is a mixture, and that is the sharper half

§8 puts two different kinds of thing in the right column. A _suggested ICD-coded diagnosis_ is a
language model's proposal. A _missing-data alert_ — "no HbA1c in the last twelve months" — is
arithmetic the deterministic assembler did over the record with no model involved at all. It is
available when the model has failed, and it is one of the few things on this screen that is simply
true.

Marking the whole panel as AI would teach a physician to discount the one group they should not.
So the enclosure goes around the model's items only, and **every item additionally carries a word**
— _AI draft_ or _From the record_ — because a boundary can be scrolled past or cropped out, and
because a distinction carried by the absence of a mark cannot survive either.

On a degraded run the panel is entirely system-derived, and says so.

---

## Accept, edit, reject

The three buttons are a record, not a colour.

Pressing one appends `AI_SUGGESTION_DECIDED` to the visit's ledger stream with the physician's
name, the time, the generation it was decided against, the suggestion's own words copied onto the
row, and — for an edit — the physician's wording. The panel folds that stream on read.

### Accepting records an intent. It does not write a prescription.

§7.3 makes the split permanent: _generative models draft; deterministic databases and a human
signature prescribe._ CP81 owns the prescription, with the interaction check, the dose validation
against renal function, the formulary and the signature that belong to it. Accepting a drafted
metformin here puts a row in the ledger saying this physician agreed with this draft at this time,
and puts nothing on any prescription.

The button therefore reads **"Accept as intent"**, and the sentence _"Accepting records that you
agree with this draft. It does not write a prescription."_ is printed under **every** drafted drug
rather than once at the top of the panel — a physician scrolling to the fourth medication has left
the panel header behind.

### A rejection is stored, not hidden

A rejected suggestion that vanished would leave no evidence the physician had considered it, which
is the wrong record in both directions. Medico-legally, _"the system suggested a thyroid function
test and the physician declined it"_ is a defensible sentence and an absent one is not.
Operationally, the rejection rate per kind is the only measurement that says whether this panel
earns the attention it costs.

The note box is offered and never required. A physician made to justify nine rejections in a
morning stops rejecting, and starts leaving the panel alone.

### An edit is a different act from an acceptance

_Agreed_ and _agreed, with this changed to that_ are different records, and the second is the more
useful one — a clinician correcting the model in their own words is the training signal a prompt
change should be argued from. An `EDITED` decision with no wording is refused, in the service and
again in the event payload's own `Validate`, because a record saying somebody changed something
without saying what is worse than no record.

### Where the decisions live

In the ledger, and nowhere else. There is no read model and no table: the decisions for one visit
are folded from that visit's own stream, which is bounded by one journey through the clinic. §4.1
says the event log and not the current-state table is the source of truth, and here that is
literally how it is read.

The cost is real: _"how often is a drafted diagnosis rejected, across the clinic, this quarter"_
cannot be answered by a query today. It needs a projection, and CP81 will want one when these
decisions start meaning something to a prescription. Building it now would be building it twice.

### The reference a decision names

`diagnosis:type_2_diabetes_mellitus` — the kind and a slug of the item's own words, never its
position in the model's answer. A re-read that returns the same items in a different order would
otherwise move every recorded decision onto the wrong line, and a physician who accepted a
diagnosis and found it against a drug is a physician who stops using the panel.

Duplicates take a numeric suffix. That is not hypothetical and was not obvious: several stations
nobody reached produce several gaps that all carry the code `station_not_reached`, and the first
version of this code gave them one reference between them — so dismissing one dismissed all four,
which the panel then drew as four decisions nobody had made. A test found it.

---

## What is withheld, and how that is said

A panel the caller may not read is **absent**, and its absence is named in `omitted` with a
sentence in both languages. A panel that is present and empty means something entirely different:
this patient has none of that thing.

Collapsing the two is the failure worth naming, because it looks like reassurance. A pharmacist
who may not read diagnoses would see _"no active conditions"_ rather than _"you may not see
this"_, and would be wrong about the patient rather than about their own permissions.

The same list carries a panel that could not be read at all, with a different sentence and no
permission named — _"This part of the record could not be read just now. Nothing has been
hidden."_ A reader who has just been told a panel is missing will otherwise assume the first
reason.

The client draws a note for **every** omission, including panels this build has never heard of.
That, too, was a hole a test found: the first version drew notes only for the five panels it knew
about and dropped a withheld trend silently, which keeps the shape of the mechanism and loses its
property.

### The allergy strip is never withheld

There is no caller who may see this screen and may not see the strip. `NONE_RECORDED` — nobody has
asked — and `NO_KNOWN_ALLERGY` — somebody asked, and a person's name is against the answer — both
arrive with an empty list and mean opposite things, and a header with no allergy line reads as
_none_. That is CP54's whole argument, and it is why an unreadable allergy state fails the request
rather than producing a payload that looks complete.

---

## RBAC, at three layers

**The route.** `patient.read.clinical`, which is sensitive: this screen is the patient's whole
clinical picture, and §4.4 blinds registration and the pharmacist from exactly that.

**The service.** Each panel is decided against the caller's subject _before_ it is read, so a
caller who may not see the summary does not pay for the query — and the omission is recorded with
the permission that would have been needed, because the remedy is a grant and whoever can make one
has to be told which.

**The serialiser.** `rbac.Marshal`, and this is CP20's serialiser's first production use. It is
default-restrictive: a field whose JSON name looks clinical and carries no `visible` tag makes the
whole type unserialisable. That fired twice while this was being written — on `clinical_id` and on
`icd10` — and both times the answer it forced was the correct one.

Three layers is not redundancy. The route knows nothing about the patient, the service knows
nothing about the wire format, and the serialiser is the only one that fails closed on a field
somebody adds next year.

---

## Break-glass

CP22 built the emergency door: a typed justification of at least twenty characters, every
administrator alarmed at the moment it opens, and an expiry. Nothing here builds a second one.

**When a record is refused**, the screen offers the door and does not say why the refusal
happened. The server answers the same 403 whatever the reason — deliberately, so that a refusal
does not confirm that a patient exists — and a client that helpfully explained _"this patient is
outside your scope"_ would have undone that. So the screen offers `/break-glass?patient={id}` on
any refusal and lets the physician decide whether the situation warrants it.

**When a record is open through the door**, the screen says so, at the top, with the physician's
own justification read back to them and the time it ends. CP22 tells the administrators; it cannot
tell the person using it while they are using it — and an emergency access somebody has forgotten
is open has stopped being an emergency.

---

## Audited

Every load writes a `patient.viewed` entry with `by: dashboard`, and `basis: BREAK_GLASS` when the
record was opened through the door.

The same kind CP31's search and CP37's timeline already use, rather than a new one. A review
asking _"who opened this patient's record"_ must get one answer; a second kind would mean every
such query missed half the openings until somebody remembered to add it — and the person writing
that query is doing it during an investigation.

The entry never carries clinical content. The trail is read by administrators who may hold no
clinical permission at all.

Recording never fails the request, and a failure to record is logged loudly. A clinician who
cannot open a patient because the audit table is busy is a worse outcome than a late audit line;
a _missing_ line is a hole in the trail.

---

## Realtime

The screen subscribes to `patient:{id}` while it is mounted, and every message on that topic
invalidates `['patient', id]` — a prefix of the dashboard's key — so a value typed at a station
appears here without a refresh.

CP73 added the publisher that makes this true for ordinary values. Every earlier publisher carries
an _exception_ — an alert, a correction request, a counselling tick, a retraining flag — and §8's
snapshot is the first surface whose whole content is other people's routine work. A dashboard fed
only by the existing publishers would come alive when something went wrong and sit frozen through
a morning that went well.

`measurement.recorded` carries the **codes** and never the values. CP27's rule: a realtime message
is a notification, not a record. A client that wrote a value into its cache from a socket would
have two paths producing what a clinician reads, and on the day they disagree the number on screen
is one no endpoint returned and no log explains.

One message per write, not one per value: six measurements entered at anthropometry are one act,
and six messages would be six refetches of one screen.

---

## Keyboard

| Key         | What it does                                           |
| ----------- | ------------------------------------------------------ |
| `1` `2` `3` | Move focus to the snapshot, the summary, the assistant |
| `r`         | Show or hide the assistant column                      |
| `p`         | Print the summary                                      |
| `?`         | Show the shortcut list                                 |
| `Escape`    | Close it                                               |

Single keys, because a modifier chord is something you have to remember and this has to be muscle
memory by the end of a week. Three rules make single keys safe: nothing fires while focus is in a
field (checked on the event target, not on a flag somebody has to set), nothing fires with a
modifier held, and Escape is never swallowed.

Pressing `2` **moves focus** rather than scrolling, so a screen reader announces the region and a
keyboard user's next Tab starts inside it. The shortcut list returns focus to whatever opened it.

---

## Panel priority and default state

The plan lists this as an open decision, and it is a clinical preference. What ships is a
**proposal**: left and centre open, right open and collapsible, remembered per user in the
browser.

The reasoning, so there is something to disagree with: the left panel is the record and the centre
is the summary of it, and those two are what a consultation opens with. The right panel is where
the physician acts on the AI, which happens later in a consultation and not at all in many of
them — so it is the one that can be out of the way without the screen losing its purpose.

It is keyed by user id, and that is the part that matters: this clinic's browsers are shared, and
an unkeyed preference would mean each physician inheriting the last one's layout.

**Dr. Nahid's to confirm.** Recorded in `docs/progress.md`.

---

## What was measured

The acceptance criterion is _"full dashboard loads in under 1.5 s at p95 for a 10-year patient"_.

### The corpus

A single patient loaded through `cmd/synthload` from a hand-built cohort: **40 visits, 520
observations, 924 ledger events**, spanning 2021-02-01 to 2026-08-10.

**A ten-_year_ patient cannot be created.** `clinical_id_counter_year_check` refuses a clinical id
for a year before 2020, and `cmd/synthload` dates a registration to the patient's first attended
visit less up to a year of jitter — so the deepest record this system can currently represent is a
first visit in early 2021. That is a finding rather than a workaround, and it is recorded in
`docs/progress.md`.

The corpus above compensates on the axis that actually drives cost: 40 visits is _more_
accumulated data than ten years of quarterly attendance would produce, and the endpoint's work is
a function of stored values rather than of calendar span.

### The numbers

**The endpoint**, 300 sequential warm requests, loopback, single reader:

|         |       ms |
| ------- | -------: |
| min     |     10.2 |
| p50     |     13.4 |
| **p95** | **17.7** |
| p99     |     19.7 |
| max     |     26.3 |

**The endpoint under concurrency**, 40 requests per worker after a per-worker warm-up:

| workers |      p50 |      p95 |      p99 | throughput |
| ------: | -------: | -------: | -------: | ---------: |
|       8 |  65.7 ms |  92.1 ms | 109.7 ms |  107 req/s |
|      16 | 132.5 ms | 157.6 ms | 171.9 ms |  111 req/s |

Throughput is flat between 8 and 16 workers and latency doubles, which is the connection pool
saturating rather than the database working hard. It is the number to watch as the fan-out grows.

**The page**, in a real browser against the production build — navigation to the dashboard being
on screen with its data in it, 20 measurements after 3 warm-ups, each preceded by a navigation
away so no measurement is a cache read:

|         |      ms |
| ------- | ------: |
| min     |     431 |
| p50     |     513 |
| **p95** | **641** |
| max     |     744 |

**641 ms against a budget of 1500 ms.**

### What these numbers are not

They are loopback, on one machine, with the database on the same host. They are the endpoint's own
work and the browser's own work with the network taken out, and pretending otherwise would make
them meaningless in both directions. What a clinic's wifi adds is a property of the clinic; what
this measures is that the screen is one request rather than twelve, which is the part the wifi
multiplies.

The browser number is on a **warm session and a warm bundle**. A cold first visit of the day
additionally pays for the JavaScript, which is a property of the application shell rather than of
this screen.

---

## Permissions

| Interface action               | Server permission       | Who                                                        |
| ------------------------------ | ----------------------- | ---------------------------------------------------------- |
| `dashboard.view`               | `patient.read.clinical` | Physician, junior doctor, QA                               |
| `summary.view`                 | `ai.synthesis.read`     | Physician, junior doctor, QA                               |
| `summary.request`              | `ai.synthesis.request`  | …and the exercise specialist, who may ask and may not read |
| `dashboard.suggestions.decide` | `ai.suggestion.approve` | Physician                                                  |

Four actions rather than one, and each split is the checkpoint rather than tidiness. Reading the
screen and reading the AI narrative are separately gated on the server, and an interface that
folded them would draw an empty centre column instead of saying it was withheld. Answering a
drafted suggestion is narrower than reading it — agreeing with a machine's proposal about a
patient is an act, not a look. Asking for a summary is narrower in the other direction: §7.1 gives
it to the assistant who finishes the last station, who has no business reading the answer.

---

## What is not here

**The timeline** (CP74), **prescription editing** (CP81) and **the records panel** (CP110). Each is
its own screen and its own read; folding them in would make one request cost what three screens
cost.

**A drug list.** The patient's current medicines are exactly what a physician wants beside a
drafted prescription, and what the record holds today is the history station's note of what the
patient _said_ they take, without this clinic's own prescriptions — which do not exist yet. Half a
drug list beside a drafted drug is worse than none. CP81.

**Coded diagnoses.** There is no diagnosis table until CP81, so §8's "active diagnoses" is served
by the coded history — comorbidities and complaints nobody has marked resolved. The field is
called `active_conditions` on the wire, because calling it _diagnoses_ would be claiming a
clinical act nobody performed.

**A picker of who is in the clinic now.** The dashboard with no patient shows _today's
registrations_, labelled as exactly that. The list a physician actually wants — patients with an
open visit, in queue order — has no endpoint: CP40's traffic board deliberately carries no patient
id and no name, because it is a wall display. Adding one is a query and a route, and guessing the
ordering a consultant wants would be answering a clinical question nobody asked.

**A decision recorded from a browser.** `AI_SUGGESTION_DECIDED` is a clinical write, and D-46
requires one to name its device. A browser has no device identity until D-71 is decided
(ADR-0021), so the buttons render and the write is refused with `DEVICE_REQUIRED` — the same wall
CP32's registration desk met. Inheriting it is deliberate; inventing a way round it here would be
deciding D-71 in a handler.
