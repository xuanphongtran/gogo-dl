# gogo-dl

Backend service for a real-time chat application built with Go, **ConnectRPC**, and PostgreSQL.

> ✨ **ConnectRPC** powers the API: 1 implementation serves 3 protocols on the **same port**:
> - **gRPC (binary HTTP/2)** — high-performance internal services, native SDKs
> - **gRPC-Web** — browser clients (no Envoy proxy required)
> - **Connect (JSON over HTTP)** — curl / Postman friendly, works like REST
>
> Legacy Gin + WebSocket code is preserved for gradual migration (see `docs/CONNECTRPC.md`).

## Stack

| Layer      | Library / Tool                               |
|------------|-----------------------------------------------|
| Transport  | [ConnectRPC](https://connectrpc.com) + h2c    |
| HTTP (legacy) | [Gin](https://gin-gonic.com)                 |
| Schema     | Protocol Buffers (`api/v1/*.proto`)           |
| Realtime   | gorilla/websocket + Connect Server Streaming  |
| Database   | PostgreSQL + sqlx                             |
| Auth       | golang-jwt/jwt v5 (access + refresh tokens)   |
| Migrations | golang-migrate                                |
| Config     | godotenv                                      |
| Logging    | zerolog                                       |
| Code gen   | protoc + buf (optional)                       |

## Project structure

```
gogo-dl/
├── cmd/server/main.go          # entry point, DI wiring, graceful shutdown
├── api/v1/                     # Protobuf API contract (Source of Truth)
│   ├── user.proto              #   UserService: Register, Login, GetMe, ...
│   └── chat.proto              #   ChatService: CreateRoom, StreamMessages, ...
├── buf.yaml / buf.gen.yaml     # Buf lint + code-gen config (optional)
├── gen/api/v1/                 # Generated from proto — NEVER EDIT
│   ├── *.pb.go                 #   Protobuf Go message types
│   └── apiv1connect/*.connect.go  #   Connect handler + client interfaces
├── internal/
│   ├── config/                 # .env loading, Config struct
│   ├── database/               # sqlx connect, migration runner
│   ├── ws/                     # Hub + clients (shared by WS legacy & Connect streaming)
│   ├── middleware/             # JWT (jwt.go), Gin middleware + Connect interceptors
│   ├── connectserver/          # stdlib http.Server + h2c — replaces Gin
│   ├── httpserver/             # Gin engine (legacy, optional dual-stack)
│   ├── user/
│   │   ├── model.go            # DB entities + use-case DTOs
│   │   ├── repository.go       # Repository interface + Postgres impl
│   │   ├── service.go          # Business logic (transport-agnostic)
│   │   ├── handler.go          # Gin HTTP handler (legacy REST)
│   │   └── connect_handler.go  # Connect handler — 1 impl → 3 protocols
│   └── chat/
│       ├── model.go / repository.go / service.go
│       ├── handler.go          # Gin HTTP handler (legacy REST)
│       └── connect_handler.go  # Connect handler + StreamMessages RPC
├── pkg/apperror/               # Custom errors + Gin/Connect mappers
├── migrations/                 # SQL migration files (golang-migrate)
├── configs/.env.example        # Env template
├── docs/CONNECTRPC.md          # Full ConnectRPC guide (read this next!)
├── Makefile                    # build, run, proto-gen, docker helpers
└── README.md
```

## Prerequisites

- Go 1.25+
- PostgreSQL 14+
- [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)
  ```
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```
- `protoc` compiler (auto-installed locally by `make proto-tools` to `./bin/`)

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
make docker-up      # docker compose up postgres
```

### 3. Install dependencies + proto toolchain

```bash
make deps            # go mod download + verify
make proto-tools     # install protoc-gen-go / protoc-gen-connect-go locally (to ./bin/)
```

### 4. Generate proto code (skip if gen/ is up to date)

```bash
make proto-gen       # protoc (via ./bin/protoc + plugins) generate gen/api/v1/*
```

### 5. Run migrations

```bash
make migrate-up
```

### 6. Run the server

```bash
make run             # Air hot-reload if installed, else go run
# or
go run ./cmd/server/main.go
```

Server listens on `http://localhost:8080` by default — serving **gRPC, gRPC-Web and Connect-JSON** on the same port.

---

## Quick-Test ConnectRPC (no DB needed, just the listening server)

```bash
# Health check
curl http://localhost:8080/health
# → {"status":"ok","time":"..."}

# Register via Connect JSON (curl-able, REST-style, same impl used by gRPC)
curl -X POST http://localhost:8080/api.v1.UserService/Register \
  -H "Content-Type: application/json" \
  -d '{"username":"bob","email":"bob@example.com","password":"12345678"}'
# → { "accessToken": "...", "refreshToken": "...", "expiresAt": 1750000000 }
```

Full protocol documentation, migration guides, streaming examples → **[docs/CONNECTRPC.md](file:///home/hoang-vu/Source/gogo-dl/docs/CONNECTRPC.md)**.

---

## API Reference

Two transport stacks coexist: **ConnectRPC (primary)** and **Gin HTTP (legacy, optional dual-stack)**.
Both share the same `user.Service` / `chat.Service` — business logic written once.

### ConnectRPC — 1 Handler, 3 Protocols

Every procedure below is callable via **gRPC binary**, **gRPC-Web**, or **Connect JSON**.

| Procedure                                  | Auth | Returns       | Description                          |
|--------------------------------------------|------|---------------|--------------------------------------|
| `/api.v1.UserService/Register`             | ✗    | TokenPair     | Register new account                 |
| `/api.v1.UserService/Login`                | ✗    | TokenPair     | Login, get JWT pair                  |
| `/api.v1.UserService/RefreshTokens`        | ✗    | TokenPair     | Swap refresh token → new pair        |
| `/api.v1.UserService/GetMe`                | ✓    | ProfileResponse | Current user profile              |
| `/api.v1.UserService/UpdateMe`             | ✓    | ProfileResponse | Update profile fields             |
| `/api.v1.UserService/DeleteMe`             | ✓    | Empty         | Delete account                       |
| `/api.v1.ChatService/ListRooms`            | ✓    | ListRoomsResponse | List all rooms                  |
| `/api.v1.ChatService/CreateRoom`           | ✓    | Room          | Create new chat room                 |
| `/api.v1.ChatService/GetRoom`              | ✓    | Room          | Get room by ID                       |
| `/api.v1.ChatService/JoinRoom`             | ✓    | Empty         | Add caller to room members           |
| `/api.v1.ChatService/ListMessages`         | ✓    | ListMessagesResponse | Paginated message history      |
| `/api.v1.ChatService/SendMessage`          | ✓    | Message       | Persist + broadcast message          |
| `/api.v1.ChatService/StreamMessages`       | ✓    | **stream** Message | Server-stream realtime messages per room |

### Legacy Gin HTTP / WebSocket

(Can be enabled side-by-side with ConnectRPC — see dual-stack mode in `docs/CONNECTRPC.md`.)

| Method | Path                            | Auth | Description                     |
|--------|---------------------------------|------|---------------------------------|
| POST   | `/api/v1/auth/register`        | ✗    | Register new account           |
| POST   | `/api/v1/auth/login`           | ✗    | Login, get token pair          |
| POST   | `/api/v1/auth/refresh`         | ✗    | Refresh access token           |
| GET    | `/api/v1/users/me`             | ✓    | Get my profile                 |
| PATCH  | `/api/v1/users/me`             | ✓    | Update my profile              |
| DELETE | `/api/v1/users/me`             | ✓    | Delete my account              |
| GET    | `/api/v1/rooms`                | ✓    | List all rooms                 |
| POST   | `/api/v1/rooms`                | ✓    | Create a room                  |
| GET    | `/api/v1/rooms/:id`            | ✓    | Get room details               |
| POST   | `/api/v1/rooms/:id/join`       | ✓    | Join a room                    |
| GET    | `/api/v1/rooms/:id/messages`   | ✓    | List messages (cursor)         |
| POST   | `/api/v1/rooms/:id/messages`   | ✓    | Send message + WS broadcast    |
| WS     | `/api/v1/ws?token=<jwt>`       | ✓    | Legacy WebSocket hub           |

---

## Makefile commands

```
make run             # run server (hot-reload with Air if available)
make build           # compile production binary to ./bin/gogo-dl
make test            # go test -race ./...
make lint            # golangci-lint (if installed)
make tidy            # go mod tidy
make clean           # remove ./bin (build artifacts)

# ── Protobuf / ConnectRPC ───────────────────────────────────────
make proto-tools     # install protoc plugins locally (./bin/protoc-gen-go, connect-go)
make proto-gen       # regenerate gen/api/v1/*.go from api/v1/*.proto
make proto-clean     # remove generated gen/ directory
make proto-gen-buf   # alternative using buf.build (needs buf CLI)
make buf-lint        # Lint .proto files (buf CLI)
make buf-breaking    # Breaking-change check vs main branch (buf CLI)

# ── Migrations ──────────────────────────────────────────────────
make migrate-up      # apply all pending migrations
make migrate-down    # roll back all migrations
make migrate-create name=add_something   # create new up/down SQL pair

# ── Docker ──────────────────────────────────────────────────────
make docker-up       # start only postgres
make docker-up-all   # build image + postgres + app
make docker-down     # stop containers (keeps volumes)
make docker-down-v   # stop containers AND destroy volumes
make docker-logs     # tail compose logs
```

---

## Architecture notes

### Transport-agnostic Service layer (write once → run anywhere)

Business logic lives in `user.Service` and `chat.Service` — **100% transport-agnostic**.
The same Service instance is used by:
- Connect handlers (3 protocols: gRPC / gRPC-Web / Connect-JSON)
- Legacy Gin HTTP handlers (REST)
- Legacy WebSocket hub
- Connect server-streaming (`StreamMessages` RPC)

This is the **key win**: every new endpoint requires exactly **one ConnectHandler method (5-10 lines)** — no separate REST controller + gRPC controller + error mapping per transport.

### ConnectRPC server

`internal/connectserver` runs a standard-library `http.Server` wrapped with `h2c` (HTTP/2 cleartext).
This single listener auto-negotiates:
- HTTP/1.1 with Connect JSON (POST /service/method, JSON body)
- HTTP/2 with gRPC binary (`application/grpc` content-type)
- HTTP/1.1 or HTTP/2 with gRPC-Web (`application/grpc-web`)

The interceptor chain runs for **all 3 protocols**:
```
Recover → Logger → Auth → user/chat.ConnectHandler → Service → Repository → Postgres
```

### Realtime broadcast — Hub fan-out to WS + Connect streamers

The `ws.Hub` is shared by legacy WebSocket clients **and** ConnectRPC streaming subscribers.
When `chat.Service.SendMessage` calls `hub.Broadcast(roomID, wsMsg)`:

```
chat.Service.SendMessage
    ↓ (persist to Postgres — source of truth)
hub.Broadcast(roomID, wsMsg) ──► broadcast channel ──► hub.Run() goroutine
                                                                ↓
                                                          fanOut(roomID)
                                                                 ├─► legacy WS clients (WritePump → ws.Conn)
                                                                 └─► dispatchStreamSubs()
                                                                       └─► Connect StreamMessages subscribers
                                                                            (chan *apiv1.Message → stream.Send(msg))
```

If the broadcast channel is full, the message is still persisted (HTTP returns 201). Realtime delivery is "at most once per subscriber" with bounded buffers to prevent slow clients from backing up the hub.

### Token refresh

Clients should proactively refresh before the access token expires (`expires_at` is returned from login/register).
The `UserService.RefreshTokens` RPC validates the refresh token, confirms the user still exists, and issues a new token pair.

---

## Further reading

| Document                                                                                      | What it covers                                                       |
|-----------------------------------------------------------------------------------------------|----------------------------------------------------------------------|
| **[docs/CONNECTRPC.md](file:///home/hoang-vu/Source/gogo-dl/docs/CONNECTRPC.md)**            | **Start here** — complete ConnectRPC guide: why, architecture, step-by-step add endpoint, streaming, dual-stack, production checklist, proto tooling options |
| [cmd/server/main.go](file:///home/hoang-vu/Source/gogo-dl/cmd/server/main.go)                | Startup sequence, DI wiring, graceful shutdown                      |
| [internal/connectserver/server.go](file:///home/hoang-vu/Source/gogo-dl/internal/connectserver/server.go) | ConnectRPC server bootstrap + interceptor chain + grpcreflection |
| [internal/user/connect_handler.go](file:///home/hoang-vu/Source/gogo-dl/internal/user/connect_handler.go) | Example unary ConnectHandler (auth + CRUD profile) |
| [internal/chat/connect_handler.go](file:///home/hoang-vu/Source/gogo-dl/internal/chat/connect_handler.go) | Unary + server-streaming ConnectHandler with `StreamMessages` |
| [internal/middleware/connect_interceptors.go](file:///home/hoang-vu/Source/gogo-dl/internal/middleware/connect_interceptors.go) | Recover / Logger / Auth interceptors for Connect |
| [pkg/apperror/connect.go](file:///home/hoang-vu/Source/gogo-dl/pkg/apperror/connect.go)     | HTTP status code → Connect.Code mapping                             |
| [api/v1/user.proto](file:///home/hoang-vu/Source/gogo-dl/api/v1/user.proto)                  | Protobuf source for UserService API                                 |
| [api/v1/chat.proto](file:///home/hoang-vu/Source/gogo-dl/api/v1/chat.proto)                  | Protobuf source for ChatService (incl. `StreamMessages`)           |
