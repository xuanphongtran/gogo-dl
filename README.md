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
cp configs/.env.example configs/.env
# Edit configs/.env — set DB credentials and JWT secrets
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

### 4. Run migrations

```bash
make migrate-up
```

### 5. Run the server

```bash
make run          # with Air hot-reload if installed
# or
go run ./cmd/server/main.go
```

Server starts on `http://localhost:8080` by default.

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
| GET    | `/api/v1/rooms/:id/messages`    | ✓    | List messages (cursor pagination)|
| POST   | `/api/v1/rooms/:id/messages`    | ✓    | Send a message (+ WS broadcast)  |

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
```

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
