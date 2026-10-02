# 09 — Scale and Observability Execution Plan

**Priority:** P2

**Status:** In progress — 09A Done on `ft/phase-9a`; 09B–09D and external rollout gates pending

**Specification:** [Phase 9 SPEC](../spec/09-scale-observability.md)

**Depends on:** 01–05; 07 shared state; 08 outbox; [Phase 6 integration](06-message-lifecycle.md) for lifecycle producers

## 1. Acceptance and execution order

Four reviewable slices: **09A may start before Phase 8**; horizontal production
scaling requires 09B–09D. Phase 5 verification remains Pending. Phase 6/7 app code is now integrated
from `develop`; distributed lifecycle producer gates remain pending.

- 09A: privacy-safe correlation/metrics/traces, probes and bounded drain.
- 09B: transactional outbox relay, per-instance delivery and recovery.
- 09C: shared presence/typing, distributed limits and authorization/privacy gates.
- 09D: two-instance staging, measured load/fault/DR evidence and gated rollout.

Broker/store/provider, telemetry backend, retention and load limits are pending
implementation-start decisions. Proposed baseline: one Redis service for Streams,
leases and guards; NATS/Kafka are ADR alternatives, not extra required services.
09A adds pinned Prometheus/OpenTelemetry libraries for actual runtime wiring.
No external infrastructure is provisioned; Redis/provider decisions remain pending.

## 2. Inspection and design record

- Inspect README/Makefile/toolchain, startup/config/probes, migrations, limiters,
  Hub/pumps/authorization and Phase 7 tests; preserve existing user changes.
- Inspect Phase 8 outbox transactions and independent consumer progress; inspect
  Phase 6 revision/tombstone queries when integrated. Confirm migration head;
  choose new numbers then, never alter deployed SQL.
- ADR: client/server versions, fixed stream namespaces, AOF/replication/failover
  semantics, groups/instance lifecycle, replay/retention and memory budgets,
  no-eviction lease/control policy, room/user sequence, publisher fencing and
  PostgreSQL recovery incarnation. Test capacity errors and partial failover loss.
- Resolve private metrics access, telemetry retention and provider shutdown/network
  constraints against official docs before deployment/config changes.

## 3. 09A — Observability and one-instance operation

**Slice status: Done.** Implementation, local integration and operational artifacts
are verified. Phase 8 work is reserved for a separate session. Provider provisioning,
staging rollout and sustained production SLO evidence remain release work in 09D.

Expected files: middleware/httpserver/database/ws/config, `cmd/server/main.go`,
configs/container wiring, README and operational dashboards/runbooks. A focused
telemetry setup package is justified only for actual shared wiring.

- [x] Define bounded metrics/enums/route templates for HTTP, DB pools, socket/Hub
      queues, drops and drain; exclude IDs/content/secrets from labels.
- [x] Add request/service/repository OpenTelemetry spans, validated tracecontext,
      bounded export/sampling and no baggage/body/SQL argument collection.
- [x] Keep zerolog; production JSON with restricted correlation fields.
- [x] Add private metrics access; test denied public access and exporter outage.
- [x] Preserve `/health`; add safe bounded `/livez` and HTTP `/readyz`, with separate
      realtime admission status and correct dependency-failure behavior.
- [x] Implement one 15 s drain budget, no new claims/upgrades, sole-writer 1001,
      finite pump completion and forced-close/resource-release accounting.
- [x] Define HTTP/DB/WS dashboards, synthetic failure alerts and HTTP SLO calculations;
      HTTP dashboards/rules and synthetic firing/recovery tests are implemented.
      Realtime eligible event × instance-local dispatch reconciliation remains
      pending 09B–09D; local queue counters cannot measure that denominator.
- [x] Measure one-node baseline; record workload/resources/DB pool settings.

Gate: probe semantics, exporter failure, redaction/cardinality, concurrent
disconnect/drain and race tests pass. Telemetry loss does not fail business writes.

## 4. 09B — Durable relay and fanout

Expected files: Phase 8 outbox repository/workers, concrete broker adapter,
chat/user services, ws/config/startup, additive sequence/progress migration if
needed, and event documentation. Keep wiring concrete and manual.

- [ ] Confirm event ID/schema/kind/aggregate sequence/version and typed references;
      use common room ordering for data/control; no copied sensitive content.
- [ ] Add missing membership/account/private-state producers in mutation transactions.
- [ ] Isolate broker relay progress from notification/cleanup worker progress.
- [ ] Implement bounded per-aggregate advisory locking and publisher fencing;
      publish stable IDs, wait for the selected acceptance/persistence policy,
      then mark progress. Retry safely and release every resource.
