# Phase 9A operations

**09A status: Done** (implementation and local verification). Provider deployment
and sustained SLO evidence remain release gates.

This slice runs one process with PostgreSQL as the source of truth. Redis,
transactional outbox delivery and shared limits belong to 09B/09C. Do not increase
instances until their gates pass. Phase 6/7 application code is now integrated from `develop`; Phase 8 remains
separate work.

## Configuration and access

| Variable | Default / behavior |
| --- | --- |
| `OTEL_SERVICE_NAME` | `gogo-dl`; 1–64 ASCII letters, digits, `.`, `_`, `-` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Empty: export disabled. Otherwise full HTTP(S) trace URL, e.g. `http://127.0.0.1:4318/v1/traces`; no URL credentials, query or fragment |
| `OTEL_EXPORTER_OTLP_HEADERS` | Empty; comma-separated `name=value` secrets. Percent-encode commas or special characters in values. Transport framing headers are rejected |
| `TRACE_SAMPLE_RATIO` | `0.05`; finite 0–1. Remote sampled flags cannot override the configured ratio |
| `METRICS_ENABLED` | `false` |
| `METRICS_LISTEN_ADDR` | `127.0.0.1:9090`; literal loopback/private IP and port 1–65535. Wildcard/public IP binds are rejected |
| `SHUTDOWN_TIMEOUT` | `15s`; 1–60s; one shared HTTP/WS/export budget |

Set endpoint/headers in the platform's secret environment settings. Use HTTPS for
external collectors. This implementation supports **OTLP over HTTP**, not gRPC.
Choose the backend, retention, access policy and credentials before enabling export
in staging. Trace export failure does not prevent database writes or startup.

Prometheus scraping requires an operator-controlled network path. With the default
loopback bind, the scraper must share the process network namespace or use an
operator-controlled tunnel. A literal private IP bind requires firewall/network
access control; an RFC1918 address alone does not authenticate scrapers. A normal
Prometheus target is `127.0.0.1:9090`, job name `gogo-dl`, path `/metrics`.
Docker Compose passes settings through `configs/.env` and publishes no metrics port.
For Render Native Go, keep metrics disabled until private collector access is
configured; OTLP HTTP can export to an external backend independently.

The public Gin listener has no `/metrics` or pprof endpoint. Do not add public routes
or proxy private scraping through the frontend. The private listener serves only
`GET /metrics` with bounded concurrent scrapes and HTTP timeouts.

## Probes and lifecycle

| Route | Meaning |
| --- | --- |
| `GET /health` | Existing `status`/`time` response, unchanged |
| `GET /livez` | `200 {"live":true}` while HTTP responds, independent of DB/Hub |
| `GET /readyz` | `200 {"ready":true}` or `503 {"ready":false}`; DB reachable, migrations already completed, process not draining |
| `GET /readyz/realtime` | Same body/status, additionally requires a running Hub accepting connections |

The database probe has a 250 ms context deadline, caches its result for 250 ms and
allows only one outstanding DB probe. Concurrent unknown state returns not ready;
probe traffic cannot consume the entire DB pool. Responses never expose dependency
errors. Drain invalidates readiness immediately, regardless of cached DB success.
Use `/readyz` as the service readiness check after deployment verification. Preserve
`/health` consumers that need its original semantics. Do not restart solely because
of transient DB readiness failure; `/livez` is the process liveness signal.

Startup connectivity has a 5-second context deadline. Embedded migration startup
has a 30-second parent deadline. The migration driver uses background contexts for
some advisory-lock/version queries, so its owned PostgreSQL session additionally
caps **each statement** at the smaller of 5 seconds and the initial remaining
startup deadline. The session setting is restored before pool return; a session
whose restoration fails is discarded. Cancellation also requests graceful migration
stop. Existing migrations remain unchanged. Future migrations needing more than
5 seconds require a reviewed migration procedure before deployment. Investigate
failed/dirty migrations and concurrent deployment locks; never use production down
as automatic recovery.

On SIGINT/SIGTERM:

1. Withdraw readiness and reject new registered `/api/v1` requests/upgrades with 503.
2. Shut down HTTP and the Hub concurrently within the shared budget.
3. Cancel authorization work; each socket's sole writer flushes queued data and
   emits close code 1001. Wait for upgrades, pumps and authorization goroutines.
4. At the deadline, forcibly close slow sockets/HTTP connections; flush telemetry
   with the remaining time, then close the database pool.

Clients should reconnect with backoff and recover messages using HTTP cursors.
`completed` drain means the server wrote a close frame, not that the peer received
or acknowledged it. `forced`/`error` identify unfinished close delivery. In-flight
HTTP persistence can succeed while socket fanout is unavailable; delivery remains
best effort. The Compose grace is 20 seconds for the default 15-second budget;
adjust it when increasing the application budget. Verify the chosen platform's
termination grace before rollout.

## Metrics, traces and logging

