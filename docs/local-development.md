# Local development

Everything DTHCMS needs runs on your machine: no cloud account, no API key, and — after
the first image pull — no internet connection.

## Prerequisites

| Tool           | Version | Notes                          |
| -------------- | ------- | ------------------------------ |
| Docker Desktop | current | **WSL 2 backend** on Windows   |
| Go             | 1.25+   |                                |
| Node.js        | 22 LTS  |                                |
| pnpm           | 10+     |                                |
| Git            | 2.40+   | Git for Windows, for the hooks |

## Starting the stack

```powershell
.\scripts\dev.ps1 up          # Windows
```

```bash
make up                        # macOS / Linux
```

Both wait until every service reports healthy, then print the addresses. First run pulls
images and builds the mock service, so allow a few minutes; afterwards it is seconds.

| Service       | Address                             | Credentials                                       |
| ------------- | ----------------------------------- | ------------------------------------------------- |
| Postgres      | `127.0.0.1:5433`                    | `dthcms` / `dthcms_local_only`, database `dthcms` |
| Redis         | `127.0.0.1:6380`                    | none                                              |
| MinIO API     | `http://localhost:9000`             | `dthcms` / `dthcms_local_only`                    |
| MinIO console | `http://localhost:9001`             | as above                                          |
| Mock AI + OCR | `http://localhost:8090/healthz`     | none                                              |
| Grafana       | `http://localhost:3001`             | none (anonymous admin, local only)                |
| OTLP intake   | `127.0.0.1:4318` HTTP, `:4317` gRPC | none                                              |
| Mailpit       | `http://localhost:8025`             | none — alert email lands here                     |

Ports clash with something already running? Copy `.env.example` to `.env` and change them.

## After starting: apply the schema

```powershell
.\scripts\dev.ps1 migrate    # Windows
```

```bash
make migrate                   # macOS / Linux
```

This applies the migrations, verifies the database's invariants, and creates the
restricted login roles the application uses. **The API will not connect until it has
run**, because its default connection is `dthcms_app_local` — a role that may append to
the event ledger and may not modify it, exactly as in production. Running with those
privileges locally is deliberate: a forbidden write fails on your machine rather than in
staging a week later. See `docs/database.md`.

The database itself is unchanged by `down`; `reset` erases it, so `migrate` follows a
`reset` every time.

## The whole thing, from nothing

```powershell
.\scripts\dev.ps1 up             # the stack, waits until healthy
.\scripts\dev.ps1 migrate        # schema, local roles, invariants
.\scripts\dev.ps1 dev-seed       # the sign-in accounts
.\scripts\dev.ps1 synth-load     # a register with a clinic morning in it

cd backend; go run ./cmd/api      # terminal 1 -- the API on :8080
cd backend; go run ./cmd/worker   # terminal 2 -- background jobs (optional)
pnpm --filter web dev             # terminal 3 -- the web app on :3100
```

Then <http://localhost:3100>, and sign in as `DOC01`.

## Everyday commands

| Windows                             | macOS / Linux         | What it does                                         |
| ----------------------------------- | --------------------- | ---------------------------------------------------- |
| `.\scripts\dev.ps1 up`              | `make up`             | Start everything, wait for healthy                   |
| `.\scripts\dev.ps1 down`            | `make down`           | Stop, **keeping** data                               |
| `.\scripts\dev.ps1 reset`           | `make reset`          | Stop and **erase** all local data, then start fresh  |
| `.\scripts\dev.ps1 status`          | `make status`         | What is running                                      |
| `.\scripts\dev.ps1 logs [service]`  | `make logs`           | Follow logs                                          |
| `.\scripts\dev.ps1 psql`            | `make psql`           | A psql shell on the local database                   |
| `.\scripts\dev.ps1 redis`           | `make redis`          | A redis-cli shell                                    |
| `.\scripts\dev.ps1 migrate`         | `make migrate`        | Apply migrations and create local roles              |
| `.\scripts\dev.ps1 synth-load`      | `make synth-load`     | Load a synthetic clinic through the event ledger     |
| `.\scripts\dev.ps1 migrate-status`  | `make migrate-status` | Which migrations have been applied                   |
| `.\scripts\dev.ps1 observability`   | `make observability`  | Re-provision dashboards and alerts, then verify them |
| `.\scripts\check-observability.ps1` | —                     | Is observability working? A terminal answer, no UI   |
| `.\scripts\verify.ps1`              | `make verify`         | Everything CI runs                                   |

## What is in the stack, and why

**Postgres 16** with `pgcrypto`, `pg_trgm`, `btree_gist` and `pg_stat_statements` installed
at first start. The extension list matches what production will have, so a query that works
here works there. Schemas, roles and grants are **not** created by the container — they
belong to migrations (CP06), so that every environment is built the same way.

