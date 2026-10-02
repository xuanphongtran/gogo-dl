# Product and Engineering Roadmap

This directory breaks the next implementation work into ordered, reviewable feature plans for the real-time chat domain.

Detailed implementation specifications for plans 01–09 are in [`spec/`](../spec/), with the combined P0 execution sequence in [`00-p0-foundation-execution.md`](./00-p0-foundation-execution.md).

## Priority model

- **P0 — MVP safety:** correctness, authorization, data integrity, and regression protection. Complete before adding user-facing breadth.
- **P1 — Core product:** capabilities expected from a useful multi-room chat product.
- **P2 — Growth:** richer experiences, horizontal scaling, and production operations.

Within the same priority, lower sequence numbers should normally be completed first. A plan may move only when product requirements change or evidence invalidates an assumption; record the reason in this file.

## Ordered roadmap

| Order | Priority | Plan | Outcome | Depends on | Status |
|---:|:---:|---|---|---|:---:|
| 01 | P0 | [Test foundation](./01-test-foundation.md) | Reliable unit, HTTP, race, and database verification | — | Done |
| 02 | P0 | [Data integrity and transactions](./02-data-integrity.md) | Valid schema and atomic multi-step mutations | 01 | Done |
| 03 | P0 | [WebSocket authorization](./03-websocket-authorization.md) | Only authorized members can subscribe or publish | 01, 02 | Done |
| 04 | P0 | [API and abuse protection](./04-api-hardening.md) | Safe validation, origin checks, limits, and consistent errors | 01, 03 | Done |
| 05 | P1 | [Room membership and roles](./05-room-membership-roles.md) | Private rooms, invitations, ownership, and moderation roles | 02–04 | Pending |
| 06 | P1 | [Message lifecycle](./06-message-lifecycle.md) | Edit, delete, and consistent real-time message events | 03–05 | Done |
| 07 | P1 | [Presence, typing, and read state](./07-presence-read-state.md) | Online state, typing indicators, and unread/read tracking | 03, 05 | Done |
| 08 | P2 | [Search, attachments, and notifications](./08-rich-messaging.md) | Discoverable messages and richer asynchronous engagement | 05–07 | In progress |
| 09 | P2 | [Scale and observability](./09-scale-observability.md) | Multi-instance delivery, metrics, tracing, and SLOs | 01–08 | In progress |

## Verification record

Stages 01–04 are complete and tracked as `Done`. Phase 05 is Pending at the
user's request. Phase 06 is Done using its existing membership/role contract,
with authorization locked during new message mutations and isolated PostgreSQL
and race verification complete. Phase 05 review fixes for public leave retries and atomic member-removal
authorization are included from develop; dedicated PostgreSQL verification
remains separate work.

Phase 07 is Done on a branch from main with the existing membership contract.
The full race suite (including isolated PostgreSQL), vet, build and independent
review/fixes are complete. Phase 06 application code is now included from develop,
with both lifecycle and read-state repository contracts retained. The exact
Phase 06 schema migration remains before version 7 during deployment.

Phase 08 is `In progress`: 08A search is implemented and verified locally on
`ft/phase-8a`, including PostgreSQL migration/index and authorization tests.
08B–08C remain proposed; provider and staging release gates remain pending.
Phase 09 is `In progress`:
09A is `Done` on `ft/phase-9a`; staging/provider and
09B–09D distributed delivery, shared state and rollout gates remain pending.
See its plan for exact checks and the local baseline.

## Phase 08–09 execution boundaries

| Slice | Can begin with | Release gate |
|---|---|---|
| 08A — Room search | Existing membership and the integrated Phase 06 lifecycle contract | Authorized, deletion-aware search and measured query plans |
| 08B — Attachments | 08A schema sequence and verified object-store/scanner capabilities | Private immutable scanned objects, quotas, cleanup and retry tests |
| 08C — Mentions and notifications | Message lifecycle, Phase 07 read state and shared outbox schema | Atomic intents, private durable feed and idempotent workers |
| 09A — Observability and lifecycle | Existing HTTP/DB/Hub paths; can proceed before Phase 08 | Tested probes, bounded labels, drain behavior and baseline measurements |
| 09B — Distributed durable delivery | Shared Phase 08 outbox and integrated durable event producers | Per-instance fan-out, recovery, sequencing and revocation tests |
| 09C — Shared ephemeral state and limits | 09B and verified shared-store capabilities | Multi-node presence/typing, admission and rate-limit invariants |
| 09D — Rollout | All Phase 09 slices verified in staging | Two-instance load/failure evidence, dashboards and rollback runbook |

The individual plans define the exact slice scope and acceptance criteria.
Migration versions are assigned against the merged history during implementation;
new branches must include every prerequisite version. Planning does not reserve
credentials, provision services or change the application's current runtime.

## Status values

- `Proposed`: scoped but not started.
- `Ready`: dependencies and open decisions are resolved.
- `In progress`: implementation is active.
- `Pending`: work is deferred at the user's request; unresolved work is recorded in the plan.
- `Blocked`: a named external decision or dependency prevents progress.
- `Done`: acceptance criteria are verified and documentation is current.

## How to execute a plan

1. Confirm dependencies and open decisions.
2. Convert acceptance criteria into tests or verifiable checks.
3. Use the Plan Checklist in [`AGENTS.md`](../AGENTS.md).
4. Keep each pull request independently deployable; split a plan into vertical slices when needed.
5. Update the status and record material scope decisions before handoff.

## Global constraints

- PostgreSQL remains the durable source of truth.
- All protected actions require server-side authorization.
- Persisted message mutations occur before best-effort real-time delivery.
- Public API and event changes are versioned or backward compatible.
- Every concurrency change runs under the race detector.
- Destructive migration or rollout steps require an explicit recovery plan.
