# 06 — Message Lifecycle Specification

**Related plan:** [plan/06-message-lifecycle.md](../plan/06-message-lifecycle.md)
**Status:** Implemented and verified; Phase 05 remains Pending
**Priority:** P1
**Depends on:** 03 and the existing membership/role contract of 05

## Objective and decisions

Add authenticated editing and deletion without changing message IDs, room IDs,
creation times, or ID-based history pagination. Phase 05 remains Pending.
The user selected these policies:

- Soft deletion retains a tombstone and erases content from the message row.
- Only the author, while still a room member, may edit. There is no edit deadline.
- An author, owner, or moderator who is currently a member may delete. Owners
  and moderators may delete any message in their room, including messages from
  other managers or a deleted account. They cannot edit another author's text.
- Deleted messages cannot be edited or restored through the API.
- Minimal deletion audit metadata records the actor and timestamp. There is
  no edit history, recovery API, moderation reason, or external audit service.

## Public message representation

Existing fields remain. Add `revision` (positive int64, initially 1), `edited_at`
and `deleted_at` (nullable UTC timestamps). A tombstone has `content: ""` and a
non-null `deleted_at`; its author, creation time, ID, and room ID remain intact.
Deleted accounts retain the existing nullable author and `[deleted user]` label.
The deletion actor is internal audit metadata and is not exposed in responses.
Creation and history resolve the author's username from PostgreSQL so lifecycle
events share the same authoritative author projection.

History includes tombstones, ordered by ID descending with the existing `before`
cursor. Mutation timestamps and revision never affect pagination order.

## HTTP contract

Both routes are under the existing authenticated, body-limited and rate-limited
`/api/v1` group. Identity comes from JWT middleware; both IDs must be positive.

### Edit

`PATCH /api/v1/rooms/:id/messages/:message_id`

```json
{ "content": "Updated text", "revision": 1 }
```

Content uses the existing send-message normalization and 4000-byte limit.
`revision` is required and must be positive. A successful edit returns `200`
with the complete Message and increments revision once. Creation time remains
unchanged; `edited_at` is assigned by PostgreSQL after locking the current row.

- Equal content at the current revision is a no-op returning `200`.
- An immediate retry with the previous revision and the same current content
  returns `200` without another increment or event.
- Other stale revisions return `409` with `message revision conflict`. This
  prevents an old request from overwriting subsequent edits, including an ABA
  sequence in which content returns to an earlier value.
- Editing a tombstone returns `409` with `message deleted`.

### Delete

`DELETE /api/v1/rooms/:id/messages/:message_id` has no request body.

Success returns `200` with the complete tombstone. The first deletion clears
content, sets `deleted_at` and internal `deleted_by`, and increments revision.
An authorized retry returns the same tombstone without changing its timestamps,
audit actor, revision, or emitting another event. Authorization is rechecked on
every retry. Delete applies to the current version without an edit revision
precondition; if an edit commits first, deletion clears that edited content.

### Authorization and errors

| Actor | Edit | Delete |
|---|---|---|
| Anonymous | 401 | 401 |
| Public-room non-member | 403 | 403 |
| Private-room non-member | 404 | 404 |
| Current member who authored the message | Allowed | Allowed |
| Other regular member | 403 | 403 |
| Other moderator or owner | 403 | Allowed |

Missing rooms and messages return `404`. A message belonging to a different
room returns `404`. Membership is checked before exposing message existence or
state. Invalid paths, content, or revision return safe `400` errors; infrastructure
failures return the existing generic `500`, without SQL, credentials, or content.

## Persistence and concurrency

Migration 000006 adds lifecycle fields and nullable deletion-actor foreign key
with `ON DELETE SET NULL`. Existing rows receive revision 1 and null lifecycle
timestamps. Constraints protect positive revisions and empty tombstone content.
The down migration drops added fields; erased content cannot be reconstructed.

Each edit/delete uses a transaction and this lock order:

1. Room row `FOR SHARE` to synchronize with ownership transfers.
2. Actor membership `FOR SHARE` to prevent demotion/removal during authorization.
3. Message row `FOR UPDATE`, scoped to the requested room.

The repository passes locked room visibility, membership, and message to a pure
service policy callback. The service decides authorization, revision conflicts,
and whether the request is a no-op; the repository writes and commits. Callback
execution must perform no I/O. Cancellation propagates through every DB call.
Failures roll back; events are emitted only after a successful commit.

Concurrent edits from one base revision have one winner, except identical edits
may return the same result as an idempotent retry. Edit/delete serialize; after
delete, edits fail, and content cannot be resurrected. Concurrent deletes produce
one transition. This work does not modify the deferred Phase 05 endpoints.

## WebSocket contract

Add server-only `message_updated` and `message_deleted` event types. Each uses
the existing envelope with string `room_id` and a typed complete Message payload,
including revision, nullable lifecycle timestamps, and numeric payload IDs.
Existing `message` events gain the same additive lifecycle fields.

```json
{
  "type": "message_deleted",
  "room_id": "10",
  "payload": {
    "id": 42, "room_id": 10, "user_id": 7, "username": "alice",
    "content": "", "created_at": "2026-09-30T08:00:00Z",
    "revision": 3, "edited_at": "2026-09-30T08:01:00Z",
    "deleted_at": "2026-09-30T08:02:00Z"
  }
}
```

Only committed changes broadcast. Retries/no-ops emit no event. Delivery remains
best effort and non-blocking; broadcast failure is logged without undoing a DB
commit or failing its HTTP response. Clients apply only higher revisions for a
known message and reconcile through history after reconnect; arrival order is
not guaranteed. Client-originated lifecycle events are rejected by the existing
join/leave-only protocol. Hub map ownership and reader/writer pumps stay intact.

## Verification and acceptance

- Service table tests cover role/author policy, removed membership, deleted
  authors, private-room privacy, invalid input, retries and revision conflicts.
- Handler tests cover paths/body validation, authentication and safe responses.
- WebSocket tests verify typed payloads, post-commit emission, no emission on
  failure/retry, dropped-event behavior and rejection of client lifecycle events.
- Isolated PostgreSQL tests cover migration from 000005 with existing messages,
  tombstone history/cursors, actor deletion, rollback, concurrent mutations and
  role changes competing with an authorization check.
- Regenerate Swagger; document the HTTP and WebSocket contracts in README.
- Run gofmt, focused tests, full race suite, vet, build and diff checks. Phase 05
  verification remains a separate, deferred work item.