**grafana/otel-lgtm** — one container holding an OpenTelemetry Collector, Prometheus, Tempo
and Grafana. The API sends traces and metrics to `127.0.0.1:4318`; three dashboards and
four alert rules are installed from `deploy/local/grafana` by a script that verifies its own
work. Alert email goes to Mailpit, so you can watch an alert actually fire
([`observability.md`](observability.md)).

**Redis 7** with append-only persistence, so a restart does not silently empty the cache
mid-debugging.

**MinIO** stands in for Google Cloud Storage. The backend speaks S3 to both, so the only
difference is the endpoint. Four buckets are created, one per data class — `identifier`,
`document`, `derived`, `backup` — mirroring the split that open decision D-01 may force on
us. None is public; objects are reached through short-lived signed URLs, here as in
production. The document bucket has versioning enabled: an uploaded patient record must
never be silently replaced.

**Mock AI and OCR** (`backend/tools/mockai`) answers in the shape of the Gemini API and of
the OCR service, with deterministic canned content. Development never calls a real model.
That is partly ADR-0007 — the Gemini free tier may not receive patient data, and the habit
of calling a real endpoint is one we should never form — and partly that tests which depend
on a live model are tests that flake.

Force failure paths with a header, which is how retry, timeout and degraded-mode handling
get tested:

```bash
curl -s -X POST http://localhost:8090/v1beta/models/gemini-2.5-flash:generateContent \
  -H 'Content-Type: application/json' \
  -H 'X-Mock-Scenario: overload' \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}'
```

| Scenario   | Behaviour                              | Exercises                 |
| ---------- | -------------------------------------- | ------------------------- |
| `default`  | Normal response                        | The happy path            |
| `slow`     | Responds after a delay                 | Timeout handling          |
| `error`    | HTTP 500                               | Retry and circuit breaker |
| `overload` | HTTP 429 with `Retry-After`            | Rate-limit backoff        |
| `invalid`  | Valid HTTP, unparseable body           | Schema validation failure |
| `refusal`  | Model declines, `SAFETY` finish reason | Safety-refusal handling   |

**Mailpit** captures any outbound email so it cannot reach a real inbox. Nothing sends
email yet; it is here so that when something does, it fails safe by default.

## Signing in for the first time

The migrations create the facility, the roles and the permission catalogue, and deliberately
create **no users** — a migration that shipped a known password would eventually run somewhere
real. So a fresh stack has nobody to sign in as, and until CP69 nobody had noticed: the Go
integration tests each insert the people they need, and the web end-to-end tests mock the API.
The gap sat exactly where no test looks, between a database that is right and a person who
wants to use it.

```powershell
.\scripts\dev.ps1 dev-seed        # macOS / Linux: make dev-seed
```

Seven accounts, one per station role, all with the password `local development only`:

| Code    | Role                 | What it is for                                                    |
| ------- | -------------------- | ----------------------------------------------------------------- |
| `ADM01` | `ADMIN`              | users, devices, the audit trail, the job queue                    |
| `DOC01` | `PHYSICIAN`          | consultation, critical-value alerts, releasing quarantined events |
| `REG01` | `REGISTRATION`       | registering patients, the traffic board                           |
| `CA01`  | `CLINICAL_ASSISTANT` | vitals, examination — the screens a tablet uses                   |
| `NUT01` | `NUTRITIONIST`       | the 24-hour recall and diet planning                              |
| `EXE01` | `EXERCISE`           | the contraindication assessment and the filtered plan             |
| `QA01`  | `QA`                 | the correction queue and the supervisor's view                    |

**Seven rather than one, on purpose.** The interesting bugs in a role-scoped system are the
ones you only see as somebody who cannot do everything — a field a role may not read is
_absent from the response bytes_, not null, so signing in as the administrator and finding
that everything works tells you very little.

It is idempotent (running it again resets the passwords), it refuses in production, and it
refuses when the database already holds users it did not create — that second guard is the
load-bearing one, because the realistic accident is a local `DTHCMS_POSTGRES_URL` pointed at a
shared database during a demo, not somebody running this against production on purpose.
`DTHCMS_DEVSEED_ANYWAY=1` overrides it.

None of these accounts has a second factor, so the password is the whole of it. Enrolling one
is the administrator console's job, and worth doing once to see that flow.

## Filling the register

A seeded stack has people who can sign in and nothing for them to look at. Every screen —
the register, the traffic board, the alert list, every trend — is empty, and an empty screen
is indistinguishable from a broken one. `cmd/synthload` fixes that by walking a synthetic
cohort through the clinic:

```powershell
.\scripts\dev.ps1 synth-load        # macOS / Linux: make synth-load
```

Sixty patients registered over the past year or two, their follow-up visits closed with
measurements attached, and sixteen of them part-way through **today** — waiting, called or at
a station, with four carrying a value that should make somebody's phone ring. It takes about
fifteen seconds.

