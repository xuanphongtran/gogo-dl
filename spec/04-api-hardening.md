# 04 — API and Abuse Protection Specification

**Related plan:** [plan/04-api-hardening.md](../plan/04-api-hardening.md)
**Execution plan:** [plan/04-api-hardening-execution.md](../plan/04-api-hardening-execution.md)
**Status:** Ready
**Priority:** P0
**Depends on:** 01, 02, 03

## Objective

Make HTTP and WebSocket trust boundaries safe for internet-facing deployment
without changing successful REST paths or the existing join/leave WebSocket
protocol. The phase must bound resource consumption, reject unsafe origins and
malformed input, and make abuse visible without logging credentials.

## Current gaps

- `websocket.Upgrader.CheckOrigin` currently accepts every origin.
- WebSocket read size is a code constant rather than an application setting.
- HTTP body/header limits, connection admission, and request rate limits are
  not enforced at the application boundary.
- Gin binding validates shape, but domain services do not consistently
  normalize or validate semantic content.
- Request logs redact the WebSocket query token, but there is no correlation
  ID contract or explicit sensitive-field logging policy.

## Trust boundaries

- `Origin`, `Host`, `RemoteAddr`, query parameters, headers, and request
  bodies are client-controlled. They are inputs to policy checks, never proof
  of identity or authorization.
- `Authorization` is parsed only by the existing JWT middleware. Rate-limit
  keys may use the authenticated user ID only after that middleware succeeds.
- `X-Forwarded-For` and similar proxy headers are ignored unless a future
  trusted-proxy configuration explicitly enables them.
- The WebSocket origin check is independent from CORS. Passing CORS does not
  authorize a WebSocket upgrade.

## Configuration contract

All values below are loaded into `config.Config`, validated at startup, added
to `configs/.env.example`, and documented in `README.md`. Invalid non-empty
values fail startup; production must not silently fall back to permissive
security settings.

| Variable | Default | Meaning |
|---|---:|---|
| `WS_ALLOWED_ORIGINS` | `http://localhost:3000,http://localhost:5173` in development; required in production | Exact comma-separated WebSocket origin allowlist |
| `WS_ALLOW_MISSING_ORIGIN` | `false` in production, `true` in development | Whether non-browser clients without an `Origin` may upgrade |
| `HTTP_MAX_BODY_BYTES` | `1048576` | Maximum REST request body size, 1 MiB |
| `HTTP_MAX_HEADER_BYTES` | `16384` | Maximum HTTP request header size |
| `WS_MAX_MESSAGE_BYTES` | `4096` | Maximum client WebSocket frame/message size |
| `WS_MAX_CONNECTIONS` | `1000` | Maximum active WebSocket connections per process |
| `WS_MAX_CONNECTIONS_PER_USER` | `5` | Maximum active connections for one authenticated user |
| `AUTH_RATE_PER_MINUTE` | `10` | Auth requests per key per rolling token-bucket minute |
| `AUTH_RATE_BURST` | `5` | Immediate auth burst permitted per key |
| `WRITE_RATE_PER_MINUTE` | `120` | Authenticated mutation requests per user per minute |
| `WRITE_RATE_BURST` | `30` | Immediate mutation burst permitted per user |
| `WS_RATE_PER_MINUTE` | `20` | WebSocket upgrade attempts per key per minute |
| `WS_RATE_BURST` | `5` | Immediate WebSocket upgrade burst permitted per key |

Rate values must be positive. Byte and connection limits must be positive and
bounded by implementation-safe upper limits so a typo cannot allocate
unbounded memory.

## WebSocket origin policy

1. Parse the request `Origin` as a URL and compare its normalized
   `scheme://host[:port]` value against the exact allowlist.
2. Do not support `*` when credentials or authenticated WebSockets are in use.
3. Reject malformed, disallowed, or unexpected origins before calling
   `Upgrader.Upgrade`, with HTTP 403 and the generic error `origin forbidden`.
4. If `Origin` is absent, apply `WS_ALLOW_MISSING_ORIGIN`; the production
   default is deny.
5. Never log the complete request URL or query string during an origin failure.
   Log only a bounded, normalized origin value and request ID.

The existing CORS allowlist may share parsing helpers, but the WebSocket check
must remain an explicit `CheckOrigin` policy.

## Resource limits and timeouts

### HTTP

- Wrap REST request bodies with `http.MaxBytesReader` before JSON binding.
- Return HTTP 413 with the stable body `{ "error": "request body too large" }`
  when the limit is exceeded.
