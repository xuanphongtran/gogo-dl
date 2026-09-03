# ConnectRPC Guide

Tài liệu này giải thích chi tiết cách **ConnectRPC** được tích hợp vào gogo-dl — tại sao chọn nó, kiến trúc thực hiện, và cách dùng / extend.

---

## 1. Tại sao ConnectRPC?

Bạn hỏi: *"viết code 1 lần nhưng vừa có REST vừa có gRPC được không?"*

**ConnectRPC = câu trả lời chính thức hiện đại (sau 10 năm gRPC)**.

Connect (connectrpc.com) được tạo bởi **Buf Build** — team Google cũ đã xây dựng hầu hết protobuf ecosystem hiện nay. Nó **không phải fork của gRPC**, mà implement lại protocol stack với các nguyên tắc:

| Protocol | Port | Client examples | Khi nào dùng |
|----------|------|-----------------|---------------|
| **gRPC (binary)** | CÙNG 1 port (h2) | Go, Java, C++, Rust (native SDKs) | Internal services, high-throughput |
| **gRPC-Web** | CÙNG 1 port (h2/h1) | Browser via `@bufbuild/connect-web` | Web clients (không cần Envoy proxy như gRPC thường) |
| **Connect (JSON)** | CÙNG 1 port (h1/h2) | **curl, Postman, fetch()** vì nó chỉ là `POST /<service>/<method>` body = JSON | Debug, mobile, REST-style 1 yêu cầu = 1 JSON |

**Điểm mấu chốt cho gogo-dl:**

> **1 lần implement ConnectHandler trong Go → phục vụ đồng thời 3 giao thức (gRPC, gRPC-Web, Connect-JSON) trên CÙNG 1 cổng, CÙNG 1 process, không cần reverse proxy.**

So sánh với các hướng khác (đã nêu trong cuộc họp):

| Hướng | Viết handler 1 lần? | Có gRPC thật? | Có REST tự nhiên? | Dễ áp dụng vào dự án này? |
|-------|--------------------|---------------|------------------|--------------------------|
| **ConnectRPC ✅** | ✅ Đúng | ✅ gRPC binary HTTP/2 | ✅ POST JSON = curl được | ⭐ Tối ưu — 1 cổng, 3 protocol |
| grpc-gateway | ✅ Đúng | ✅ Có (chạy 2 server/process) | ✅ Đúng hơn (GET/PATCH path params) | Khó setup (protoc plugin phức tạp) |
| Thin adapter per transport | ~80% (adapter 5-10 dòng) | ✅ Có | ✅ 100% (Gin native) | Viết 2 handler thay vì 1 |

---

## 2. Kiến trúc tổng quan

```
                    ┌─────────────────────────────────────────────────────┐
                    │           connectserver.New(...) - 1 Port            │
                    │                                                      │
Client (gRPC) ───►  │  protocol sniffing (h2 binary)                      │
Client (Web)  ───►  │    ↓                                                │
Client (curl) ───►  │  Connect runtime: 1 handler = 3 transports          │
                    │    ↓                                                │
                    │  UnaryInterceptors chain:                           │
                    │    1. Recover     (panic → 500)                     │
                    │    2. Logger      (zerolog per RPC)                 │
                    │    3. Auth        (JWT via Authorization header)   │
                    │    ↓                                                │
                    │  ┌─────────────────────┐  ┌──────────────────────┐  │
                    │  │ user.ConnectHandler │  │ chat.ConnectHandler │  │
                    │  │  Register / Login   │  │  CreateRoom /        │  │
                    │  │  GetMe / UpdateMe   │  │  StreamMessages      │  │
                    │  └──────────┬──────────┘  └──────────┬───────────┘  │
                    │             │                          │              │
                    └─────────────┼──────────────────────────┼──────────────┘
                                  │                          │
                            ┌─────▼──────┐           ┌──────▼──────┐
                            │ user.Service│           │ chat.Service │
                            └─────┬──────┘           └──────┬──────┘
                                  │                          │
                            ┌─────▼──────┐           ┌──────▼──────┐  ┌────────┐
                            │user.Postgres│           │chat.Postgres │  │ ws.Hub │
                            │  Repository │           │  Repository  │  │ (fanOut│
                            └────────────┘           └──────┬──────┘  │  →subs)│
                                                            │         └───┬────┘
                                                       ┌────▼────┐       │
                                                       │PostgreSQL│◄──────┘
                                                       └─────────┘
```

