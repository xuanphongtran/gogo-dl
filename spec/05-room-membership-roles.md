# 05 — Room Membership and Roles Specification

**Related plan:** [plan/05-room-membership-roles.md](../plan/05-room-membership-roles.md)
**Status:** Implementation complete; PostgreSQL verification pending
**Priority:** P1
**Depends on:** 02, 03, 04

## Objective

Replace the current boolean room-membership model with an explicit access and
moderation model. Users must be able to discover and join public rooms, receive
invitations to private rooms, and manage membership without bypassing the
service authorization boundary. The database remains the source of truth;
WebSocket state is revoked or updated only after the durable mutation commits.

## Decisions

The open decisions in the implementation plan are resolved as follows:

- Public rooms are discoverable and can be joined by any authenticated user.
- Message history and WebSocket subscription require current membership for
  both public and private rooms. A public room is discoverable, not readable
  without joining.
- An owner must transfer ownership before leaving or deleting their account.
  There is no archival-owner state in this phase.
- Room visibility is selected at creation time and is immutable in this phase.
  Visibility changes require a later, separately specified migration and
  revocation policy.
- A removal revokes the member's current access and active subscriptions.
  In a public room, the user may explicitly join again after removal; a
  removal is not a permanent ban.

## Scope

In scope:

- Public/private room visibility.
- `owner`, `moderator`, and `member` roles.
- Durable invitations with accept and decline transitions.
- Public self-join, self-leave, moderator removal, and ownership transfer.
- Room/member authorization for room details, history, writes, and WebSocket
  subscription.
- Transactional membership, invitation, role, and ownership mutations.
- Durable membership events followed by best-effort real-time delivery.
- HTTP, WebSocket, migration, service, handler, repository, and race tests.

Out of scope:

- Permanent bans, mute state, invite links, email notifications, or bulk
  invitations.
- Changing room visibility after creation.
- Cross-instance membership revocation or distributed event delivery.
- Message edit/delete, presence, typing, read state, attachments, and search.

## Domain model

### Room visibility

`visibility` is one of:

- `public`: appears in the authenticated room list and any authenticated user
  may request membership with `POST /rooms/:id/join`.
- `private`: appears only to current members in room listings and cannot be
  joined without an accepted invitation.

Visibility is server-owned. Clients cannot set or override it after room
creation. Existing rooms are migrated as `public` for backward compatibility.

### Roles

`role` is one of:

- `owner`: exactly one member per room; may perform all room administration.
- `moderator`: may invite users and remove regular members; cannot transfer
  ownership, change roles, or remove owners/moderators.
- `member`: may read room content, send messages, and leave.

The existing `rooms.created_by` column remains the durable owner user ID for
backward-compatible model and query fields. It must equal the sole
`room_members` row whose role is `owner`. New code must treat this equality as
an invariant, not as a client-provided field.

### Invitation states

An invitation has one of these states:

- `pending`: invitee may accept or decline.
- `accepted`: the invitee is a room member; repeated accept is idempotent.
- `declined`: the invitee is not a member; a later authorized invite may reset
  this invitation to `pending`.

There is at most one pending invitation for a `(room_id, invitee_id)` pair.
Inviting an existing member or retrying an existing pending/accepted invitation
is idempotent and does not create duplicate rows or duplicate events.

## Authorization matrix

The authenticated user ID comes only from JWT middleware. `target_user_id` and
path IDs are untrusted input and are checked by the service.

| Operation | Anonymous | Public non-member | Member | Moderator | Owner |
|---|---:|---:|---:|---:|---:|
| List public rooms | 401 | allowed | allowed | allowed | allowed |
| List a private room | 401 | hidden | allowed | allowed | allowed |
| Read room details | 401 | public only | allowed | allowed | allowed |
| Read message history | 401 | forbidden | allowed | allowed | allowed |
| WebSocket subscribe | 401 | forbidden | allowed | allowed | allowed |
| Send a message | 401 | forbidden | allowed | allowed | allowed |
| Join a public room | 401 | allowed | idempotent | idempotent | idempotent |
| Join a private room | 401 | forbidden | forbidden; use invitation | forbidden; use invitation | forbidden; use invitation |
| Invite a user | 401 | forbidden | forbidden | allowed | allowed |
| Accept/decline own invitation | 401 | allowed for own invite | allowed | allowed | allowed |
| Leave the room | 401 | forbidden | allowed | allowed | transfer first |
| Remove a regular member | 401 | forbidden | forbidden | allowed | allowed |
| Promote/demote moderator | 401 | forbidden | forbidden | forbidden | allowed |
| Transfer ownership | 401 | forbidden | forbidden | forbidden | allowed |
| List room members | 401 | forbidden | allowed | allowed | allowed |

For private-room reads, return the existing safe not-found response to users
who are not members so private room existence is not disclosed. Mutation
authorization failures use the stable forbidden response unless the resource
does not exist. A removed user loses current history, send, member-list, and
WebSocket access immediately after the durable removal is committed.

## HTTP contract

All routes are under `/api/v1` and require the existing JWT authentication
middleware unless stated otherwise. Request bodies contain no server-owned
identity fields.

