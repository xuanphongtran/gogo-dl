# Phase 04 — API Hardening Execution Plan

**Status:** Done
**Spec:** [spec/04-api-hardening.md](../spec/04-api-hardening.md)
**Parent plan:** [04 — API and Abuse Protection](./04-api-hardening.md)

## Objective

Implement the Phase 04 security and abuse controls as independently reviewable
vertical slices. Preserve existing successful HTTP/WebSocket contracts while
making limits, origin policy, validation, and observability explicit.

## Dependency gate

Start only after plans 01–03 remain `Done` and these commands pass with an
isolated PostgreSQL database:

```text
go test -race -count=1 ./...
go vet ./...
make build
```

The phase is complete only when the new tests pass with the same commands and
the configuration/documentation contract is synchronized.

## Workstream 0 — Freeze compatibility and baseline

1. Record current REST status codes and JSON fields for auth, profile, rooms,
   messages, health, and WebSocket join/leave.
2. Record the current `CORS_ALLOWED_ORIGINS`, HTTP timeout, and WebSocket
   message-size behavior.
3. Add regression tests for unchanged successful responses before tightening
   policy.

Expected areas: `README.md`, existing handler tests, `internal/httpserver`.

## Workstream 1 — Configuration and policy primitives

1. Add the Phase 04 configuration fields and safe defaults to
   `internal/config.Config`.
2. Parse positive integers, rates, byte sizes, and origin lists with explicit
   startup errors; reject permissive production combinations.
3. Add `.env.example` entries and README documentation without committing
   `configs/.env`.
4. Implement small consumer-owned helpers for origin matching, request IDs,
   and stable limit errors.

Expected areas: `internal/config`, `internal/middleware`, `pkg/apperror`,
`configs/.env.example`, `README.md`.

Exit gate: table-driven config tests cover defaults, malformed values,
production safety, and origin parsing.

## Workstream 2 — WebSocket origin and connection admission

1. Replace `CheckOrigin: true` with exact normalized origin matching.
2. Define explicit behavior for missing `Origin`; do not use CORS middleware as
   the authorization decision.
3. Add Hub-owned admission requests for global and per-user connection counts.
4. Reserve capacity before upgrade, release on failed upgrade, and release on
   unregister/shutdown exactly once.
5. Keep all Hub client/room state mutation inside `Hub.Run`; admission must not
   read Hub maps from handlers or pumps.

Expected areas: `internal/ws`, `internal/chat/handler.go`, `internal/config`.

Exit gate: allowed/disallowed/missing-origin tests, global/per-user limit
tests, failed-upgrade release tests, and disconnect/shutdown race tests pass.

## Workstream 3 — HTTP body/header and WebSocket frame limits

1. Apply `http.MaxBytesReader` before Gin JSON binding on API routes.
2. Map body overflow to stable HTTP 413 without returning parser internals.
3. Wire `ReadHeaderTimeout`, `MaxHeaderBytes`, and existing server timeouts
   from safe constants/configuration.
4. Pass configured WebSocket message size into each Client.
5. Ensure read-limit errors produce `payload_too_large` before normal close
   ownership handles the connection.

Expected areas: `internal/httpserver`, `internal/middleware`, `internal/ws`,
`pkg/apperror`.

Exit gate: over-limit HTTP/WS tests prove bounded reads, stable responses, no
Hub blocking, and no goroutine leaks.

## Workstream 4 — Rate limiting

1. Implement a standard-library token bucket with an injectable clock and
   bounded key store.
2. Add separate middleware/configuration for auth, authenticated writes, and
   WebSocket upgrades.
3. Use peer address as the pre-auth key and authenticated user ID after Auth;
   do not trust forwarded headers by default.
4. Return HTTP 429 and integer `Retry-After` without logging credentials.
5. Make rejected and expired buckets observable through bounded structured
   fields, not unbounded attacker-controlled labels.

Expected areas: new `internal/middleware/ratelimit.go`, tests, route wiring in
`internal/httpserver`, and WebSocket upgrade handling.

Exit gate: deterministic burst/refill/eviction tests and route-class key tests
pass without sleeps or global state.

## Workstream 5 — Domain normalization and validation

1. Add shared semantic validators at the user/chat service boundary.
2. Normalize username, email, room name, message content, and avatar URL as
   defined by the spec before persistence or expensive bcrypt work.
3. Add the email canonicalization migration only after a duplicate preflight;
   do not edit historical migrations.
4. Keep handlers responsible for shape binding and services responsible for
   semantic validation and authorization.
5. Map validation failures to stable safe errors while preserving existing
   successful response fields and statuses.

Expected areas: `internal/user`, `internal/chat`, `migrations`,
`pkg/apperror`, README API notes.

Exit gate: table-driven boundary tests cover whitespace, Unicode length,
control characters, byte limits, duplicate canonical email, and service/HTTP
parity.

## Workstream 6 — Correlation IDs, redaction, and timeout review

1. Generate or validate `X-Request-ID` and return it on every HTTP response.
2. Add `request_id`, route, status, and duration to structured request logs.
3. Redact authorization headers, token query parameters, passwords, refresh
   tokens, and full DSNs on all modified paths.
4. Review request header/body/read/write/idle timeouts and document the
   slow-client behavior.
5. Add tests that capture logs or inspect structured fields without printing
   real credentials.

Expected areas: `internal/middleware/logger.go`, `internal/httpserver`,
`internal/config`, tests, README operations notes.

Exit gate: log/redaction and timeout tests pass and no sensitive value appears
in test output or source changes.

## Workstream 7 — Verification and rollout

1. Run focused middleware, domain, HTTP, and WebSocket tests after each slice.
2. Run PostgreSQL migration upgrade/rollback tests for the email constraint if
   the migration is included.
3. Run `gofmt`, `go vet ./...`, `go test -race -count=1 ./...`, `make build`,
   and `git diff --check`.
4. Update `README.md`, `.env.example`, API/error documentation, and this plan
   with verified defaults and operational limitations.
5. Deploy behind a known proxy configuration and monitor 403/413/429 rates
   before increasing limits.

## Verification evidence

- `TEST_DATABASE_URL=postgres://... go test -race -count=1 ./...` passed against the disposable PostgreSQL instance.
- `go vet ./...`, `go build -o /tmp/gogo-dl-phase04 ./cmd/server`, and `git diff --check` passed.
- `golangci-lint` was not installed in the environment, so `make lint` was not run.

## Rollback and recovery

- Configuration rollout can be reverted to the previous values, but production
  must never revert to `CheckOrigin: true`.
- A database migration must have a tested down path and a duplicate-data
  preflight; if the preflight fails, stop without changing the schema.
- Rate-limit state is process-local and can be discarded on restart. Do not
  claim distributed enforcement until plan 09.
- If a limit causes false positives, adjust the configured value and preserve
  the protection; do not remove the boundary or log sensitive request data.

## Definition of done

- All Phase 04 acceptance criteria and spec tests pass.
- Existing success contracts remain compatible and are covered by regression
  tests.
- Limits and origin policy are documented, configurable, and safe by default.
- No secrets, tokens, personal databases, or `.env` files are added.
- The roadmap status moves from `Ready` to `In progress` only when coding
  starts, and to `Done` only after all verification evidence is recorded.
