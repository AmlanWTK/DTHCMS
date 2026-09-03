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
