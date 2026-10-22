# Verification record

This is a summary of recorded local work, not a claim that every check ran
against a newly selected release SHA. Candidate evidence is [REL-02](../plan/pending.md).
Completed execution diaries were removed; Git retains their original details.

| Area | Recorded evidence | Limit |
| --- | --- | --- |
| Phases01–07 | Unit/HTTP/WS/race and dedicated PostgreSQL verification; phase05 completion recorded 2026-10-17 | Earlier slice runs, not new hosted rollout proof |
| 08A | Search privacy/cursors/lifecycle, schema upgrade and100k-message query plans | [Local search report](phase-8a-search.md); not an overall capacity claim |
| 08B processing | Dedicated PostgreSQL/race tests, TXT/PNG/JPEG real R2 processing and signed final GET; isolated sanitizer memory/admission tests | Does not establish all hosted timing/egress or prolonged cleanup behavior; no new provider tests scheduled |
| 08B schema13 | Atomic binding/retry/rollback, projection, revocation, account/message cleanup and quota/migration tests exist; dedicated runs recorded during implementation | Historical final runs differ in DSN/skip status. Attach specific upgrade/down-guard/quota results to the candidate; test existence is not execution proof |
| 08C | Dedicated PostgreSQL/race checks for inbox ownership/generation/preferences, atomic intents and duplicate worker recovery | Earlier local execution, not a new browser report |
| 09A | Probe/privacy/exporter/drain tests, alert fixtures and brief one-node smoke | No sustained throughput/SLO guarantee |
| Recovery/restore/crash | Actual race-enabled binaries, new PostgreSQL16 fixtures, WS1001/rejoin, history/inbox/retry, DB-path outage, full-table/sequence restore comparison, uncommitted crash rollback | Local fixtures, not Neon PITR/host/disk failure or DB/object restore proof |
| Startup diagnostics | Unit classification/redaction, actual DB-unavailable/dirty-schema process rejection and no HTTP-start markers | Local dedicated startup DB; dirty state intentionally retained |
| Frontend | Completion confirmed by user 2026-10-22 | Not additional agent-run browser test evidence |

## Recent backend verification

On Go1.23 in Docker, the startup-diagnostics slice passed:

```bash
go test -buildvcs=false -tags=integration -race -count=1 -timeout=180s ./...
go vet -buildvcs=false -tags=integration ./...
GOFLAGS=-buildvcs=false make build
make verify-local-backup
make verify-local-crash-recovery
```

The ordinary full suite had **no `TEST_DATABASE_URL`**, so earlier package DB tests
skipped. The separate Docker rehearsal targets did execute real source/restore/
startup-failure databases, with race-enabled binaries. Review added safe failure
classification and deterministic HTTP-start log assertions. Existing local FE
readiness remained200. Owned fixture volumes were retained after stop.

`-buildvcs=false` was used for a host-mounted Git ownership limitation, not a
production build requirement. golangci-lint was unavailable and is not reported
as passing. No application tests or provider calls are rerun by editing these docs.

## Single-instance smoke

A recorded race-instrumented local run used 200 authenticated history reads with
four clients: 266ms total, p95 14.789ms. Other brief runs varied; this is a tiny
fixture, not saturation, sustained mixed chat load, Render capacity or a latency
SLO. The server pool uses 25 open/5 idle connections. Record workload/resources,
WS/message mix, errors/drops, pool wait and worker lag for any capacity claim.

## Reproduce safely

Use [local recovery](local-recovery.md) for the isolated Docker targets. Other
DB tests require a dedicated disposable `TEST_DATABASE_URL` and may down-migrate
or reset schemas. Follow repository authorization rules; never use application,
personal Neon or production data. Regenerate Swagger/AsyncAPI only after their
source changes; consistency tests must pass before releasing the candidate.
