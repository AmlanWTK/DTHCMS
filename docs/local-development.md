# Local development

Everything DTHCMS needs runs on your machine: no cloud account, no API key, and — after
the first image pull — no internet connection.

## Prerequisites

| Tool           | Version | Notes                          |
| -------------- | ------- | ------------------------------ |
| Docker Desktop | current | **WSL 2 backend** on Windows   |
| Go             | 1.23+   |                                |
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

| Service       | Address                         | Credentials                                       |
| ------------- | ------------------------------- | ------------------------------------------------- |
| Postgres      | `localhost:5432`                | `dthcms` / `dthcms_local_only`, database `dthcms` |
| Redis         | `localhost:6379`                | none                                              |
| MinIO API     | `http://localhost:9000`         | `dthcms` / `dthcms_local_only`                    |
| MinIO console | `http://localhost:9001`         | as above                                          |
| Mock AI + OCR | `http://localhost:8090/healthz` | none                                              |
| Mailpit       | `http://localhost:8025`         | none                                              |

Ports clash with something already running? Copy `.env.example` to `.env` and change them.

## Everyday commands

| Windows                            | macOS / Linux | What it does                                        |
| ---------------------------------- | ------------- | --------------------------------------------------- |
| `.\scripts\dev.ps1 up`             | `make up`     | Start everything, wait for healthy                  |
| `.\scripts\dev.ps1 down`           | `make down`   | Stop, **keeping** data                              |
| `.\scripts\dev.ps1 reset`          | `make reset`  | Stop and **erase** all local data, then start fresh |
| `.\scripts\dev.ps1 status`         | `make status` | What is running                                     |
| `.\scripts\dev.ps1 logs [service]` | `make logs`   | Follow logs                                         |
| `.\scripts\dev.ps1 psql`           | `make psql`   | A psql shell on the local database                  |
| `.\scripts\dev.ps1 redis`          | `make redis`  | A redis-cli shell                                   |
| `.\scripts\verify.ps1`             | `make verify` | Everything CI runs                                  |

## What is in the stack, and why

**Postgres 16** with `pgcrypto`, `pg_trgm`, `btree_gist` and `pg_stat_statements` installed
at first start. The extension list matches what production will have, so a query that works
here works there. Schemas, roles and grants are **not** created by the container — they
belong to migrations (CP06), so that every environment is built the same way.

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
