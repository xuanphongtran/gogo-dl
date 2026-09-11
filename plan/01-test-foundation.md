# 01 — Test Foundation

**Priority:** P0  
**Status:** Proposed  
**Depends on:** None

## Goal

Create a fast, deterministic safety net before changing persistence, authentication, and concurrent WebSocket behavior.

## Scope

- Add table-driven unit tests for user and chat services using small repository fakes.
- Add `httptest` coverage for binding, status codes, authentication boundaries, and error responses.
- Add focused Hub and Client tests for registration, room membership, fan-out, slow consumers, and shutdown.
- Establish isolated PostgreSQL integration tests for repository queries and migrations.
- Add a CI workflow that runs formatting checks, `go vet`, unit tests with `-race`, and build.

## Acceptance criteria

- [ ] Core register/login/profile/room/message happy paths have automated coverage.
- [ ] Unauthorized, forbidden, not-found, conflict, and dependency-failure paths are covered.
- [ ] WebSocket tests use synchronization and finite timeouts rather than long sleeps.
- [ ] Migrations apply cleanly to an empty supported PostgreSQL instance.
- [ ] `go test -race -count=1 ./...`, `go vet ./...`, and `make build` pass in CI.
- [ ] Test setup does not depend on developer-owned data or credentials.

## Out of scope

- Product behavior changes.
- A coverage percentage target without risk-based justification.
