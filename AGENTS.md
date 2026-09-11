# AGENTS.md

## Role and objectives

Work in this repository as a senior Go engineer. Prioritize correctness, simplicity, operability, security, and testability. Make the smallest change that fully satisfies the request. Do not introduce broad refactors, public API changes, or new dependencies without a concrete need.

When a requirement is incomplete, inspect the existing code and documentation, make the safest reasonable assumption, and disclose that assumption in the handoff. Do not silently fix unrelated issues.

## Repository overview

`gogo-dl` is a real-time chat backend built with Go 1.23, Gin, PostgreSQL/sqlx, JWT, and gorilla/websocket.

- `cmd/server/main.go`: composition root, dependency wiring, startup, and graceful shutdown.
- `internal/httpserver`: Gin engine, middleware, and route registration.
- `internal/user`, `internal/chat`: domain packages following handler -> service -> repository.
- `internal/ws`: WebSocket hub, client pumps, and event envelopes.
- `internal/config`, `internal/database`, `internal/middleware`: shared internal infrastructure.
- `pkg/apperror`: application errors and HTTP response mapping.
- `migrations`: PostgreSQL migrations in golang-migrate format.

Preserve the current dependency direction: transport depends on services, services depend on repository interfaces, and repository implementations depend on PostgreSQL. Keep concrete dependency wiring in `cmd/server/main.go`. Do not add a dependency injection framework merely to reduce wiring code.

## Repository context

### Runtime lifecycle

The application runs as a single Go process:

1. Load `configs/.env` and environment variables.
2. Connect to PostgreSQL and verify connectivity.
3. Apply pending migrations from `migrations`.
4. Start `ws.Hub.Run` in its own goroutine.
5. Wire repositories, services, and handlers manually.
6. Start the HTTP server and wait for `SIGINT` or `SIGTERM`.
7. Shut down the HTTP server, WebSocket hub, and database resources gracefully.

The server depends on both PostgreSQL and migration files at runtime. When changing the Dockerfile, working directory, or startup flow, verify that `file://migrations` still resolves correctly.

### Request and data flow

```text
HTTP request
  -> Gin middleware: recovery, logging, CORS, and auth where required
  -> domain handler
  -> domain service
  -> repository/sqlx
  -> PostgreSQL
```

Real-time message delivery extends this flow after persistence succeeds:

```text
chat.Service.SendMessage
  -> PostgreSQL: source of truth
  -> ws.Hub.Broadcast: best-effort and non-blocking
  -> Hub.Run event loop
  -> Client.send
  -> WritePump
  -> WebSocket connection
```

HTTP and WebSocket are transports for the same domain, not independent sources of truth. Important mutations must pass through a service and be persisted before a corresponding real-time event is emitted.

### Important contracts

- REST endpoints live under `/api/v1`; the health endpoint is `/health`.
- Private routes accept `Authorization: Bearer <token>`; WebSocket connections currently also support `?token=`.
- Database identifiers are `int64`; WebSocket `room_id` values are strings. Convert and validate explicitly.
- Message history uses ID-based cursor pagination. Do not replace it with offset pagination unless the contract is intentionally changed.
- Broadcast delivery is best-effort and non-blocking. A database commit may succeed even if real-time delivery is dropped.
- The Hub event loop owns in-memory connection and room state. PostgreSQL owns durable state.

### Configuration and environments

- Commit only `configs/.env.example`; never commit `configs/.env`.
- The local server defaults to port `8080`; PostgreSQL defaults to `localhost:5432`.
- Docker Compose overrides `DB_HOST` with the `postgres` service name.
- The production image uses `scratch`. Do not assume a runtime shell, debugging utilities, or arbitrary files exist in the container.
- A new configuration value requires updates to `Config`, validation where appropriate, `.env.example`, container wiring, and README documentation.

## Working process

1. Read `README.md`, `Makefile`, and every package directly involved in the task.
2. Run `git status --short`. Existing changes belong to the user; do not overwrite or revert them.
3. Identify affected contracts: HTTP status and body, JSON fields, database schema, WebSocket events, authorization, and concurrency.
4. Add or update tests with behavioral changes. For a bug fix, prefer a regression test that reproduces the failure first.
5. Run formatting and checks proportional to the risk of the change.
6. Provide a concise handoff listing changed behavior, verification performed, and anything that could not be verified.

