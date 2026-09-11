# 05 — Room Membership and Roles

**Priority:** P1  
**Status:** Proposed  
**Depends on:** 02, 03, 04

## Goal

Provide a complete room access model for private and moderated conversations.

## Scope

- Add public/private room visibility.
- Add owner, moderator, and member roles.
- Add invite, accept, decline, leave, remove-member, and transfer-ownership flows.
- Restrict room details, history, and WebSocket subscription according to membership and visibility.
- Emit durable membership changes followed by real-time events.

## Acceptance criteria

- [ ] Every room action has an explicit authorization matrix.
- [ ] A room always has a valid owner or a documented archival state.
- [ ] Duplicate joins and invite retries are idempotent.
- [ ] Removed members lose HTTP and WebSocket access.
- [ ] Membership and ownership changes are transactional.
- [ ] API, schema, event contracts, and tests are documented together.

## Open decisions

- Whether public rooms permit reading history before joining.
- Whether ownership transfer is mandatory before the owner leaves.