**Lưu ý về sự khác biệt vs Gin cũ:**

| Thành phần | Trước (Gin) | Sau (ConnectRPC) |
|------------|-------------|------------------|
| Entry | `httpserver.New(...)` → Gin engine | `connectserver.New(...)` → stdlib `http.Server` + h2c |
| Transport middleware | `gin.HandlerFunc` (cors, auth, logger, recover) | `connect.UnaryInterceptorFunc` chain, **1 lần cho 3 protocol** |
| Handler per domain | `user/handler.go` + `chat/handler.go` (Gin Context binding) | `user/connect_handler.go` + `chat/connect_handler.go` (proto Request → connect.Request[Msg]) |
| Error response | `apperror.Respond(c, err)` → Gin JSON | `return nil, apperror.ToConnect(err)` → auto encode status code và message theo protocol (gRPC status / Connect error / HTTP status) |
| Auth userID in context | `c.Set(ContextKeyUserID, ...)` rồi `middleware.MustGetUserID(c)` | `context.WithValue(ctx, key, ...)` rồi `middleware.MustGetUserIDConnect(ctx)` |
| Realtime | `/ws` upgrade → `gorilla/websocket` → `ws.Hub` | 2 options: (a) giữ lại legacy WS `/ws`, (b) dùng `StreamMessages` server-stream RPC via Connect streaming → `ws.Hub` (đã làm!) |

---

## 3. Cấu trúc files mới / thay đổi

Files **mới** (ConnectRPC-only):

```
gogo-dl/
├── api/v1/                              # 📦 Source of Truth = Protobuf API contract
│   ├── user.proto                       #     UserService + message types
│   └── chat.proto                       #     ChatService + streaming RPC
│
├── buf.yaml                             # Buf lint/breaking config (chọn dùng buf thay protoc)
├── buf.gen.yaml                         # Buf code-gen config (hoặc dùng make proto-gen)
│
├── gen/api/v1/                          # ⚙️ Generated (từ protoc) — NEVER EDIT
│   ├── user.pb.go                       #     Protobuf message types (apiv1 package)
│   ├── chat.pb.go
│   └── apiv1connect/
│       ├── user.connect.go              #     UserServiceHandler interface + NewUserServiceHandler
│       └── chat.connect.go              #     ChatServiceHandler (bao gồm cả streaming)
│
├── internal/
│   ├── connectserver/
│   │   └── server.go                    # stdlib mux + h2c handler, grpcreflect, health
│   │
│   ├── middleware/
│   │   └── connect_interceptors.go      # Recover / Logger / Auth (Unary interceptors)
│   │
│   ├── user/
│   │   └── connect_handler.go           # IMPLEMENTS apiv1connect.UserServiceHandler
│   │
│   ├── chat/
│   │   └── connect_handler.go           # IMPLEMENTS apiv1connect.ChatServiceHandler
│   │                                    #   + StreamMessages (server streaming)
│   │
│   └── ws/
│       └── stream_subs.go               # Stream subscriber registry (Connect StreamMessages ↔ Hub.fanOut)
│
└── pkg/apperror/
    └── connect.go                       # AppError → *connect.Error (HTTP code ↔ connect.Code)
```

Files **đã sửa / mở rộng**:

```
├── cmd/server/main.go                   # Thay httpserver.New → connectserver.New
├── Makefile                             # Thêm targets: proto-tools, proto-gen, proto-gen-buf, proto-clean, buf-lint
├── internal/ws/hub.go                   # fanOut() → thêm dispatchStreamSubs() cho Connect streaming
└── configs/.env.example                 # Thêm ghi chú ConnectRPC
```

---

## 4. Workflow: Thêm endpoint mới

### Bước 1 — Sửa `.proto` (source of truth)

Ví dụ: thêm `GetUserByID` vào `user.proto`:

