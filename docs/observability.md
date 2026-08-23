# Observability

Established at CP07, before any clinical feature exists. Observability retrofitted after
an incident is observability that did not exist when it mattered.

---

## 1. What is instrumented

| Layer      | What you get                                                           |
| ---------- | ---------------------------------------------------------------------- |
| HTTP       | One server span per request, named for the route template; RED metrics |
| PostgreSQL | One span per query, nested under the request that issued it            |
| Redis      | One span per command                                                   |
| Process    | Goroutines, heap, GC, uptime, connection pool saturation               |
| Logs       | `trace_id` and `span_id` on every line emitted inside a span           |

Everything speaks OTLP. No vendor SDK appears anywhere in the code, which is what makes
the backend decision (D-35) a configuration change rather than a rewrite —
see [ADR-0009](adr/0009-vendor-neutral-observability.md).

## 2. Looking at it

```powershell
.\scripts\dev.ps1 up                  # Grafana on http://localhost:3001, DTHCMS folder
.\scripts\check-observability.ps1     # answer "is it working?" from the terminal
.\scripts\dev.ps1 observability       # re-provision dashboards and alerts
```

`check-observability.ps1` reports whether the dashboards and alert rules are installed,
whether metrics and traces are arriving, and — the check that is least visible and matters
most — whether metric labels are route templates rather than raw paths. Add `-Detailed`
to print every route and status code actually recorded. It changes nothing.

One container, `grafana/otel-lgtm`, holds the OpenTelemetry Collector, Prometheus, Tempo
and Grafana. The application sends to `127.0.0.1:4318`.

Three dashboards, and the order to read them in during an incident:

**Errors** — is it failing? 5xx and 4xx are shown separately, deliberately: 5xx is the
system at fault, 4xx is usually the system correctly refusing something, and a dashboard
that treats them alike is one people stop reading.

**Latency** — is it slow, and where? Percentiles by route template. The rate is on the
same screen because latency without it is unreadable: slow-and-busy is a capacity
problem, slow-and-idle is a specific query, and they have opposite fixes.

**Saturation** — is it running out of something? The connection-pool gauge is the one
that earns its place. When every request suddenly takes seconds, the cause is usually not
a slow query but that every connection is checked out and requests are queueing for one.
Those look identical from outside and are fixed in opposite directions.

## 3. Alerts

Four rules, delivered by email to the address in `DTHCMS_ALERT_EMAIL` — locally, into
Mailpit at `http://localhost:8025`, so an alert firing is something you can watch happen
rather than something you have to believe.

| Alert                         | Fires when                           | Sustained | Severity |
| ----------------------------- | ------------------------------------ | --------- | -------- |
| API 5xx rate above 5%         | more than 1 request in 20 is failing | 5 min     | critical |
| p95 latency above 2 seconds   | 1 request in 20 takes over 2s        | 10 min    | warning  |
| Database pool above 80%       | most connections checked out         | 5 min     | warning  |
| The API has stopped reporting | no metrics arriving at all           | 5 min     | critical |

The thresholds are choices, and each is defended in `deploy/local/grafana/alerting/rules.json`
under `annotations.description`. In short:

**Two seconds** is where a station operator stops believing the tap registered and taps
again, turning a slow system into a busier one.

**Five and ten minutes**, not instantly. A deploy or a brief database blip produces a
spike that resolves itself, and an alert that fires on those is one people learn to
ignore — which is worse than having no alert, because it also silences the real one.

**Eighty percent** of the pool, not a hundred. At a hundred the incident has already
started.

**"Stopped reporting"** is the alert that fires when the others cannot. A process that has
crashed or hung produces no error rate and no latency, so every threshold alert goes
quiet — which looks exactly like everything being fine. That rule's `noDataState` is
`Alerting`, so silence is the alarm.

### On call

Amlan, at `DTHCMS_ALERT_EMAIL`. Dr. Nahid is not on the rota: a physician paged about
connection-pool saturation cannot act on it, and adding a recipient who cannot act is how
a rota becomes noise. That changes when CP16 puts the system in front of real patients,
at which point clinical-impact alerts get a separate route.

