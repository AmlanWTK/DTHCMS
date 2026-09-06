# Who entered this

CP61. Blueprint §4.2, [R-03]. Dr. Nahid's requirement, verbatim:

> Any reviewer sees who entered a value **instantly, without digging**.

Two words in that sentence decide the design. _Instantly_ means one interaction — a hover or a
keyboard focus on the web, one tap on a phone — never a navigation. _Without digging_ means the
answer is beside the value, not in an audit screen somebody has to know exists.

---

## A directory, not a name on every value

Every clinical read already carried the ids: who recorded it, in what role, at which station,
from which device, and how it reached the server. What none of them carried was a **name**, and a
uuid answers a different question from the one a physician is asking.

The obvious fix is to join the staff record into every clinical query. It was rejected:

- a patient's timeline is hundreds of values written by a dozen people, and joining two rows onto
  each of them copies the same twelve names into every payload all day;
- every future clinical read would have to remember the join, and the failure when somebody
  forgets is a screen that renders a blank rather than an error anybody notices.

So `GET /v1/directory` returns the small, closed sets — staff, devices, stations — once per
session, and the client resolves. The name becomes a **lookup** rather than a property of the
value, which is what it actually is.

**Deactivated staff and retired devices stay in the list.** Attribution on a value taken last
March names whoever took it, and much of what a reviewer asks about is somebody who has since
left; a directory of current staff only would render a blank for exactly the person the question
is about. `status` is reported so a screen can say "no longer at the clinic" rather than sending
somebody to ask a colleague who has gone.

Nothing sensitive is in it: names, staff codes, device names, station names. No contact details,
no credentials, no role grants — the role a value carries is the role its author was wearing _at
the time_, which belongs on the value rather than on the person. A session is the only
requirement, because every clinical screen renders attribution and there is no patient in the
response.

---

## What was missing, and what filling it cost

Three read models were dropping half the answer on the floor. An observation recorded the device
and the station; a history item, an allergy and an allergy assertion did not — although the event
behind every one of them carried both in its envelope since CP24.

That asymmetry was not a design. It is what happens when each checkpoint writes the columns its
own screen happened to need, and the failure it produces is small and permanent: "which tablet
recorded this penicillin allergy" is answerable for a weight and unanswerable for the allergy,
and nobody notices until the question is asked about a device that turns out to have been shared.

Migration `00040` adds the columns and fills them from the envelope, and a standing invariant —
`assert_every_clinical_record_is_attributed` — is what notices the next projection that stops
filling one in. Rows written before it keep a null device and an empty station and source, which
reads as an honest absence rather than as a claim.

---

## Three states, not two

`source` is deliberately three-valued on the client:

| value    | means                                                   |
| -------- | ------------------------------------------------------- |
| absent   | this record type has no source field at all             |
| empty    | the record has the field and nobody filled it           |
| a string | STATION, OCR, FIELD, MOBILE_ONLINE, MOBILE_OFFLINE_SYNC |

Collapsing the middle case into the first draws an unknown provenance as nothing, and nothing
looks exactly like a station entry — on the whole archive written before the migration. So a
blank source says **"source not recorded"** in words.

Criterion 3 — an OCR-sourced value visibly different from a typed one — is carried by a **word
and a shape**, not by a tint alone: this is read on cheap tablets, in a room with a window, by
people one man in twelve of whom cannot use hue.

---

## What is still missing, and why it is not patched

- **A growth percentile carries no author.** The card matches the score back to the observation it
  was computed from, and a percentile whose measurement cannot be found says it cannot say who —
  rather than borrowing the newest measurement's name, which would be a plausible-looking wrong
  answer.
- **An observation names no corrector.** `status` and `replaced_by` say a value was corrected and
  point at the replacement; nothing says who wrote it. That is CP62's chain, and the screens say
  the value was changed and name nobody until it exists.
- **A critical alert has no source of its own, and it is not getting one.** An alert's number is a
  _snapshot_ of an observation, and a copied provenance beside a copied value drifts from the
  original the moment the observation is corrected. The alert carries `observation_id`; following
  that link is the honest fix. The cost is named rather than hidden: today the alarm screen cannot
  say that a critical number was read off a photograph.

---

## The audit test

Criterion 4 is "no screen renders a clinical value without the component", and the only way to
keep that true is a test that fails when somebody does. Both clients carry one, and both say in
their own comments what they cannot catch — a value pre-formatted into a bare string, a payload
type not on the list, whether the attribution shown belongs to the value beside it.

Each also carries **canaries**: hand-written wrong source that the matchers are asserted to flag.
A scan that cannot fail is worse than no scan, and the web canary earned its place immediately —
it caught a regex that missed indented imports, which would have left the audit quietly passing
everything.
