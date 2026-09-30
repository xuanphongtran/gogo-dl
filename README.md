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
| GET | `/api/v1/rooms/:id/presence` | ✓ | Room presence and typing snapshot |
| GET | `/api/v1/rooms/:id/read-state` | ✓ | My read cursor and unread count |
| PUT | `/api/v1/rooms/:id/read-state` | ✓ | Advance my read cursor |

Invitation actions use `GET /api/v1/users/me/invitations`, `POST /api/v1/invitations/:id/accept`, and `POST /api/v1/invitations/:id/decline`. Public rooms can be discovered and joined by authenticated users; private rooms require an accepted invitation. Message history and WebSocket subscriptions require current membership.

Leaving a public room returns `204` even when the caller is no longer a member,
so retries are safe. A private room without membership, or a missing room,
returns `404`. Owners must transfer ownership before leaving (`409`).

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
deleted-author messages. Future message tombstones count by ID; edits/deletes
do not create another unread item. Reading history does not advance the cursor.
Leaving/removal clears it, and rejoining starts at `0`.

Changed cursors are persisted before private `read_state` events are delivered
to your connections. Other members do not receive read receipts. Events are
best effort; after reconnect, reload GET for an authoritative count. Keep the
maximum cursor when processing out-of-order events; old counts may be stale.

Phase 7 carries the exact migration `000006` from Phase 6 as a schema prerequisite
followed by `000007` for read cursors. Lifecycle endpoints remain on the Phase 6
branch. Apply migrations in order: deploying `000007` without `000006` prevents
the migration runner from discovering `000006` on a later merge.

### WebSocket

```
GET /api/v1/ws?token=<access_token>
```

Once connected, send/receive JSON envelopes:

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

Unit and WebSocket tests run without external services. PostgreSQL integration tests are enabled only when TEST_DATABASE_URL points to an isolated disposable database; they never load configs/.env.

    TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/gogo_dl_test?sslmode=disable go test -race -count=1 ./...

The integration suite covers clean migration, upgrade from migration 000001, rollback, transaction integrity, deleted-author history, and typed constraint mapping.
Phase 7 tests additionally cover upgrade from schema 5, concurrent read advances,
membership locking, unread counts and leave/rejoin cursor cleanup. Use a fresh
disposable database whose name ends in `_test`; the Phase 7 fixture refuses an
existing application schema. Its cleanup drops only that test database's schema.

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
