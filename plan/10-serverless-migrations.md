# Deployment migrations

**Implementation:** complete locally, including actual-process startup rejection.
**Open release/deployment work:** [pending.md](pending.md).

## Contract

- Embed migration SQL in the binary and apply pending versions before HTTP/WS.
- Use PostgreSQL migration locking for concurrent starts; repeated unchanged
  starts are safe. Keep the recorded schema version and dirty state authoritative.
- Bound DB connect/startup/migration work. A failed/dirty migration exits nonzero
  and never opens HTTP; do not skip it, clear dirty state or auto-downgrade.
- A different working directory must not affect migration resolution.
- Build/run the package `./cmd/server`, including its startup diagnostic helper.

Startup logs expose bounded `failure_stage`/`failure_reason` values and preserve
wrapped causes internally; raw credentials/DSNs/errors are not logged.
A dirty schema reports `migrations/dirty_schema`; connectivity failure reports
`database/connection_failed`. See [deployment troubleshooting](../docs/deploy-render-neon.md).

## Verification

Existing PostgreSQL tests cover clean/upgrade/no-change/concurrent/dirty cases.
The isolated Docker recovery script also creates an empty `startup_failure_test`
DB with an initial dirty migration record; it never overwrites an application DB.
Actual binaries must exit nonzero for that dirty fixture and for an unavailable
PostgreSQL endpoint. Tests check TCP admission and absence of migration-success/
HTTP-start markers, preserved dirty state, no created app tables and secret-safe
logs. Backup/restore and process-crash rehearsals are separate supported targets.

[Verification evidence](../docs/verification.md) records executed checks and
limits. A mounted Docker checkout may need `GOFLAGS=-buildvcs=false` for build
verification only; production builds retain normal stamping.

## Deployment

Select the target commit, back up the target DB and inspect upgrade compatibility.
Schema13 requires old backend/cleanup processes drained before bound traffic.
Use the [Render/Neon guide](../docs/deploy-render-neon.md) and
[recovery runbook](../docs/render-free-operations.md). Retain compatible schema/
binaries on rollback; never force production down migrations.