## Engineering rules

The terms MUST, MUST NOT, SHOULD, and MAY indicate requirement strength.

### MUST

- MUST keep changes within scope and preserve backward compatibility unless a breaking change is explicitly required.
- MUST propagate `context.Context` through request paths for all I/O and honor cancellation and deadlines.
- MUST validate input at trust boundaries and enforce resource authorization in the service layer.
- MUST persist a mutation before emitting an event that represents that mutation.
- MUST wrap infrastructure errors with operational context using `%w`, while returning safe messages to clients.
- MUST release owned resources, including rows, transactions, tickers, connections, goroutines, and channels.
- MUST use parameterized SQL and a new migration for schema changes.
- MUST update tests and documentation when API, configuration, schema, or WebSocket contracts change.
- MUST run `gofmt` on changed Go files and perform verification proportional to risk.

### MUST NOT

- MUST NOT log or commit passwords, JWTs, secrets, full DSNs, `.env` files, or sensitive user data.
- MUST NOT discard an error when it can leave partial state or violate an invariant.
- MUST NOT use `context.Background()` to bypass request cancellation.
- MUST NOT access Hub-owned maps outside the Hub event loop.
- MUST NOT perform blocking database or network I/O inside the Hub event loop.
- MUST NOT trust client-provided identity, room authorization, ownership, or server-generated event fields.
- MUST NOT edit a historical migration that may already have been deployed. Add a subsequent migration.
- MUST NOT run destructive database or volume operations without explicit user authorization.
- MUST NOT add abstractions, interfaces, packages, or dependencies for speculative future needs.

### SHOULD

- SHOULD keep functions focused and use early returns to reduce nesting.
- SHOULD use table-driven tests and small, typed dependency fakes.
- SHOULD use structured zerolog fields such as `user_id`, `room_id`, and `client_id`.
- SHOULD design operations to be retry-safe or idempotent when clients or infrastructure may retry.
- SHOULD prefer typed DTOs, events, and errors over `map[string]interface{}` once a payload contract is stable.
- SHOULD log or measure dropped best-effort events rather than silently discarding them.
- SHOULD write comments that explain rationale and invariants, not comments that repeat the code.

### MAY

- MAY perform a local refactor when it makes the requested change safer and remains easy to review.
- MAY add a shared helper when multiple real call sites exist or when it isolates an error-prone rule.
- MAY use PostgreSQL integration tests for queries, transactions, and migrations; unit tests remain the default for business logic.

## Standard commands

```bash
make deps             # download and verify Go modules
make build            # build ./bin/gogo-dl
make test             # go test -race -count=1 ./...
make lint             # golangci-lint run ./..., when installed
make tidy             # run only when dependencies or imports change
make docker-up        # start local PostgreSQL
make migrate-up       # apply pending migrations
```

Use focused tests during development:

```bash
go test ./internal/chat -run TestName -count=1
go test ./internal/ws -run TestName -race -count=1
```

Before completing a Go change, run at minimum:

```bash
gofmt -w <changed-go-files>
go test -race -count=1 ./...
go vet ./...
```

If a check requires PostgreSQL, Docker, or an unavailable tool, report the limitation. Never claim that a check passed when it was not run.

## Go conventions

- Follow standard Go idioms and `gofmt`. Use short lowercase package names. Exported identifiers require useful documentation comments.
- Accept `context.Context` as the first parameter for I/O operations. Pass `c.Request.Context()` from handlers through services to repositories.
- Handle every meaningful error. Wrap infrastructure errors with `%w` and operation context, for example `fmt.Errorf("chat repo ListMessages: %w", err)`.
- Use `errors.Is` and `errors.As` for classification. Do not compare error strings. When touching PostgreSQL error handling, prefer typed driver errors and codes over string matching.
- Define interfaces on the consumer side and keep them minimal. Do not create an interface for a concrete type without a substitution or testing need.
- Avoid mutable global state, unowned goroutines, and channels without a shutdown strategy.
- Do not use panic for input errors or expected runtime failures. Recovery middleware is a final safety boundary, not control flow.
- Use `time.Duration`, named constants, and explicit domain types instead of magic values.
- Use structured zerolog logging instead of `fmt.Printf` in new or modified server code.
- Prefer the standard library and existing dependencies. When adding a module, explain why and update both `go.mod` and `go.sum`.

