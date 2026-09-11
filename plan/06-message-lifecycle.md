# 06 — Message Lifecycle

**Priority:** P1  
**Status:** Proposed  
**Depends on:** 03, 05

## Goal

Support safe message editing and deletion with consistent HTTP history and real-time state.

## Scope

- Add edit and delete operations with author/moderator authorization.
- Track `edited_at` and a clear deletion representation.
- Add typed `message_updated` and `message_deleted` events.
- Define idempotency and conflict behavior for retries and concurrent edits.
- Preserve cursor pagination guarantees.

## Acceptance criteria

- [ ] Only authorized actors can edit or delete a message.
- [ ] Persistence commits before update/delete events are broadcast.
- [ ] HTTP history and WebSocket events converge on the same representation.
- [ ] Deleted content follows an explicit retention policy.
- [ ] Concurrent or repeated operations return deterministic results.
- [ ] Tests cover author, moderator, forbidden, not-found, and race-sensitive cases.

## Open decisions

- Soft deletion versus hard deletion.
- Edit time window and moderation audit requirements.
