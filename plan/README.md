# Product and Engineering Roadmap

This directory breaks the next implementation work into ordered, reviewable feature plans for the real-time chat domain.

Detailed implementation specifications for plans 01–03 are in [`spec/`](../spec/), with the combined execution sequence in [`00-p0-foundation-execution.md`](./00-p0-foundation-execution.md).

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
| 05 | P1 | [Room membership and roles](./05-room-membership-roles.md) | Private rooms, invitations, ownership, and moderation roles | 02–04 | Proposed |
| 06 | P1 | [Message lifecycle](./06-message-lifecycle.md) | Edit, delete, and consistent real-time message events | 03–05 | Proposed |
| 07 | P1 | [Presence, typing, and read state](./07-presence-read-state.md) | Online state, typing indicators, and unread/read tracking | 03, 05 | Proposed |
| 08 | P2 | [Search, attachments, and notifications](./08-rich-messaging.md) | Discoverable messages and richer asynchronous engagement | 05–07 | Proposed |
| 09 | P2 | [Scale and observability](./09-scale-observability.md) | Multi-instance delivery, metrics, tracing, and SLOs | 01–08 | Proposed |

## Verification record

Stages 01–04 are complete and tracked as `Done`; Phase 04 implementation and verification evidence are recorded in the linked execution plan.

## Status values

- `Proposed`: scoped but not started.
- `Ready`: dependencies and open decisions are resolved.
- `In progress`: implementation is active.
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