## Layer boundaries

### Handlers

- Handle transport concerns only: parse path, query, and body values; retrieve authenticated identity; call a service; map the result to HTTP.
- Validate request shape with Gin binding. Validate domain semantics in services.
- Do not write SQL, contain business rules, or fabricate domain data to avoid a repository lookup.
- Return errors through `apperror.Respond`; never expose database or internal implementation details.
- After a successful WebSocket upgrade, do not write to `gin.Context.Writer`.

### Services

- Own business invariants and authorization. Repositories do not decide who may view or mutate a resource.
- Evaluate atomicity for every multi-step mutation. Operations that must succeed or fail together require a transaction.
- Treat PostgreSQL as the source of truth. For messages, use validate -> persist -> broadcast.
- A broadcast failure must not erase an already persisted record. Log or measure the failure clearly.
- Do not depend on Gin or HTTP-specific types.

### Repositories

- Handle persistence and map database failures to stable application errors where appropriate.
- Always use SQL placeholders. Never concatenate user input into SQL.
- Use context-aware sqlx methods. Close rows and check scan errors, `rows.Err()`, and `RowsAffected()` where relevant.
- Avoid `SELECT *` in new or modified queries. List columns explicitly to make schema evolution safer.
- Create transactions at the boundary that covers the complete use case, and roll back on every error path.

## HTTP, authentication, and errors

- Keep versioned endpoints under `/api/v1`; private routes must use `middleware.Auth`.
- Never trust client-provided `user_id`, username, or ownership. Read identity from authenticated context and authoritative data from the source of truth.
- Preserve error semantics: 400 invalid input, 401 missing or invalid authentication, 403 authenticated but unauthorized, 404 missing resource, 409 conflict, and 500 internal failure.
- Do not change JSON fields, status codes, or pagination semantics without updating tests and `README.md`.
- JWT validation must verify the signing algorithm, expiry, and token type.
- WebSocket query tokens are sensitive. Do not log raw query strings; prefer authorization headers where clients support them.

## WebSocket and concurrency

`ws.Hub.Run` is the sole owner of `clients`, `rooms`, and room membership. All mutations of these maps must continue through the event loop. Do not scatter mutexes around the code or access these maps from handlers or client goroutines.

- Each connection has exactly one reader (`ReadPump`) and one writer (`WritePump`). Route all outbound payloads through `client.send`.
- Never block the Hub on network I/O, database I/O, or a slow client. Define channel capacity and backpressure or drop behavior explicitly.
- Treat join, leave, and message events as untrusted input. Validate event types, room IDs, payload size, and membership before subscription or broadcast in new or modified paths.
- Do not allow clients to broadcast events representing mutations that have not been validated and persisted by a service.
- Make shutdown and close paths idempotent or protect them with an appropriate owner or `sync.Once`. Prevent send-on-closed-channel, double close, and goroutine leaks.
- Run changed Hub or Client code with the race detector. Test slow consumers, disconnects, and shutdown when those behaviors are affected.

## Database and migrations

- Do not modify an applied migration to evolve the schema. Create a new pair with `make migrate-create name=<snake_case_name>`.
- Every up migration requires a corresponding, reasonable down migration.
- Review nullability, foreign key actions, unique constraints, and indexes against real query patterns. `ON DELETE SET NULL` requires a nullable column by design.
- A schema change requires synchronized updates to models, queries, tests, and configuration or API documentation where applicable.
- Do not run `migrate-down`, `docker-down-v`, or any destructive data operation without explicit authorization.

## Testing

