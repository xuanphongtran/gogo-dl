# 07 — Presence, Typing, and Read State Specification

**Related plan:** [plan/07-presence-read-state.md](../plan/07-presence-read-state.md)
**Status:** Implemented and verified
**Depends on:** 03 and the existing membership contract of 05

## Outcome and boundaries

Members can see room presence and typing, and maintain a durable personal read
cursor. Phase 06 application code and Phase 05 membership review fixes are
included from develop. Dedicated Phase 05 verification remains Pending.
Defaults below are implementation assumptions because the roadmap leaves them
open. No new dependencies or environment settings are needed.

## Presence and privacy

- Presence means an authorized WebSocket subscription to this room, not global
  account activity. A user is online while at least one connection is subscribed.
- The first subscription emits online; subsequent tabs do not duplicate it. Only
  removal of the last subscription emits offline. Explicit socket leave,
  disconnect, durable membership revocation and stale-connection cleanup remove
  subscriptions and typing state.
- Only current room members may request presence or send typing. Public
  nonmembers receive HTTP 403; private nonmembers and missing rooms receive 404.
  WebSocket access failures use the existing generic forbidden protocol error.
- A successful socket join sends `presence_snapshot` to that connection. HTTP
  `GET /api/v1/rooms/:id/presence` returns the same snapshot, with string
  `room_id`, sorted distinct `online_user_ids` and `typing_user_ids` arrays.
  Empty arrays are `[]`. Usernames, client IDs and last-active times are omitted.
- `presence` is server-only, with payload `{ "user_id": 7, "online": true }`.
  The envelope retains string `room_id`. Legacy join/leave events remain.
- Snapshots and ephemeral events reflect this process. Reconnect requires a
  fresh authorized join; restart resets presence. Keep one service instance
  until Phase 09 introduces shared delivery.

## Typing protocol and concurrency

Client commands are `typing_started` and `typing_stopped`, with a positive string
`room_id` and no payload. The server derives identity and expiry. Unknown events
and client-originated presence, snapshot and read-state events are rejected.

- A connection must already be subscribed. Durable membership is rechecked by
  the chat service outside the event loop before an effective typing transition.
  Already-stopped retries perform no work and emit no event. A stop while start
  authorization is pending returns `authorization_pending`; retry after it ends.
- Starts are accepted at most once per second per connection. Excess starts
  return a stable `rate_limited` protocol error and do not refresh expiry.
  Stops are idempotent. Each accepted start expires after 5 seconds, including
  refresh starts; clients should refresh approximately every 2 seconds while
  typing. An expired start can be cleared within a 250 ms sweep interval.
- Typing is aggregated by user across connections. A stop/expiry on one tab
  leaves the user typing if another tab remains active. Server events have the
  command types and payload `{ "user_id": 7, "expires_at": "..." }`; stops use
  `expires_at: null`. A refresh can extend the user's advertised deadline.
- A single Hub-owned ticker expires typing. The Hub event loop owns all
  subscription, presence, typing and pending-authorization state. It performs
  no database/network I/O; all outgoing frames use the existing client queue.
- Authorization work is bounded to one pending command per connection, uses
  the connection context and a deadline, and delivers results through a channel.
  Leave/revocation invalidate pending authorization so an old successful result
  cannot restore subscription or typing after removal.
- Existing ping/pong deadlines remove stale connections. Shutdown stops the
  ticker, cancels connections/work and closes each owned send channel once.
  Slow consumers use the existing bounded queue and disconnect/drop policy.

## Personal read state HTTP contract

Authenticated endpoints:

| Method | Path | Request | Success |
|---|---|---|---|
| GET | `/api/v1/rooms/:id/read-state` | none | 200 ReadState |
| PUT | `/api/v1/rooms/:id/read-state` | `{ "last_read_message_id": 42 }` | 200 ReadState |

ReadState is `{ "room_id": 1, "last_read_message_id": 42, "unread_count": 3 }`.
There is one cursor per user per room. GET returns cursor 0 when unset. Reading
history, joining or sending a message does not implicitly advance the cursor.

