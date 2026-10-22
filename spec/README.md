# Specifications

These documents define contracts. [The roadmap](../plan/README.md) tracks
implementation; [pending](../plan/pending.md) owns open work and
[release preparation](../docs/release.md) defines the candidate scope.

| Contract | Status |
| --- | --- |
| [01 — Testing](01-test-foundation.md) | Implemented |
| [02 — Data integrity](02-data-integrity.md) | Implemented |
| [03 — WS authorization](03-websocket-authorization.md) | Implemented |
| [04 — API limits/origins](04-api-hardening.md) | Implemented |
| [05 — Membership/roles](05-room-membership-roles.md) | Implemented and verified locally |
| [06 — Message lifecycle](06-message-lifecycle.md) | Implemented |
| [07 — Presence/typing/read state](07-presence-read-state.md) | Implemented; process-local realtime |
| [08 — Search/attachments/inbox](08-rich-messaging.md) | Implemented; schema13 |
| [08B — Processing boundary](08b-scanner-integration.md) | Implemented |
| [08B — Lightweight policy](08b-lightweight-processing.md) | Implemented; ClamAV not required |
| [08B — Binding/download](08b-message-attachments.md) | Implemented |
| [09 — Observability and future distributed design](09-scale-observability.md) | 09A implemented; 09B–09D not implemented |

Frontend completion is user-confirmed. Local verification does not establish
hosted rollout, production SLOs or PITR/object consistency. The release scope is
single-instance; future broker/cross-node requirements are explicitly deferred.

All contracts preserve server-side authorization, PostgreSQL durability before
best-effort events, context-aware I/O and immutable historical migrations.
