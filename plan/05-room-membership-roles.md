# 05 — Room Membership and Roles

**Priority:** P1  
**Status:** Done locally — implementation and dedicated PostgreSQL/HTTP/WS/race verification complete
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

- [x] Every room action has an explicit authorization matrix.
- [x] A room always has a valid owner or a documented archival state.
- [x] Duplicate joins and invite retries are idempotent.
- [x] Removed members lose HTTP and WebSocket access.
- [x] Membership and ownership changes are transactional.
- [x] API, schema, event contracts, and tests are documented together.

Resolved in [spec/05-room-membership-roles.md](../spec/05-room-membership-roles.md):

- Public rooms are discoverable and self-joinable, but history and WebSocket
  subscription still require membership.
- Ownership transfer is mandatory before the owner leaves or deletes their
  account; archival ownership is out of scope.
- Visibility is immutable after creation in this phase.

## Completion record — 2026-10-17

Resumed at the user's request on `ft/phase-5-completion`, based on
`origin/develop` `14256cb`. The base includes the FE re-invitation/CORS fixes
and Phase 09 Free cleanup recovery verification. No new dependency, migration,
configuration or route is introduced by this completion slice.

### Regression fixes

- A demoted manager could issue an invitation after the service's initial role
  read. Invitation creation now evaluates the service policy on its locked actor.
- A former owner could change another member's role after ownership transfer.
  Role mutation now locks the room/actor/target and rechecks the service policy;
  unchanged assignments are idempotent and do not emit duplicate events.
- History/member lists could be read after membership removal between the initial
  checks and the repository query. Protected reads now authorize and query inside
  one transaction while holding shared room/membership locks.
- Private member-list access by an outsider now returns the specified safe 404.

Each regression was reproduced against the prior behavior before the fix.
Repository transactions invoke service-owned policies; handlers remain thin.
Mutations commit before realtime events/revocation; HTTP fields/event names,
ID cursor behavior and the existing reinvitation contract are preserved.

### Acceptance evidence

| Requirement | Verification |
|---|---|
| Authorization matrix | `phase05_http_integration_test.go`: real JWT middleware, public/private owner/moderator/member/outsider/anonymous routes; `phase05_membership_integration_test.go`: management boundaries and stale-authority regressions |
| Owner invariant | `phase05_migration_integration_test.go`: clean schema 5 and legacy 3 upgrade to current embedded migrations; backfill, invalid roles/visibility, exactly one matching owner, transfer rollback and owner account-deletion guard |
| Invitation/join idempotency | `phase05_websocket_integration_test.go`: decline/retry, cross-user denial, pending unique index, failed accept rollback and concurrent accept; moderation test covers duplicate public joins; existing re-invitation integration covers concurrent manager retries |
| Removal/privacy | Real authenticated WS join denied before acceptance, joined after acceptance, revoked after removal/leave, stale rejoin denied, fresh invite accepted; private history/member list concealment and read-lock cancellation tests |
| Atomicity | Forced final transfer-statement failure preserves both roles/owner; forced membership-insert failure preserves pending invitation; active protected reads block removal until their transaction ends |
| HTTP/WS/schema consistency | SPEC/README plus existing service/handler, SQL failure, Hub race and 08C membership-generation/inbox tests |

Test files above are in `internal/database/`; the original empty Phase 05 test
placeholder in `internal/chat/` is replaced with manager-policy tests.
Durable membership/invitation rows remain the source of truth. Future distributed
membership/account outbox producers remain Phase 09B scope.

### Verification and rollout boundaries

Executed in a Go 1.23.12 container with a separate disposable PostgreSQL 16
container and `TEST_DATABASE_URL` targeting `gogo_phase05_test`:

- `go test -race ./internal/database -run '^TestPhase05' -count=1 -timeout=90s`: PASS.
- `go test -race -count=1 -timeout=180s ./...`: PASS, including existing migration,
  mention-generation/inbox, notification/cleanup recovery and Hub tests.
- `go vet ./...`: PASS.
- `go build -buildvcs=false -o /tmp/gogo-phase05-server ./cmd/server`: PASS.
  VCS stamping is disabled only for the host-mounted container checkout.
- `gofmt` on every changed Go file, local documentation link checks and
  `git diff --check`: PASS.
- Independent code/test/documentation review: no remaining blocking findings.
- `golangci-lint` is unavailable; no lint result is claimed.

No personal or hosted application data is used. The fixture owns its schema and
refuses existing application tables; all worker/Hub goroutines are stopped and
joined. The dedicated test containers are removed after verification.

Phase 05 Done means its implementation and local acceptance checks are complete,
consistent with the Phase 06/07 completion convention. Direct frontend browser
acceptance and final-host redeploy/recovery checks remain Phase 08/09 release
work. No deployment, push or hosted mutation is performed in this slice.
