# 09 — Observability and scaling

**Current implementation:** 09A and local single-instance recovery are complete.
**Not implemented:** 09B–09D; deferred outside the current release.
**Future contracts:** [Phase 09 spec](../spec/09-scale-observability.md).
**Open work:** [pending.md](pending.md).

## Current release topology

One Go HTTP/WS process, PostgreSQL source of truth and private R2 attachments.
Presence, typing, socket fanout and rate/admission limits are process-local.
Durable notification/cleanup/processing workers run inside the web process.
No broker or shared ephemeral store is configured.

Render Free spin-down is accepted. Work resumes from eligible PostgreSQL jobs
when the process runs; no always-on notification latency or uptime SLA is promised.
Clients reconnect/rejoin and recover durable state through REST. Restart clears
local presence/typing and rate buckets. Do not increase the instance count.

## Implemented and verified locally

- Privacy-safe request correlation, bounded metrics/traces, private metrics,
  probes and a shared bounded shutdown/drain budget.
- HTTP/DB/WS dashboards and alert fixtures; telemetry outages do not fail writes.
- Actual binary restart: WS 1001, REST history/inbox recovery, idempotent retry,
  membership revocation across restart and retained future broker intents.
- DB-path outage: readiness 503, liveness 200, recovery without restarting the app.
- Isolated logical backup/restore: every table/sequence compared before restored
  login/history/inbox/retry and REST/WS authorization checks.
- PostgreSQL process crash: committed state survives, an acknowledged uncommitted
  update rolls back, and the restored application becomes ready.

Run `make verify-local-backup` or `make verify-local-crash-recovery`.
Both targets create only their own DB fixtures and retain stopped data volumes.
See [local recovery](../docs/local-recovery.md) and
[verification evidence](../docs/verification.md) for commands and limits.
The 200-read/four-client local smoke is not sustained capacity or an SLO.

## Future distributed slices

| Slice | Required work | Status |
| --- | --- | --- |
| 09B | Transactional relay, per-instance consumers, ordering, dedup/fencing, replay and private fanout | Not implemented |
| 09C | Shared leases/presence/typing, distributed admission/rates, revocation/privacy barriers | Not implemented |
| 09D | Two-node fault/load/privacy/DR evidence and gated rollout | Not implemented |

The preserved distributed spec is a design target, not a runtime contract.
Broker/provider selection, cost and persistence guarantees require an explicit
implementation decision. No selected broker is implied by outbox intent creation.
All remaining work, including hosted rollout and capacity/backup ownership, is
listed in [pending.md](pending.md).
