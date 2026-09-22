# 03 — WebSocket Authorization and Protocol Safety Specification

**Related plan:** [plan/03-websocket-authorization.md](../plan/03-websocket-authorization.md)  
**Status:** Proposed  
**Priority:** P0

## Objective

Ensure a WebSocket client can observe room events only after server-side
authorization and cannot fabricate durable or server-owned events.

## Trust boundaries

The server trusts only:

- the authenticated user ID attached during JWT validation;
- room membership returned by the chat service/repository;
- message and lifecycle fields constructed by server code after validation and
  persistence.

The server must not trust client-provided `user_id`, `username`, timestamps,
message IDs, membership state, or event types.

## Protocol contract

### Client-to-server commands

The first implementation supports only these commands:

```json
{ "type": "join", "room_id": "123" }
{ "type": "leave", "room_id": "123" }
```

Rules:

- `room_id` must be a positive base-10 `int64`; aliases, empty strings, and
  oversized values are rejected.
- Unknown event types are rejected with a stable error event.
- Client payloads cannot contain or override server-owned identity or message
  fields.
- Client WebSocket messages do not create chat messages. Message creation
  continues through the authenticated chat service, which persists first and
  broadcasts second.

### Server-to-client events

Existing event names remain available where applicable: `message`, `join`,
`leave`, `user_list`, and `error`.

Protocol errors use this stable shape:

```json
{
  "type": "error",
  "room_id": "123",
  "payload": {
    "code": "forbidden",
    "message": "you are not allowed to access this room"
  }
}
```

Stable error codes include:

- `invalid_command`
- `invalid_room_id`
- `unsupported_event`
- `forbidden`
- `not_member`
- `payload_too_large`
- `server_shutdown`

Messages must remain client-safe and must not expose SQL, JWT, or internal
implementation details.

## Authorization architecture

Use pre-authorized subscription commands:

1. The client sends `join`.
2. Input is decoded and validated outside the Hub state mutation path.
3. A chat-domain authorizer checks room existence and membership using the
   connection's authenticated user ID.
4. Only an approved command is sent to the Hub event loop.
5. The Hub mutates its room maps and emits the server-generated join event.

The authorization lookup must not execute database I/O inside `Hub.Run`.
The implementation may use a callback/worker associated with the connection,
but the Hub remains the sole owner of `clients`, `rooms`, and `Client.rooms`.

Leaving a room is limited to rooms the connection has actually joined. A leave
for another room is harmless but must not create membership state.

## Membership revocation

After a durable membership removal commits, the chat service sends a control
command to the Hub. The Hub removes all matching active subscriptions in its
event loop and emits no further room events to those clients.

The documented consistency bound is: revocation takes effect after the
membership transaction commits and the Hub processes its control command. The
control path is separate from best-effort message broadcast and must report a
shutdown or enqueue failure to the caller/logging system.

## Connection and concurrency invariants

- Exactly one reader and one writer operate on each WebSocket connection.
- Only the Hub event loop mutates in-memory connection and room maps.
- The Hub never performs database or network I/O.
- Outbound delivery remains non-blocking for the Hub; slow clients may lose
  best-effort events and are logged/measured.
- Channel close ownership is centralized and shutdown is idempotent.
- Read and write goroutines terminate within bounded deadlines on disconnect
  and shutdown.

## Input limits and failure behavior

- Enforce the configured WebSocket read limit before decoding.
- Reject malformed JSON and unsupported shapes with `invalid_command`.
- Do not broadcast malformed, unauthorized, or client-fabricated events.
- Repeated protocol violations may close the connection using a documented
  close code; the first slice must at least reject them without panicking or
  leaking goroutines.

Origin policy, HTTP request limits, and rate limiting are specified in plan 04;
this plan must not treat the existing CORS middleware as WebSocket origin
authorization.

## Required tests

- Member can join and leave an authorized room.
- Non-member receives `forbidden` and is not added to the room.
- Unknown event and malformed/invalid room ID receive stable errors.
- Client cannot forge a persisted message or server identity fields.
- Membership revocation removes an active subscription.
- Slow consumer does not block Hub processing.
- Disconnect, duplicate unregister, and shutdown are race-safe.
- No database call occurs on the Hub event loop.

## Acceptance criteria

- Non-members cannot join, observe, or publish to a room.
- Client-originated events cannot choose authoritative identity or lifecycle
  fields.
- Invalid commands are rejected safely with stable error events.
- Revocation behavior meets the documented consistency bound.
- Hub state ownership and one-reader/one-writer invariants remain intact.
- All changed WebSocket code passes `go test -race -count=1 ./...`.

## Out of scope

- WebSocket origin allowlists and connection rate limits; plan 04.
- Durable message edit/delete events; plan 06.
- Cross-instance authorization/cache invalidation; plan 09.