| Metric | Labels / use |
| --- | --- |
| `gogo_http_requests_total` | `method`, route template, `status_class`; all HTTP requests |
| `gogo_http_request_duration_seconds` | Same labels; histogram seconds, WS measures handshake only |
| `gogo_http_availability_total` | Fixed `result=good|bad`; eligible authenticated API attempts |
| `gogo_db_open_connections`, `gogo_db_in_use_connections`, `gogo_db_max_open_connections` | DB pool gauges; configured max 25, idle 5 |
| `gogo_db_wait_total`, `gogo_db_wait_seconds_total` | Cumulative pool wait count/time |
| `gogo_ws_connections`, `gogo_ws_queue_depth` | Hub snapshots every 250 ms; fixed inbound/room/user/client queues, aggregate client depth |
| `gogo_ws_dropped_total` | Fixed queue/reason; outbound full/marshal failures and stopped Hub rejects |
| `gogo_ws_admission_rejected_total` | Fixed limit/draining reasons |
| `gogo_ws_drain_total` | Fixed completed/forced/error results |
| `gogo_trace_export_errors_total`, `gogo_trace_export_dropped_spans_total` | Failed batches and spans in those batches |
| `go_*` | Standard Go process collector |

There are no user, room, event, connection, IP, raw-path or token labels. Unknown
paths become `unmatched`, unusual methods become `OTHER`. Scraping reads only
atomic collectors and DB stats; it never accesses Hub-owned maps. Queue gauges are
snapshots, not per-socket measurements. Closed socket/process state is not durable.

Traces follow HTTP → service → repository via request contexts. Only a valid,
55-character W3C `traceparent` is extracted. Tracestate and baggage are discarded.
HTTP attributes contain method, registered route and status; inner operation names
are fixed strings. No bodies, query strings, JWTs, SQL statements/arguments or raw
errors are captured. Production request logs contain route/method/status/latency,
bounded request ID and trace ID; raw path, user-agent and remote address are omitted.

The exporter queue holds at most 512 spans, batches at most 128, flushes every second
and times out after 2 seconds with retries disabled. Enqueue is nonblocking; overload
can drop spans. Failed export batches are counted, **SDK queue-overflow drops are
not counted** by these counters. Use collector ingestion diagnostics and sampling
adjustment to assess total telemetry loss; trace counters are not a completeness
proof. SDK errors are logged as a safe constant, without provider response text or
credentials.

Prometheus client v1.23.2 supplies the actual scrape registry and collector APIs;
OpenTelemetry v1.38.0 supplies actual HTTP export and context propagation. These
versions retain the repository's Go 1.23 toolchain. Their primary module manifests
are [Prometheus client](https://github.com/prometheus/client_golang/blob/v1.23.2/go.mod)
and [OpenTelemetry](https://github.com/open-telemetry/opentelemetry-go/blob/v1.38.0/go.mod).

## Dashboard, alerts and proposed SLOs

Import [dashboard.json](observability/dashboard.json) into Grafana and select the
Prometheus datasource. Load [alerts.yml](observability/alerts.yml) as Prometheus
rules with `job="gogo-dl"`. Adapt thresholds after measuring normal workload.

HTTP availability targets 99.9% over 30 days. Eligible attempts are registered
protected API routes with a verified, unexpired access JWT, excluding expected
4xx responses; 429 and 5xx count as failures. Public auth, probes, unmatched paths
and WS handshakes are excluded. Early admission 429/503 is classified by the same
local JWT verification even when Auth has not run. This does not imply the account
or requested resource still exists; authorization-related expected 4xx is excluded.

```promql
1 - sum(increase(gogo_http_availability_total{result="bad"}[30d]))
    / clamp_min(sum(increase(gogo_http_availability_total[30d])), 1)
```

Successful non-upload API route p95 targets 300 ms:

```promql
histogram_quantile(0.95, sum by (le, route) (
  rate(gogo_http_request_duration_seconds_bucket{
    status_class="2xx",route=~"/api/v1/.*",route!="/api/v1/ws"
  }[5m])
))
```

There are currently no upload routes; add explicit exclusions when Phase 8 adds
uploads. Empty denominators/windows do not prove availability. A down process
cannot record its own missing requests, so pair these counters with target-down
alerts and an external synthetic authenticated API/WS monitor. Configure that
monitor privately and validate real firing/recovery in staging before an SLO claim.
The rules include target-down, availability-budget, route latency, pool saturation,
dropped events, export failure and forced-drain warnings. The synthetic rule fixture
checks firing and recovery of all seven alerts; it does not replace a
real provider notification test.

The future realtime target is each committed event × eligible instance-local
dispatch, including missing dispatches. A successful other-node enqueue cannot hide
a lost dispatch. This slice has neither outbox/relay reconciliation nor distributed
presence, so realtime dispatch and crash-presence SLOs remain pending 09B–09D.
No browser-delivery or multi-instance SLO is claimed from local drop counters.

Validate rules with:

```sh
cd docs/observability
promtool check rules alerts.yml
promtool test rules alerts_test.yml
```

## Deployment checks and rollback

Deploy one instance. Verify migration completion → Hub startup → HTTP listen,
then the four probes and an authenticated API/WS request. Confirm public `/metrics`
is 404, private scrape is reachable only by the chosen collector, and trace export
contains fixed operation names without content. Exercise collector outage without
API write failure. Send SIGTERM in staging and verify 1001, readiness withdrawal
and bounded exit. Verify alert notifications/recovery and retention/access ownership.

Rollback to the previous binary using one instance after drain. This slice changes
no schema and needs no migration down. Telemetry settings may be removed/disabled
independently. Earlier roadmap phases retain their own integration gates.

## Local verification evidence

See [the Phase 9 plan](../plan/09-scale-observability.md) for executed commands and
measured local baseline. Local results establish regression protection and smoke
behavior; production capacity, sustained SLOs and provider deployment are pending.
