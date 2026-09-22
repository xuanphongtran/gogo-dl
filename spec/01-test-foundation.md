# 01 — Test Foundation Specification

**Related plan:** [plan/01-test-foundation.md](../plan/01-test-foundation.md)  
**Status:** Done
**Priority:** P0

## Objective

Establish deterministic verification for the user, chat, HTTP, repository, and
WebSocket layers before changing persistence or concurrent connection behavior.
The test suite must run without developer-owned credentials or data.

## Scope and test boundaries

### Service tests

Use small repository fakes and table-driven cases for:

- user registration, duplicate username/email, password hashing, and token
  generation;
- login success, unknown email, invalid password, and repository failure;
- refresh token validation, deleted user, and repository failure;
- profile read/update/delete and ownership enforcement;
- room creation, room lookup, join behavior, and not-found handling;
- message send authorization, persistence failure, broadcast failure, and
  cursor pagination delegation.

Service tests must assert business outcomes and error classes, not private SQL
or mock call ordering unless that ordering is the behavior under test.

### HTTP handler tests

Use `httptest` with an isolated Gin router. Cover:

- request binding failures and boundary values;
- expected success status codes and JSON fields;
- missing, invalid, and wrong-type JWTs;
- forbidden and not-found responses;
- dependency failures mapped to safe `500` responses;
- path and query parsing for room IDs and message cursors.

Tests must not rely on a real network listener.

### WebSocket tests

Use a real in-process WebSocket connection where protocol behavior matters and
direct Hub-level synchronization where it does not. Cover:

- client registration and unregistration;
- authorized room membership behavior;
- fan-out to multiple clients;
- sender exclusion where the protocol requires it;
- malformed input and unsupported events;
- a slow consumer with a full send buffer;
- disconnect cleanup and duplicate close paths;
- Hub shutdown and writer goroutine termination.

Use channels and bounded `select` timeouts. Do not use long `time.Sleep` calls.
Changed Hub or Client tests must run under the race detector.

### PostgreSQL integration tests

Integration tests run only when an explicit test database is available, for
example through `TEST_DATABASE_URL`. They must:

- apply migrations to an isolated database or schema;
- verify the clean migration path;
- verify the upgrade path from the current schema;
- clean up the owned schema/database after each run;
- avoid credentials or data from a developer's normal database.

Repository tests must verify SQL behavior, constraint mapping, cursor ordering,
transaction rollback, and `rows.Err()` handling where applicable.

## Test setup contract

- Unit tests run with no external service.
- Integration tests fail clearly as skipped or unavailable when no test
  database is configured; they must not silently use `configs/.env`.
- All test-created resources use unique names or an isolated schema.
- Every goroutine and connection created by a test has an owner and a bounded
  shutdown path.
- Test errors must not print passwords, tokens, DSNs, or raw credentials.

## CI verification

The CI workflow must run:

```text
gofmt check on changed Go files
go vet ./...
go test -race -count=1 ./...
make build
```

The workflow must not require a developer machine, personal PostgreSQL
instance, or uncommitted `.env` file. Repository integration tests may use a
disposable PostgreSQL service provided by CI.

## Acceptance criteria

- Core user and chat service paths have automated coverage.
- HTTP tests cover success, invalid input, authentication, authorization,
  not-found, conflict, and dependency-failure behavior.
- WebSocket tests cover registration, membership, fan-out, slow consumers,
  disconnect, and shutdown under `-race`.
- Migration tests cover both a clean database and an upgrade path.
- `go test -race -count=1 ./...`, `go vet ./...`, and `make build` pass in CI.
- No test depends on personal data or credentials.

## Out of scope

- Changing product behavior solely to increase coverage.
- A fixed global coverage percentage without risk-based justification.
- Load testing beyond the bounded concurrency cases needed for correctness.
