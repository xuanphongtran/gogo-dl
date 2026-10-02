# 08 — Search, Attachments, and Notifications Specification

**Priority:** P2

**Status:** In progress — 08A implemented and verified locally; 08B R2 foundation implemented, scanner/provider gates pending; 08C implemented locally, dedicated DB/race verification pending

**Execution plan:** [Phase 8 plan](../plan/08-rich-messaging.md)

**Depends on:** 05 membership; [06 lifecycle integration](../plan/06-message-lifecycle.md); [07 read state](./07-presence-read-state.md)

## 1. Outcome and boundaries

Members can find messages in their rooms, attach verified private files and receive
durable mention notifications while offline. PostgreSQL remains authoritative;
object storage holds file bytes. Existing text sends and ID-based history remain
compatible. Section 3 (08A) is implemented with local verification recorded in
the execution plan; the remaining attachment provider gates are future work while
the notification section is implemented locally with dedicated verification pending.

Deliver three slices: **08A search**, **08B attachments**, **08C mentions/inbox**.
Phase 6 application code is now integrated from `develop`, including its
revision/tombstone behavior. Phase 5 verification remains Pending
although its review fixes are on main. Provider proofs can begin independently;
release requires the relevant integration and verification gates.

Out of scope: global/semantic search, relevance pagination, attachment-only messages,
file-content indexing, rich HTML, link previews, public file URLs, email/push
delivery and public read receipts. External channels require a later provider
decision; the in-app worker establishes a real delivery boundary.

## 2. Proposed defaults and trust boundaries

| Area | Proposed default |
| --- | --- |
| Search | PostgreSQL full-text, `simple` dictionary, AND terms, newest ID first |
| Upload | Private Cloudflare R2; unique create-only quarantine keys; exact-byte scan and promotion |
| Types | JPEG, PNG, UTF-8 plain text; reject SVG/HTML/PDF and executable formats |
| Bounds | 10 MiB/file, 5 attachments/message, 10 distinct mentions/message |
| URL lifetime | Upload 5 minutes; download 60 seconds |
| Quota | 100 MiB/user pending+unattached; 1 GiB/user and 10 GiB/room total logical bytes |
| Retention | Unattached clean file 24 h; inbox 30 days; bound file until deletion |
| Send retry key | Required for attachment/mention sends, retained at least 24 h |

These are initial design choices, not measured capacity or provisioned services.
Provider ADR must verify exact APIs, conditional-write immutability, size constraints,
checksums, TLS, signed response headers, object deletion, scanner integration,
cost and retention. Disable attachments if a mandatory capability is unavailable.

Client controls content, query, filenames, declared size/type, IDs and retry keys.
Services derive identity, membership, object keys, scan state, versions and recipients.
Never trust supplied storage keys, public URLs, metadata or scan verdicts. All routes
require Bearer auth; handlers remain bind → service → response. Provider errors
are wrapped internally and mapped to safe categories, never returned verbatim.

## 3. Room search contract — 08A

`GET /api/v1/rooms/:id/messages/search?q=...&limit=20&before=...`

- `q`: trimmed, valid UTF-8, 1–256 bytes; reject empty/nonsearchable input with 400.
- `limit`: default 20, range 1–100. `before`: optional positive message ID.
- Require current membership, including public rooms. Public nonmember gets 403;
  inaccessible private/missing room gets 404. Membership and query use a
  transaction-consistent authorization boundary against removal.
- Parameterize `plainto_tsquery('simple', q)` against
  `to_tsvector('simple', content)`. Punctuation is text, not advanced query syntax;
  all produced terms must match. No stemming/accent folding guarantee, including
  Vietnamese; document accented examples rather than promise equivalence.
- Only this room's nondeleted messages with `id < before`, ordered ID descending.
  Response: `{"messages":[<MessageDTO>],"next_before":42}`; last returned ID if
  another match exists, otherwise `null`. No offsets, cross-room totals, ranking
  cursor or HTML highlights.
