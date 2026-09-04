# The hard stop

CP54. Blueprint §3 step 4's checkpoint, [R-01]. Decision: ADR-0028.

> No file proceeds without either "No Known Allergies" explicitly asserted or a coded allergy
> entry.

Four words in the acceptance criteria decide the whole design:

> 4. The gate cannot be bypassed by any client.

---

## Where the gate is, and why it is not in Go

A rule enforced in an application is a rule that holds for the paths somebody remembered. The
path nobody remembers is the one that matters — a support script at eleven at night, a
migration, next year's second client, the integration written after everyone who read the plan
has left.

So the gate is a **trigger on `core.queue_entry`**:

```
BEFORE INSERT ON core.queue_entry
  → if the target station's sequence_hint is past the history station's
      and core.allergy_status(patient) = 'NONE_RECORDED'
        → refuse
```

`TestTheGateCannotBeBypassedByAnyClient` proves it the only honest way: with a plain `INSERT`,
not through the API at all.

A standing invariant then guards the gate itself. A migration that dropped the trigger and kept
the function would leave a clinic with no hard stop **and no error** — which is the worst
available outcome, because everybody would still believe there was one.
`assert_the_allergy_gate_is_wired` fails the deployment instead.

The station comparison uses each station's own `sequence_hint`, so a clinic that reorders its
floor does not silently move the checkpoint. Re-entering the history station is always allowed —
it is where the status gets recorded.

---

## Three answers, and why there is no fourth

| Status               | Means                                               | Gate       |
| -------------------- | --------------------------------------------------- | ---------- |
| `NONE_RECORDED`      | nobody has asked                                    | **closed** |
| `ALLERGIES_RECORDED` | one or more on file                                 | open       |
| `NO_KNOWN_ALLERGY`   | asked, and the patient knows of none                | open       |
| `UNABLE_TO_ASSESS`   | asked, and no answer could be got — with the reason | open       |

The obvious objection to an absolute gate is the unconscious patient, or the child brought in
by a neighbour who does not know. Blocking them from the consultant would be worse than an
unrecorded allergy, so the usual answer is an **override**: a button that advances them anyway,
with a reason.

That answer is wrong here, and the plan says why in its own risk note:

> Operators may assert NKA reflexively to clear the gate.

An override is the same failure with better manners. A gate with a way past it is a gate people
learn the shape of, and within a month the override is the normal path.

`UNABLE_TO_ASSESS` is the honest alternative. It **is** allergy status: somebody looked,
somebody is named, and the record says what was found — which is emphatically not that the
patient has no allergies. The medication safety engine will treat the two very differently, and
it can only do that because they are different rows rather than one row and a missing one.

It requires a reason for the same purpose: the point of the third state is that it is
reviewable, and "we could not ask" with no reason is a silent gap wearing a label.

---

## "No known allergies" is never a default

Criterion 2, made structural rather than remembered. There is **no column anywhere in this
system that means "no allergies" by being empty.** The only way to say it is an event, with an
actor, in the ledger.

A standing invariant refuses a `DEFAULT` on `kind` or `asserted_by`, because that is the shape
the failure would actually take: not a bad row, but somebody adding a default to make something
pass, after which every patient asserts and none of them was asked.

The corollary matters on every screen: **an empty allergy list means two opposite things.** A
patient with `NONE_RECORDED` and one with `NO_KNOWN_ALLERGY` both have zero allergies, and a
header that drew them the same way would be lying in the safe-looking direction. Both surfaces
carry a named test for exactly that.

---

## The risk the plan names, and what is done about it

> Mitigate by requiring the assertion to be a deliberate action and by surfacing NKA rates per
> operator in QA.

`GET /v1/allergies/assertion-rates` is that view — per operator, over a window, defaulting to
thirty whole days.

It is deliberately **a view and not a rule**. An officer whose patients genuinely have no
allergies sits near the top of that list, and so does one who taps the button without asking.
An automatic threshold would punish the first and be gamed by the second. It belongs in front
of a QA officer.

The window is half-open — `from ≤ t < to` — so two consecutive windows count every assertion
exactly once, and the default bounds are whole days rather than "until this instant". An upper
bound of _now_ would silently exclude the assertion somebody just made, which is the one a QA
officer checking their own screen is most likely to look for and least likely to doubt.

---

## Everything else about the record

**Coded where the catalogue has the substance, in words where it does not.** The escape hatch
matters more here than anywhere else in the system: an allergy nobody could code is far more
dangerous in a note field than it is on the header, marked as uncoded.

**The reaction is checked against the vocabulary.** The alternative is a row a header cannot
render, and an allergy that shows as a blank line is worse than one nobody recorded — the blank
line reads as "checked, nothing found".

**`is_emergency` belongs to the reaction, not the severity.** Anaphylaxis is an emergency
whatever the operator ticked beside it, and a screen that let "anaphylaxis, mild" through would
be recording a contradiction. The list comes back worst first, so a header showing three
allergies leads with the one that stops a heart rather than burying it under a rash from 1998.

**Recording an allergy does not withdraw an earlier NKA.** Both are true statements about their
own moment — somebody asked in March and was told there were none, somebody found one in June —
and the record keeps both. A live allergy simply outranks any assertion when the status is
worked out. Withdrawing the March row would delete the fact that anybody asked.

**Nothing is deleted.** An allergy somebody withdrew is one somebody disagreed with, and the
next clinician needs to know a colleague once believed it. The change history keeps both halves.

**Reading is not blinded.** `patient.read.allergies` reaches the pharmacist and the prescription
educator — roles §4.4 blinds to diagnoses — because an allergy has to reach the person handing
over the medicine. Blinding them would mean the last person who could catch the mistake is the
one who cannot see the warning.

---

## What is still open

- **The substance list.** The plan marks its source as needing clinical confirmation. Twenty-one
  seeded allergens, drug classes and individual drugs both, because a patient says "penicillin"
  far more often than "amoxicillin". A proposal.
- **The manual verification the plan asks for**: try to move a patient to the next station
  without allergy status, and confirm it is impossible and that the message explains why. The
  gate is tested; the message on a real floor at eleven in the morning is not.
- **Cross-reactivity** is explicitly out of scope and belongs to the medication safety engine.
- **The QA rate view has no screen yet** — the endpoint and the typed client call exist; the QA
  console is a later checkpoint and dropping a table into its placeholder would pre-empt it.