### Room creation

`POST /api/v1/rooms`

Request:

```json
{
  "name": "engineering",
  "visibility": "private"
}
```

`visibility` is optional and defaults to `public` to preserve existing clients.
Only `public` and `private` are accepted. The creator is inserted as the sole
owner in the same transaction as the room. The response remains `201 Created`
and includes the existing room fields plus `visibility` and the caller's
`role: "owner"`.

### Room listing and details

`GET /api/v1/rooms` returns public rooms and private rooms where the caller is
a member. Each room view includes:

```json
{
  "id": 10,
  "name": "engineering",
  "created_by": 7,
  "created_at": "2026-01-01T12:00:00Z",
  "visibility": "private",
  "role": "member"
}
```

For a public room viewed by a non-member, `role` is `null`. `GET
/api/v1/rooms/:id` follows the same visibility rule but does not grant access
to history or WebSocket events.

### Joining and leaving

`POST /api/v1/rooms/:id/join` joins a public room. It preserves the existing
successful response shape:

```json
{ "message": "joined" }
```

Joining an already joined public room returns `200` with the same body. A
private room returns `403`; accepting an invitation is the only path that
creates private-room membership.

`DELETE /api/v1/rooms/:id/membership` leaves the room. It is idempotent for a
non-member only when the room is otherwise accessible; an owner receives
`409` with `owner transfer required` until ownership is transferred. A
successful leave returns `204 No Content` and revokes active WebSocket
subscriptions after the transaction commits.

### Invitations

`POST /api/v1/rooms/:id/invitations` is available to owners and moderators.

Request:

```json
{ "user_id": 42 }
```

The target must be an existing user and cannot be the inviter. A new pending
invitation returns `201 Created`; an existing pending, accepted, or membership
equivalent request returns `200` with the current invitation state. No
duplicate pending invitation or duplicate event is created.

`GET /api/v1/users/me/invitations?status=pending&limit=50` lists invitations
owned by the authenticated user. `status` accepts `pending`, `accepted`,
`declined`, or `all`; `limit` defaults to 50 and is capped at 100. The
response includes invitation ID, room ID/name, inviter ID, status, and
timestamps, but never password, email, or internal database details.

`POST /api/v1/invitations/:id/accept` atomically marks a pending invitation as
accepted and inserts membership. Repeating accept after success returns `200`
without duplicate membership or event. `POST
/api/v1/invitations/:id/decline` atomically marks a pending invitation declined.
Only the invitee may call either action. Accepting/declining a non-pending
invitation returns `409` except for the idempotent repeated-success case.

### Membership administration

`GET /api/v1/rooms/:id/members` is available to current members. It returns
user ID, username, role, and joined timestamp; it does not return email,
password, or private profile data.

`DELETE /api/v1/rooms/:id/members/:user_id` removes a regular member. Owners
may remove members and moderators; moderators may remove regular members only.
The owner cannot be removed. A successful removal returns `204`, commits the
membership deletion before revoking the target's active subscriptions, and
emits a membership change event after commit.

`PATCH /api/v1/rooms/:id/members/:user_id` changes a member role. The body is:

```json
{ "role": "moderator" }
```

Only the owner may set `moderator` or `member`. The endpoint cannot change the
owner role; ownership uses the dedicated transfer endpoint. Repeating a role
assignment is idempotent.

`POST /api/v1/rooms/:id/ownership` transfers ownership to an existing member.
The body is:

```json
{ "user_id": 42 }
```

The current owner becomes a moderator and the target becomes the sole owner.
The room owner field and both membership role changes commit in one
transaction. The target must not be the current owner and must already be a
member.

## HTTP errors

Use the existing `apperror.Respond` mapping and safe bodies:

| Condition | Status | Error |
|---|---:|---|
| Invalid room/user/invitation ID or body | 400 | `invalid request` |
| Missing/invalid JWT | 401 | existing auth error |
| Authenticated but action is not allowed | 403 | `forbidden` |
| Non-member reads a private room | 404 | `not found` |
| Target room/user/invitation does not exist | 404 | `not found` |
| Invalid invitation state or owner leave | 409 | stable conflict message |
| Repository/transaction failure | 500 | `internal server error` |

Do not expose SQL constraint names, invitation existence to unrelated users,
or whether a private room ID is valid when the caller is not a member.

## Database and transaction contract

Add a new migration pair; do not edit migrations `000001`–`000003`.

### Schema changes

- Add `rooms.visibility TEXT NOT NULL DEFAULT 'public'` with a check for
  `public` or `private`.
- Add `room_members.role TEXT NOT NULL DEFAULT 'member'` with a check for
  `owner`, `moderator`, or `member`.
- Backfill each existing room creator's membership as `owner`; fail the
  migration if the owner invariant cannot be established.
- Add a partial unique index enforcing at most one `owner` per room.
- Add a deferred database constraint trigger requiring exactly one owner per
  room and requiring that owner to match `rooms.created_by` after each
  transaction.
- Add `room_invitations` with `id`, `room_id`, `invitee_id`, nullable
  `invited_by`, `status`, `created_at`, `updated_at`, and `responded_at`.
  Room and invitee deletion cascades; a deleted inviter is represented as
  nullable history.
