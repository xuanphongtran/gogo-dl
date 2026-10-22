# Serverless deployment: database migrations (spec + plan)

**Status:** Implemented and verified with an isolated local PostgreSQL 16
database; Neon staging and Render deployment remain pending. **Scope:** PostgreSQL migrations when the hosting platform
does not offer an interactive shell or a separate migration command.

## Spec

The application must apply pending migrations automatically before it accepts
HTTP or WebSocket traffic. No CLI or public migration endpoint is required.
`schema_migrations` remains the version record. A failed or dirty migration
must fail startup with a useful, secret-free error; it must never be skipped
to make the service appear healthy. Repeated starts with no pending migration
must be safe.

`cmd/server/main.go` applies migrations before starting the HTTP server.
Migration SQL is now embedded with Go `embed` and golang-migrate's `iofs`
source, so startup does not depend on the working directory. The versioned
SQL files remain the single source of truth.

Multiple instances may start at once. Confirm the PostgreSQL migration lock
serializes them and that every instance checks the resulting schema version
before serving traffic. Bound startup time according to the platform's
startup limit. If a migration cannot finish within that limit, use a
platform-supported one-off deployment job instead of running it at startup;
this choice depends on the selected provider.

Deploy schema changes so old and new application versions can coexist during
a rolling rollout. Never run destructive down migrations automatically.
Back up production data and define a manual recovery path for dirty migrations.

## Plan

1. Confirm the provider supports a long-running HTTP/WebSocket service,
   PostgreSQL connectivity, injected secrets, and enough startup time for the
   expected migrations. Map its listen port to `SERVER_PORT`.
2. Embed `migrations/*.sql` in the binary; update the migration runner to use
   the embedded source and preserve `ErrNoChange` handling. Remove reliance on
   `file://migrations` and the runtime working directory.
3. Test clean install, upgrade from the previous schema, no-change restart,
   concurrent starts, and failed/dirty migration against an isolated test DB.
   Verify the HTTP server never becomes ready after a migration failure.
4. Build the production image and start it from a different working directory.
   Deploy to staging twice and verify the recorded schema version and health.
5. Document production secrets, database backup, migration logs, and the
   provider-specific recovery procedure before the first production rollout.

**Acceptance:** A fresh deployment and a redeployment reach `/health` without
manual migration commands; concurrent starts preserve one consistent schema;
failed migrations prevent readiness and do not expose credentials.

**Related deployment risk:** the current WebSocket hub stores connections in
process memory. Multi-instance delivery and scale-to-zero behavior need a
separate assessment for the chosen provider; migration readiness alone does
not guarantee a working real-time deployment.

## Actual-process startup failure verification — 2026-10-22

The local 09 recovery tooling now also creates a separate empty
`startup_failure_test` database. `TestStartupFailureDoesNotServe` creates only
its initial migration-state table with version13/dirty=true; it never overwrites
an application schema, resets a database or clears a dirty flag.

Two actual binary starts cover a loopback endpoint that cannot establish a
PostgreSQL connection, and the dirty migration fixture. Each must exit nonzero
within8s without accepting HTTP. The captured logs must contain neither the
HTTP start marker nor migration-success marker, so a short-lived listener cannot
escape detection between TCP probes. Dirty version13 is preserved and no
application tables are created.

The previous generic fatal message now includes bounded `failure_stage` and
`failure_reason` fields. Error causes remain available through wrapping for
classification, but raw errors, connection URLs, passwords and tokens are not
logged. Tests cover config/database/migration/telemetry/storage/scanner/listener
classifications, dirty schema, timeout and unknown errors, plus secret redaction.
This changes startup diagnostics only; HTTP/WS contracts and migration SQL stay
unchanged. Startup commands now build/run `./cmd/server` as a package in the
Makefile, Dockerfile and README so the helper is included.

Run `make verify-local-backup` (or its explicit crash variant) to include these
cases. They use a dedicated startup-failure DB alongside the source/restore
fixtures and retain it in the test volume for inspection. Hosted schema-specific
rollout and provider startup/termination timing remain deployment checks.

Verification observed: unit redaction/classification tests passed under race;
`go test -buildvcs=false -tags=integration -race -count=1 -timeout=180s ./...`,
`go vet -buildvcs=false -tags=integration ./...` and
`GOFLAGS=-buildvcs=false make build` passed in Docker Go1.23. VCS stamping was
disabled only for the mounted verification checkout after Git ownership blocked
the initial build; normal build commands retain stamping. The ordinary full
suite had no `TEST_DATABASE_URL`, so earlier database tests skipped.

The separate actual-DB crash recovery flow passed with the new startup cases.
After review strengthened the HTTP-start marker assertion, the default
`make verify-local-backup` flow also passed: source/startup suite13.18s and
restored-server checks7.33s including bcrypt/race overhead. The startup-failure
cases themselves took0.15s. `gofmt`, script syntax and `git diff --check` passed.
golangci-lint is unavailable. Test fixtures were stopped with volumes retained;
the existing FE backend stayed healthy and was not rebuilt/restarted.
