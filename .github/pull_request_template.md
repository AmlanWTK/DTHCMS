## Checkpoint

<!-- e.g. CP01 â€” Repository, Monorepo Scaffolding & CI Skeleton -->
**CP:**
**What this delivers:**

## Scope discipline

- [ ] Everything in the checkpoint's SCOPE is implemented
- [ ] Nothing in its OUT OF SCOPE list is implemented
- [ ] Any new ambiguity found during implementation is raised as an open decision, not guessed

## Definition of Done

**Implementation**
- [ ] Follows the project coding standards; all linters pass
- [ ] No `TODO`s or commented-out code left behind (deferred work is a tracked issue)

**Testing**
- [ ] Unit tests cover this change, including failure paths
- [ ] Integration tests cover data and service interactions where applicable
- [ ] All tests pass in CI â€” not "pass locally", not "pass except one flaky test"

**Verification**
- [ ] The checkpoint's MANUAL VERIFICATION procedure has been performed, and the result recorded below
- [ ] Every ACCEPTANCE CRITERION is objectively satisfied
- [ ] Clinical behaviour (if any) has been verified by Dr. Nahid

**Security and data**
- [ ] No secrets in code, config, logs or fixtures
- [ ] No patient data of any kind â€” synthetic only
- [ ] New endpoints declare and enforce permissions
- [ ] Migrations included, reversible where feasible, and tested

**Interface**
- [ ] Loading, empty, error and offline states implemented
- [ ] Renders correctly in Bangla and English
- [ ] Clinical values use the attribution and dual-unit components

**Documentation**
- [ ] Architecture docs / ADRs updated if a decision was made
- [ ] The open-decision register is updated â€” decisions recorded, new ambiguities added

## Manual verification performed

<!-- What you actually did, and what you observed. Screenshots welcome. -->

## Open decisions raised or resolved

<!-- D-nn references, or "none" -->