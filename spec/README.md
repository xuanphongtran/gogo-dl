# Implementation Specifications

This directory contains the detailed specifications for phases 01–09. The documents define behavior, boundaries, decisions, and verification
requirements and verification decisions.

## Specifications

- [01 — Test Foundation](./01-test-foundation.md)
- [02 — Data Integrity and Transactions](./02-data-integrity.md)
- [03 — WebSocket Authorization and Protocol Safety](./03-websocket-authorization.md)
- [04 — API and Abuse Protection](./04-api-hardening.md)
- [05 — Room Membership and Roles](./05-room-membership-roles.md)
- [06 — Message Lifecycle](./06-message-lifecycle.md)
- [07 — Presence, Typing, and Read State](./07-presence-read-state.md)
- [08 — Search, Attachments, and Notifications](./08-rich-messaging.md)
- [09 — Scale and Observability](./09-scale-observability.md)

The related execution plans remain in [`plan/`](../plan/).

Plans 01–04 are Done after verification. Phase 05 is Pending at the user's
request. Phase 06 is implemented and verified using the existing membership
and role contract.

Phase 07 was implemented and verified from main; Phase 06 application code
is now included from develop. Its schema includes migration 000006 before 000007 to preserve
deployment order; testing and review/fix results are recorded in its plan.

Phase 08 is in progress: 08A room search is implemented and verified locally;
08B R2 attachment foundation is implemented with scanner/provider gates pending; 08C mentions/inbox is Done locally after PostgreSQL/race verification and review fixes. Phase 09 is in progress: slice 09A is
Done on `ft/phase-9a` after implementation and local verification. The remaining 09B–09D target
contracts, provider access and capacity gates describe future behavior and must
be verified before release.

## Shared constraints

- PostgreSQL remains the durable source of truth.
- Protected actions are authorized server-side.
- Database mutations complete before events representing those mutations are
  emitted.
- Existing migration files are immutable; schema changes use a new migration
  pair.
- HTTP and WebSocket behavior must remain backward compatible unless a
  contract change is explicitly documented.
- Concurrency tests use synchronization and finite deadlines, not arbitrary
  sleeps.
