# 06 — Message Lifecycle

**Priority:** P1  
**Status:** Done — SPEC, implementation and verification complete
**Depends on:** 03 and the existing membership/role contract of 05
**Specification:** [spec/06-message-lifecycle.md](../spec/06-message-lifecycle.md)

## Goal

Support safe message editing and deletion with consistent HTTP history and real-time state.

## Scope

- Add edit and delete operations with author/moderator authorization.
- Track `edited_at` and a clear deletion representation.
- Add typed `message_updated` and `message_deleted` events.
- Define idempotency and conflict behavior for retries and concurrent edits.
- Preserve cursor pagination guarantees.

## Acceptance criteria

- [x] Only authorized actors can edit or delete a message.
- [x] Persistence commits before update/delete events are broadcast.
- [x] HTTP history and WebSocket events converge on the same representation.
- [x] Deleted content follows an explicit retention policy.
- [x] Concurrent or repeated operations return deterministic results.
- [x] Tests cover author, moderator, forbidden, not-found, and race-sensitive cases.

## Resolved decisions

- Soft deletion erases content and retains an ID-stable tombstone.
- Current-member authors may edit without a time limit; owners/moderators may
  delete any message but may not rewrite other authors' messages.
- PATCH requires an expected revision; DELETE returns an idempotent tombstone.
- Internal deletion actor/timestamp provide minimal audit; edit history is out
  of scope. The user accepted the proposed policy.

## Execution plan

1. **SPEC:** define authorization, REST responses, lifecycle fields, retention,
   retries, concurrency and event reconciliation in the linked specification.
2. **Schema and projection:** add migration 000006 up/down, extend Message and
   edit DTO, and update create/history projections while preserving cursors.
3. **Repository:** lock room, actor membership and message in one transaction;
   invoke service policy on locked state; write lifecycle/audit fields and commit.
4. **Service:** enforce current membership and author/manager rules; implement
   revision conflicts and idempotent retries; emit typed events after commit.
5. **Transport:** register authenticated PATCH/DELETE routes; keep handlers thin;
   add server-only event types and HTTP Swagger annotations.
6. **Docs:** regenerate Swagger and describe REST payloads, tombstones, revisions,
   retention and client reconciliation in README. Keep Phase 05 Pending.
7. **Verification:** behavioral service/handler/WebSocket tests and isolated
   PostgreSQL migration, history, rollback and concurrency checks; gofmt, full
   race tests, vet, build, available lint and final diff review.

## Files and boundaries

- `migrations/000006_message_lifecycle.*.sql`: additive schema, reversible columns.
- `internal/chat/model.go`, repositories and new lifecycle files: DTOs, locked
  mutations, policy and handlers. Existing service dependency direction stays.
- `internal/ws/message.go`: event constants; the Hub remains the sole state owner.
- `pkg/apperror`: stable deleted-message/revision conflict errors.
- Tests beside chat/ws and database integration tests: observable contracts and
  transaction behavior; only disposable test DBs are used.
- README, Swagger, spec and roadmap: synchronized public contract and status.

No new dependency or configuration is needed. Changes to deferred Phase 05
endpoint behavior, distributed delivery, search and read state are out of scope.

## Completion record

Implemented migration 000006, authenticated PATCH/DELETE, locked authorization,
revision conflicts/retries, tombstones/audit and typed lifecycle events. Creation
now resolves author usernames from PostgreSQL to match history and events.
Swagger and README document the public contracts and client reconciliation.

Verified using Go 1.23.12 and disposable PostgreSQL 16:

- `gofmt` on every changed Go file: complete.
- `go test ./internal/chat ./internal/ws -count=1`: passed.
- `go test ./internal/database -run TestPhase06 -count=1`: passed with
  `TEST_DATABASE_URL` configured for an isolated test DB.
- `go test -race -count=1 -p 1 ./...`: passed with all PostgreSQL integration
  tests enabled, including upgrade, rollback, audit/history and concurrency.
- `go vet ./...`: passed.
- `make build` with `BUILD_DIR` in `/tmp`: passed.
- `make docs`: generated Swagger; lifecycle paths, authentication, required
  revision, minimum revision and nullable fields were checked in generated JSON.
- `git diff --check` and final diff review: passed.
- `make lint`: not run because `golangci-lint` is unavailable.

Swag 1.8.12 emitted parser warnings for standard-library constants while
generating valid documents. The disposable DB and temporary tools were removed
after verification. Phase 05 follow-up fixes/verification stay Pending.

Deployment applies embedded migration 000006 before opening HTTP. Clients must
send revision when editing and apply lifecycle events by increasing revision;
they recover missed best-effort delivery through history. Down migration cannot
restore content erased by deletion.
