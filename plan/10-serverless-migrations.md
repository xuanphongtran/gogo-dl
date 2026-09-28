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
