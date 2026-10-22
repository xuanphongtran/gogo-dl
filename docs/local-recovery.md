# Local single-instance recovery verification

The opt-in `integration/recovery` suite launches the actual server binary with
production configuration, an empty working directory and a dedicated local
PostgreSQL database. It supplies its own JWT secrets and disables attachment
admission, processing and metrics export. It never loads developer `.env` files
or contacts R2/scanner services.

## Run

Build the binary and run the tagged test with Go 1.23+, a C compiler for the race
detector, and a **new, empty local database whose name ends in `_test`**:

```bash
CGO_ENABLED=1 go build -race -o /tmp/gogo-recovery-server ./cmd/server
RECOVERY_SERVER_BINARY=/tmp/gogo-recovery-server \
RECOVERY_DATABASE_URL='postgres://postgres:LOCAL_TEST_PASSWORD@127.0.0.1:5432/gogo_recovery_test?sslmode=disable' \
CGO_ENABLED=1 go test -tags=integration -race -v -count=1 -timeout=120s ./integration/recovery
```

`RECOVERY_DATABASE_URL` is deliberately separate from `TEST_DATABASE_URL`.
The test refuses nonlocal hosts, non-test database names and existing application
tables in any nonsystem schema. Run against a disposable database, never the FE
database or Neon. If a previous run reached migration startup, create another
empty test database for the next run. No down migration, reset, truncate or drop
is performed; fixtures remain available for inspection.

Only one `sslmode=disable` query parameter is accepted. Query overrides such as
`host`, `port`, `dbname`, `user` and `options` are rejected so the emptiness check
and child server cannot connect to different databases. User/database names use
ASCII letters, digits and underscores; fixture passwords cannot contain whitespace,
control characters, single quotes or backslashes, because the application builds
a keyword DSN. These restrictions apply to the harness input only.

When the host lacks Go, use `golang:1.23` with the repository and module/build
caches mounted. A PostgreSQL container started with `--network none` and a
persistent data volume can share its network namespace with the Go container via
`--network container:<test-postgres-container>`. This allows the same localhost
URL without exposing a database or HTTP port. Prepopulate module caches before
entering that isolated namespace. Use `-buildvcs=false` for a host-mounted
checkout if Git ownership prevents build stamping.

## Assertions

- Startup applies embedded migrations, then health/liveness/HTTP and realtime
  readiness succeed. Public metrics remain unavailable.
- Two users register through HTTP, join a public room, and create a persisted
  mention. A locked preference row prevents the first worker from completing
  the inbox effect before shutdown.
- SIGTERM yields WS close code `1001` and a successful process exit within the
  configured 5-second drain budget plus a 2-second harness allowance. A race
  binary normally spends an additional second exiting the race runtime.
- A new server process retains tokens/history and reconnects/rejoins. Replaying
  the same message/key returns its original ID. The startup worker creates one
  matching inbox entry. Only the isolated fixture's pending/backoff/lease
  timestamps are advanced; no production lease duration is shortened.
- A TCP proxy closes existing pooled connections and denies new DB connections.
  Readiness becomes `503`; liveness and legacy `/health` stay `200`. Restoring the
  path restores readiness and authorized history without a server restart.
- After membership removal and another process restart, both REST history and
  WS join deny the removed user. Schema13 stays clean. All retained future broker
  deliveries remain pending; no distributed relay is inferred.
- A bounded 200-request, four-client history read reports elapsed time and p95.
  This tiny race-instrumented fixture is a smoke measurement, not a capacity
  limit, saturation test, Render measurement or uptime/latency commitment.

The proxy and test-owned processes/connections are joined on exit. The test does
not stop any existing application/database, simulate a PostgreSQL crash, restore
a backup, exercise a browser, or establish hosted redeploy/cold-start behavior.
Those environment-specific release gates remain separate in the
[Phase 09 plan](../plan/09-scale-observability.md).

## Isolated backup/restore rehearsal

```bash
make verify-local-backup
```

Requires Bash, Docker, `diff`, and the `golang:1.23`/`postgres:16-alpine` images
(Docker can pull them when absent). Host Go/PostgreSQL tools are unnecessary.
The script downloads Go modules into a Docker cache, then uses a new PostgreSQL
container with no network and no published ports. Go processes join only that
container's network namespace. The repository is mounted read-only; no developer
env files are loaded. R2/scanner are disabled.

The source recovery test leaves two users, two canonical messages, one retained
mention inbox entry, a send retry key and a revoked member. After the source
server exits, the script writes a custom-format `pg_dump` archive and restores
into a newly created empty database with `pg_restore --single-transaction
--exit-on-error`. It compares row counts and deterministic whole-table digests
for every public table, plus each sequence's value/called state, before starting
the restored application.

`TestRestoredServer` then starts the actual binary from the restore and verifies
readiness, login against restored password hashes, history, the original send
retry ID, the retained inbox reference and denied REST/WS access for the removed
member. The test uses the script's known fixture identities; it is not a tool for
testing arbitrary existing databases.

Every run allocates unique container/volume names and a `/tmp/gogo-backup.*`
artifact directory. On exit it stops only its own DB container, preserving its
named data volume and dump at `/var/lib/postgresql/data/recovery.dump`. The script
prints the retained container/volume and fingerprint paths; it never drops a DB,
resets a schema or deletes a volume. Fingerprints contain counts/digests and
sequence values, not row bodies. The archive itself contains test fixture data.
Module/build caches can be selected with `RECOVERY_MODULE_CACHE` and
`RECOVERY_BUILD_CACHE`; defaults reuse the existing local Go caches.

This proves a quiescent local logical backup/restore with no attached objects.
It does not establish Neon PITR/backup retention, production RPO/RTO, PostgreSQL
crash recovery, R2/database consistency or hosted recovery. Keep cleanup/upload
admission disabled when rehearsing a database restore without its matching
object snapshot, as described in the operational runbook.

Both Docker targets also create an empty `startup_failure_test` fixture and run
`TestStartupFailureDoesNotServe`. It seeds a dirty migration state only in that
empty DB and separately tests an unavailable DB endpoint. The binary must exit
nonzero without HTTP startup or a migration-success log, preserve dirty state,
and emit the expected safe failure stage/reason without credentials. No dirty
flag in an existing DB is modified or automatically cleared.

## PostgreSQL crash recovery

```bash
make verify-local-crash-recovery
```

This explicit mode runs the backup/restore rehearsal, then starts a transaction
on the restored fixture. `UPDATE ... RETURNING` acknowledges a changed message
row; a second marker confirms the session remains inside the transaction without
COMMIT. A psql pipe holds the session open, without timing-dependent sleeps.

The script sends SIGKILL only to the unique PostgreSQL container created by that
invocation, then starts it again with the same retained volume. After TCP
readiness, every table/sequence fingerprint must match the source snapshot:
committed state survived and the uncommitted message change disappeared. The
actual server then repeats restored login/history/inbox/retry and membership
checks. The interrupted psql process is joined, including on failure cleanup.

The ordinary `verify-local-backup` target does not inject a crash. This local
process-crash rehearsal does not test host/power loss, disk corruption, a crash
during backup, Neon failover/PITR, object reconciliation or production RPO/RTO.
