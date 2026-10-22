# 08B completion: message bindings and authorized downloads

**Status:** implemented in schema13. Lightweight processing is supported;
ClamAV is deferred indefinitely and is not a release gate. Current rollout and
verification work is tracked in [pending](../plan/pending.md).

## Contract

POST /api/v1/rooms/:id/messages accepts existing attachment_ids, max5 distinct
positive IDs, normalized ascending; Idempotency-Key required for file sends.
Content may be blank only with files; existing text-only validation unchanged.
Claim only own ready/unexpired files in this room, once. Room membership and
idempotency are checked inside the same transaction as message, bindings, mention
and broker intents. Retry of normalized body/key returns current authorized DTO;
changed body/key or already-claimed/incompatible file409. Foreign/missing file404.
No duplicate event on retry. Cross-user/cross-room metadata is never fabricated.

Message.attachments is a typed list of id, filename, processed content_type,
size_bytes and sha256, verified=true (processing passed, not antivirus guarantee).
Send/history/search/message and message_updated WS payloads share the DTO, without
storage identity/URLs. Tombstones return no attachments. Attachment-only messages
are not searchable by file content. Edit still changes text only, keeping files.

GET /rooms/:id/messages/:message_id/attachments/:attachment_id/download returns
{download_url, expires_at}; live membership, live message in room, attached binding
and complete processed metadata required.404 for invalid bindings; room policy
keeps403 public/404 private nonmembers. Private R2 GET signed60s with verified
Content-Type, safe Content-Disposition attachment and private/no-store headers.
No signed URL in logs/events or message DTO. Already issued bearer links are not
instantly revocable; request begun before expiry may continue.

Schema13 adds nullable message_id FK (SET NULL preserves cleanup tombstones),
attached state and processed metadata requirement. New bound-aware constructors
are wired only in production composition after migrations; existing older-schema
fixtures/constructors remain supported. Keep original source quota/hash immutable.
100MiB/10 pending-unattached per user;1GiB total per user and10GiB per room.
Bound files do not expire after24h. Cancel/complete on attached409; Get attached
metadata requires current room membership and a live bound message.

Deleting message schedules cleanup atomically and clears public binding via state;
account deletion pins user/room/member/message/attachment locks, schedules all
owned file cleanup before cascade, increments affected live-message revisions and
writes content-free update intents. Best-effort WS refresh after commit; REST is
source of truth. Room/message hard-delete cascades retain original key; sweeper
reconciles attached orphans. Cleanup waits upload expiry+grace and60s download
window, retains quota until physical deletion, deletes quarantine+final.

## Verification

Unit validation/idempotency/HTTP/auth/download signing; isolated PostgreSQL13
upgrade/backfill/down guard, concurrency/rollback/expiry/revoke/cancel/delete,
projection parity and quota. Full race/vet/build, Swagger+AsyncAPI regeneration.
Real R2 signed download and full hosted smoke after deployment when possible;
do not claim hosted proof for local binaries. Rollback refuses live bound state.

## Operational decisions

Stop/drain older backend and cleanup binaries before schema13 binding traffic;
they do not preserve attached retention/cancel semantics. Down refuses live
bindings; never force production downgrade. Account authorization cleanup after
commit uses an owned synchronous5s context independent of HTTP cancellation;
optional database refresh keeps the request context. Existing deleted-user history
is preserved even if a file-only message becomes blank after account deletion.
See [FE contract and rollout](../docs/attachments.md).