| Flag                | Default | What it is                                                    |
| ------------------- | ------- | ------------------------------------------------------------- |
| `-n`                | 60      | how many patients to generate                                 |
| `-seed`             | 1       | the same seed and profile always give the same people         |
| `-today`            | 16      | how many are part-way through today's clinic                  |
| `-in cohort.ndjson` | —       | load a cohort `synthgen` already wrote, instead of generating |

`make synth-load N=200 SEED=42 TODAY=40` for a busier morning.

**Everything it writes goes through the event ledger**, using the same domain services the
API calls, with the same synchronous projections inside the same transactions. Nothing is
inserted into `read.*`. That is what makes the loaded database a database you can trust what
you see in: `make migrate-verify` still passes afterwards, and a projection rebuild
reproduces every screen rather than emptying it.

Each act is attributed to the `devseed` account that would really have performed it — `REG01`
registers, `CA01` takes the vitals, `NUT01` the diet recall, `DOC01` closes the visit — so
attribution on every screen reads as a clinic rather than as one robot. The device on those
events is a reserved id belonging to no tablet, which is what "typed by nobody" looks like in
a query.

It refuses in production, and it refuses when the database already holds patients — whether
somebody else's, because a local `DTHCMS_POSTGRES_URL` is still pointed at a shared database,
or its own from a previous run, because it is **not idempotent** and a second run would double
the register rather than reconcile with it. `DTHCMS_SYNTHLOAD_ANYWAY=1` overrides both.

Two things it does not load, and they are absences worth knowing about rather than bugs:
**coded medical history**, because a coded diabetes diagnosis puts the patient behind CP57's
counselling gate and satisfying that would mean inventing seven conversations nobody had; and
**consent records**, because a fresh database has no consent template and nothing in the
system authors one. The registrations carry a paper consent reference instead.

### Seeing it

The loaded data is reachable from an **enrolled device**, not from a plain browser session.
Every clinical read handler builds its actor from the request, and an actor needs a device
[R-03] — so a session opened with only a password gets `DEVICE_REQUIRED` on the register, the
board and the alert list. That is D-71, and it is unchanged by this command: enrol a device
through the administrator console (`POST /v1/devices`, then `POST /v1/auth/device/enrol`) and
sign the requests, as the tablet does.

The asynchronous read models — `read.station_activity`, which is what §14.2's bottleneck
analysis reads — stay empty until `cmd/projector` runs, exactly as they would after a real
clinic day:

```powershell
cd backend; go run ./cmd/projector run
```

## Running the backend

With the stack up, the API needs no configuration at all — every default points at the
local services:

```powershell
cd backend
go run ./cmd/api
```

```powershell
# in another terminal
Invoke-RestMethod http://localhost:8080/healthz
Invoke-RestMethod http://localhost:8080/readyz
Invoke-RestMethod http://localhost:8080/version
```

`/readyz` reports each dependency by name. Stop Redis and watch it turn:

```powershell
docker compose stop redis
Invoke-RestMethod http://localhost:8080/readyz   # 503, redis: unavailable
docker compose start redis
```

There are four binaries, all sharing the same configuration and shutdown behaviour:

| Binary         | Purpose             | Real work arrives at      |
| -------------- | ------------------- | ------------------------- |
| `cmd/api`      | HTTP API            | modules, from CP15 onward |
| `cmd/worker`   | Background jobs     | CP69                      |
| `cmd/realtime` | WebSocket gateway   | CP26                      |
| `cmd/migrate`  | Database migrations | CP06                      |

### Configuration

Every setting is an environment variable prefixed `DTHCMS_`, and the defaults are the
local stack. A few worth knowing:

| Variable                      | Default            | Notes                                                     |
| ----------------------------- | ------------------ | --------------------------------------------------------- |
| `DTHCMS_ENV`                  | `local`            | `local`, `test`, `dev`, `staging`, `production`           |
| `DTHCMS_HTTP_ADDR`            | `:8080`            |                                                           |
| `DTHCMS_POSTGRES_URL`         | local stack        |                                                           |
| `DTHCMS_REDIS_ADDR`           | `127.0.0.1:6380`   |                                                           |
| `DTHCMS_AI_TIER`              | `mock`             | `mock`, `free`, `paid` — see ADR-0007                     |
| `DTHCMS_LOG_LEVEL`            | `info`             | `debug`, `info`, `warn`, `error`                          |
| `DTHCMS_LOG_FORMAT`           | `json`             | `text` is easier to read while developing                 |
| `DTHCMS_SECRET_KEY_ID`        | `local-1`          | Names the key that seals secrets at rest (ADR-0012)       |
| `DTHCMS_SECRET_KEY`           | a known local key  | 32 bytes, base64. Refused outside `local`/`test`          |
| `DTHCMS_AUDIT_SIGNING_KEY_ID` | `audit-local-1`    | Names the key that signs audit exports (CP22)             |
| `DTHCMS_AUDIT_SIGNING_SEED`   | a known local seed | 32 bytes, base64, Ed25519. Refused outside `local`/`test` |
| `DTHCMS_SECRET_PREVIOUS_KEYS` | empty              | `id=key,…` — old keys still able to open, for rotation    |

