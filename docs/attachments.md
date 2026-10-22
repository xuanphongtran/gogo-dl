# Message attachments: frontend contract

Implemented in schema13. A service supports this contract only after deploying
a compatible backend through schema13. Lightweight processing
is supported; ClamAV is deferred indefinitely and is not required.

All API calls below use `/api/v1` and `Authorization: Bearer <access_token>`.
The caller must remain a member of the room. Files stay in private R2 storage.

## Upload and send

1. POST `/rooms/{room_id}/attachments/uploads` with `Idempotency-Key` and
   `{filename, content_type, size_bytes, sha256}`. Allowed: JPEG/PNG/TXT, 1 byte–10 MiB.
   Use the original bytes for size and SHA-256. The key is 16–128 printable ASCII
   characters and should identify this upload attempt.
2. PUT raw bytes directly to the returned `upload_url`, using the returned headers.
   Never send the user JWT to R2. Browser Content-Length is automatic. A repeated
   successful create-only PUT returns412; do not overwrite the object.
3. POST `/rooms/{room_id}/attachments/{attachment_id}/complete`;202 acknowledges
   processing, not verification. Poll GET of that attachment until `ready` or
   `rejected`. Processing outages fail closed. `verified` means policy passed,
   not an antivirus guarantee.
4. POST `/rooms/{room_id}/messages` with a **message-specific** `Idempotency-Key`:

   ```json
   {"content":"Here is the file","attachment_ids":[123],"mention_user_ids":[]}
   ```

   Up to five distinct positive IDs; all must be your own ready/unexpired files
   in this room. Empty content is allowed with files. The transaction either
   claims every file and creates the message or changes nothing. A file can be
   attached once. Retry with the same key/body after a lost response; a different
   normalized body or incompatible claim returns409. Missing/foreign files404.
   Text-only messages retain their existing validation and optional retry key.
   Mentions continue to require a key as before.

Send responses, history, search and WS `message`, `message_updated`,
`message_deleted` payloads use the same Message DTO. `attachments` is always an
array, with processed output metadata:

```json
{"attachments":[{"id":123,"filename":"photo.png","content_type":"image/png","size_bytes":8421,"sha256":"<64 hex characters>","verified":true}]}
```

No bucket, object key or signed URL appears there. Image re-encoding may change
size/hash. EXIF orientation/color metadata is not preserved, so presentation may
change. Attachment GET describes original input before binding and processed
output after binding. Do not assume the original hash still identifies output.
Search indexes caption text, not file content. Editing changes only the caption;
it may be blank if attachments remain. File-only messages with no caption will
not match text search.

## Download

GET `/rooms/{room_id}/messages/{message_id}/attachments/{attachment_id}/download`:

```json
{"download_url":"<short-lived private R2 URL>","expires_at":"<UTC timestamp>"}
```

The URL permits GET for60 seconds, with the verified Content-Type, safe
`Content-Disposition: attachment` and `Cache-Control: private, no-store`.
Do not persist/log/cache this bearer credential. Obtain it on demand. The API
response itself is also `no-store`. The downloader must currently belong to the
room and the message/binding must be live. A previously issued URL cannot be
instantly revoked; a request begun before expiry may finish later.

Use the URL for download navigation. If fetching R2 bytes in browser JavaScript,
allow your exact frontend origin and GET in bucket CORS; allow PUT, Content-Type
and If-None-Match for uploads. Expose Content-Disposition if JavaScript reads it.
Never make the bucket public.

## Retention and errors

- Ready/unattached files expire after24h; bound files remain while the message is
  live. Attached files cannot be independently completed/canceled:409.
- Message deletion removes attachment metadata and atomically schedules physical
  cleanup. Account deletion removes that uploader's file metadata, advances
  affected message revisions and revokes subscriptions before best-effort refresh.
  Historical text messages retain existing deleted-user behavior; a file-only
  message can become blank after its uploader is deleted.
- Cleanup waits at least the upload expiry plus10-minute grace and the60-second
  download window. Quota remains charged until physical deletion succeeds.
- Logical quotas use original bytes:100 MiB/10 pending unattached files per user,
  1 GiB total per user,10 GiB total per room.
- Invalid request400; missing/invalid auth401; public-room nonmember403,
  private-room nonmember404; missing/deleted binding404; claim/key conflict409;
  unavailable storage503. Do not retry a409 using a new key without reconciling
  the original message/upload state.

## Rollout

1. Back up the target DB. Stop/drain all older backend and cleanup processes
   before applying schema13 and accepting attachment messages. Old binaries do
   not implement attached retention/cancel behavior; do not overlap versions.
2. Deploy this backend; startup embeds/applies migration13. Keep existing R2,
   processor, upload and scan configuration. No new dependency/service/env is needed.
3. Verify health, ready upload, send with files, authorized and forbidden download,
   history/search/WS metadata, exact retry, caption edit and deletion cleanup on staging.
4. Watch processing/cleanup dead letters and orphan quota. The schema13 down
   migration refuses live bindings. Do not force production rollback; retain the
   schema-compatible binary or plan an explicit cleanup/recovery procedure.

Swagger: `/swagger/index.html`; AsyncAPI: `/asyncapi/` when documentation is enabled.
These URLs reflect only the deployed version, not uncommitted local changes.

Current release scope and open operation work are tracked in
[release preparation](release.md) and [pending](../plan/pending.md). Frontend
completion is user-confirmed; this is the current contract, not a new test report.
