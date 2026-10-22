# API guide

REST routes use `/api/v1`. Swagger (`/swagger/index.html` outside production)
contains endpoint/request/response schemas; [AsyncAPI](asyncapi.yaml) defines WS
messages. [Attachments](attachments.md) documents file uploads and downloads.

## Authentication and browser policy

Register/login return access and refresh tokens plus access expiry. Refresh before
expiry and replace both tokens. Private REST requests use `Authorization: Bearer`.
Avatar URLs must be absolute HTTP(S) URLs.

Set exact frontend origins in both CORS and WS allowlists. WebSocket origin checks
are separate from CORS. Browsers may send `Idempotency-Key`; CORS exposes
`Retry-After` and `X-Request-ID`. Respect rate-limit cooldowns.

## Membership and privacy

Authenticated users discover/join public rooms; private rooms require invitations.
History/search/presence/read state/WS subscriptions require current membership.
Public nonmembers get 403; private nonmembers/missing rooms get 404. Public room
metadata can be discoverable with `role:null`. Members have owner/moderator/member
roles. Only the owner may transfer ownership and must do so before leaving.

A re-invite after removal creates a new pending invitation. Old accepted invites
cannot restore missing membership (409); retries remain successful while the
membership exists. Rejoin creates a new membership generation: previous read
state and mention inbox do not reappear. Leave on an already-left public room is
idempotent 204; inaccessible private rooms remain404.

Services persist authorization-sensitive mutations under transaction locks before
emitting events. Removal waits for an already-authorized protected read to finish;
subsequent reads are denied. REST remains authoritative after missed WS controls.
See [membership spec](../spec/05-room-membership-roles.md).

## Messages, pagination and retries

- History uses descending immutable message IDs and exclusive positive `before`
  cursors. It retains tombstones. Do not implement offset or relevance pagination.
- Text is trimmed, valid UTF-8 and at most 4000 **bytes**. Text-only sends must be
  nonblank; sends/edits may be blank while files remain. Request-body overflow
  is 413, semantic content errors 400.
- Messages include `revision`, `edited_at`, `deleted_at`, `attachments` and
  `mention_user_ids`. Collections are arrays, not null. Usernames come from DB.
- Authors who remain members may edit without a time limit. PATCH sends content
  and revision. Equal/current text or immediate identical retry at the previous
  revision returns the same result; other stale revisions 409. Only authors edit.
- Authors, owners and moderators can delete under the current room policy.
  DELETE returns 200/tombstone. Authorized repeats do not change revision/time or
  emit another event. No restore/edit-history endpoint exists.
- Attachment/mention sends require a 16–128 printable-ASCII `Idempotency-Key`.
  Same normalized body/key returns the original authorized message; conflicting
  body/claim 409. Keys last at least 24h, not forever. Use separate keys for upload
  reservations and message sends. File binding is atomic and limited to five.

Search uses PostgreSQL `simple` full text:1–256 UTF-8 bytes, all terms, no accent
folding/stemming guarantee or query operators. Tombstones are excluded; edits
change matches. Search cursors are not snapshots; restart pagination when `q`
changes. File content is not indexed; file-only messages have no searchable caption.

## Presence, typing and read state

Presence means a room subscription in this process, aggregated across a user's
tabs. Presence snapshots use string `room_id`; personal REST read state uses a
numeric `room_id`, `last_read_message_id` and `unread_count`.

PUT read state advances only to the maximum valid message ID. GET/history/send
and joining do not implicitly mark read. Unread excludes own messages but counts
older room history, deleted-author messages and tombstones by ID. Leave/removal
clears the cursor; rejoin starts at 0. Private `read_state` events reach that user's
connections only. Refresh REST counts after reconnect; an old event's count may
be stale. There are no public read receipts.

## Mentions and inbox

Send typed `mention_user_ids`, not parsed usernames. Recipients must be current
members; invalid IDs get a generic 400. Self-mentions do not notify. Edits retain
the original recipients; deletion/removal suppresses stale effects. The worker
uses membership generation and independent outbox progress.

`GET /users/me/notifications` is an owned, ID-descending cursor feed with optional
`unread_only`. Marking a notification read is idempotent and does not move the
room read cursor. Global/room preferences accept explicit false booleans; defaults
are mentions enabled and room unmuted. Inbox retention is 30 days. Notifications
contain references/availability, not copied message text or signed file URLs.

## WebSocket

Connect to `/api/v1/ws`, using a Bearer header where supported, or `?token=` for
browser clients. Query tokens are credentials: never log full connection URLs.
Send one JSON command per frame, for example:

```json
{"type":"join","room_id":"1"}
```

Client commands are `join`, `leave`, `typing_started`, `typing_stopped`.
WS leave changes only the subscription; REST controls durable membership. Durable
chat messages are sent through REST, not fabricated WS mutation events.

A server text frame may contain several newline-separated JSON envelopes. Parse
each event. Envelope room IDs are strings; Message payload room IDs are numbers.
`message`, `message_updated`, `message_deleted` carry the same complete Message DTO.
Apply only newer revisions; delivery/order is best effort and needs REST recovery.

Typing requires a joined/authorized socket, is throttled to one start per second,
expires after 5s and is swept within 250ms. Refresh about every 2s. Stops have null
expiry. `authorization_pending`, `not_joined` and `rate_limited` are protocol
errors clients must handle. Leave/disconnect/removal clear that connection's state.

Reconnect, join authorized rooms again, then fetch snapshots/history/inbox/read
state. Restart resets ephemeral state. SIGTERM normally sends WS1001 within the
shared drain budget. One instance is required; there is no broker/cross-node
fanout or browser-delivery ACK guarantee in this release.