- Edits update indexed text atomically; deletion clears text and removes the
  document. A cursor is not a snapshot: concurrent edits/inserts may change matches.
  Restart pagination when changing the query.

Use a stored/generated `tsvector` with explicit configuration and GIN index excluding
tombstones; combine room/ID filters with existing indexes. Inspect representative
query plans for selective/common terms. External search requires measured evidence.
[PostgreSQL query parsing](https://www.postgresql.org/docs/16/textsearch-controls.html),
[full-text indexes](https://www.postgresql.org/docs/16/textsearch-indexes.html)

## 4. Attachment HTTP contract — 08B

### Implemented foundation and pending release gates

Migration 9 implements reservations, unverified metadata, idempotent complete/cancel,
attachment aggregate sequences, independent outbox purposes and fenced cleanup.
Public initiation validates input then returns 503 while the scanner is pending.
`ATTACHMENT_UPLOAD_ENABLED=true` fails startup. No ready/attached state, download,
scan consumer, final promotion, message binding or realtime attachment event exists
in this foundation. Nonempty message `attachment_ids` returns 503 before persistence.
The table below describes the intended complete 08B contract; its download route is
not registered yet.

Room/account cascades currently set attachment parent references to NULL, retaining
opaque object identity. The sweeper atomically creates cleanup intent and marks a
tombstone deleting after the deadline, including orphaned/scanning reservations.
This supports reservation cleanup; atomic cleanup for future attached messages and
account/lifecycle producers remains pending. Scan intents are retained without a
consumer; complete never means verified. No runtime path enables initiation yet.


IDs are positive int64 JSON numbers. Room actions require current membership;
upload state belongs to its authenticated uploader. Other members cannot inspect
or claim an unattached file (404).

| Method/path under `/api/v1` | Request | Success |
| --- | --- | --- |
| `POST /rooms/:id/attachments/uploads` | `filename`, `content_type`, `size_bytes`, `sha256`; `Idempotency-Key` | 201: ID, expiry and typed upload URL/method/required fields/headers |
| `POST /rooms/:id/attachments/:attachment_id/complete` | No client object key/verdict | 202: ID/state; idempotent enqueue/current state |
| `GET /rooms/:id/attachments/:attachment_id` | None | 200: safe metadata/state; uploader only until attached |
| `DELETE /rooms/:id/attachments/:attachment_id` | None | 204: cancel own unattached upload; repeat succeeds |
| `GET /rooms/:id/messages/:message_id/attachments/:attachment_id/download` | None | 200: signed URL and expiry |

Initiation trims/sanitizes filename (1–255 UTF-8 bytes, no controls/path separators),
validates allowlisted type, positive size ≤ limit and 64-hex SHA-256. Sign only a
server-created key/narrow upload operation. Prefer provider policies enforcing
bytes/checksum/type; prove behavior rather than assume S3-compatible support.
DB and provider calls cannot form one transaction: persist reservation, sign with
a bounded deadline and reconcile stranded reservations. Same key/body returns the
same reservation, mismatch 409; expired reservation requires a new key. Persist
expiry before returning credentials. Signing cannot extend the stored deadline.

Safe metadata: `id`, `filename`, `content_type`, `size_bytes`, `sha256`, `verified`,
`state`, `created_at`, `upload_expires_at`, nullable `expires_at`. The upload deadline
is immutable; unattached ready retention uses `expires_at`, which is null after
attachment. Before scan/verification `verified=false` identifies declared metadata,
never a trusted type/hash. Hide buckets, keys, version IDs, scanner
reports and provider errors. Invalid shape/type/size is 400; room policy above;
incompatible state/quota is 409; temporary provider dependency failure is 503.
Rejection exposes a fixed safe category. No large-file proxy route is introduced.

Download requires a live message in this room and its attached clean object;
unattached/cross-room/deleted bindings get 404. Return a short-lived URL with verified
type and safe `Content-Disposition: attachment`; bucket remains private. Signed URLs
are bearer credentials: removal cannot instantly revoke an issued URL, and a download
started before expiry may continue. The 60-second issue window is a limit, not an
immediate-revocation promise. Never persist/log URLs or put them in events.
[S3 presigned URLs](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html)

## 5. Upload, scan and cleanup invariants

States: `pending_upload → scanning → ready → attached`; failure/cancellation leads
to `rejected`, `expired` or `cancelled`, then `deleting → deleted`. Transitions use
DB compare-and-set/version checks. Complete/cancel/retries are idempotent. Complete
only enqueues bounded work, not synchronous scan. Missing bytes are retryable before
expiry. A ready file cannot be claimed twice; cancellation of attached files is 409.

1. Worker pins an immutable quarantine **object key and ETag**, checks actual length/SHA-256/type from
   bytes. HEAD/client metadata alone cannot establish type or cleanliness. Strictly
   decode allowed images, reject invalid/excessive dimensions (proposed 25 megapixels)
   with bounded decoder memory; reject invalid UTF-8/binary text.
2. Scanner processes those exact bytes. Unknown verdict, timeout/outage or unverifiable
   response fails closed: never downloadable. Bounded retries end in quarantine/
   rejection and alert. Scratch app image has no antivirus binary; use a separate
   scanner rather than executing a shell command.
3. Promote **those exact verified bytes** to a unique server-only private final key
   using create-only writes; verify destination/checksum, then mark ready with final identity. Retries never
   silently overwrite attached bytes.
4. Failed DB finalization after promotion is reconciled/cleaned. No broad provider
   listing/deletion on request paths or inside `Hub.Run`.

Cloudflare R2 has no S3 bucket versioning API. Each reservation uses a unique
`quarantine/<uuid>` key. The PUT signature binds `If-None-Match: *`, Content-Type
and Content-Length. Reuse must fail after the first successful create. Never delete
and recreate this key while upload credentials remain valid. ETag is a consistency
condition, not a SHA-256 proof: scan actual bytes and verify the declared checksum.
Future promotion must consume the verified bytes and conditionally create a distinct
final key; no unqualified copy of a mutable/latest source is acceptable.

Real R2 tests must prove concurrent create-only PUT, signed header enforcement,
size limits, CORS/browser upload, in-flight write completion and conditional reads.
These gates remain pending and upload admission remains closed.
[Cloudflare R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/),
[R2 presigned URLs](https://developers.cloudflare.com/r2/api/s3/presigned-urls/)

Logical quotas include reservations and clean/attached files. Serialize new
reservations using room/membership locks followed by user then room advisory quota
locks. Retain cancelled reservations in quota through upload expiry plus a proposed
10-minute grace, and release only after provider deletion and fenced database
acknowledgement. Current foundation caps each user at 100 MiB and 10 outstanding
reservations; room cap is 10 GiB. Physical-storage alerts/lifecycle rules and proof
of the grace period remain deployment gates.

Unattached ready files expire after 24 h. Message deletion immediately hides metadata
and atomically writes cleanup intent; private deletion retries until confirmed.
Room/account deletion schedules owned object cleanup before cascades lose keys.
Retain minimal cleanup tombstones until all related objects are removed. Storage outage
does not undo committed deletion. Document backup retention; no legal hold assumed.

## 6. Send and lifecycle compatibility

Extend `POST /api/v1/rooms/:id/messages` additively:

```json
{"content":"Please review this","attachment_ids":[123],"mention_user_ids":[7]}
```

Content stays required, trimmed and ≤ 4000 UTF-8 bytes. Arrays default empty with
distinct positive IDs and configured maxima. Mention IDs are authoritative, not
display names or `@everyone`. Recipients must currently belong to the room; self
mention is stored without notification. Invalid recipient is generic 400 without
disclosing membership elsewhere.

Feature-bearing sends require `Idempotency-Key`: 16–128 printable ASCII bytes,
scoped to authenticated user. Hash normalized content, room and sorted ID arrays.
In one transaction lock memberships/attachments in stable order, reserve key,
validate ownership/room/ready state, insert message, claim files, store mentions
and outbox intents. Any failure rolls back all DB state; competing claims are 409.

Same key/body under fresh membership authorization returns existing current
authorized MessageDTO with 201, no new claim/event/notification; another body/room
gets 409. Retain keys ≥ 24 h; later retries may create another message. Plain text
sends without keys preserve behavior. Keys are not logged; provider I/O is outside
this transaction.

Message DTOs gain safe `attachments` and `mention_user_ids` arrays, no keys/URLs.
Preserve existing fields/status/cursor and Phase 6 revision semantics. Edits change
text only, preserve bindings/original mention recipients and create no new mention
notifications. Deletion returns the Phase 6 tombstone with arrays empty, removes
mention rows and writes cleanup intent atomically. Account deletion preserves the
existing deleted-author contract, removes owned file references and advances affected
message revisions/events before cleanup; it must not leave live broken bindings.
Phase 7 read cursors/counts stay unchanged; room reads do not mark inbox read.

## 7. Mentions, preferences and private inbox — 08C

| Method/path under `/api/v1` | Request | Success |
| --- | --- | --- |
| `GET /users/me/notifications` | `before` positive ID, `limit` 1–100 (default 20), `unread_only` boolean | 200: `notifications`, `next_before` ID/null |
| `PUT /users/me/notifications/:id/read` | None | 200: own DTO with stable `read_at`; repeat idempotent |
| `GET /users/me/notification-preferences` | None | 200: `mentions_enabled` (default true) |
| `PUT /users/me/notification-preferences` | Required boolean `mentions_enabled`, including false | 200: saved preference |
| `GET /rooms/:id/notification-preferences` | None | 200: `muted` (default false) |
| `PUT /rooms/:id/notification-preferences` | Required boolean `muted`, including false | 200: saved member preference |

NotificationDTO: `id`, `kind=mention`, `room_id`, `message_id`, `created_at`, nullable
`read_at`, `availability=available|deleted`. No copied text, filename or signed URL.
Feed is ID descending and filtered by current room authorization; unknown/other
user's ID gets 404. User identity cannot be selected. Clients fetch referenced
messages via authorized room history; this phase does not assume a new detail API.
Existing notification may show `deleted` without content; workers skip creation for
an already-deleted source. Retention cleanup removes expired rows idempotently.

Add immutable DB `membership_generation` per **insertion** into `room_members`
(backfill; globally unique generated bigint suffices). Role changes preserve it;
leave/rejoin changes it. Snapshot recipient generation in mention intents; worker
matches current generation. Room preference/inbox rows are generation-scoped and
cascade on removal; rejoin starts defaults without old feed. Removing recipient
also clears their mention binding. Generation is internal, not client identity or
public receipt. Phase 9 reuses this field, not a duplicate epoch.

Evaluate current global/room preferences at delivery: disabled/muted terminally
suppresses, no backfill on re-enable. No suppression inferred from Phase 7 cursor.
Persist one in-app effect per `(event_id, recipient, channel)` uniqueness constraint.
Materialize default preference rows with insert-on-conflict before locking them;
locking an absent row cannot serialize a concurrent disable/mute. Preference writes
and workers use the same stable membership/preference lock order. Hold those locks
while inserting to serialize leave/removal and preference changes. Retries/crashes
cannot duplicate rows or resurrect old feed.

In-app row and private notification intent commit together; best-effort WS
`notification` then targets that user's sockets only, with string room ID and safe
NotificationDTO. REST recovers loss; no client command/public broadcast/SSE added.
Declare server-only events in the AsyncAPI assets now integrated from `develop`.
Use a small consumer-side delivery interface with a real in-app adapter/test fake;
external channels need separate provider/consent/retry decisions, not speculative code.

## 8. Shared durable outbox and schema

Create immutable `domain_outbox` reused by Phase 9: `event_id` UUID,
`schema_version=1`, `kind`, `occurred_at`, `aggregate_type`, `aggregate_id`,
`aggregate_seq`, nullable `aggregate_version`, optional numeric room/user targets
and bounded typed references. Mentions snapshot recipient IDs/generations, not text.
Server IDs/versions cannot be supplied by client commands.

Message create/edit/delete and membership control share room aggregate; allocate
sequence under room/counter lock in the mutation transaction. Revision is separate.
Personal read/notification events use user aggregates. Invitations use recipient
ownership and invitation revision/state, including before membership exists.
IDs/timestamps do not establish commit order; no cross-aggregate order or change
to message ID pagination. Room counter lock order/choke point needs measurement.

Independent purpose delivery/progress records serve notification, object cleanup
and future broker relay: do not destructively compete for one row. Bounded
`SKIP LOCKED` claims, fenced leases, deadlines, backoff, retry caps/dead letters
recover abandoned work. Cleanup tombstones retain opaque provider object identity
needed after cascades, server-only; other intents carry references only. No provider
I/O in claim transactions or Hub loop. Failed intent insertion rolls back mutation.

Relay requires exact resource revision at original sequence. If later edits/deletion
make payload unavailable, transport a content-free terminal-skip progress marker,
advance ordering and recover via authorized current state. Never substitute newer
content at old sequence; it could precede revocation. Notification workers use current
generation and no copied content. Retention respects every purpose's recovery horizon.
Processing is at least once, not exactly-once browser delivery.

New migration pairs cover vector/index, membership generation/uniqueness,
attachments/claims/cleanup, retry keys, mentions/preferences/inbox, outbox/progress/
counters. Assign numbers after integrated 000007; never edit 000006/000007 or
reserve conflicting versions here. Review explicit columns, constraints, FK actions
and actual query indexes. Backfills/uniqueness precede dependent writers.

## 9. Operations, verification and release gates

Proposed settings: `ATTACHMENTS_ENABLED=false`, endpoint/region/private buckets/
credentials, scanner endpoint/deadline/credentials, type/size/quotas, URL TTLs,
cleanup grace, bounded worker batch/concurrency/lease/retry and retention. Mention
workers have an explicit enable gate. Enabled features require valid dependencies;
disabled upload admission returns 503 and issues no credentials, while existing
authorized retrieval and cleanup continue. Add Config validation/tests,
`.env.example`, container wiring and README with implementation. Redact credentials,
URLs/filenames; metric labels are bounded kind/state/result, not user/room/key IDs.

ADR includes retention/recovery/ownership and pinned-version malware overwrite
proof. Isolated PostgreSQL/provider tests cover clean/upgrade schema, rollback,
lock order, claims, generation reset and cleanup. Fake scanner/store tests aid
deterministic failures; they do not satisfy the actual scan/provider gate.

- [ ] Search auth, deletion/edit indexing, UTF-8/cursor behavior verified.
- [ ] Upload overwrite/version/size/type/hash/scan fail-closed proofs pass.
- [ ] Quotas, duplicate completion/claims, cancellation/expiry/orphans/GC pass.
- [ ] Send retries and rollback preserve all-or-nothing state.
- [ ] Mention generation/preferences/leave races, inbox ownership/retention pass.
- [ ] Independent outbox purposes, fenced leases/retries/crash recovery pass.
- [ ] Race/vet/build and affected Swagger/AsyncAPI checks pass where integrated.
- [ ] Provider, retention, rollout/rollback and remaining risks reviewed.

Release search first, upload admission off until store/scanner proof, then mentions
with staged workers. Rollback stops new admission; retain retrieval, cleanup,
reservations and intents until drained. No production down migrations. Drafting
satisfies none of these implementation checkboxes.
