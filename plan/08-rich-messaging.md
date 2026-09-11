# 08 — Search, Attachments, and Notifications

**Priority:** P2  
**Status:** Proposed  
**Depends on:** 05, 06, 07

## Goal

Make conversations discoverable and useful when users are offline while preserving security and storage boundaries.

## Scope

- Add room-scoped message search with membership enforcement.
- Add attachment metadata and an object-storage upload workflow.
- Add mentions and durable notification records.
- Add notification preferences and a pluggable delivery interface.
- Define retention, content-type, size, and malware-scanning policies.

## Acceptance criteria

- [ ] Search never returns content from unauthorized rooms.
- [ ] Upload credentials are short-lived and scoped to one authorized operation.
- [ ] Application servers do not proxy large files without an explicit reason.
- [ ] Notification creation is idempotent and decoupled from request latency.
- [ ] Deleted or inaccessible messages are handled consistently in search and notifications.
- [ ] Storage limits, cleanup, and failure recovery are documented and tested.

## Open decisions

- PostgreSQL full-text search versus an external search service.
- Supported object store and notification providers.
