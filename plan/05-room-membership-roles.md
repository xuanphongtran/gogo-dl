# 05 — Room Membership and Roles

**Priority:** P1  
**Status:** Pending — deferred at the user's request; review fixes and PostgreSQL verification outstanding
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

Deferred review fixes include idempotent public leave retries and atomic
member-removal authorization. Phase 06 uses the current role/membership contract
and locks authorization state for its own mutations; Phase 05 verification stays
separate.
The HTTP, service, repository, migration, and WebSocket membership paths are
implemented. Further Phase 05 work is deferred at the user's request.

Outstanding in the current checkout:

- Make public-room leave retries return `204` when membership is already absent,
  while preserving private-room privacy and the owner-transfer requirement.
- Keep member-removal authorization and deletion in one transaction using
  current, locked membership roles.
- Complete dedicated PostgreSQL checks for owner backfill/constraints,
  invitation transactions/concurrency, and ownership transfer with a disposable
  database and representative data.

The fixes discussed in the preceding review are not present in this checkout.
Resume from the recorded outstanding work when Phase 05 is reactivated.
