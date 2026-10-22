# gogo-dl

Backend service for a real-time chat application built with Go, Gin, WebSocket, and PostgreSQL.

## Stack

| Layer      | Library                      |
|------------|------------------------------|
| Framework  | [Gin](https://gin-gonic.com) |
| Realtime   | gorilla/websocket            |
| Database   | PostgreSQL + sqlx            |
| Auth       | golang-jwt/jwt v5 (access + refresh tokens) |
| Migrations | golang-migrate               |
| Config     | godotenv                     |
| Logging    | zerolog                      |

## Project structure

```
gogo-dl/
├── cmd/server/main.go          # entry point, wiring, graceful shutdown
├── internal/
│   ├── config/                 # .env loading, Config struct
│   ├── database/               # sqlx connect, migration runner
│   ├── ws/                     # WebSocket hub (hub.go, client.go, message.go)
│   ├── middleware/              # JWT (auth.go, jwt.go), cors, logger, recover
│   ├── httpserver/              # Gin engine + route registration
│   ├── user/                   # Register, Login, Refresh, Profile CRUD
│   └── chat/                   # Rooms, Messages, realtime broadcast
├── pkg/apperror/               # Custom error types + gin response helper
├── migrations/                 # SQL migration files (golang-migrate format)
├── configs/.env.example        # Environment variable template
├── .env.local                  # Local-only configuration (gitignored)
├── Makefile
└── README.md
```

## Prerequisites

- Go 1.23+
- PostgreSQL 14+
- [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)
  ```
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```

## Setup

### 1. Clone and configure

```bash
git clone https://github.com/xuanphongtran/gogo-dl.git
cd gogo-dl
cp configs/.env.example .env.local
# Edit .env.local — set local DB credentials and JWT secrets
```

### 2. Start PostgreSQL (Docker)

```bash
# Option A: docker compose (add a docker-compose.yml or use the helper below)
make docker-up

# Option B: manual
docker run -d \
  --name gogo-pg \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=gogo_dl \
  -p 5432:5432 \
  postgres:16-alpine
```

### 3. Install dependencies

```bash
make deps
```

### 4. Run the server

```bash
make run          # with Air hot-reload if installed
# or
go run ./cmd/server
```

Server starts on `http://localhost:8080` by default.
The server applies pending SQL migrations before accepting requests. Migration
files are embedded in the binary, so deployments need only the built executable
and environment variables. `.env.local` is loaded locally; existing
`configs/.env` is still supported, and process environment variables take
precedence over both files. Never deploy `.env.local` or commit credentials.

On startup failure, JSON logs include `failure_stage` and `failure_reason` without
raw error details. For example, `database/connection_failed` identifies DB
connectivity and `migrations/dirty_schema` identifies a failed migration state.
See the [deployment guide](docs/deploy-render-neon.md) before attempting recovery;
dirty schema state is never automatically cleared.

For a Render Go Web Service backed by Neon, follow the
[Render and Neon deployment guide](docs/deploy-render-neon.md). It covers the
build and start commands, required environment variables, and checks before
the first production rollout.

In development, interactive Swagger UI is available at
`http://localhost:8080/swagger/index.html`. It is disabled when
`APP_ENV=production`. Regenerate the committed API document after changing
Swagger annotations with `make docs` (requires the Swag CLI).

For internet-facing deployments, set `WS_ALLOWED_ORIGINS` to comma-separated exact `http://` or `https://` origins. Production rejects missing origins unless explicitly configured otherwise in a controlled environment. HTTP bodies, headers, WebSocket frames, connections, and in-process request rates are bounded by the `HTTP_*`, `WS_*`, `*_RATE_*`, and `*_BURST` settings in `configs/.env.example`.

---

## API Reference

### Auth

| Method | Path                    | Auth | Description              |
|--------|-------------------------|------|--------------------------|
| POST   | `/api/v1/auth/register` | ✗    | Register a new account   |
| POST   | `/api/v1/auth/login`    | ✗    | Login, get token pair    |
| POST   | `/api/v1/auth/refresh`  | ✗    | Refresh access token     |

### Users

| Method | Path               | Auth | Description            |
|--------|--------------------|------|------------------------|
| GET    | `/api/v1/users/me` | ✓    | Get my profile         |
| PATCH  | `/api/v1/users/me` | ✓    | Update my profile      |
| DELETE | `/api/v1/users/me` | ✓    | Delete my account      |

Profile updates accept an avatar URL only when it is an absolute `http://` or
`https://` URL. Other schemes, including `file://`, `ftp://`, and
`javascript:`, are rejected.

### Rooms

| Method | Path                            | Auth | Description                      |
|--------|---------------------------------|------|----------------------------------|
| GET    | `/api/v1/rooms`                 | ✓    | List all rooms                   |
| POST   | `/api/v1/rooms`                 | ✓    | Create a room                    |
| GET    | `/api/v1/rooms/:id`             | ✓    | Get room details                 |
| POST   | `/api/v1/rooms/:id/join`        | ✓    | Join a room                      |
| DELETE | `/api/v1/rooms/:id/membership` | ✓ | Leave the current room membership |
| GET | `/api/v1/rooms/:id/members` | ✓ | List room members and roles |
| POST | `/api/v1/rooms/:id/invitations` | ✓ | Invite a user to a room |
| DELETE | `/api/v1/rooms/:id/members/:user_id` | ✓ | Remove a room member |
| PATCH | `/api/v1/rooms/:id/members/:user_id` | ✓ | Change member role |
| POST | `/api/v1/rooms/:id/ownership` | ✓ | Transfer room ownership |
| GET    | `/api/v1/rooms/:id/messages`    | ✓    | List messages (cursor pagination)|
| GET    | `/api/v1/rooms/:id/messages/search` | ✓ | Search live room messages (ID cursor) |
| POST   | `/api/v1/rooms/:id/messages`    | ✓    | Send a message (+ WS broadcast)  |
| PATCH | `/api/v1/rooms/:id/messages/:message_id` | ✓ | Edit my message using its revision |
| DELETE | `/api/v1/rooms/:id/messages/:message_id` | ✓ | Delete a message and return its tombstone |
| GET | `/api/v1/rooms/:id/presence` | ✓ | Room presence and typing snapshot |
| GET | `/api/v1/rooms/:id/read-state` | ✓ | My read cursor and unread count |
| PUT | `/api/v1/rooms/:id/read-state` | ✓ | Advance my read cursor |

Invitation actions use `GET /api/v1/users/me/invitations`, `POST /api/v1/invitations/:id/accept`, and `POST /api/v1/invitations/:id/decline`. Public rooms can be discovered and joined by authenticated users; private rooms require an accepted invitation. Message history and WebSocket subscriptions require current membership.

Re-inviting a removed member creates a new `pending` invitation with a new ID.
The invitee must explicitly accept that invitation before regaining private-room
access. Repeated manager invites reuse the current pending invitation. Replaying
an older `accepted` invitation cannot recreate missing membership and returns
409 (`invitation is no longer pending`); acceptance retries remain successful while
membership exists. Rejoining creates a fresh membership generation, so previous
mention/inbox records do not reappear.

Management writes recheck current roles under transaction locks. Protected history
and member-list reads hold membership stable until the read transaction commits;
removal waits for an already authorized read, and later reads are denied. Private
room/member-list reads return `404` for non-members; public member-only reads
return `403`. Realtime membership controls remain best effort after DB commit;
clients recover from the authenticated REST source of truth.

Leaving a public room returns `204` even when the caller is no longer a member,
so retries are safe. A private room without membership, or a missing room,
returns `404`. Owners must transfer ownership before leaving (`409`).

For a public room the caller has not joined, `role` is `null`; otherwise it is
`owner`, `moderator`, or `member`.

### Message lifecycle

Sending and editing require nonblank content of at most 4000 UTF-8 bytes after
trimming whitespace. Content validation failures return `400`; `413` means the
entire HTTP request body exceeds `HTTP_MAX_BODY_BYTES`, including JSON overhead.
The byte limit can be reached before 4000 characters for non-ASCII text.

Messages include `revision` (initially `1`), `edited_at` and `deleted_at` (nullable
UTC timestamps). The author must remain a room member to edit, with no edit time
limit. Owners and moderators can delete any message in their room; only the
author can edit its text. A public-room non-member receives `403`; a private-room
non-member receives `404` for either mutation.
Message creation resolves the author's username from PostgreSQL, matching
history and lifecycle event projections.

To edit, send `PATCH /api/v1/rooms/:id/messages/:message_id`:

```json
{ "content": "Updated text", "revision": 1 }
```

The response is `200` with the complete Message. Content has the same validation
and 4000-byte limit as sending. Changed text increments revision and sets
`edited_at`. Equal text at the current revision, or an immediate identical retry
using the previous revision, returns the current Message without another event.
Other stale revisions return `409` (`message revision conflict`); reload history
before deciding whether to retry. Editing a deleted message returns `409`
(`message deleted`).

`DELETE /api/v1/rooms/:id/messages/:message_id` has no body and returns `200` with
a tombstone: empty `content`, non-null `deleted_at`, and an incremented revision.
Authorized repeated deletes return the same tombstone without changing revision,
timestamps or audit actor. Deletion applies to the current message version.
History retains tombstones and continues paginating by immutable message ID.
Deletion clears the current database row's content; database backups and client
copies have separate retention. There is no restore or edit-history endpoint.

Membership, current roles and message state are locked until the mutation commits.
Internal deletion audit retains actor and time; deleting that actor's account
sets the audit reference to null. See the [Phase 6 specification](spec/06-message-lifecycle.md)
for concurrency, retry and retention details. Migration `000006` is embedded and
applies automatically before the updated server accepts requests.

### Room message search

`GET /api/v1/rooms/:id/messages/search?q=chào%20bạn&limit=20&before=123`
requires authentication and current membership. Public nonmembers receive `403`;
private nonmembers and missing rooms receive `404`. Authorization holds room and
membership locks until the search transaction completes.

The trimmed query must be valid UTF-8, 1–256 bytes, and contain searchable terms.
Empty/punctuation-only queries, invalid bounds, and invalid encoding return `400`.
`limit` defaults to 20 (1–100); `before` is an optional positive, exclusive message
ID cursor. Results use descending message IDs, without relevance ranking:

```json
{"messages":[],"next_before":null}
```

When another matching page exists, `next_before` is the last returned ID.
All query terms must match; punctuation does not enable operators or prefix search.
The PostgreSQL `simple` configuration does not promise stemming or accent folding:
`chào` matches `xin chào bạn`, while `chao` is a different term. See
[PostgreSQL's plain-text query parsing](https://www.postgresql.org/docs/16/textsearch-controls.html).
Search returns current message fields, excludes tombstones, and updates atomically
with edits/deletions. Pagination is not a snapshot; edits can change later pages.
Restart from the first page when changing `q`. Existing history behavior is unchanged.

Embedded migration `000008` backfills a generated search vector and adds a partial
GIN index. It rewrites existing messages and acquires a table lock; measure it on
representative staging data before rollout. The startup migration statement budget
is 5 seconds; larger databases need a reviewed migration procedure. Local query
plans and timings are recorded in [the Phase 8A report](docs/phase-8a-search.md).

### Presence and personal read state

These endpoints require current membership. Public nonmembers receive `403`;
private nonmembers and missing rooms receive `404`.

Presence is room subscription activity in this process. A user appears online
while at least one authorized socket is joined to the room; multiple tabs are
combined. `GET /api/v1/rooms/1/presence` returns:

```json
{ "room_id": "1", "online_user_ids": [7], "typing_user_ids": [] }
```

`GET /api/v1/rooms/1/read-state` returns:

```json
{ "room_id": 1, "last_read_message_id": 42, "unread_count": 3 }
```

To acknowledge history through a message, use `PUT` on the same path with
`{ "last_read_message_id": 42 }`. The ID must be positive and belong to this room
(`404` otherwise). The cursor only increases; valid equal/older retries return
the current state. An unset cursor is `0`. Unread counts all room message IDs
above the cursor except your own messages, including history before joining and
deleted-author messages. Message tombstones count by ID; edits/deletes
do not create another unread item. Reading history does not advance the cursor.
Leaving/removal clears it, and rejoining starts at `0`.

Changed cursors are persisted before private `read_state` events are delivered
to your connections. Other members do not receive read receipts. Events are
best effort; after reconnect, reload GET for an authoritative count. Keep the
maximum cursor when processing out-of-order events; old counts may be stale.

Message lifecycle and read-state endpoints are included together. Embedded
migration `000006` provides message lifecycle fields, followed by `000007` for
read cursors. Both apply automatically in order before the server accepts requests.

### WebSocket

The machine-readable WebSocket contract is [docs/asyncapi.yaml](docs/asyncapi.yaml)
(AsyncAPI 3.0). It lists the handshake authentication options, client commands,
server events, payload schemas, and examples. Swagger covers the REST API;
AsyncAPI covers WebSocket messages. To validate or render the document locally
with the [AsyncAPI CLI](https://www.asyncapi.com/docs/tools/cli/usage) (requires
Node.js 22 via nvm):

```bash
nvm use 22
npx --yes @asyncapi/cli@4.1.1 validate docs/asyncapi.yaml
make docs-asyncapi
```

Open `http://localhost:8080/asyncapi/` (or `/asyncapi/index.html`) to read the
generated documentation. Download the contract at `/asyncapi/asyncapi.yaml`.
Like Swagger, these routes are available only outside production; they return
404 when `APP_ENV=production`.

Update `docs/asyncapi.yaml` whenever the WebSocket contract changes, then run
`nvm use 22` and `make docs-asyncapi`. Include the regenerated `docs/asyncapi/`
assets with the YAML change. A Go test checks that the generated source stamp
matches the current contract. HTML, CSS, JavaScript, and YAML are embedded in
the Go binary, so regular Go builds and Render deployments do not require Node
or documentation files at runtime.

```
GET /api/v1/ws?token=<access_token>
```

Clients may send `join`, `leave`, `typing_started` and `typing_stopped` commands. `join` requires current room
membership; use the REST room endpoints to join a public room or accept a private
room invitation first. A `leave` command ends only the WebSocket subscription.
To send a durable chat message, call `POST /api/v1/rooms/:id/messages` and then
receive its WebSocket broadcast. The server can send several newline-separated
JSON events in one WebSocket text frame; parse each nonempty line separately.
Send one JSON command per client frame. For example:

```jsonc
// Join a room
{ "type": "join", "room_id": "1" }

// Receive a new message broadcast
{
  "type": "message",
  "room_id": "1",
  "payload": {
    "id": 42,
    "user_id": 7,
    "username": "alice",
    "content": "Hello!",
    "created_at": "2026-01-01T12:00:00Z"
  }
}

// Start/refresh typing (no payload); stop with type "typing_stopped"
{ "type": "typing_started", "room_id": "1" }

// Server typing event: identity and expiry are server-generated
{ "type": "typing_started", "room_id": "1", "payload": { "user_id": 7, "expires_at": "2026-01-01T12:00:05Z" } }

// Aggregate presence; socket joins also receive a presence_snapshot
{ "type": "presence", "room_id": "1", "payload": { "user_id": 7, "online": true } }

// Delivered only to this user's connections after the cursor commits
{ "type": "read_state", "room_id": "1", "payload": { "room_id": 1, "last_read_message_id": 42, "unread_count": 3 } }
```

Server-only `message_updated` and `message_deleted` events use this same envelope
with a complete Message payload, including numeric `room_id`, `revision`,
`edited_at` and `deleted_at`. The envelope `room_id` remains a string. New
`message` events also include those additive fields. Lifecycle events are emitted
after the database commit; retries/no-ops emit no additional event. Delivery is
best effort. For a known message, apply only greater revisions so a late edit
cannot overwrite a tombstone. Refresh relevant history after reconnect or a gap;
event arrival order is not guaranteed.

Typing requires an authorized joined socket. Starts are throttled to once per
second per connection (`rate_limited` on excess), expire in 5 seconds and are
cleared within a 250 ms sweep interval. Refresh approximately every 2 seconds
while typing. Stops use `expires_at: null`. Tabs are aggregated, so one stopped
tab does not stop another active tab. Leave, disconnect and membership
revocation clear that connection's state. Already-stopped retries emit no event
and perform no database work. Only one authorization command may be
pending per connection (`authorization_pending`); typing before join returns
`not_joined`. Clients may not supply event payloads or forge server-only events.

Reconnect requires a fresh join and snapshot. Existing ping/pong deadlines
remove stale sockets; restart clears ephemeral presence/typing. Keep one server
instance until shared delivery is implemented. Full contracts and privacy rules
are in [the Phase 7 specification](spec/07-presence-read-state.md).

---

## Makefile commands

```
make run             # run server (hot-reload with Air if available)
make build           # compile binary to ./bin/gogo-dl
make test            # go test -race ./...
make lint            # golangci-lint
make migrate-up      # apply all pending migrations
make migrate-down    # roll back all migrations
make migrate-create name=add_something   # create new migration pair
make tidy            # go mod tidy
make clean           # remove ./bin
```

---

## Testing

For an isolated actual-process restart and PostgreSQL backup/restore rehearsal,
run `make verify-local-backup` with Docker. It creates and retains its own test
DB/volume, without changing the FE database. See the
[local recovery guide](docs/local-recovery.md) for scope, assertions and limits.
Use `make verify-local-crash-recovery` to additionally interrupt an uncommitted
transaction with a PostgreSQL process crash on that invocation's own fixture.

Unit and WebSocket tests run without external services. PostgreSQL integration tests are enabled only when TEST_DATABASE_URL points to an isolated disposable database; they never load configs/.env.

    TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/gogo_dl_test?sslmode=disable go test -race -count=1 ./...

The integration suite covers clean migration, upgrade from migration 000001, rollback, transaction integrity, deleted-author history, and typed constraint mapping.
Phase 7 tests additionally cover upgrade from schema 5, concurrent read advances,
membership locking, unread counts and leave/rejoin cursor cleanup. Use a fresh
disposable database whose name ends in `_test`; the Phase 7 fixture refuses an
existing application schema. Its cleanup drops only that test database's schema.

Phase 6 integration tests additionally cover upgrade from schema 000005,
lifecycle constraints, tombstone pagination/audit, write rollback, concurrent
edits/deletes, and membership locking. They require a disposable database whose
name ends in `_test` and reset its data/schema during cleanup.

Phase 8A tests cover clean schema/upgrade from 7, vector backfill and rollback,
room privacy, UTF-8/plain-text search, ID cursors, edit/delete index consistency,
membership-removal locking, and query plans on 100,000 messages in two rooms.
They reuse the fresh disposable database fixture and never load local env files.

## Architecture notes

### WebSocket Hub

The hub is a singleton event loop (`ws.Hub`) that runs in a single goroutine. This eliminates the need for mutexes on the internal maps (`clients`, `rooms`). All state mutations flow through channels:

```
domain service → hub.Broadcast(roomID, msg) → broadcast channel
                                               → Run() loop → fanOut() → client.sendJSON()
                                                                         → client.send channel
                                                                         → WritePump() → ws.Conn
```

### Realtime broadcast flow (SendMessage)

1. HTTP handler validates input and calls `chatService.SendMessage()`.
2. Service checks room exists + caller is a member.
3. Message is inserted into PostgreSQL (source of truth).
4. Service calls `hub.Broadcast(roomID, wsMsg)` — **non-blocking** channel send.
5. Hub's `Run()` goroutine fans the message out to all clients that joined the room via `{"type":"join","room_id":"<id>"}`.
6. If the broadcast channel is full (hub busy), the broadcast is dropped and an error is logged, but the HTTP response still returns 201 (message is persisted).

### Token refresh

Clients should proactively refresh before the access token expires (`expires_at` is in the login/register response). The refresh endpoint issues a brand-new token pair.


## Safety behavior for realtime access

- WebSocket clients may send `join`, `leave`, `typing_started` and `typing_stopped` commands. Joins and typing are checked against PostgreSQL membership outside the event loop. Pending authorization is bounded and invalidated by leave/revocation.
- Client-originated message and lifecycle events are rejected; durable messages are persisted through the chat service before broadcast.
- Membership revocation removes active room subscriptions after the membership transaction commits and the Hub processes the control event.
- If an account is deleted, authored message history is retained with a nullable `user_id` and the username `[deleted user]`.
- WebSocket upgrades require an exact configured Origin and are subject to global/per-user connection admission and per-connection frame limits.
- Invalid JSON/request semantics return stable `invalid request` errors; oversized HTTP bodies return `request body too large`, and exhausted limiters return `rate limit exceeded` with `Retry-After`.
- Allowed frontend origins may send `Authorization`, `Content-Type` and
  `Idempotency-Key`; preflight also retains `Origin`. Responses expose
  `Content-Length`, `Retry-After` and `X-Request-ID` to browser JavaScript.
  Keep the same idempotency key and immutable payload when explicitly retrying
  a message whose response was lost. Unconfigured origins remain denied.

## Observability and shutdown

Phase 9A adds `/livez`, `/readyz` and `/readyz/realtime`; `/health` keeps its
existing response. Readiness checks PostgreSQL with a 250 ms deadline/cache and
withdraws immediately during drain. Realtime readiness also requires Hub admission.

Prometheus metrics use a separate private listener and are disabled by default.
Set `METRICS_ENABLED=true` and `METRICS_LISTEN_ADDR=127.0.0.1:9090` for a local
collector. The public API has no `/metrics` route. OTLP tracing is enabled only
with a full `OTEL_EXPORTER_OTLP_ENDPOINT` URL; credentials belong in
`OTEL_EXPORTER_OTLP_HEADERS` secrets. `OTEL_SERVICE_NAME` defaults to `gogo-dl`,
`TRACE_SAMPLE_RATIO` to `0.05`, and `SHUTDOWN_TIMEOUT` to `15s`.

Production logs are JSON with request/trace correlation and route templates.
On SIGTERM the server rejects new API/WS requests, withdraws readiness and drains
HTTP, sockets and telemetry within one shared budget. Registered sockets receive
close code 1001; slow writers are forcibly closed at the deadline. Docker Compose
allows 20 seconds for the default drain. Keep the platform grace longer than any
configured `SHUTDOWN_TIMEOUT`.

See [the operations guide](docs/observability.md) for metrics, dashboards, alerts,
SLO definitions, deployment checks and local verification evidence.

## Phase 08B: R2 uploads, message attachments and downloads

Cloudflare R2 is selected. The AWS SDK core signer signs requests locally using
Cloudflare credentials; no AWS account is needed. All objects must remain private.
Migration `000009` adds reservations, retained cleanup tombstones, attachment
aggregate counters and independent outbox deliveries. Migration `000011` adds
processing, `000012` stores processed artifacts, and `000013` adds atomic message
binding, authorized downloads and attached-file lifecycle cleanup. See the
[frontend contract](docs/attachments.md) and [completion SPEC](spec/08b-message-attachments.md).
The lightweight processor is the supported default; ClamAV is deferred indefinitely.

Set all four variables together to run the cleanup worker:
`R2_ACCOUNT_ID`, `R2_BUCKET`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`.
Use the account's 32-character lowercase hex ID and a private bucket name.
Store credentials in local env files or Render secrets, never in Git.
Docker Compose loads these through its existing `configs/.env` entry.
Upload admission defaults to false. Set `ATTACHMENT_UPLOAD_ENABLED=true` with
`ATTACHMENT_SCAN_ENABLED=true` and complete R2/scanner settings to issue tickets.
This enables quarantine uploads only. Without R2 configuration the
cleanup worker is inactive and reservations retain quota/storage until configured.

Authenticated routes under `/api/v1`:

| Method | Route | Current behavior |
| --- | --- | --- |
| POST | `/rooms/:id/attachments/uploads` | 201 private quarantine PUT ticket when enabled; 503 when disabled |
| GET | `/rooms/:id/attachments/:attachment_id` | 200 current metadata; unattached files are uploader-only; attached files are visible to current members |
| POST | `/rooms/:id/attachments/:attachment_id/complete` | 202 scan request/current state, never a clean verdict |
| DELETE | `/rooms/:id/attachments/:attachment_id` | 204 idempotent cancel and delayed cleanup; attached files return 409 |
| GET | `/rooms/:id/messages/:message_id/attachments/:attachment_id/download` | 200 authorized final-object GET URL, valid 60 seconds |

Upload metadata fields: `filename`, `content_type`, `size_bytes`, `sha256`.
Allowed extension/type pairs: `.jpg`/`.jpeg` + JPEG, `.png` + PNG, `.txt` + plain
text (extension case-insensitive); size 1–10 MiB; checksum 64 hex characters.
Retry keys are 16–128 printable ASCII bytes. Quotas are 100 MiB and 10 pending
unattached reservations per user, 1 GiB total per user and 10 GiB per room.
Message `attachment_ids` accepts up to five distinct own ready/unexpired files in
the same room and requires `Idempotency-Key`. A file-only message may have empty
content. Binding and message creation commit atomically; retries do not claim twice.

Each reservation owns a unique `quarantine/<uuid>` key. PUT credentials bind
`If-None-Match: *`, Content-Type and Content-Length with a five-minute deadline.
Content-Length is set automatically by browsers and must match the declared size.
The cleanup worker waits until this deadline plus ten minutes, keeps quota charged
until deletion succeeds, and uses 30-second fenced leases and eight attempts with
backoff. A storage outage never exposes unverified files. Cancel, account deletion
and room deletion retain cleanup identity; orphaned reservations are swept later.
Scan deliveries have an opt-in consumer. Message REST/WS payloads include safe
processed attachment metadata, never object keys or signed URLs. Attached files
outlive the 24-hour ready TTL; message/account deletion schedules delayed cleanup.

Monitor `domain_outbox_deliveries` for cleanup `dead` rows, failed attempts and oldest
pending jobs, and `attachments` for orphaned/deleting rows and reserved bytes.
Investigate the storage failure before any operator-controlled replay; dead jobs
retain their keys and quota. No automatic retention policy removes these records.
Real R2 conditional PUT, concurrent writes, signed size/type headers, browser CORS,
late writes and hosted scanner/promotion remain operational rollout checks. See [spec](spec/08-rich-messaging.md) and [plan](plan/08-rich-messaging.md).

## Phase 08C: mentions and private inbox

Messages accept an optional `mention_user_ids` array. Mention sends require an
`Idempotency-Key` header; normalized requests are retained for 24 hours and
retries return the original authorized message. Mention recipients are tied to
their current membership generation, so leaving and rejoining does not restore
old notifications. The private feed and preferences are available at
`/api/v1/users/me/notifications`, `/api/v1/users/me/notification-preferences`,
and `/api/v1/rooms/:id/notification-preferences`. Inbox rows contain only room
and message references. A worker emits a best-effort user-only WebSocket
`notification` event after durable insertion; REST remains the recovery path.


The following notification endpoints require `Authorization: Bearer <access_token>`:

| Method | Path | Request / response |
|---|---|---|
| GET | `/api/v1/users/me/notifications` | `before` positive ID, `limit` 1–100 (default 20), `unread_only` boolean; returns `notifications` and nullable `next_before` |
| PUT | `/api/v1/users/me/notifications/:id/read` | No body; returns the owned notification with stable `read_at` on retries |
| GET / PUT | `/api/v1/users/me/notification-preferences` | Default `mentions_enabled=true`; PUT requires a boolean, including false |
| GET / PUT | `/api/v1/rooms/:id/notification-preferences` | Default `muted=false`; PUT requires a boolean, including false; current membership required |

Mention IDs must be positive, distinct, and limited to 10; normalized IDs are
sorted. Self mentions remain on the message but generate no notification.
`Idempotency-Key` must contain 16–128 printable ASCII bytes; reusing a retained key
with different normalized room/content/recipient data returns `409`. Retries
recheck current room authorization. Content may be empty with valid attachments;
file sends also require the key, whose normalized body includes attachment IDs.

Notification DTOs contain `id`, `kind`, `room_id`, `message_id`, `created_at`,
nullable `read_at`, and `availability` (`available` or `deleted`). Other users'
notification IDs return `404`. The feed expires after 30 days; marking a
notification read does not advance the room read cursor. Preferences are evaluated
at delivery: disabled/muted delivery is suppressed permanently without backfill.
A removed membership clears its inbox and room preference; rejoining starts with
room defaults. Existing rows may show a deleted reference without exposing text.

Inbox insertion, its private `notification.created` outbox intent, and the mention
lease acknowledgement commit together. Broker progress remains separate and awaits
Phase 9B; no distributed relay is enabled by Phase 8C. The current deployment
continues to use one application instance and REST recovery for dropped WS events.

## Render Free operation checks

Run `make smoke SMOKE_ARGS='-url https://gogo-dl.onrender.com'` for one bounded
probe pass. Optional token-file/room flags check authorized REST recovery and two
WebSocket joins without printing tokens or response bodies. See the
[Free operation runbook](docs/render-free-operations.md) for before/after redeploy
checks, worker backlog inspection and rollback. Distributed Phase 09B–09D remain
deferred while running one Free instance.

## Independent attachment scanner integration

The backend can consume attachment scan intents with an independently deployed
`gogo-scanner` service. Migration `000011` adds fenced scan metadata and `ready` state.
Set `ATTACHMENT_SCAN_ENABLED=true`, all four R2 settings, `SCANNER_URL` (for example
`http://gogo-scanner:10000` on the same private network) and `SCANNER_API_TOKEN`.
The scanner and backend must share a random 32–512-byte nonspace ASCII service
secret, distinct from both JWT secrets. No service token belongs in the frontend.

Use HTTPS outside private networks; HTTP is accepted only for loopback/private IPs,
`.internal` or single-label private service hostnames. Verify the actual private
network/DNS configuration; environment-supplied endpoints are operator controlled.
URL credentials, paths, query strings, redirects and environment proxies are not
accepted. Optional scanner settings must be a complete valid pair even when off.

The single sequential worker verifies conditional R2 reads, exact 10 MiB-bounded
bytes/hash, full JPEG/PNG decoding (max 4 megapixels) or UTF-8 nonbinary text, then
calls the versioned `/v2/process` contract. Default `sanitize-v1` rewrites images;
optional `clamav-v1` returns original bytes after a clean scan. Both bind input and
output identity. The worker conditionally creates and reads back processed bytes
at `final/<reservation UUID>` before atomically committing ready plus scan
acknowledgement. Scan jobs have a 60 s lease/45 s work budget; other purpose leases
remain 30 s. Ready unattached retention is 24 h. Cancellation, lost leases, removal,
invalid/rejected content and untrusted/failed responses never publish ready.
Sanitizer acceptance is not an antivirus verdict. `verified` means the named
processing policy passed. Migration `000012` keeps original upload metadata/quota
unchanged and stores final artifact hash/size/type/engine/policy separately.

Cleanup retains the original quarantine identity through cascades and always
removes both deterministic quarantine/final keys before releasing quota, including
crashes after promotion. Cleanup has a 12 s work budget within its 30 s lease. Both
physical objects may exist during retention; budget/monitor provider bytes and
cleanup backlog separately from logical reservation quota. Do not run older cleanup
binaries against promoted objects. Migration `000011` down refuses live prepared scans;
never use production down/force to bypass that guard. Migration `000012` down
also refuses live sanitized artifacts because schema11 cannot interpret them.

**Upload admission is opt-in:** `ATTACHMENT_UPLOAD_ENABLED=true` requires configured
R2 and an enabled configured processing worker. Ticket creation and processing do
not alone complete 08B. Schema13 implements message binding, authorized download
and lifecycle cleanup. Hosted end-to-end binding/download/recovery verification
remains a rollout gate. Network/provider operations
are outside DB transactions and Hub; worker cancellation is joined at shutdown.

See [scanner integration SPEC](spec/08b-scanner-integration.md) and
[plan](plan/08b-scanner-integration.md) for verification and rollout boundaries.


### Deploy lightweight processing (08B)

**Schema13 rollout:** stop/drain all older backend and cleanup processes before
applying migration13 and accepting file messages. Older binaries do not understand
attached retention/cancel rules. Do not overlap them with binding traffic. Keep
R2 objects private. Rollback to schema12 refuses live bindings; do not force it
or run production migration-down. See [rollout steps](docs/attachments.md#rollout).

1. Stop/drain old backend scan workers before migration `000012`; schema11 workers
   cannot finalize ready without artifact metadata. Temporarily deploy with
   `ATTACHMENT_UPLOAD_ENABLED=false` and `ATTACHMENT_SCAN_ENABLED=false`, let
   claims drain, then upgrade/re-enable.
   Deploy the **gogo-scanner** repo first using its default Dockerfile (static Go,
   `sanitize` mode). Configure `SCANNER_MODE=sanitize`,
   `SCANNER_LISTEN_ADDR=0.0.0.0:10000`, `GOMEMLIMIT=320MiB` and the shared
   `SCANNER_API_TOKEN`; health path `/readyz`. It needs no ClamAV/signature volume.
2. Deploy this backend through migration `000013`, existing R2 settings and
   `SCANNER_URL=https://<scanner-service>.onrender.com` (base URL only),
   `SCANNER_API_TOKEN`, `ATTACHMENT_SCAN_ENABLED=true` and
   `ATTACHMENT_UPLOAD_ENABLED=true` to enable quarantine tickets. A developer's `127.0.0.1` is unreachable
   from Render. The processor's 22-second caller timeout requires a responsive
   service; hosted cold-start behavior remains a deployment check.
3. R2 IP filtering must permit the backend service's outbound IPs as well as any
   local test IP. Local provider smoke does not prove Render egress access.
4. Drain in-flight scan jobs before changing processor policy/codec or engine;
   immutable final artifacts may differ across versions. Retry never overwrites.
   To restore ClamAV, deploy scanner `Dockerfile.clamav` with `SCANNER_MODE=clamav`
   and sufficient RAM/signatures. The backend and v2 API stay unchanged.

No PDF/Office/SVG/HTML/archives are added. Images are re-encoded without metadata;
EXIF orientation/color metadata is not preserved, so presentation may change.
Upload admission is opt-in. Message binding and authorized download are implemented
by schema13; deploying this branch and hosted smoke remain separate rollout gates.

Local real-provider test (creates/deletes only random owned object keys):

```sh
PROCESSOR_R2_SMOKE=true PROCESSOR_SMOKE_ENV_FILE=.env.local \
SCANNER_URL=http://127.0.0.1:8091 \
go test -count=1 -timeout=180s ./internal/attachment -run TestProcessingRealR2 -v
```

The optional env file supplies R2/service credentials without logging them.
See [lightweight SPEC](spec/08b-lightweight-processing.md) and
[implementation/evidence](plan/08b-lightweight-processing.md).


### Frontend upload flow

1. Compute SHA-256 and POST `/api/v1/rooms/{id}/attachments/uploads` with Bearer
   authentication, a16–128 character `Idempotency-Key` and
   `{filename,content_type,size_bytes,sha256}`. Only current room members may upload.
2. Use the returned short-lived `upload_url`, method `PUT` and headers to upload
   raw bytes directly to private R2. Do not attach the user JWT to R2 requests.
   Browsers set Content-Length automatically. Retrying a successful create-only
   PUT returns412; do not overwrite or request a new key for the same attempt.
3. After successful PUT, POST `/api/v1/rooms/{id}/attachments/{attachment_id}/complete`.
   Poll GET of the same attachment for `scanning`, `ready` or `rejected`.
   `ready` means processing policy passed, not an antivirus guarantee. Metadata
   size/hash describe original upload; output metadata remains internal.
4. Send up to five ready IDs in message `attachment_ids` with a message-specific
   `Idempotency-Key`; content may be blank when files are present. Use the
   [binding/download contract](docs/attachments.md) for response metadata and GET.
5. DELETE the attachment route to cancel an unattached file; cleanup waits for
   upload expiry/grace. Unattached ready files expire after24h; attached files
   follow message/account cleanup instead and cannot be independently canceled.

Configure bucket CORS for your exact frontend origins and PUT (plus GET for
browser-fetched downloads), permitting
Content-Type and If-None-Match; otherwise browser uploads fail despite successful
backend ticket creation. Never expose the bucket publicly or log/cache tickets.
Keep cleanup running and monitor scan/cleanup dead jobs; scanner outages can
leave uploads queued until retry budget is exhausted.