- Place tests beside packages as `*_test.go`. Prefer table-driven cases with behavior-oriented names.
- Service tests should use small fakes implementing the existing `Repository` interface. Avoid mocking every implementation detail.
- Handler tests should use `httptest` and verify status codes, response bodies, validation, and authorization boundaries.
- Keep repository and integration tests isolated, with an explicit test database lifecycle and no dependency on personal developer data.
- Do not rely on long `time.Sleep` calls or fragile timing in concurrency tests. Use channels, synchronization, and finite timeouts.
- Cover the happy path, validation, unauthorized and forbidden cases, not found and conflict cases, and dependency failures.

## Skills and playbooks

When a task matches a playbook below, apply it and inspect only the packages needed for the work.

### Skill: Go feature development

Use when adding a use case, endpoint, or domain behavior.

1. Define DTOs, public contracts, and compatibility requirements.
2. Extend the repository interface only when persistence requires it.
3. Implement business rules, authorization, and error mapping in the service.
4. Keep handlers thin: bind -> service -> response.
5. Wire dependencies and routes at the appropriate composition root.
6. Add service and handler tests, then run focused tests and the full race-enabled suite.

### Skill: Bug diagnosis and repair

Use for runtime failures, incorrect responses, regressions, or races.

1. Reproduce the issue with the smallest test or collect concrete evidence.
2. Trace the data flow to the source of truth and identify the root cause.
3. Add a failing regression test before the fix where practical.
4. Fix the problem in the layer that owns the violated invariant.
5. Run affected tests and the race detector for concurrency-related changes.

### Skill: HTTP API design

Use when adding or changing REST endpoints, middleware, DTOs, or status codes.

- Define method, path, authentication, request, response, status, and errors before implementation.
- Use Gin binding for shape validation and services for semantic validation and authorization.
- Do not expose models containing sensitive or persistence-only fields. Create response DTOs when needed.
- Use `httptest` for happy paths, invalid input, unauthenticated and forbidden cases, and dependency failures.
- Update the README API reference when a public contract changes.

### Skill: PostgreSQL and migrations

Use when adding or changing tables, columns, indexes, queries, or transactions.

- Read the model, repository queries, and both migration directions involved.
- Test a clean database and the upgrade path when the environment permits.
- Use constraints to protect durable invariants and map violations to stable application errors.
- Add indexes for actual `WHERE`, join, and `ORDER BY` patterns, not speculation.
- Define the transaction boundary and test rollback for multi-step mutations.

### Skill: WebSocket and concurrency

Use when changing the Hub, Client, events, room membership, broadcast, or shutdown.

- Document goroutine, channel, and state ownership before editing.
- Preserve one reader and one writer for each WebSocket connection.
- Keep the event loop non-blocking and define buffers, timeouts, backpressure, and drop behavior.
- Validate room membership from server-side data before subscription or broadcast.
- Test disconnects, slow clients, full channels, shutdown, and duplicate closes. Always run the race detector.

### Skill: Security review

Use when work touches authentication, JWT, CORS, WebSocket upgrades, user input, or secrets.

- Enumerate trust boundaries and all client-controlled data.
- Evaluate authentication, authorization, token type, expiry, signing method, and resource ownership separately.
- Ensure logs, errors, and responses do not expose credentials or internal details.
- Do not treat CORS middleware as a substitute for WebSocket `CheckOrigin`; define origin policy explicitly.
- Add negative tests for invalid, expired, and wrong-type tokens, non-members, and malformed payloads.

### Skill: Code review

Use when the user requests a review or audit without implementation.

- Prioritize correctness, security, data loss, races, deadlocks, leaks, and contract regressions before style.
- Each finding must include severity, file and line, a failure scenario, and a concrete remediation.
- Do not modify code when the scope is review-only.
- If no findings exist, still report observed test gaps or residual risks.

## Plan checklist

Use this checklist for tasks that change code. Skip items that do not apply, but never mark an item complete without verifying it.

### 1. Understand the request

- [ ] Summarize the desired outcome in one or two sentences.
- [ ] Define verifiable acceptance criteria.
- [ ] Identify in-scope and out-of-scope work.
- [ ] Record assumptions that affect implementation.
- [ ] Evaluate backward compatibility and affected public contracts.