```protobuf
// api/v1/user.proto
service UserService {
  // ... existing ...
  rpc GetUserByID(GetUserByIDRequest) returns (ProfileResponse);
}

message GetUserByIDRequest {
  int64 id = 1;
}
```

### Bước 2 — Generate code

```bash
# 1 lần cài plugins (nếu chưa)
make proto-tools

# Generate lại gen/api/v1/*.pb.go + apiv1connect/*.connect.go
make proto-gen
```

Sau khi chạy:
- `user.pb.go` thêm struct `GetUserByIDRequest`
- `user.connect.go` thêm method `GetUserByID` vào interface `UserServiceHandler`
  → compiler sẽ báo lỗi vì `user/connect_handler.go` chưa implement

### Bước 3 — Implement business logic trong Service

Ví dụ thêm vào `user/service.go` (nếu chưa có):

```go
func (s *Service) GetPublicProfile(ctx context.Context, id int64) (*ProfileResponse, error) {
    u, err := s.repo.GetByID(ctx, id)
    if err != nil { return nil, err }
    return u.ToProfile(), nil
}
```

### Bước 4 — Implement trong ConnectHandler (1 dòng gọi service!)

```go
// internal/user/connect_handler.go
func (h *ConnectHandler) GetUserByID(
    ctx context.Context,
    req *connect.Request[apiv1.GetUserByIDRequest],
) (*connect.Response[apiv1.ProfileResponse], error) {
    profile, err := h.svc.GetPublicProfile(ctx, req.Msg.Id)
    if err != nil {
        return nil, apperror.ToConnect(err) // ← CHUYỂN ĐỔI LỖI 1 CÂU
    }
    return connect.NewResponse(toProtoProfile(profile)), nil
}
```

✅ **Xong!** Endpoint này có thể gọi qua:

```bash
# 1. Connect JSON (curl được luôn như REST)
curl -X POST http://localhost:8080/api.v1.UserService/GetUserByID \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"id": 42}'

# 2. gRPC binary (buf CLI hoặc Go/Java SDK)
buf curl --protocol grpc \
  http://localhost:8080 \
  --data '{"id": 42}' \
  --schema api/v1/user.proto \
  api.v1.UserService/GetUserByID

# 3. gRPC-Web từ browser
# const client = createPromiseClient(UserService, transport);
# await client.getUserByID({ id: 42n }, { headers: { Authorization: "Bearer ..." } });
```

---

## 5. Workflow: Migration path (Gin → Connect)

Code Gin cũ KHÔNG bị xóa. Bạn có thể chạy **dual-stack** nếu cần backward compatible với client cũ.

### Tùy chọn A: Chạy ConnectRPC-only (MẶC ĐỊNH HIỆN TẠI)

Đây là trạng thái `main.go` hiện tại. Tất cả traffic đi qua Connect, các legacy Gin handler / middleware vẫn compile nhưng không được wire.

### Tùy chọn B: Dual-stack (Gin + Connect trên 2 port khác nhau)

Mở comment trong `cmd/server/main.go`:

```go
// Uncomment lines in main.go:
userGinH := user.NewHandler(userSvc)
chatGinH := chat.NewHandler(chatSvc, hub)
ginSrv   := httpserver.New(cfg, userGinH, chatGinH)
ginErr   := make(chan error, 1)
go func() { ginErr <- ginSrv.Start() }()
// ... và trong shutdown block cũng gọi ginSrv.Shutdown(...)
```

Cập nhật `.env` có HTTP_SERVER_PORT=8080 (Gin) / SERVER_PORT=8081 (Connect).

---

## 6. Auth & Interceptors

Gin có `middleware/*.go` (5 files). ConnectRPC có **`connect_interceptors.go`** — 1 file cover 100% các cross-cutting concerns, **1 lần áp dụng cho 3 protocol**.

Thứ tự chạy chain (đăng ký trong `connectserver.New`):

```
1. ConnectRecoverInterceptor  →  catch panic, log, return CodeInternal
     ↓
2. ConnectLoggerInterceptor   →  log procedure / protocol / peer / latency / code
     ↓
3. ConnectAuthInterceptor     →  check allowlist (public procedures), parse JWT, set userID via context
     ↓
                     user / chat ConnectHandler
```

