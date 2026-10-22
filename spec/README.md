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

Plans 01–05 are Done locally after verification. Phase 05 dedicated PostgreSQL,
authenticated HTTP and WebSocket verification completed on 2026-10-17, including
stale-role and protected-read fixes. Phase 06 is implemented and verified using
the membership and role contract. Provider/browser rollout gates remain separate.

Phase 07 was implemented and verified from main; Phase 06 application code
is now included from develop. Its schema includes migration 000006 before 000007 to preserve
deployment order; testing and review/fix results are recorded in its plan.

Phase 08 is in progress: 08A room search is implemented and verified locally;
08B message attachment binding/download and lightweight processing are implemented;
hosted file acceptance remains separate. R2/scanner re-verification is outside the
current scope (2026-10-20), and ClamAV is deferred indefinitely.
08C mentions/inbox is Done locally after PostgreSQL/race verification and review fixes. Phase 09 is Pending under the Render Free decision: 09A is Done locally;
09B/09C and the multi-node 09D rollout are deferred. Single-instance staging
verification remains follow-up work. Local actual-process restart, inbox/history
recovery and DB-path readiness recovery are covered by the
[opt-in integration suite](../docs/local-recovery.md). Distributed contracts and capacity gates
are preserved for an explicitly resumed scaling effort.

The 2026-10-22 local follow-up also provides a separate, isolated logical
backup/restore rehearsal (`make verify-local-backup`), including whole-table and
sequence comparison and authenticated checks against the restored server.
Production PITR/retention, DB/object reconciliation and hosted recovery remain
deployment gates; this does not expand the distributed scope.

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
