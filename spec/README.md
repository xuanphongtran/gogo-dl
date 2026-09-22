# Implementation Specifications

This directory contains the detailed specifications for the first three P0
plans. The documents define behavior, boundaries, decisions, and verification
requirements before implementation begins.

## Specifications

- [01 — Test Foundation](./01-test-foundation.md)
- [02 — Data Integrity and Transactions](./02-data-integrity.md)
- [03 — WebSocket Authorization and Protocol Safety](./03-websocket-authorization.md)

The related execution plans remain in [`plan/`](../plan/). A specification is
not an implementation status update: all three plans remain `Proposed` until
their acceptance criteria are verified.

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