Để đọc userID trong handler:
```go
userID := middleware.MustGetUserIDConnect(ctx)  // panics nếu interceptor auth bị bỏ sót route
```

---

## 7. Streaming (Realtime thay thế WebSocket)

ConnectRPC hỗ trợ 4 loại streaming: Unary, Client-stream, Server-stream, Bidi-stream. Chúng ta đã implement **Server streaming** cho `ChatService.StreamMessages`.

Flow:
1. Client gọi gRPC / Connect stream: `StreamMessages(room_id=5)`
2. Connect `chat.ConnectHandler.StreamMessages`:
   - Verify auth (từ interceptor context)
   - Verify membership via `svc.JoinRoom` (idempotent)
   - Register callback với `ws.RegisterStreamSubscriber(clientID, roomID, cb)`
   - Callback convert `ws.Message` payload → `*apiv1.Message` → gửi vào `chan *apiv1.Message`
3. Khi `chat.Service.SendMessage` gọi `hub.Broadcast` → `fanOut` gọi:
   - `client.sendJSON()` (legacy WS clients, cũ)
   - **`dispatchStreamSubs()` (MỚI) → tất cả Connect streaming subscribers**
4. Defer cleanup: `ws.UnregisterStreamSubscriber` + close channel khi client disconnect / ctx cancel

**So sánh WS cũ:**

| Đặc điểm | Legacy `/ws` (gorilla/websocket) | Connect `StreamMessages` RPC |
|----------|-----------------------------------|-------------------------------|
| Protocol | WS (RFC 6455) trên HTTP/1.1 | HTTP/2 server stream (gRPC / Connect) |
| Auth | `?token=` query param | `Authorization: Bearer` header ( chuẩn ) |
| Envelope | JSON `{"type":"message", "payload":...}` | Strongly-typed proto `Message { id, room_id, content... }` |
| Room mgmt | Client tự gửi `{"type":"join", ...}` | 1 RPC call per room (explicit stream = subscribe) |
| Client SDK | Custom | Generated protobuf clients (type-safe) |
| Shared state | ws.Hub maps | **ws.Hub CÙNG instance** + stream_sub registry |

---

## 8. Proto toolchain options

Có 2 cách generate code, đều được support:

### Cách 1 — Make + protoc (DEFAULT, không cài thêm gì)
```bash
make proto-tools    # cài protoc-gen-go & protoc-gen-connect-go vào ./bin/
make proto-gen      # protoc generate code
make proto-clean    # rm -rf gen/
```

### Cách 2 — Buf CLI (nếu thích Buf ecosystem)
```bash
# Install buf: https://buf.build (1 binary)
make proto-gen-buf       # buf generate (dùng buf.gen.yaml)
make buf-lint            # lint proto rules
make buf-breaking        # check breaking changes so vs main branch
```

Khi team lớn, recommend Buf vì có **BSR (Buf Schema Registry)** — push/pin version proto thay vì copy repo.

---

## 9. Testing with Connect

Connect package export `connect.NewServer` / `httptest` — extremely testable.

Ví dụ test `UserService.Login` **không cần port thật**:

```go
func TestLoginConnect(t *testing.T) {
    // Setup in-memory
    svc := user.NewService(mockUserRepo, testCfg)
    h := user.NewConnectHandler(svc)
    path, handler := apiv1connect.NewUserServiceHandler(h)
    server := httptest.NewUnstartedServer(handler)
    server.EnableHTTP2 = true
    server.StartTLS()
    defer server.Close()

    client := apiv1connect.NewUserServiceClient(
        server.Client(),
        server.URL,
    )

    // Act
    res, err := client.Login(context.Background(), connect.NewRequest(&apiv1.LoginRequest{
        Email: "alice@example.com", Password: "correct-horse",
    }))

    // Assert
    require.NoError(t, err)
    require.NotEmpty(t, res.Msg.AccessToken)
}
```

→ **1 test = cover cả gRPC + Connect JSON path** vì dùng chung implementation.

---

## 10. Production checklist

