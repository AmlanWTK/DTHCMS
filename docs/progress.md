# Checkpoint progress

The running record of what has been built, so that any session — or any engineer joining
later — can see the state of the project without reading the whole implementation plan.

Full specifications: [`implementation-plan.md`](implementation-plan.md) §16.

| CP   | Name                                             | Status       | Notes                                                                                               |
| ---- | ------------------------------------------------ | ------------ | --------------------------------------------------------------------------------------------------- |
| CP01 | Repository, monorepo scaffolding & CI skeleton   | **Done**     | Repo, hooks, CI, blueprint custody hashes recorded                                                  |
| CP02 | Architecture guardrails, ADRs & coding standards | **Done**     | `dthclint` arch + PHI checks; 7 ADRs; standards; Definition of Done                                 |
| CP03 | Cloud project, environments & IaC baseline       | **Deferred** | Hosting decision postponed (D-01). Nothing before CP69 needs it                                     |
| CP04 | Local development environment                    | **Done**     | Postgres, Redis, MinIO, mock AI/OCR, mail capture; one-command start                                |
| CP05 | Go backend skeleton & platform layer             | **Done**     | Four binaries, fail-fast config, PHI-safe logging, error model, health endpoints, graceful shutdown |
| CP06 | Database foundation & migration framework        | **Done**     | goose migrations embedded; six schemas; grants making `ledger` append-only at the database; sqlc    |
| CP07 | Observability baseline                           | Next         |                                                                                                     |
| CP08 | Prototype assessment                             | **Closed**   | No prototype and no patient data exist (D-51). One-line decision record                             |
| CP09 | Design system foundation                         |              |                                                                                                     |
| CP10 | Web application shell                            |              |                                                                                                     |
| CP11 | Mobile application shell                         |              |                                                                                                     |
| CP12 | API contract & generated clients                 |              |                                                                                                     |
| CP13 | Test harness & synthetic data generator          |              |                                                                                                     |
| CP14 | Phase 0 review & architecture sign-off           |              |                                                                                                     |

## Decisions taken so far

| ID   | Decision                                                                                                                 | Recorded in                                                  |
| ---- | ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------ |
| D-51 | No prototype, no existing patient data — greenfield                                                                      | Implementation plan §3.H                                     |
| D-52 | Built by Dr. Nahid and Claude; second engineer recommended by Phase 2                                                    | Implementation plan §3.H                                     |
| D-07 | Google Gemini as the AI provider; free tier for development only, paid or Vertex AI for anything touching a real patient | [ADR-0007](adr/0007-gemini-provider-and-tier.md)             |
| —    | Modular monolith rather than microservices                                                                               | [ADR-0002](adr/0002-modular-monolith.md)                     |
| —    | Event sourcing for clinical data only                                                                                    | [ADR-0003](adr/0003-event-sourcing-scope.md)                 |
| —    | Offline-first station app from Phase 1                                                                                   | [ADR-0004](adr/0004-offline-first-from-phase-1.md)           |
| —    | Staff authentication implemented in-house                                                                                | [ADR-0005](adr/0005-self-implemented-staff-auth.md)          |
| —    | PostgreSQL now, AlloyDB only when measured                                                                               | [ADR-0006](adr/0006-postgresql-first.md)                     |
| —    | The append-only ledger is enforced by database privilege, not application code                                           | [ADR-0008](adr/0008-database-enforced-append-only-ledger.md) |
| D-61 | `facility_id` on every facility-scoped table from CP06, enforced by an assertion                                         | [`database.md`](database.md) §3                              |

## Still blocking, by the checkpoint they gate

| Decision                                        | Gates                          | Owner                     |
| ----------------------------------------------- | ------------------------------ | ------------------------- |
| D-01 hosting region and PDP Act posture         | CP03, and everything deployed  | Dr. Nahid + legal counsel |
| D-21 pediatric growth reference standard        | CP47, CP48                     | Dr. Nahid                 |
| D-27 critical-value table and escalation        | CP50                           | Dr. Nahid                 |
| D-22 drug knowledge base and interaction source | CP77, CP78                     | Dr. Nahid                 |
| D-24 terminology: ICD version, SNOMED licence   | CP52                           | Legal + clinical          |
| D-53 counseling template content                | CP55                           | Dr. Nahid                 |
| D-70 administrator account recovery             | Before CP16 reaches production | Dr. Nahid                 |

## Carried forward

| From | Item                                                                                                                                                                    | Lands at |
| ---- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| CP06 | _Migrate-up on a restored snapshot._ Covered in part by `TestMigrationsRunOverExistingData`, but there is one wave of migrations and no production data to snapshot yet | CP23     |

Clinical content authoring (implementation plan §17.4) can start at any time and does not
wait for software. It is the most common cause of slip in projects of this shape.
