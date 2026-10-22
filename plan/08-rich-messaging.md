# 08 — Rich messaging

**Implementation:** 08A search, 08B attachments/lightweight processing and 08C
mentions/inbox are implemented. Frontend completion is user-confirmed.
**Release:** pending items are tracked only in [pending.md](pending.md).

## Shipped scope

| Slice | Current behavior | Contract |
| --- | --- | --- |
| 08A | Authorized room-scoped PostgreSQL search, ID cursors, tombstone filtering | [Phase 08 spec](../spec/08-rich-messaging.md) |
| 08B | Private create-only R2 uploads, bounded v2 processing, atomic max-five file binding, signed downloads and lifecycle cleanup | [Attachment API](../docs/attachments.md), [binding spec](../spec/08b-message-attachments.md) |
| 08C | Typed mentions, idempotent sends, membership-generation private inbox, preferences and independent worker progress | [Phase 08 spec](../spec/08-rich-messaging.md) |

## Configuration and schema

- Migration head: 13. Migrations9–13 provide reservations/outbox, mentions,
  processing, transformed artifacts and message bindings.
- Upload admission defaults off. Enabling requires all R2 settings, scanner URL/
  token and both upload/scan flags true. Compose reads `configs/.env`; host Go
  execution also reads `.env.local`.
- JPEG/PNG are re-encoded; TXT is strictly validated. Verification is a processing
  policy result, not a promise of malware detection. PDF/Office/archives/SVG/HTML
  are unsupported. ClamAV is not required for this release.
- Drain old backend/cleanup binaries before accepting schema13 bound messages.
  Do not downgrade across live bindings or transformed artifacts.

## Verification and release boundary

[Verification evidence](../docs/verification.md) records local service/HTTP/DB,
worker, actual WS and provider results, with skipped/candidate-specific gates
kept explicit. Do not claim a new hosted test from frontend completion or local
runtime configuration. Re-testing R2/scanner is excluded from the current work.

[Pending](pending.md) owns the remaining release/deployment/evidence items;
this plan no longer repeats historical scanner/approval/deployment checklists.