- IDs are positive int64 values. Invalid path/body shapes are 400, missing or
  invalid authentication is 401. Membership errors follow the privacy rules
  above. A submitted message absent from this room is 404, including a message
  belonging to another room. Infrastructure failures return a safe 500.
- A valid cursor advances to `max(stored, submitted)`. Equal or older valid
  retries return the current state without another event. Concurrent updates
  serialize so the durable cursor never regresses.
- Unread is the count of room messages whose ID is greater than the cursor,
  excluding messages authored by this user. Deleted-author messages count.
  Cursor 0 includes all existing room history, including before membership.
- Message tombstones count by ID; editing or deleting
  does not create a new unread item or move the cursor. Counts do not depend on
  `revision` or `deleted_at`, so lifecycle mutations preserve read-state semantics.
- Leave/removal clears the read state through its membership foreign key;
  rejoining starts at cursor 0. The cursor is a watermark, with no foreign key
  to an individual message: removing an old message must not move it backward.
- New sends can change unread counts immediately. Clients derive tentative
  counts from message events and refresh GET after reconnect or missed delivery.
  A cursor is an ID-based acknowledgement, not an exact per-message receipt;
  it retains the existing history ordering and its concurrent-insert semantics.

## Durable authorization, transactions and events

Repositories lock the room row then the actor's membership before evaluating a
pure service policy. The policy requires current membership and preserves
private-room privacy. Locks remain until read/write transaction completion,
preventing concurrent membership removal from invalidating an accepted mutation.
Cursor validation, monotonic upsert and the returned unread count belong to the
same transaction. All I/O receives the request context and failures roll back.

After a changed cursor commits, emit server-only `read_state` to all connections
of that user, using the full ReadState payload and the room envelope. Other room
members never receive it. No event is emitted for no-op retries or failed writes.
Delivery is best effort; failure is logged and does not undo a committed cursor.
Clients keep the maximum cursor if events arrive out of order, and obtain fresh
unread counts from GET rather than trusting an old event's count.

## Schema and deployment sequencing

Migration `000007_presence_read_state` creates `room_read_states` with composite
primary key `(room_id, user_id)`, nonnegative BIGINT `last_read_message_id`, and a
composite cascading foreign key to `room_members`. The primary key matches the
lookup/upsert; existing `(room_id, id DESC)` messages index serves counts.
Its down migration drops only this table, losing saved cursors.

Migration `000006` provides Phase 06 lifecycle fields before migration `000007`
so clean/upgrade deployments apply 5 → 6 → 7. Both application contracts are
included in this branch. Deploying 7 while omitting 6 would
cause golang-migrate to skip 6 on a later merge, so that sequence is unsupported.
No historical migration is edited. No production database is changed during implementation/testing.

## Acceptance and verification

- Unit tests: policy, retries, own-message exclusion, error/cancellation paths,
  safe handler responses and authentication.
- Hub/race tests: multiple tabs, authorized snapshots, forged commands, typing
  throttle/aggregation/expiry, leave/revocation during authorization, reconnect,
  slow clients, duplicate disconnect and shutdown. Synchronize with channels and
  explicit synthetic time for expiry; avoid sleep-based assertions.
- Isolated PostgreSQL: clean schema, upgrade from 5, down/reapply 7, durable
  counts/cursors, same-room validation, concurrent advances, rollback, membership
  locking and leave/rejoin cascade. Tests never load local application env files.
- Formatting, full race-enabled tests, vet, build, generated Swagger validation,
  available lint and a separate review/fix pass precede the requested commit.

## Completion record

The full race suite (including isolated PostgreSQL), vet, build and generated
Swagger validation passed with Go 1.23.12 and PostgreSQL 16. Independent review
covered service authorization, transaction locks, Hub state ownership and stale
authorization. Review fixes revoke existing subscriptions on denied repeat joins,
avoid database work for already-stopped typing retries, and make revocation tests
independent of map iteration order. The final revocation regression passed 50
race-enabled runs. Detailed commands and limitations are recorded in the plan.