**A misconfigured process refuses to start**, and reports every problem at once rather
than the first:

```
api: cannot start: configuration is invalid (5 problem(s)):
  - DTHCMS_LOG_LEVEL="shouty" is not a level
  - DTHCMS_AI_TIER=free is not permitted in production: the Gemini free tier may be
    trained on and read by human reviewers (ADR-0007)
  - DTHCMS_POSTGRES_URL must not disable TLS in production
  - DTHCMS_POSTGRES_URL still contains the local development password
  - DTHCMS_BLOB_USE_SSL must be true in production
```

That is deliberate. A wrong setting found at deploy time costs minutes; the same setting
found at 11:40 on a clinic morning, through a failure nobody can explain, costs far more.

## About the credentials

They are weak, and they are committed on purpose. This stack must never hold real data, so
hiding its passwords would be theatre rather than security. Production secrets live in
Secret Manager and never in a file — and the pre-commit hook will block a `.env`, a
`.pem` or anything credential-shaped from entering the repository.

## Hot reload

Once there is a server to run (CP05):

```bash
go install github.com/air-verse/air@latest
cd backend && air
```

Configuration is in `backend/.air.toml`. Nothing depends on it.

## Troubleshooting

**"Docker is installed but the engine is not running"** — start Docker Desktop and wait for
the whale icon to settle.

**A port is already in use** — copy `.env.example` to `.env` and change the port. A local
Postgres on 5432 is the usual culprit.

**Postgres starts but the extensions are missing** — the init script runs only when the
data volume is created. `.\scripts\dev.ps1 reset` recreates it.

**The mock service will not build** — it builds from the repository root using
`deploy/local/mockai.Dockerfile`. Check that Docker has enough disk, then
`docker compose build --no-cache mockai`.

**A service answers, but it is the wrong one.** This has now happened twice on Windows:
a natively-installed PostgreSQL on 5432, and Memurai — a Redis-compatible server — on 6379. Both answer correctly, so nothing appears broken until data goes missing or a
health check lies. That is why the stack publishes **5433** and **6380** rather than the
standard ports. Check who owns them:

```powershell
foreach ($p in 5432, 5433, 6379, 6380, 8080, 8090, 9000, 9001, 8025) {
  Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue |
    Select-Object @{n='Port';e={$p}}, LocalAddress,
                  @{n='Process';e={(Get-Process -Id $_.OwningProcess).ProcessName}}
}
```

On start-up the backend logs what it actually connected to — server version and operating
system — so a substitution of this kind is visible in the first three lines rather than
discovered later.

**`password authentication failed for user "dthcms"`** — you are reaching a different
PostgreSQL. A natively-installed server on the host commonly holds port 5432 and wins it;
the credentials are right, the server is wrong. Find out who owns the port:

```powershell
Get-NetTCPConnection -LocalPort 5432 -State Listen |
  Select-Object LocalAddress, @{n='Process';e={(Get-Process -Id $_.OwningProcess).ProcessName}}
```

If `postgres` appears there and it is not Docker, that is the culprit. This is why the
container publishes **5433**, not 5432. Confirm the container itself is fine — this
connects inside it, bypassing the host network entirely:

```powershell
docker compose exec postgres psql "postgresql://dthcms:dthcms_local_only@127.0.0.1:5432/dthcms" -c "select current_user"
```

**`An attempt was made to access a socket in a way forbidden by its access permissions`**
— Windows has reserved that port range, usually for Hyper-V or WinNAT. Nothing is using
the port; the operating system simply will not allow a bind. List the reserved ranges:

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

Pick a port outside them — below 49152 is generally safe — and set it in `.env`. This is
why the stack uses 5433 rather than something in the 50000s.

**Buckets are missing, or bucket creation failed** — the task that creates them runs
separately from the main stack, because `docker compose up --wait` treats a container that
exits as a failure even when it exited successfully. Run it again on its own:

```bash
docker compose --profile init run --rm minio-init
```

**A pull times out mid-download** — a slow link, not a broken setup. Docker keeps the layers
it already fetched, so pulling the single image again resumes rather than restarting:
`docker pull postgres:16-alpine`, then start the stack as usual.

**Everything is strange after a Docker Desktop upgrade** — `.\scripts\dev.ps1 reset` is
almost always the answer, and it is safe: there is nothing here worth keeping.
