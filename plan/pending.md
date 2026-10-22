# Pending and deferred work

As of 2026-10-22. This is the current open-work list. Implementation status is in
[the roadmap](README.md); release scope is in [release.md](../docs/release.md).

## Before tagging / publishing the candidate

| ID | Open item | Completion evidence |
| --- | --- | --- |
| REL-01 | Select final commit, version and release target | Reviewed candidate SHA/version and clean Git state; no version/tag is selected by this document |
| REL-02 | Attach checks for that exact candidate | CI/race/vet/build results, plus dedicated DB evidence for schema13 upgrade/down guard, quotas and bindings; existing local evidence is summarized separately |
| REL-03 | Check generated contracts match the candidate | Swagger/AsyncAPI consistency checks; regenerate only when annotations/YAML change |

REL-02 requires existing test results to be attached or missing checks to be run
safely. Historical notes that a test was added, or a full suite without a DSN,
are not proof that all database cases ran. Do not silently convert an old skipped
or approval-blocked run into a pass. This is an evidence gate, not missing feature
implementation. Tests that reset schemas/down-migrate require a disposable DB
and the repository's authorization rules.

## Before promoting to a hosted environment

| ID | Open item | Completion evidence |
| --- | --- | --- |
| DEP-01 | Target env and origins | Production env/secrets, explicit CORS/WS origins, configured R2/processor if uploads are enabled; one instance |
| DEP-02 | Backup and coordinated schema13 upgrade | Target backup/restore ownership and tested recovery path; older backend/cleanup processes drained before binding traffic |
| DEP-03 | Hosted startup/redeploy | Clean migration log, health/readiness, authenticated REST/WS recovery and measured termination window for the selected commit |

Local recovery/crash/restore checks have passed, but are not hosted cold-start,
Neon PITR/retention or production DB/object reconciliation evidence. Render quota
availability remains an environment constraint until a target is selected.
Frontend is complete per user confirmation; no new FE implementation is queued.
R2/scanner re-testing is excluded by the user's scope decision; this list does
not reopen provider tests or require ClamAV.

## Follow-up operation work

- Sustained one-instance chat/load measurements; current 200-read smoke is not a
  saturation result or a production SLO. Required before advertising capacity.
- Confirm private telemetry access, alert destination/owner and retention.
- Record production backup retention/PITR and DB/object reconciliation procedure.
- Late-write/grace behavior beyond recorded local evidence and physical-storage
  alerts/lifecycle ownership remain operational limitations to track. No new R2
  or scanner smoke test is scheduled or required by this scope decision.

## Deferred functionality — outside the single-instance release

| Phase | Not implemented | Resume condition |
| --- | --- | --- |
| 09B | Broker relay, per-instance fanout, sequencing, dedup/ACK/recovery | Explicit scaling decision, broker/provider ADR and durable event producer review |
| 09C | Shared presence/typing, cross-node authorization, distributed limits | 09B plus verified shared-store lease/failure semantics |
| 09D | Two-instance load/fault/privacy/DR rollout | 09B/09C implementation and staging resources |
| ClamAV | Supported deployment/operation of the optional antivirus engine | Explicit user decision; deferred indefinitely |

Broker choice is **not finalized**. The distributed spec preserves a Redis
Streams candidate and alternatives; no Redis, NATS, Kafka or Cloudflare broker
has been provisioned or wired into this backend. PostgreSQL outbox rows do not
establish cross-node delivery; future broker deliveries must remain unconsumed.