### 2. Inspect the repository

- [ ] Read `README.md`, `Makefile`, and `git status --short`.
- [ ] Locate relevant entry points, call sites, interfaces, and tests.
- [ ] Trace the path from the transport boundary to the source of truth.
- [ ] Identify the layer that owns the rule being changed.
- [ ] Identify existing user changes and avoid overwriting them.

### 3. Design the change

- [ ] Select the smallest design that fully satisfies the request.
- [ ] List the files and packages expected to change and why.
- [ ] Define error paths, authorization boundaries, and failure behavior.
- [ ] Define resource ownership, cancellation, and transaction boundaries.
- [ ] Choose a test strategy before implementation.
- [ ] Avoid unnecessary dependencies, abstractions, and breaking changes.

### 4. Implement

- [ ] Preserve handler -> service -> repository dependency direction.
- [ ] Propagate context for every I/O operation.
- [ ] Validate input and authorization at the correct boundaries.
- [ ] Wrap internal errors without exposing sensitive details.
- [ ] Release rows, transactions, tickers, connections, goroutines, and channels through their owners.
- [ ] Update wiring, DTOs, configuration, migrations, and documentation where required.
- [ ] Keep unrelated refactors out of the change.

### 5. Test and verify

- [ ] Add or update tests for changed behavior.
- [ ] Cover the happy path and important failure paths.
- [ ] Run focused package tests during development.
- [ ] Run `gofmt` on every changed Go file.
- [ ] Run `go vet ./...`.
- [ ] Run `go test -race -count=1 ./...`.
- [ ] Run `make lint` when golangci-lint is available.
- [ ] Run `make build` for wiring, configuration, dependency, or startup changes.
- [ ] Run `git diff --check` and review the complete final diff.
- [ ] Record any check that could not run and the reason.

### 6. Area-specific checks

#### HTTP API

- [ ] Method, path, authentication, request, response, and status are defined.
- [ ] Validation errors do not expose internal details.
- [ ] Tests cover invalid input, unauthenticated access, and forbidden access where applicable.
- [ ] README and API documentation are updated for public contract changes.

#### Database

- [ ] Queries use placeholders and explicit column lists.
- [ ] Nullability, foreign keys, unique constraints, and indexes were reviewed.
- [ ] Multi-step mutations have correct transaction and rollback behavior.
- [ ] A new up and down migration pair exists; deployed migrations remain unchanged.
- [ ] Models, queries, scans, and schema definitions remain synchronized.

#### WebSocket and concurrency

- [ ] Goroutine, channel, and state ownership are explicit.
- [ ] The Hub event loop performs no blocking I/O.
- [ ] Membership and authorization are validated server-side.
- [ ] Backpressure, timeout, and drop behavior are intentional.
- [ ] Disconnect, slow-client, shutdown, and duplicate-close behavior were reviewed or tested.
- [ ] The race detector was run.

#### Security and configuration

- [ ] Client-provided identity and ownership are never trusted.
- [ ] Secrets, tokens, and passwords do not appear in logs, errors, or the diff.
- [ ] JWT type, expiry, and signing method are validated where relevant.
- [ ] WebSocket origin policy was reviewed when upgrade behavior changed.
- [ ] New configuration has appropriate defaults and validation and appears in `.env.example`.

### 7. Handoff

- [ ] Summarize the outcome and changed behavior.
- [ ] List the primary files changed.
- [ ] Report the exact verification commands and results.
- [ ] Identify required migration, configuration, or deployment actions.
- [ ] Report residual risks, verification limitations, and necessary follow-up work.

## Definition of Done

A change is complete only when:

- The requested scope is satisfied without unrelated cleanup.
- Code is formatted, builds successfully, and relevant tests pass; concurrency changes have run under the race detector.
- Error paths, authorization, resource cleanup, and backward compatibility have been considered.
- Migration, configuration, API, and WebSocket contracts are updated consistently where required.
- No secret, build artifact, or `.env` file has been added to the repository.
- The handoff states what was verified and identifies any remaining risk or limitation.