Trước khi deploy ConnectRPC lên production:

1. **TLS**: Hiện tại dùng `h2c` (HTTP/2 cleartext) cho dev/staging. Production nên terminate TLS ở reverse proxy (Caddy, Nginx, Cloudflare) hoặc pass `ListenAndServeTLS()` vào `connectserver`.
2. **CORS cho gRPC-Web / Connect JSON**: Với browser clients từ origin khác, cần set CORS headers. Vì Connect handlers chạy trên stdlib `http.ServeMux`, wrap 1 CORS handler (ví dụ `rs/cors` hoặc gin-contrib/cors style).
   → Nếu cần CORS: Thêm middleware vào `connectserver.New` trước khi pass vào h2c.
3. **Rate limiting / Circuit breaker**: Thêm interceptor tương tự như Logger/Auth.
4. **Observability**: connectrpc có `otelconnect` interceptor, `promconnect` metrics out-of-the-box → chỉ cần import và add vào chain.
5. **gRPC Reflection**: Đã bật `grpcreflect.NewStaticReflector` trong connectserver → Postman / `grpcurl` / `buf curl` auto-discover schema.
6. **Deadlines / Timeouts**: Clients nên set deadline. Interceptor có thể check context deadline và return `CodeDeadlineExceeded`.
7. **Request Validation**: Thêm `protovalidate` (Buf) + interceptor để auto-validate proto messages (thay vì Gin binding tags).

---

## 11. Mapping: Gin REST ↔ Connect Procedure

| Endpoint REST cũ (Gin) | Connect Procedure (3 protocols) |
|------------------------|---------------------------------|
| POST `/api/v1/auth/register` | `/api.v1.UserService/Register` |
| POST `/api/v1/auth/login` | `/api.v1.UserService/Login` |
| POST `/api/v1/auth/refresh` | `/api.v1.UserService/RefreshTokens` |
| GET `/api/v1/users/me` | `/api.v1.UserService/GetMe` |
| PATCH `/api/v1/users/me` | `/api.v1.UserService/UpdateMe` |
| DELETE `/api/v1/users/me` | `/api.v1.UserService/DeleteMe` |
| GET `/api/v1/rooms` | `/api.v1.ChatService/ListRooms` |
| POST `/api/v1/rooms` | `/api.v1.ChatService/CreateRoom` |
| GET `/api/v1/rooms/:id` | `/api.v1.ChatService/GetRoom` |
| POST `/api/v1/rooms/:id/join` | `/api.v1.ChatService/JoinRoom` |
| GET `/api/v1/rooms/:id/messages?limit=&before=` | `/api.v1.ChatService/ListMessages` |
| POST `/api/v1/rooms/:id/messages` | `/api.v1.ChatService/SendMessage` |
| WS `GET /api/v1/ws` → client joins room | **Stream**: `/api.v1.ChatService/StreamMessages` (1 room per stream) |

---

## 12. Quickstart: Test server với curl + buf

(Bỏ qua bước DB chạy — chỉ test reflection + health)

```bash
# Health (giống Gin cũ)
curl http://localhost:8080/health
# → {"status":"ok","time":"..."}

# Register với Connect JSON (như REST POST)
curl -X POST http://localhost:8080/api.v1.UserService/Register \
  -H "Content-Type: application/json" \
  -d '{"username":"bob","email":"bob@example.com","password":"12345678"}'
# → {"accessToken":"...","refreshToken":"...","expiresAt":...}
```

Với `buf curl` (gRPC binary):
```bash
echo '{"email":"bob@example.com","password":"12345678"}' | \
buf curl --protocol grpc --data @- \
  http://localhost:8080 \
  api.v1.UserService/Login
```

---

## 13. References

- ConnectRPC chính thức: https://connectrpc.com/docs/go/getting-started
- Generated handler interface pattern: https://connectrpc.com/docs/go/interceptors
- Streaming server: https://connectrpc.com/docs/go/streaming#server-streaming
- Buf + Protobuf style guide: https://buf.build/docs/best-practices/style-guide
- Migration guide từ gRPC-Go → Connect: https://connectrpc.com/docs/go/migrating-from-grpc