- [ ] Separate immutable intent fields from the transport wrapper's PostgreSQL
      `publisher_generation` and `recovery_incarnation`; reject/reconcile stale
      generations outside the Hub without skipping aggregate sequence gaps.
- [ ] Implement fixed scoped streams and one ACK group per instance; no shared
      socket group. Cap pending entries, recover abandoned work and detect trimming.
- [ ] Implement identity ownership, dead-group cleanup, recovery incarnation and
      DB/stream watermark bootstrap before admission.
- [ ] Hydrate only the exact revision at its original sequence outside Hub;
      obsolete revisions use content-free terminal-skip markers. Current bodies
      come only from later matching events or authorized recovery snapshots.
      Test create 10 → revoke 15 → edit 20 with delayed create hydration; never
      expose revision 20's text before control 15. Keep attachment URLs on HTTP.
- [ ] Add bounded dedup/adapter/gap queues, Hub enqueue completion, ACK semantics
      and resync; retain local slow-socket best-effort handling.
- [ ] Add compatible server-only WS metadata/reconnect docs; regenerate existing
      AsyncAPI using the assets now integrated from `develop`.
- [ ] Record poison-aggregate unblock, retention and stream-loss replay runbooks.

Gate: two-node room/private fanout, lost publish reply, crash before progress,
redelivery/cache eviction, stale publisher, poison isolation, store restart/replay
and mixed-schema tests pass. ACK never implies browser delivery; failed outbox
insertion rolls back the mutation.

## 5. 09C — Shared state and privacy

Expected files: shared-state adapter/scripts, WS state/authorization workers,
limiter middleware/config, room/user repositories and tests. Hub still owns maps;
all DB/store/broker work is outside its loop.

- [ ] Reuse `membership_generation`, add control/data watermark gates and local
      generation invalidation. Reject stale joins/read events after leave/rejoin.
- [ ] Gate room read/mention events by membership generation; invitations instead
      validate recipient ownership and exact invitation revision/state so current
      nonmember invitees receive events without stale invitation resurrection.
- [ ] Pause joins/typing/fanout on disconnect/gap/unknown schema/excess lag;
      reauthorize/rebuild/snapshot before recovery.
- [ ] Test current-authorized snapshots/private targets; no event after revocation
      sequence reaches the removed member (earlier queued bytes are not retractable).
- [ ] Implement atomic leases, first/final online transitions, aggregate versions,
      TTL sweeper, token release and removal cleanup; multi-node tabs stay online.
- [ ] Aggregate typing expiry with Phase 7's existing 5 s/1 s rules.
- [ ] Add global per-user upgrade reservation/renewal, local process cap and
      authoritative PostgreSQL recovery-incarnation fencing; drain old reservations
      on shared state loss. Without old-node drain ACKs, pause new admission for
      the maximum previous lease TTL plus measured renewal/drain timing margin.
      Test a partitioned old Redis and resumed old process, not only clean restart.
- [ ] Add shared bounded rate buckets, trusted peer policy, 429 retries and 503
      shared-guard failures; retain local frame/memory safety.
- [ ] Test timeouts/restart/partitions, node crashes, duplicate callbacks and
      abandoned keys/leases with finite coordinated deadlines.

Gate: cross-node tabs, final crash expiry ≤ 16 s, caps/limits, join/revoke/rejoin,
account deletion and missed-control privacy pass. Local-only fallback or eventual
timer-only revocation fails this gate.

## 6. 09D — Verification and rollout evidence

Use disposable owned PostgreSQL and broker/store fixtures; no personal secrets or
application data. Destructive cleanup is limited to those explicit fixtures.
Use injected clocks/exporters and channels for concurrency cases, not long sleeps.

Run focused tests, gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `make build`,
affected docs regeneration and lint when installed. Test clean/upgrade migrations,
simultaneous startup advisory locking, transaction rollback/resource release,
mixed versions and backward-compatible statuses/bodies; review the complete diff.

Load report: commit, versions, instance resources, DB pools, room sizes/connections,
message/upload mix, duration, errors/latencies/drops, backlog, CPU/memory and
saturation point. Proposed 30-minute two-node steady test plus 2-hour soak with
node/store faults; increase load to establish measured capacity. Do not call
planned targets verified SLOs. Reconcile expected eligible instance dispatches,
including a dropped second-node dispatch; enqueue is not browser receipt. Test
alert firing/recovery and bounded drain.