There is no paging, no escalation and no rota rotation, because there is one engineer and
no production system. Both arrive with the hosting decision (D-01).

## 4. PHI in telemetry

The rule for logs applies unchanged to spans and metric labels. They leave the process the
same way, reach the same third-party backend, and are retained as long — but they do not
_look_ like logging, which is exactly why the rule gets forgotten. `attribute.String` is
one character away from `slog.String`.

Three layers, over one list of keys (`internal/platform/logging.PHIKeys`):

1. **Build time.** `dthclint`'s `phi` check fails the build on a banned key in a log call,
   a span attribute or a metric label — including the namespaced forms OpenTelemetry
   conventions use, so `enduser.name` is caught as well as `name`.
2. **Run time, logs.** The slog handler redacts, catching keys built from variables that
   static analysis cannot see.
3. **Run time, spans.** Every span passes through a redacting exporter on its way out.
   This is the layer that covers attributes we did not write: most attributes on a span
   come from instrumentation libraries, and one added by a future library is scrubbed by
   the same rule.

The span exporter also scrubs two things that are not in the key list at all:

**Query strings.** `/v1/patients/0190…/visits` is fine — a patient id is an opaque
identifier and is what we ask people to record. `?q=Rahima+Begum` is a patient's name,
typed into a search box, and would otherwise travel to the telemetry backend in full.

**SQL literals.** Parameterised SQL carries nothing; `$1` is safe. A statement built by
concatenation, or a driver configured to interpolate parameters, is not. The statement
shape survives, because the shape is what makes a slow-query trace useful.

`otelpgx` is configured **without** `WithIncludeQueryParameters`. Turning that on would
put every value the application writes — names, national IDs, diagnoses — into a span
attribute.

### Cardinality is a safety property here

Metric labels use the route template, never the raw path. A label whose value varies per
patient creates one time series per patient, which breaks the metrics backend as surely as
it breaks confidentiality. Unmatched requests share the single label `unmatched`, so a
scanner probing a thousand paths creates one series rather than a thousand.

## 5. Telemetry never takes the clinic down

Every failure path degrades to "no telemetry", never to "no service". An unreachable
collector does not stop a process starting or a request being served; export failures are
logged once a minute at most, because a collector that is down fails every batch and a log
line per failure would bury the application's own output.

The one place this is inverted is production configuration: `DTHCMS_OTEL_ENABLED=false`
and `DTHCMS_OTEL_INSECURE=true` are both refused at start-up. A production incident with
no traces is diagnosed by guessing.

## 6. Sampling

Local and test record every trace: sampling exists to control cost and volume, and a
developer has neither problem — but does have the problem of the one request they care
about being the one that was not sampled.

Production uses `DTHCMS_OTEL_SAMPLE_RATIO`, parent-based, so a decision made by a station
app is honoured by the server. A server that sampled independently would produce
half-traces, which are worse than none because they look complete.

**Known limitation:** head sampling drops errors at the same rate as successes. Keeping
every error trace needs tail sampling in a collector, which is a collector configuration
decision that belongs with the hosting one (D-01/D-35).

## 7. Known gaps

| Gap                                       | Why                                                          | Lands at |
| ----------------------------------------- | ------------------------------------------------------------ | -------- |
| Disk-space alert                          | Needs host metrics, which need a host                        | CP03     |
| Tail sampling to retain every error trace | Collector configuration, tied to the backend decision        | CP03     |
| Cloud Monitoring / Trace / Logging export | Blocked on D-01; OTLP means it is a config change            | CP03     |
| Correlation into background jobs          | There is no job runner yet; the context plumbing is in place | CP32     |
| Correlation into WebSocket sessions       | There is no realtime gateway yet                             | CP60     |
| Paging, escalation, rota                  | One engineer, no production system                           | CP69     |

The plan's CP07 lists a disk alert. There is no disk to measure until there is a host, so
it is recorded here rather than replaced by something that looks like it but is not. The
fourth alert is "the API has stopped reporting", which is measurable today and catches a
failure the other three cannot.
