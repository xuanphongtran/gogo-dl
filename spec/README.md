# Implementation Specifications

This directory contains the detailed specifications for phases 01–05 and 07. The documents define behavior, boundaries, decisions, and verification
requirements and verification decisions.

## Specifications

- [01 — Test Foundation](./01-test-foundation.md)
- [02 — Data Integrity and Transactions](./02-data-integrity.md)
- [03 — WebSocket Authorization and Protocol Safety](./03-websocket-authorization.md)
- [04 — API and Abuse Protection](./04-api-hardening.md)
- [05 — Room Membership and Roles](./05-room-membership-roles.md)
- [07 — Presence, Typing, and Read State](./07-presence-read-state.md)

The related execution plans remain in [`plan/`](../plan/).

Plans 01–04 are Done after verification. Phase 05 is Pending at the user's
request; its review fixes and dedicated PostgreSQL verification remain
outstanding, as recorded in the related plan.

Phase 07 is implemented and verified from main, independently of the Phase 06
application code. Its schema includes migration 000006 before 000007 to preserve
deployment order; testing and review/fix results are recorded in its plan.

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
