# P0 — Foundation, Data Integrity, and WebSocket Authorization Execution Plan

**Status:** Proposed  
**Scope:** plans 01–03  
**Detailed specifications:** [`spec/`](../spec/)

## Objective

Complete the first three P0 safety tasks in dependency order:

```text
01 Test Foundation
        ↓
02 Data Integrity and Transactions
        ↓
03 WebSocket Authorization and Protocol Safety
```

Each stage must leave the repository buildable and independently verifiable.
Do not start the next stage while the previous stage's exit gate is failing.

## Stage 1 — Test foundation

### Work items

1. Create reusable unit-test helpers and typed repository fakes.
2. Add table-driven user service tests for registration, login, refresh,
   profile, deletion, validation outcomes, and dependency failures.
3. Add chat service tests for room and message authorization, persistence
   failures, broadcast failures, and cursor arguments.
4. Add handler tests with `httptest` for JSON binding, JWT boundaries, status
   codes, safe errors, path parsing, and pagination queries.
5. Add Hub/Client tests for registration, room fan-out, slow consumers,
   disconnect, duplicate unregister, and shutdown under `-race`.
6. Add isolated PostgreSQL integration setup for migrations and repository
   queries. It must use an explicit test database configuration.
7. Add CI checks for formatting, `go vet`, race-enabled tests, and `make build`.

### Exit gate

- Unit, handler, WebSocket, and migration/repository coverage exists for the
  behavior listed in [spec/01-test-foundation.md](../spec/01-test-foundation.md).
- `go test -race -count=1 ./...` passes.
- `go vet ./...` passes.
- `make build` passes.
- Tests do not use `configs/.env`, personal databases, or personal secrets.

## Stage 2 — Data integrity and transactions

### Work items

1. Add a new migration pair; do not edit `migrations/000001_*`.
2. Apply the deletion policy from [spec/02-data-integrity.md](../spec/02-data-integrity.md):
   retain message history with nullable authors, retain room ownership as
   non-null, and reject account deletion while rooms remain owned.
3. Add an atomic repository operation for room creation plus creator
   membership insertion.
4. Make account deletion transactional and map owned-room conflicts to a
   stable `409` domain error.
5. Replace `SELECT *` in touched queries with explicit projections.
6. Check all scans, row iteration errors, and `RowsAffected` results.
7. Classify PostgreSQL errors through `*pq.Error` SQLSTATE codes.
8. Update models/DTOs and message history joins for nullable deleted authors.
9. Add clean-schema, upgrade-path, rollback, deletion, and constraint tests.
10. Update API documentation for the deleted-author representation if the
    response can contain `user_id: null`.

### Exit gate

- Room creation cannot return success without creator membership.
- Account deletion follows the documented conflict and retention policy.
- Clean migration and upgrade migration both pass.
- Existing migration `000001` is unchanged.
- Repository and service tests pass under the race-enabled suite.

## Stage 3 — WebSocket authorization and protocol safety

### Work items

1. Introduce separate typed inbound commands and outbound events. Initially
   accept only `join` and `leave` from clients.
2. Validate JSON shape, event type, and canonical positive `int64` room IDs.
3. Add a pre-authorized subscription path in which the chat service checks
   membership outside `Hub.Run` before the Hub mutates room state.
4. Reject client-originated message/lifecycle events that have not passed
   through the chat service and persistence path.
5. Add stable `error` events with safe error codes and messages.
6. Add a post-commit membership-revocation control path owned by the Hub event
   loop.
7. Preserve one-reader/one-writer ownership and make unregister/shutdown
   channel ownership idempotent.
8. Add tests for authorized join, forbidden join, forged events, malformed
   input, revocation, slow consumers, disconnect, and shutdown under `-race`.

### Exit gate

- A non-member cannot join, observe, or publish to a room.
- The Hub event loop performs no database or network I/O.
- Only the Hub event loop mutates connection and room maps.
- Revocation takes effect within the documented post-commit processing bound.
- Invalid commands produce stable safe errors and never panic.
- All WebSocket tests pass with the race detector.

## Expected file areas

| Area | Expected changes |
|---|---|
| `internal/user` | Tests, deletion error behavior, nullable-author compatibility where needed |
| `internal/chat` | Transactional room creation/deletion, authorization entry points, query projections, tests |
| `internal/ws` | Typed protocol, authorization boundary, revocation, lifecycle fixes, tests |
| `internal/middleware` | Only if testable token/origin contract changes require it |
| `migrations` | One new up/down pair for data-integrity changes |
| `internal/httpserver` / handlers | Only for documented contract and test wiring changes |
| `.github/workflows` | CI workflow for the Stage 1 exit gate |
| `README.md` | API/event/deleted-author documentation updates where contracts change |

## Review checkpoints

At the end of each stage, review:

- context propagation and cancellation;
- authorization at the service boundary;
- transaction rollback and resource cleanup;
- error mapping without leaking infrastructure details;
- Hub map/channel ownership and race safety;
- backward compatibility of HTTP and WebSocket contracts;
- migration upgrade and recovery behavior.

## Rollback and recovery

- Stage 1 is test-only except for CI/test helpers and is reverted by removing
  those additions.
- Stage 2 uses a reversible migration pair. Never rewrite an applied
  migration; if the migration has shipped, add a corrective migration.
- Stage 3 keeps client message creation on the existing HTTP service path while
  the new protocol is introduced, so unauthorized WebSocket commands can be
  rejected without deleting durable data.
- No destructive database or volume command is part of this plan.

## Definition of done

The three stages are complete only when all linked specifications' acceptance
criteria pass, the complete race-enabled test suite and `go vet` pass, the
application builds, documentation reflects changed contracts, and the roadmap
statuses are updated from `Proposed` only after evidence is recorded.
