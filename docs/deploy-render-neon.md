# Deploy on Render with Neon

This guide uses one Render **Go Web Service** instance and a Neon PostgreSQL
database. The application applies embedded SQL migrations before opening its
HTTP listener. It needs no separate migration command or migration files at
runtime.

## Render Free deployment scope

The current deployment choice is Render Free with one instance. Free web services
cannot scale horizontally. Local WebSocket delivery, presence, typing and limits
match this topology; Phase 09B/09C and the multi-node 09D rollout are deferred.
The [Phase 9 plan](../plan/09-scale-observability.md) tracks single-instance staging
checks and preserves future distributed requirements.

Free spin-down and cold starts are accepted. Notification and cleanup workers run
inside the web process and resume eligible PostgreSQL work after it wakes; they
have no always-on latency guarantee. Clients reconnect/rejoin and recover durable
messages/inbox through REST. No Redis broker or paid background worker is required.
Do not use Free Key Value as a persistent broker: its data has no disk persistence.

Use the [Free operation runbook](render-free-operations.md) and
`make smoke` for bounded probe, REST recovery and reconnect verification.

References: [Render scaling](https://render.com/docs/scaling),
[Free services](https://render.com/docs/free),
[Key Value persistence](https://render.com/docs/key-value#data-persistence).

## Before the first deploy

1. Run `go test -race -count=1 ./...` and `go vet ./...` with Go available.
2. Test a clean install, an upgrade, and a restart against a separate Neon
   branch or staging database. Back up production data before first use of
   startup migrations on an existing database. Do not point tests that roll
   back migrations at production.
3. Commit and push the branch Render will deploy. Keep `.env.local` and
   `configs/.env` out of Git and out of Render's environment file uploads.

## Neon connection

In Neon's **Connection Details**, choose the target branch and database and
copy the **Direct connection** details. The direct hostname does not contain
`-pooler`. Split the connection string into its host, port, user, password,
and database name for the `DB_*` variables below. The service uses one
connection configuration for both requests and startup migrations. Set
`DB_SSLMODE=require`.

Use a Neon branch or separate database for the staging checks. The test suite
that uses `TEST_DATABASE_URL` performs migration rollbacks and expects an
isolated disposable database.

## Render service

In Render, choose **New → Web Service**, connect this Git repository, and
select the pushed branch. Set:

| Setting | Value |
| --- | --- |
| Language | `Go` |
| Build Command | `CGO_ENABLED=0 go build -o gogo-dl ./cmd/server` |
| Start Command | `./gogo-dl` |
| Health Check Path | `/health` |
| Instances | `1` |

Render's native Go runtime uses its current Go version, which can differ from
the version in this repository's Dockerfile. If an exact toolchain version is
required, deploy using Docker instead.

Add these variables under **Environment**. Use Render's secret storage for
passwords and JWT secrets; never paste a full Neon URL into `DB_HOST`.

| Variable | Value |
| --- | --- |
| `APP_ENV` | `production` |
| `SERVER_HOST` | `0.0.0.0` |
| `SERVER_PORT` | `10000` |
| `DB_HOST` | Neon direct hostname (without `-pooler`) |
| `DB_PORT` | Neon port, normally `5432` |
| `DB_USER` | Neon user |
| `DB_PASSWORD` | Neon password |
| `DB_NAME` | Neon database name |
| `DB_SSLMODE` | `require` |
| `JWT_ACCESS_SECRET` | Long random secret |
| `JWT_REFRESH_SECRET` | A different long random secret |
| `CORS_ALLOWED_ORIGINS` | Exact HTTPS origin of the frontend |
| `WS_ALLOWED_ORIGINS` | Exact HTTPS origin of the frontend |
| `WS_ALLOW_MISSING_ORIGIN` | `false` |

The application reads `SERVER_PORT`, whereas Render provides `PORT` by
default. Set `SERVER_PORT=10000` explicitly. If there are several frontend
origins, use comma-separated exact origins in both origin variables. Do not
copy `configs/.env.example` to Render: its values target development.

## Verify the deployment

1. Confirm the logs show `database connected`, then `migrations up to date`,
   then HTTP startup. A migration failure stops startup, so the deploy cannot
   pass `/health`. A restart with no new migrations should also reach health.
2. Request `https://<service>.onrender.com/health` and expect HTTP 200 with
   `status: "ok"`. This route confirms the process started after migration;
   it does not recheck database connectivity on every request.
3. From the allowed frontend origin, exercise registration or login and a
   WebSocket connection to
   `wss://<service>.onrender.com/api/v1/ws?token=<access_token>`. Send a room
   `join` event and verify message delivery. Treat the URL token as sensitive.
   Swagger UI is disabled when `APP_ENV=production`.

The WebSocket hub stores subscriptions in one process. Keep one instance until
cross-instance delivery exists; clients should reconnect after an instance
replacement. Render Free services may sleep after 15 minutes without inbound
HTTP requests or WebSocket messages and incur a cold start. Use a paid instance
if the service must remain ready.

Each startup checks for pending migrations. If a migration fails or leaves
`schema_migrations` dirty, inspect the error and database state, restore from
backup or repair it deliberately, and redeploy. Do not run `migrate-down` on
production to make a failed deploy healthy.

Provider references: [Render Go](https://render.com/docs/deploy-go-nethttp),
[web services and ports](https://render.com/docs/web-services),
[health checks](https://render.com/docs/health-checks),
[WebSockets](https://render.com/docs/websocket),
[Free service behavior](https://render.com/docs/free), and
[Neon connection pooling](https://neon.com/docs/connect/connection-pooling).