- Set `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, and
  `MaxHeaderBytes` explicitly on the HTTP server.
- Keep `/health` lightweight and exempt it from mutation rate limits.
- Do not use unbounded buffering or read a body once for validation and again
  for business logic.

### WebSocket

- Pass the configured message limit to every `Client`; do not retain a hidden
  production-only constant.
- A frame over the limit produces the stable protocol error code
  `payload_too_large` and then closes the connection through the normal owner
  path.
- Admission for global and per-user connection limits is decided before the
  connection becomes an active Hub client. Failed upgrades release any
  reservation.
- Hub-owned client and room maps remain owned by `Hub.Run`; the limit mechanism
  must not introduce unsynchronized map access.

## Rate limiting

Use a standard-library in-memory token bucket because the current deployment is
a single process. This is intentionally process-local; distributed limits are
deferred to plan 09.

| Class | Applies to | Key |
|---|---|---|
| Auth | register, login, refresh | normalized TCP peer address |
| Write | room creation/join and message/profile mutations | authenticated user ID; peer address before authentication |
| WebSocket | authenticated upgrade attempts | authenticated user ID plus peer address |

Required behavior:

- Return HTTP 429 with `Retry-After` in seconds and a stable safe error.
- Count rejected requests according to the class policy; never log tokens or
  passwords as part of a rejection.
- Bound the number of bucket keys and evict idle keys to prevent attacker-driven
  memory growth.
- Exempt health checks, CORS preflight, and WebSocket frames after upgrade;
  frame-level abuse is handled by message size and connection policy.
- Make limiter state injectable so unit tests do not depend on wall-clock
  sleeps or global state.

## Input normalization and validation

Shape validation remains in Gin; semantic validation remains in the service
layer so HTTP and future transports share the same rules.

| Input | Rules |
|---|---|
| Username | Trim surrounding Unicode whitespace; 3–50 Unicode code points; letters, numbers, `_`, `-`, and `.` only; preserve case for backward compatibility |
| Email | Trim surrounding whitespace; lowercase the canonical value before persistence and lookup; validate standard email shape |
| Room name | Trim surrounding whitespace; 1–100 Unicode code points; reject control characters |
| Message content | Trim surrounding whitespace; 1–4000 UTF-8 bytes; reject control characters other than newline, carriage return, and tab |
| Avatar URL | Preserve the current URL validation and reject control characters |

Email lowercasing changes uniqueness semantics. Before enabling it, the
database migration must detect case-insensitive duplicates and fail with an
operationally clear error. The migration then replaces the case-sensitive
email uniqueness constraint with a unique index on `lower(email)`. The down
migration restores the previous constraint only when that is safe.

Validation failures use stable client-safe errors and do not expose validator,
SQL, or driver details.

## Error and logging contract

- Validation: HTTP 400, `{"error":"invalid request"}`.
- Origin rejection: HTTP 403, `{"error":"origin forbidden"}`.
- Body limit: HTTP 413, `{"error":"request body too large"}`.
- Rate limit: HTTP 429, `{"error":"rate limit exceeded"}` plus `Retry-After`.
- WebSocket protocol errors retain the existing typed `error` envelope and
  stable codes.
- Every request receives a bounded `X-Request-ID` response header. If a client
  supplies one, accept it only after length/character validation; otherwise
  generate a cryptographically random ID.
- Structured logs include `request_id`, route, status, duration, and bounded
  policy fields. They must not include `Authorization`, passwords, refresh
  tokens, access tokens, raw query strings, or full DSNs.

## Required tests

### Configuration and middleware

- Valid and invalid limits, rates, origins, and production missing-origin
  settings.
- Exact origin allow, deny, malformed origin, and missing-origin behavior.
- HTTP body over-limit returns 413 before handler binding.
- Header/request timeout fields and maximum header size are wired.
- Request ID is generated/propagated and sensitive values are absent from logs.

### Rate and connection limits

- Token bucket allows the configured burst, rejects the next request, returns
  `Retry-After`, refills deterministically using an injected clock, and evicts
  idle keys.
- Auth, write, and WebSocket classes use their intended keys.
- Global and per-user WebSocket admission reject at the boundary and release
  reservations after failed upgrades and disconnects.

### Domain validation

- Boundary lengths, whitespace, control characters, Unicode counts, email
  canonicalization, duplicate canonical email, and message byte limits.
- HTTP and service paths produce the same semantic result.

### WebSocket and HTTP behavior

- Disallowed origins never reach the upgrade path.
- Oversized frames produce `payload_too_large` and terminate cleanly.
- Slow clients and repeated rejected requests do not block Hub or leak
  goroutines.
- Existing success status codes, JSON fields, pagination, and join/leave
  behavior remain compatible.

All concurrency tests run with `-race` and use finite channel-based deadlines.

## Out of scope

- External WAF/CDN configuration and distributed rate-limit storage.
- Spam classification, content moderation, CAPTCHA, and account reputation.
- Refresh-token rotation or session revocation.
- Presence, message lifecycle, attachments, or multi-instance Hub behavior.
