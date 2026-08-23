# 9. Observability is OpenTelemetry-native, and telemetry fails open

Date: 2026-08-23

## Status

Accepted

## Context

Two questions had to be answered before the first line of clinical code, and neither could
wait for the hosting decision (D-01).

**Which backend?** The implementation plan assumes Google Cloud Monitoring, Trace and
Logging. That assumption is unresolved (D-35) and depends on a hosting decision that
depends in turn on a legal one about where Bangladeshi patient data may live. Writing
against a vendor SDK now would mean rewriting instrumentation if the answer is anything
else — and instrumentation is spread through every layer, so that rewrite touches
everything.

**What happens when telemetry breaks?** The default behaviour of most instrumentation is
to fail loudly: an exporter that cannot reach its collector retries, blocks, logs on every
attempt. In a clinic, that turns an unreachable metrics backend into slow requests and an
unreadable application log — an outage caused by the thing that was supposed to explain
outages.

## Decision

**Everything is OpenTelemetry, exported over OTLP.** No vendor SDK appears in application
code. Backends are chosen by pointing `DTHCMS_OTEL_ENDPOINT` at a collector; changing from
one to another is a configuration change and a collector configuration, not a code change.

Locally, a single `grafana/otel-lgtm` container receives it. Dashboards and alert rules are
committed as JSON and installed through Grafana's API by a script that then verifies its
own work.

**Telemetry fails open.** An unreachable collector never prevents a process from starting
or a request from being served. Export failures are reported through a throttled error
handler — once a minute at most — because a collector that is down fails every batch.

The one inversion: production refuses to start with telemetry disabled or sent in the
clear. Fail-open is about runtime failures, not about being configured blind.

**PHI redaction applies to spans and metric labels, not only logs**, over one shared key
list, enforced at build time by `dthclint` and at run time by both the log handler and a
redacting span exporter.

## Consequences

**A backend switch is cheap.** Whatever D-01 decides — Cloud Monitoring, a self-hosted
Grafana stack in-country, something else — the application does not change.

**A local collector is now part of the stack.** One more container, one more image to
pull, and a developer machine that is doing more work. Worth it: instrumentation nobody
can see is instrumentation nobody trusts, and a trace that only exists in staging gets
debugged by guessing on a laptop.

**OTLP is a lowest common denominator.** Vendor SDKs offer features OTLP does not —
Cloud Profiler, error grouping, some log-based metrics. If one of those turns out to
matter, it can be added at the collector rather than in application code, which is the
right place for a vendor-specific concern anyway.

**Fail-open means telemetry can be silently absent.** A misconfigured endpoint produces a
warning once a minute and otherwise looks normal. This is the cost of the trade, and it is
why one of the four alerts fires on the _absence_ of metrics: the monitoring system's own
failure is itself monitored.

**Head sampling drops errors at the same rate as successes.** Retaining every error trace
needs tail sampling in the collector, which is a collector decision and therefore
deferred with the backend one. Local and test sample everything, so this affects
production only.
