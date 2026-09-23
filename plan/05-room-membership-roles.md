# 05 — Room Membership and Roles

**Priority:** P1  
**Status:** In progress — implementation complete; PostgreSQL verification pending
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

Resolved in [spec/05-room-membership-roles.md](../spec/05-room-membership-roles.md):

- Public rooms are discoverable and self-joinable, but history and WebSocket
  subscription still require membership.
- Ownership transfer is mandatory before the owner leaves or deletes their
  account; archival ownership is out of scope.
- Visibility is immutable after creation in this phase.

Implementation is complete for the HTTP, service, repository, migration, and
WebSocket paths. The remaining verification requires a disposable PostgreSQL
database with representative room, membership, and invitation data.