- Add a partial unique index allowing at most one `pending` invitation for a
  room/invitee pair.
- Add indexes for member-by-room, member-by-user, pending invitee lookup, and
  invitation room lookup based on the actual query predicates.

The migration down direction drops invitations and the new indexes/columns in
dependency order. It is a schema rollback only; it must clearly document that
new role, visibility, and invitation data cannot be represented by the old
schema.

### Transaction boundaries

The repository owns transactions for:

1. room insert plus owner membership insert;
2. invitation create/retry state transition;
3. invitation accept plus membership insert;
4. invitation decline state transition;
5. leave/remove plus any owner/role invariant checks;
6. role change;
7. ownership transfer, including room owner and membership role updates.

Use row locks (`FOR UPDATE`) for invitation state transitions and ownership or
role mutations. Check `RowsAffected` and map unique/foreign-key violations to
stable application errors. PostgreSQL must commit before the service emits a
corresponding WebSocket event or asks the Hub to revoke a subscription.

The service, not the repository, owns authorization. Repository methods may
enforce database invariants but must not decide whether a moderator may remove
another user.

## WebSocket contract

The existing `join` command remains the subscription request. Its room
authorization now requires current durable membership for both visibility
values. A public non-member must call the HTTP join endpoint first. A private
non-member receives the existing safe `forbidden` protocol error.

Add these server-generated event types:

```text
membership_changed
invitation
```

Membership event shape:

```json
{
  "type": "membership_changed",
  "room_id": "10",
  "payload": {
    "action": "role_changed",
    "user_id": 42,
    "role": "moderator",
    "actor_user_id": 7
  }
}
```

Allowed actions are `joined`, `left`, `removed`, `role_changed`, and
`ownership_transferred`. Identity, action, role, and timestamps are always
constructed by the server. Room members receive room-scoped changes; an
invitation event is delivered only to the invitee's active connections.

Invitation event shape:

```json
{
  "type": "invitation",
  "room_id": "10",
  "payload": {
    "invitation_id": 91,
    "action": "created",
    "status": "pending"
  }
}
```

The event is best effort. The durable invitation or membership remains the
source of truth and is available through HTTP if the recipient is offline.
After removal or leave, the service invokes the Hub's owner-controlled
revocation path. The target may receive one final `membership_changed` or
`membership_revoked` control event, then must no longer receive room events.
The revocation path must not perform database I/O in `Hub.Run`.

## Testing requirements

### Service and repository

- Table-driven authorization tests for every row in the matrix.
- Public join succeeds; duplicate join is idempotent; private join is denied.
- Create-room transaction rolls back if owner membership insertion fails.
- Invitation create, retry, accept, decline, and invalid-state transitions are
  idempotent or conflict as specified.
- Accept creates membership atomically; concurrent accepts create one member.
- Owner cannot leave or be removed; transfer changes both owner records
  atomically; failed transfer rolls back.
- Moderator boundaries prevent role changes, ownership transfer, and removal
  of moderators/owners.
- Removed members lose service authorization and trigger Hub revocation.
- Repository queries use explicit columns, placeholders, row locks where
  required, and check affected rows.

### HTTP

Use `httptest` to cover:

- public/private room creation and default visibility;
- list/detail/history visibility;
- join, leave, invite, accept, decline, remove, role, and transfer paths;
- unauthenticated, forbidden, hidden-private-room, not-found, conflict, and
  dependency-failure responses;
- stable response fields and absence of sensitive user data.

### Database and WebSocket

- Apply the migration to a clean database and upgrade from migration 000003.
- Verify the owner backfill, unique owner constraint, pending invitation
  constraint, rollback behavior, and transaction rollback paths.
- Verify a non-member cannot complete a WebSocket join for either visibility,
  and a removed member is revoked after commit.
- Verify membership events are emitted only after durable success and are not
  fabricated from client payloads.
- Run all changed Hub/client code with `go test -race -count=1 ./...`; use
  finite channel-based deadlines rather than sleeps.

## Acceptance criteria

- Every room action has an explicit, tested authorization rule.
- Every room has exactly one durable owner represented by both `rooms.created_by`
  and an owner membership row.
- Public joins and invitation retries are idempotent.
- Private rooms are not disclosed to non-members through list/detail/history
  responses.
- Removed users lose current protected HTTP and WebSocket access after the
  documented post-commit revocation bound.
- Membership, invitation, role, and ownership changes are transactional.
- Durable state is committed before membership events or revocation commands
  are emitted.
- Schema, API, WebSocket events, documentation, and tests agree.

## Operational and compatibility notes

- Existing rooms become public and existing memberships become regular members,
  except each room creator is backfilled as owner.
- Existing successful public room creation, join response, message response,
  and cursor pagination remain compatible; new fields are additive.
- A new configuration value is not required for this phase.
- Log actor, room, target, invitation, and request IDs with structured fields;
  never log passwords, JWTs, invitation secrets, or private profile data.
- Monitor failed post-commit Hub revocations and event drops separately from
  database mutation failures.
