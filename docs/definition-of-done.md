# Definition of Done

A checkpoint is **not** done because the code works on the developer's machine.

This list is the project-wide standard, referenced by the pull request template and applied
at every checkpoint. Items that do not apply to a particular checkpoint are marked so
explicitly — "not applicable" is a statement, "unchecked" is a question.

## Universal — every checkpoint

### Implementation

1. Everything in the checkpoint's SCOPE is implemented.
2. Nothing in its OUT OF SCOPE list is implemented. **Scope creep is a defect**, not generosity.
3. Code follows `docs/engineering-standards.md`; all linters pass, including `dthclint`.
4. No architectural boundary violations.
5. No `TODO` comments or commented-out code in the merged result. Deferred work is a tracked
   issue with a checkpoint number, not a comment nobody will find.

### Testing

6. Unit tests cover the checkpoint's logic, including boundaries and failure paths.
7. Integration tests cover its data and service interactions, **against real Postgres and
   Redis** — not mocks. Mocking the database is how event-sourcing bugs reach production.
8. All tests pass in CI. Not "pass locally". Not "pass except one flaky test".
9. Coverage meets the agreed floor for the affected packages.
10. Every bug found during the checkpoint has a regression test. A fixed bug with no test
    is a bug scheduled to return.

### Verification

11. The checkpoint's MANUAL VERIFICATION procedure has been performed, and the result
    recorded in the pull request.
12. Every ACCEPTANCE CRITERION is objectively satisfied and demonstrated.
13. **For clinical checkpoints: Dr. Nahid has personally verified the clinical behaviour.**
    A passing test proves the code does what the developer intended; only the clinician can
    confirm the intention was right.

### Quality

14. No known blocking or critical bugs remain.
15. Known non-blocking issues are recorded as tracked items with a severity.
16. Performance is within the stated budget for the paths this checkpoint touches.

### Security

17. Security implications are assessed and stated in the pull request.
18. New endpoints declare and enforce permissions.
19. New data is classified (`IDENTIFIER` / `CLINICAL` / `DOCUMENT` / `DERIVED` / `ANALYTIC`)
    and handled according to its class.
20. No secrets in code, configuration, logs or fixtures.
21. **No patient data of any kind.** Synthetic only, always.
22. Automated security scans pass.

### Data

23. Migrations are included, reversible where feasible, and tested against a
    production-shaped snapshot.
24. New tables follow the naming and column conventions, including `facility_id` where applicable.
25. Indexes exist for the queries introduced, justified by `EXPLAIN`.
26. **Clinical writes go through the event store. No exceptions.**

### Contracts

27. New or changed endpoints appear in the OpenAPI specification; the conformance check passes.
28. Generated clients are regenerated and committed.
29. Breaking changes are versioned and communicated.

### Interface

30. UI work is integrated and reachable — not an orphaned component in Storybook.
31. All states are implemented: loading, empty, error, success, offline, unauthorised.
32. Renders correctly in Bangla and English.
33. Accessibility checks pass; keyboard operation works on web; touch targets meet the
    minimum on mobile.
34. Clinical values render through the attribution and dual-unit components — never as raw text.

### Observability

35. Meaningful logs, metrics and traces exist for the new paths.
36. New failure modes are alertable.
37. New background jobs report to the queue dashboard.

### Documentation

38. Architecture documentation is updated if the architecture changed.
39. An ADR exists for any significant decision.
40. Runbook entries exist for new operational procedures.
41. User-facing changes are reflected in training material.
42. **The open-decision register is updated** — decisions made are recorded, and new
    ambiguities discovered during implementation are added rather than resolved silently.

## Additional, by checkpoint type

| Type         | Also required                                                                                                                                                                   |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Clinical** | Formulas verified against published reference values · Dr. Nahid's sign-off · fail-closed behaviour tested · the version of any clinical content used is recorded with the data |
| **AI**       | Grounding validation active · evaluation set run and recorded · PHI minimisation verified · AI output visibly labelled · degraded mode tested · cost measured, not assumed      |
| **Offline**  | The full offline test matrix passes · local-versus-server integrity check clean · sync status accuracy verified with a non-technical user                                       |
| **Security** | Threat model updated · authorisation tests for every new resource · penetration-test scope updated                                                                              |
| **Print**    | **Printed on the actual clinic printer and approved on paper** · Bangla rendering verified including conjuncts · output is deterministic                                        |
| **Research** | Anonymisation verified · statistics verified against an independent implementation · small-cell suppression active                                                              |

## The phase gate

A phase closes only when every checkpoint in it is done, **the audit trail is verified end
to end by Dr. Nahid on a real patient journey**, no critical defects are open, performance
and security reviews have passed, training is delivered, and Dr. Nahid has signed off in
writing.

This mirrors blueprint §15.3: _"Phase acceptance = features live + audit trail verified
end-to-end + Dr. Nahid's sign-off. No phase closes with open critical defects."_
