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
go run ./cmd/server/main.go
```

Server starts on `http://localhost:8080` by default.
The server applies pending SQL migrations before accepting requests. Migration
files are embedded in the binary, so deployments need only the built executable
and environment variables. `.env.local` is loaded locally; existing
`configs/.env` is still supported, and process environment variables take
precedence over both files. Never deploy `.env.local` or commit credentials.

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
| POST   | `/api/v1/rooms/:id/messages`    | ✓    | Send a message (+ WS broadcast)  |
| PATCH | `/api/v1/rooms/:id/messages/:message_id` | ✓ | Edit my message using its revision |
| DELETE | `/api/v1/rooms/:id/messages/:message_id` | ✓ | Delete a message and return its tombstone |

Invitation actions use `GET /api/v1/users/me/invitations`, `POST /api/v1/invitations/:id/accept`, and `POST /api/v1/invitations/:id/decline`. Public rooms can be discovered and joined by authenticated users; private rooms require an accepted invitation. Message history and WebSocket subscriptions require current membership.

Leaving a public room returns `204` even when the caller is no longer a member,
so retries are safe. A private room without membership, or a missing room,
returns `404`. Owners must transfer ownership before leaving (`409`).

### Message lifecycle

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
PUPPETEER_SKIP_DOWNLOAD=1 npx --yes @asyncapi/cli@4.1.1 generate fromTemplate docs/asyncapi.yaml @asyncapi/html-template@2.3.14 --install --no-interactive -o /tmp/gogo-dl-asyncapi
```

The generated HTML is a local documentation artifact; the Go server does not
serve it. Update `docs/asyncapi.yaml` whenever the WebSocket contract changes.

```
GET /api/v1/ws?token=<access_token>
```

Clients may send only `join` and `leave` commands. `join` requires current room
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
```

Server-only `message_updated` and `message_deleted` events use this same envelope
with a complete Message payload, including numeric `room_id`, `revision`,
`edited_at` and `deleted_at`. The envelope `room_id` remains a string. New
`message` events also include those additive fields. Lifecycle events are emitted
after the database commit; retries/no-ops emit no additional event. Delivery is
best effort. For a known message, apply only greater revisions so a late edit
cannot overwrite a tombstone. Refresh relevant history after reconnect or a gap;
event arrival order is not guaranteed. Clients may send only `join` and `leave`.

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

Unit and WebSocket tests run without external services. PostgreSQL integration tests are enabled only when TEST_DATABASE_URL points to an isolated disposable database; they never load configs/.env.

    TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/gogo_dl_test?sslmode=disable go test -race -count=1 ./...

The integration suite covers clean migration, upgrade from migration 000001, rollback, transaction integrity, deleted-author history, and typed constraint mapping.

Phase 6 integration tests additionally cover upgrade from schema 000005,
lifecycle constraints, tombstone pagination/audit, write rollback, concurrent
edits/deletes, and membership locking. They require a disposable database whose
name ends in `_test` and reset its data/schema during cleanup.

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

- WebSocket clients may send only `join` and `leave` commands. Room joins are checked against PostgreSQL membership before the Hub subscribes the connection.
- Client-originated message and lifecycle events are rejected; durable messages are persisted through the chat service before broadcast.
- Membership revocation removes active room subscriptions after the membership transaction commits and the Hub processes the control event.
- If an account is deleted, authored message history is retained with a nullable `user_id` and the username `[deleted user]`.
- WebSocket upgrades require an exact configured Origin and are subject to global/per-user connection admission and per-connection frame limits.
- Invalid JSON/request semantics return stable `invalid request` errors; oversized HTTP bodies return `request body too large`, and exhausted limiters return `rate limit exceeded` with `Retry-After`.