Rollout: 09A one node → expand/outbox one node → shadow consumers → distributed
one node → two-node staging → privacy/load/fault review → selected production
topology. Upgrade every durable control producer first or pause writes/joins;
old local producers cannot be assumed compatible. Expand/contract migrations wait
for binary compatibility and removal of old versions. Rollback drains to one node
and retains schema/intents, never production down.

DR rehearsal: pause traffic; restore consistent DB/object state; use a new recovery
incarnation allocated in PostgreSQL and namespace; reset stale broker/groups/dedup/
leases; rebuild auth/watermarks and revalidate sessions before admission. Require
old-node drain acknowledgements or the maximum-old-TTL-plus-margin barrier before
new reservations. Record measured RPO/RTO and object consistency.

Gate: all 09A–09C acceptance evidence, per-dispatch SLO reports, two-node privacy/
load/fault tests, measured recovery limits, provider ownership and reviewed rollback
runbooks pass before expanding production topology.

## 7. Completion checklist

- [ ] Slices, exact migrations/config/dependencies and provider ADR reviewed.
- [ ] Delivery/privacy/limits/recovery/telemetry meet SPEC.
- [ ] Required Go and DB/broker/store integration/race checks pass with evidence.
- [ ] Load/fault/drain/alert/DR reports establish capacity and recovery limits.
- [ ] Ownership, retention, access and rollback runbooks agreed.
- [ ] README, operational docs and Swagger/AsyncAPI when applicable synchronized.
- [ ] Handoff reports completed slices, measured limits, skipped checks and risks.

No completion checkbox is satisfied merely by drafting this plan.

## 8. Phase 09A implementation and verification (2026-10-02)

Branch `ft/phase-9a` starts from Phase 7 commit `301c1e1`. Existing uncommitted
Phase 8/9 design documents are preserved. Phase 5 stays Pending. A subsequent
merge fast-forwarded this branch to `origin/develop` (`e1291bf`), integrating
Phase 6/7 app code and AsyncAPI while preserving the uncommitted 09A work.
No schema changes, broker infrastructure or deployment changes
were performed.

Implemented private Prometheus metrics, bounded optional OTLP HTTP tracing through
request/service/repository contexts, production JSON correlation, separate live/DB/
realtime probes, readiness withdrawal and a shared default 15-second drain. Hub
state remains event-loop owned; no DB/network operations were added to that loop.
Registered connections receive sole-writer 1001; slow writers are force-closed and
pumps/authorization work are awaited. Startup migration lock waits now have a
session statement timeout with restoration/discard safeguards.

Operations: [runbook](../docs/observability.md),
[Grafana dashboard](../docs/observability/dashboard.json),
[Prometheus rules](../docs/observability/alerts.yml). Swagger probe definitions,
README, env example and Compose grace are synchronized. AsyncAPI is now integrated
from `develop` and documents the 09A close/reconnect behavior; no event payload
schemas are changed by 09A.

### Executed checks

Go 1.23.12; commands use `GOTOOLCHAIN=local`, temporary module/build caches and
`GOMAXPROCS=2`; test compilation uses `-p 2` to respect local memory limits.
PostgreSQL checks use only an owned temporary PostgreSQL 16.15 container, trust
authentication, an isolated `_test` database, no application data or named volume.

- `go test -p 2 -race -count=1 -timeout=120s ./...`: PASS, including clean/upgrade/
  concurrent migrations, the blocked advisory-lock regression and Phase 7 DB tests.
- `go vet -p 2 ./...`: PASS.
- `make build`: PASS.
- `make docs` with pinned swag v1.8.12: PASS; generator emits existing Go-constant
  evaluation warnings, but produces the expected three new probe paths.
- `promtool check rules alerts.yml`: PASS (7 rules).
- `promtool test rules alerts_test.yml` using a disposable Prometheus v3.5.0 tool
  container: PASS (all 7 alerts fire and recover under synthetic fixtures).
- `go test -p 2 ./internal/middleware -run '^$' -bench BenchmarkTelemetryHTTP
  -benchmem -benchtime=2s -count=1`: PASS, 1,069 ns/op, 1,088 B/op, 16 allocs/op.
  This measures Gin+telemetry+httptest overhead with export disabled, not API capacity.
- `git diff --check`, JSON/Swagger probe paths and local documentation links: PASS.
- All 62 domain span edits reviewed; no domain logic or SQL changes.
- `golangci-lint` is not installed; lint is not claimed.

