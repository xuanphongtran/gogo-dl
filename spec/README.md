# Implementation Specifications

This directory contains the detailed specifications for the first five planned phases. The documents define behavior, boundaries, decisions, and verification
requirements and verification decisions.

## Specifications

- [01 — Test Foundation](./01-test-foundation.md)
- [02 — Data Integrity and Transactions](./02-data-integrity.md)
- [03 — WebSocket Authorization and Protocol Safety](./03-websocket-authorization.md)
- [04 — API and Abuse Protection](./04-api-hardening.md)
- [05 — Room Membership and Roles](./05-room-membership-roles.md)

The related execution plans remain in [`plan/`](../plan/).

Plans 01–04 are Done after verification. Phase 05 is Pending at the user's
request; its review fixes and dedicated PostgreSQL verification remain
outstanding, as recorded in the related plan.

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
