# gogo-dl

Real-time chat backend: Go 1.23+, Gin, PostgreSQL, JWT and WebSocket.
Supports public/private rooms, moderation, message editing/deletion, search,
mentions/inbox and private R2 attachments with lightweight processing.

The supported topology is **one backend instance**. Broker/cross-node delivery
is not implemented. See [release preparation](docs/release.md) and the
[separate pending list](plan/pending.md).

## Run locally

Requires PostgreSQL 14+ (Docker example uses 16). For a host Go process:

```bash
cp configs/.env.example configs/.env
# Set local DB credentials, distinct JWT secrets and frontend origins.
docker compose --env-file configs/.env up -d postgres
make deps
make run
```

Default API: `http://localhost:8080`. Startup applies embedded migrations before
opening HTTP. Migration CLI is not required for normal startup.

For both API and DB in Docker Compose:

```bash
# Reuse the configs/.env created above.
docker compose --env-file configs/.env up -d --build
```

Host execution loads `.env.local`, then `configs/.env`; existing process variables
win. **Compose injects `configs/.env`, not `.env.local`**, and overrides `DB_HOST`
with `postgres`. Keep local credentials out of Git and use deployment secrets on
Render. Never copy a staging/production DSN into the local Compose DB settings.

## Configuration

Use [configs/.env.example](configs/.env.example) for all settings.

- `DB_*`: PostgreSQL connection; use TLS for hosted databases.
- `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`: long, distinct secrets.
- `CORS_ALLOWED_ORIGINS`, `WS_ALLOWED_ORIGINS`: exact frontend origins.
- `SERVER_HOST`, `SERVER_PORT`: bind address; Render uses `0.0.0.0:10000`.
- `APP_ENV=production`: production logging/origin policy; documentation UI disabled.

Uploads default off. To enable, configure all four `R2_*` values, `SCANNER_URL`,
`SCANNER_API_TOKEN`, `ATTACHMENT_SCAN_ENABLED=true` and
`ATTACHMENT_UPLOAD_ENABLED=true`. The supported processor validates JPEG/PNG/TXT
and rewrites images; it is not an antivirus guarantee. ClamAV is deferred.
See [attachment contract](docs/attachments.md) for upload/download, quotas and CORS.

## API and operations

| Resource | Location |
| --- | --- |
| REST documentation (development) | `/swagger/index.html` |
| WebSocket documentation (development) | `/asyncapi/` |
| WebSocket YAML | [docs/asyncapi.yaml](docs/asyncapi.yaml) |
| API usage and recovery rules | [API guide](docs/api-guide.md) |
| Probes, metrics, drain and alerts | [Observability](docs/observability.md) |
| Render + Neon deployment | [Deployment guide](docs/deploy-render-neon.md) |
| Restart, DB outage, backup/restore and crash rehearsal | [Local recovery](docs/local-recovery.md) |
| Free hosting operation/recovery | [Operations runbook](docs/render-free-operations.md) |

`/health` is the legacy health response; `/livez` checks liveness, `/readyz` checks
HTTP readiness and `/readyz/realtime` also checks realtime admission. Public
`/metrics` is unavailable; private metrics must be configured separately.
Startup failures log safe `failure_stage`/`failure_reason`, never raw causes.

## Development

```bash
make build                       # bin/gogo-dl
make test                        # full race suite; DB tests require TEST_DATABASE_URL
make lint                        # requires golangci-lint
make verify-local-backup          # isolated Docker fixtures, retained volume
make verify-local-crash-recovery  # additionally crash only the newly created fixture
```

`TEST_DATABASE_URL` must target a dedicated disposable test DB. Some existing
DB tests roll back migrations or reset schemas. Never use the application DB,
Neon personal data or production. Do not run destructive down/volume commands
on data you need to retain.

After REST annotation changes, `make docs` requires Swag 1.8.12. After WebSocket
contract changes, edit `docs/asyncapi.yaml`, run `nvm use 22`, then
`make docs-asyncapi`; commit regenerated assets. Node is not needed at runtime.

Code flows through handler → service → repository; PostgreSQL owns durable state.
The Hub owns socket state and emits best-effort events after persistence.
[Specifications](spec/README.md) define contracts; [roadmap](plan/README.md)
tracks implementation. [Verification evidence](docs/verification.md) distinguishes
local checks from pending release/deployment evidence.