### One-process local smoke baseline

Linux amd64, Intel i5-13400, Go 1.23.12, `GOMAXPROCS=2`; PostgreSQL 16.15 container
has no explicit CPU/memory limit. App and load generator share the local host.
Pool max 25/idle 5; localhost connections; no representative production network
latency. Eight concurrent clients run authenticated `GET /api/v1/users/me` for
10 seconds, one WS connection open, production JSON logs enabled, private scrape
enabled, trace ratio 0.05 and the collector deliberately unreachable.

| Measurement | Observed |
| --- | --- |
| Requests / HTTP errors | 25,281 / 0 |
| Throughput | 2,527 requests/s |
| Client p50 / p95 / p99 | 2.46 / 7.34 / 12.03 ms |
| Process RSS near end | 52,520 KiB |
| SIGTERM to process exit | 0.017 s |
| WS close / exit status | 1001 / 0 |

Smoke probes: health/live/HTTP readiness/realtime readiness all 200, public metrics
404, private scrape 200; failed trace exports were counted. After observing failed
exports, room creation, message persistence and HTTP history recovery also passed
on the final binary. Race tests separately
exercise blocked collectors, slow non-reading sockets, repeated shutdown and
cancelled authorization, one outstanding readiness probe, and an inbound reader
that must not preempt the writer's 1001 frame. This brief read workload is a smoke baseline, not a
saturation result, write/chat load test, 30-minute steady run or production SLO.

### Remaining release gates

Choose collector/private scrape access, retention/access owner and staging alert
notifications; verify Render termination grace and real staging drain. Realtime
per-dispatch and crash-presence SLOs need 09B–09D. SDK queue-overflow span loss is
not included in failed-batch counters. No provider credentials/configuration,
production capacity claim, external deployment, commit or push was performed.

### Post-merge verification

Merged latest `origin/develop` (`e1291bf`) into `ft/phase-9a` by fast-forward,
then restored the uncommitted 09A/design work. Resolved three stash conflicts,
retaining develop's authoritative message username/lifecycle behavior and both
roadmap histories. Added the same context-only tracing to the integrated edit/delete
paths; Phase 6/7 domain behavior and migrations are retained.

Re-ran full `go test -p 2 -race -count=1 -timeout=120s ./...` with a fresh owned
PostgreSQL 16 container and isolated `_test` database: PASS, including lifecycle
and read-state integration tests. `go vet -p 2 ./...`, `make build`, `make docs`
and `git diff --check`: PASS. AsyncAPI validation with CLI 4.1.1 under nvm Node 22:
valid, zero errors/warnings; informational recommendation for schema 3.1.0 only.
Updated its shutdown/reconnect description and synchronized design integration
notes. The initial baseline predated this develop merge; the table above was refreshed
with the completed combined binary after the final regression fixes. It remains
a brief local smoke measurement, not a capacity result. No 09A commit or push was made.

### 09A completion verification

The independent 09A slice is Done. Phase 8 implementation is out of this session;
09B/09C remain unimplemented and depend on its outbox work. Phase 9 overall stays
In progress until those slices and 09D release gates are verified.

Two additional failures were reproduced before their fixes:

- A drain budget expiring before the event loop publishes its stopped snapshot
  could leave a slow writer alive. Force-close is now armed after the loop's
  immutable snapshot is available even when cancellation already happened. It
  closes safe registered sockets before returning when the loop has stopped.
- A broadcast after shutdown could select an available queue over the closed done
  channel and report success without a consumer. Room/private broadcasts now
  first reject an already-stopped Hub and count a bounded `stopped` drop reason.
  Existing best-effort semantics during races with drain remain unchanged.

Added regression tests plus simultaneous WS upgrade/disconnect/drain coverage;
all run under the race detector. Prometheus fixtures now verify firing/recovery
of every shipped alert: target-down, HTTP availability, HTTP latency, pool
saturation, realtime drops, trace-export failure and forced socket drain.

Final checks after the fixes: full race suite with isolated PostgreSQL, vet, build,
rule syntax/tests and whitespace checks PASS. Latest one-process smoke baseline
is the table above; the final Phase 6/7/9A binary also persists/reads messages with
an unreachable exporter, has private metrics/public 404, emits WS 1001 on SIGTERM
and exits 0. No migrations, dependencies or Phase 8 implementation were added
in this completion pass. golangci-lint remains unavailable. The owned test DB is
removed after verification. External provider/Render rollout is not executed.
