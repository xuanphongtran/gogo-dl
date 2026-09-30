# 07 — Presence, Typing, and Read State

**Priority:** P1  
**Status:** Done — SPEC, implementation, testing, review and fixes complete
**Depends on:** 03 and the existing membership contract of 05
**Specification:** [spec/07-presence-read-state.md](../spec/07-presence-read-state.md)

## Goal

Add responsive collaboration signals without turning ephemeral socket state into unreliable durable state.

## Scope

- Track online presence per user across multiple connections.
- Add throttled typing-started and typing-stopped events.
- Persist a per-user, per-room read cursor for unread counts.
- Define reconnect and stale-connection behavior.
- Expose a room presence snapshot through an authorized path.

## Acceptance criteria

- [x] Multiple tabs do not mark a user offline until the final connection closes.
- [x] Typing events are ephemeral, authorized, throttled, and automatically expire.
- [x] Read cursors advance monotonically and retries are idempotent.
- [x] Unread counts derive from durable cursors and message IDs.
- [x] Disconnect and reconnect behavior is tested without timing-dependent sleeps.
- [x] Privacy rules for presence visibility are documented.

## Out of scope

- Exact last-active analytics.
- Cross-device push notifications, which belong to plan 08.

## Decisions and assumptions

- Presence is room subscription state, aggregated across connections, visible
  only to current members. Snapshot HTTP and join events support reconnect.
- Typing starts are limited to once/second per connection, expire in 5 seconds,
  and aggregate per user. Durable authorization happens outside the Hub loop.
- Read state is private to the user. Cursor 0 includes history; own messages are
  excluded from unread. Future Phase 06 tombstones count by ID without requiring
  the Phase 06 application model.
- Migration 000007 follows an exact copy of Phase 06's migration 000006, preserving
  deployment order while this branch starts from main and has no lifecycle code.
- Phase 05 deferred fixes/verification remain separate.

## Execution sequence

1. **SPEC:** define HTTP/WS contracts, privacy, expiry, retries and transaction
   boundaries in the linked specification.
2. **PLAN:** divide into independent Hub, chat transport/persistence and database
   verification tasks; reserve schema order and arrange isolated test tooling.
3. **IMPLEMENT:** add schema, typed DTOs/events, locked cursor operations, service
   policy, authenticated routes, Hub snapshots and aggregated presence/typing.
4. **TEST:** focused behavior tests, disposable PostgreSQL upgrade/concurrency
   checks, full race suite, vet, build and regenerated Swagger.
5. **REVIEW:** independently inspect security, Hub ownership, resource cleanup,
   transaction failure paths, migration order and protocol compatibility.
6. **FIX:** address concrete review findings and rerun affected checks.
7. **COMMIT:** record verification, update roadmap and commit on ft/phase-7.

## Files and ownership

- `internal/ws`: Hub-owned presence/typing, command authorization and tests.
- `internal/chat`: DTOs, repository interface/implementation, policy, handlers and
  service/handler tests. `internal/httpserver` registers the new private routes.
- `migrations/000006*`, `000007*`: compatible schema prerequisite and new cursor
  table. `internal/database` verifies schema, transactions and concurrency.
- README, Swagger, spec and roadmap: public protocol and completion record.

No new dependency or configuration value is planned. No deployment, external
message, production DB mutation or push is part of this task.

## Completion record

Implemented authorized room snapshots, per-user presence across tabs, bounded
command authorization, throttled/expiring typing, personal GET/PUT read state,
locked monotonic persistence and private events after commit. HTTP and WS privacy,
reconnect behavior, schema ordering and client reconciliation are documented in
README and SPEC; Swagger includes the three new authenticated operations.

Verified with Go 1.23.12 and an owned disposable PostgreSQL 16 database:

- `gofmt` on every changed Go file: complete.
- `go test -race -count=1 ./internal/ws`: passed after production fixes.
- `go test -race -count=1 ./internal/chat ./internal/httpserver`: passed.
- `go test ./internal/database -run TestPhase7 -count=1 -timeout=90s`: passed
  with `TEST_DATABASE_URL` configured only for the disposable test database.
- `make test` (`go test -race -count=1 ./...`): passed with all PostgreSQL
  integration tests enabled.
- `go vet ./...`: passed.
- `make build BUILD_DIR=<temporary directory>`: passed.
- `make docs`: passed using Swag 1.8.12; generated JSON assertions verified paths,
  bearer authentication, success types, required positive cursor and arrays.
- `go test -race -count=50 ./internal/ws -run '^TestDeniedMembershipClearsAllUserSubscriptions$'`:
  passed after the final test-only review fix.
- `git diff --check` and final diff review: passed.
- `make lint`: not run because `golangci-lint` is unavailable.

Independent review found and resolved a denied repeated join retaining existing
subscriptions and a regression test assuming map iteration order. Already-stopped
typing retries now avoid unnecessary authorization queries; pending starts retain
explicit `authorization_pending` behavior. No remaining concrete review findings.
Swag emitted parser warnings for standard-library/dependency constants while
generating valid output. No new application dependency or configuration was added.
The owned PostgreSQL test container and temporary Go tools/build files were
removed after verification.

The branch starts from `main`; migration 000006 matches `ft/phase-6` byte for byte,
and 000007 follows it. Startup applies both before opening HTTP. Phase 05 deferred
fixes/verification remain Pending. No deployment or push was performed.
