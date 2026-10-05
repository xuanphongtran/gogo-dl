# 08 — Search, Attachments, and Notifications Execution Plan

**Priority:** P2

**Status:** In progress — 08A Done locally; 08B R2 foundation implemented; 08C Done locally; scanner/provider and release verification gates pending

**Specification:** [Phase 8 SPEC](../spec/08-rich-messaging.md)

**Depends on:** 05 membership; [06 lifecycle integration](./06-message-lifecycle.md); 07 read state

## 1. Acceptance and execution order

Implement **08A search → 08B attachments → 08C mentions/inbox** as reviewable
slices. Store/scanner investigation can run alongside search. Cleanup's shared
outbox is reused by notifications and Phase 9. Allocate actual migration versions
against integrated history. Phase 6 app is now integrated from `develop`; schema alone is
insufficient. Phase 5 verification remains Pending, a release gate for membership.

Defaults: PostgreSQL `simple` search, private Cloudflare R2 with create-only keys and separate
fail-closed scan, durable in-app mention feed. Provider/cost/scanner capability,
retention and measured quotas are readiness decisions. External email/push,
file-content/global search and attachment-only sends are outside scope. This plan
provisions nothing. 08A is implemented and locally verified; 08B foundation is implemented; full attachments remain pending and 08C is Done locally.

## 2. Inspect and establish contracts

- Read README, Makefile, git status, transport/service/repositories, Phase 6 branch,
  Phase 7 contracts, migrations and relevant tests. Preserve user changes.
- Trace membership transactions, lifecycle, account/room deletion, history DTOs
  and broadcast wiring; list changes in each slice before editing.
- Provider ADR: immutable conditional writes, upload size/hash constraints, actual scan,
  promotion, object deletion, signed headers, lifecycle, TLS/cost/ownership
  and recovery. Prove overwrite resistance in isolated staging.
- Confirm bounds, inbox retention and download bearer access window.
- Define schema, lock order and independent consumer progress before workers.
- Turn SPEC examples/error/authorization paths into behavioral tests.

## 3. 08A — Room search

Expected files: chat DTO/handler/service/repository, routes, new vector/index
migration pair, tests, README and Swagger annotations/generated docs.

- [x] Define query limits and ID-desc response/cursor; use parameterized
      `plainto_tsquery('simple', q)`, reject no-searchable-term queries.
- [x] Authorize current room membership transactionally; preserve public
      403/private 404 concealment during removal races.
- [x] Add tombstone-filtered GIN vector/index, no historical migration edit.
- [x] Keep edit/delete index state consistent with integrated lifecycle.
- [x] Add handler/service auth/validation and PostgreSQL query/index tests.
- [x] Record common/selective plans/latency at realistic room sizes; test accented
      Vietnamese, punctuation, tombstones and cursor changes during edits.
- [x] Update README/Swagger; existing history contract remains unchanged.

Gate: no cross-room leak, correct lifecycle/index behavior, clean/upgrade migrations
and measured query plans. External search needs evidence and a separate decision.

### 08A completion record — 2026-10-02

Implemented on `ft/phase-8a`: authenticated room-scoped search, service validation
and privacy policy, repository transaction locks, generated vector/partial GIN
migration `000008`, ID lookahead pagination, README and generated Swagger.
No new Go dependencies or WebSocket events. Phase 05 retains its independent
pending verification gate; this does not declare membership release complete.

Verification on Go 1.23.12 and disposable PostgreSQL 16.15:

- Focused search service/HTTP/repository tests: passed.
- PostgreSQL clean/7→8 upgrade, 8 down/reapply, edit/delete, Unicode/punctuation,
  privacy/cursor and both membership-removal orderings: passed under `-race`.
- `go test -race -p 2 -count=1 ./...` with the owned `TEST_DATABASE_URL`: passed.
- `go vet -p 2 ./...`, `make build`, `make docs`, gofmt and
  `git diff --check`: passed.
- `golangci-lint` unavailable; no lint result claimed.

The [measurement report](../docs/phase-8a-search.md) records 100k-row migration
and selective/common plans. Production/Neon migration timing is unverified;
stage representative data before rollout because startup statements have a 5 s
budget and the vector backfill rewrites/locks the table. No push or external
deployment was performed for this slice.

## 4. 08B — Attachments and shared outbox

### Scanner-pending R2 foundation

- [x] Migration 9: reservations, retained orphan tombstones, outbox intents,
      attachment aggregate counters and independent purpose leases.
- [x] Owner/member-authorized metadata, complete and cancel; idempotent reservation
      persistence with 100 MiB/10 pending reservations per user and 10 GiB per room.
- [x] Cloudflare R2 SigV4 adapter: signed create-only PUT headers and retry-safe DELETE.
      Uses the core AWS signer locally with R2 credentials; no AWS account needed.
- [x] Cleanup sweeper, bounded provider I/O, lease fencing, capped retries/backoff,
      terminal dead letters and joined cancellation at shutdown.
- [x] Closed runtime admission and explicit rejection of message attachment IDs.
- [ ] Actual R2 staging capability/CORS/late-write tests; scanner selection and
      exact-byte scan/promotion. Upload admission remains closed until these pass.
- [ ] Ready/attached states, signed download, atomic message binding/retry and
      attached-message/account lifecycle producers. These require a later migration.

The checklist below tracks the complete 08B target, rather than declaring the
scanner-pending foundation a finished attachment feature.


Expected files: focused attachment service/repository/transport and actual store/
scanner adapters; send/lifecycle/account cleanup wiring; new schema; worker startup/
shutdown/config; tests/docs. Keep concrete manual composition and existing layers.

- [ ] Add reservations/state CAS, quotas, upload retry keys, claims and cleanup
      tombstones with constraints/FKs/indexes matching actual queries.
- [ ] Establish immutable `domain_outbox`, independent purpose progress and room/
      user counters; define transactional sequence allocation, lock order/fencing.
- [ ] Scoped direct initiation/asynchronous completion; never trust object/type/
      verdict fields or assume presigned credentials are one-use.
- [ ] Pin exact quarantine bytes through verify/scan/promotion to immutable private
      final identity. Unknown/outage scan state cannot become ready.
- [ ] Separate logical reservation from physical-object budget; retain quota and
      URL admission charges after cancel until expiry + grace, delete all related objects
      and reconcile late uploads independently of object state.
- [ ] Authorized signed download with 60-second bearer limitation/safe headers;
      DTOs/events/logs contain no provider keys or URLs.
- [ ] Atomic send retry hash, owned ready room claim, message and intents;
      same body/key returns existing authorized DTO, conflicting claims roll back.
- [ ] Deletion/account/room cleanup intent precedes losing references; remove public
      bindings, advance affected revisions/events and retain cleanup tombstones.
- [ ] Bounded workers/deadlines, fenced claims/backoff, resource ownership and
      stop-claim shutdown; no external I/O inside DB transaction/Hub loop.
- [ ] Config validation/default-disabled admission, env/container/docs updates.

Gate: real provider/scanner proof, mutable-version attack, type/size/hash rejection,
claim/send/rollback races, quota/cancel/late-upload/object-GC, scan outage and
promotion/DB failure recovery all pass. Fakes supplement actual capability tests.
R2 is selected. Scanner selection and real provider proofs are pending; full 08B is not Done.

## 5. 08C — Mentions and private inbox

Expected files: additive chat DTO/validation; notification repository/service/
handlers/worker; preferences; routes/config/startup; shared outbox purpose; new
migration pairs; private WS event/tests and API docs.

- [x] Backfill immutable `room_members.membership_generation` per insertion;
      preserve role changes, reset rejoin, generation-scoped keys/cascades.
- [x] Snapshot bounded typed recipients/generations in send transaction; no username
      parsing, no self notification, generic invalid-recipient errors.
- [x] Text edits preserve original mentions; deletion/removal clears bindings.
- [x] Add global/room preferences, owned cursor inbox/read endpoints, false booleans
      and current generation/room authorization filtering.
- [x] Materialize default preference rows before locking; worker and preference
      writes lock consistently. Uniquely insert effect and private intent together;
      never resurrect old generation feed.
- [x] Independent cleanup/notification/future relay progress; prove crash/retry
      cannot double-create rows or consume another purpose's work.
- [x] Best-effort user-only WS after commit, REST recovery, no copied deleted text;
      leave removes old feed, room read cursor behavior remains unchanged.
- [x] PostgreSQL in-app adapter and typed repository test fake; external channels
      need provider decisions and are not implemented speculatively.
- [x] Update README/Swagger; the AsyncAPI assets now integrated from `develop`;
      document required content, optional send arrays and retry-key retention.

Gate: membership/leave/rejoin/preference races, feed ownership, safe deleted
references, duplicate processing, retry-key reuse/expiry and rollback pass.

## 6. Test → review → fix

Use typed fakes and barriers for service/HTTP/worker tests. Disposable PostgreSQL
tests cover clean/upgrade migrations, lock order/deadlocks, removal authorization,
competing claims, cascades and purpose leases. Provider staging contains no personal
application data. Do not destroy shared databases/buckets for tests.

Each slice: focused tests → gofmt → `go vet ./...` →
`go test -race -count=1 ./...` → `make build`; lint when installed; regenerate and
validate affected docs. Review full diff/`git diff --check`, redaction and dependency
need. Findings get regression tests/fixes before commit; skipped gates stay pending.

Review order: authorization/privacy → atomicity/immutability → cancellation/resource
release → retry/recovery → compatibility → measured storage/query budgets. Test
intent rollback, abandoned claims/lost replies, independent progress and bounded
shutdown. Phase 9 adds broker and distributed load/failure gates.

## 7. Rollout and handoff

- Expand/backfill before writers; verify integrated 000006/000007 ancestry and
  binary compatibility, then release 08A.
- Upload admission stays off until capability proof; canary with small budgets.
  Monitor pending age, rejection, orphan bytes, scan/cleanup backlog and cost.
- Enable mention worker/inbox on one instance; check suppression/private delivery.
  Provider latency never runs within the send transaction.
- Define runbooks/thresholds for scan outages, retry exhaustion/dead letters, storage
  cost, cleanup lag and consistent backup/restore.
- Rollback stops new writers/admission and retains retrieval/cleanup/recovery until
  drained. Keep schema/intents; no production down migration.
- Commit reviewed slices after authorization for implementation/commit. Handoff
  lists changed contracts, exact checks/results, migrations/config, provider limits
  and remaining gates. Planning does not authorize provisioning.

## 8. Completion checklist

- [ ] SPEC/provider decisions and prerequisite integrations resolved.
- [ ] 08A/08B/08C acceptance gates pass with recorded evidence.
- [ ] Required race/vet/build and isolated DB/provider checks completed.
- [ ] HTTP/WS docs/config/retention/runbooks synchronized.
- [ ] Review findings fixed; scan/capacity/access-window limitations disclosed.

08A is Done locally. 08B foundation is implemented; scanner/provider release gates remain pending. 08C is Done locally after dedicated PostgreSQL/race verification and review fixes; Phase 05 and staging rollout remain separate release gates.

### 08B foundation verification — 2026-10-02

- `go test -race -p 2 -count=1 ./...`: passed, including PostgreSQL 16 integration
  tests using a disposable localhost database, never the personal Neon database.
- `go vet -p 2 ./...`, `make build`, `go mod verify`, `git diff --check`: passed.
- `make docs`: generated Swagger successfully (existing swag parser warnings).
- Integration evidence: 8→9→8→9 upgrade/rollback preserves prior rooms;
  idempotent reservation/complete/cancel; cross-owner/private authorization;
  concurrent 100 MiB/10 reservation quota with int64 IDs; state/outbox rollback;
  delayed cleanup; independent scan purpose; stale lease ACK/retry rejection;
  bounded crash retries; parent cascade retains cleanup identity.
- Unit evidence: R2 upload signed headers/deadline, signed DELETE/idempotent 404,
  cancelled request cause, safe HTTP errors, closed admission, message attachment
  rejection before persistence and cleanup worker cancellation.
- `golangci-lint` and `govulncheck` are not installed; these checks were not run.
- R2 staging credentials/browser CORS tests and scanner are unavailable; provider
  immutability/grace/scan/promotion proofs remain pending. No commit/push/deployment.


### 08C completion verification — 2026-10-05

Continued on `ft/phase-8c` after implementation and CI fixes were merged into
develop. Go 1.23.12 and a dedicated PostgreSQL 16.15 `_test` database were used;
notification tests own private schemas to avoid racing migration tests in another
package. Personal Neon credentials/database were not used.

Review fixes: worker/preference/account paths now lock user → room → membership
→ global preference → room preference consistently; the source message is locked
before effect insertion. Deleted accounts/rooms/messages and stale generations
terminally suppress. Retention shares the bounded worker timeout; claim/retry
failures are logged. Inbox effect, private `notification.created` intent, and
lease ACK commit atomically; duplicate recovery emits no second intent or WS.
Broker claims accept content-free intents without an attachment reference.
HTTP IDs are parsed strictly; all six notification endpoints and retry headers
are included in regenerated Swagger. AsyncAPI source/assets remain synchronized.

Evidence:

- Original worker reproduced the preference lock-order failure using a Go source
  overlay; the regression passes with the corrected implementation.
- Dedicated tests cover 9→10→9→10 migration/member backfill, atomic send/invalid
  recipient rollback, concurrent retry, conflict and expiry, unauthorized retry,
  self-mention suppression, global/room preferences, membership generation reset,
  feed ownership/idempotent read, deletion/rejoin privacy and 30-day retention.
- Worker tests cover concurrent delivery, independent broker progress, duplicate
  effects, stale lease rollback/reclaim, suppression after account/room/message
  deletion, disabled/muted/rejoined recipients, and preference lock ordering.
- HTTP tests cover false/missing/null/wrong boolean, malformed/overflow IDs,
  invalid cursors/page bounds and invalid unread filters.
- `go test -race -count=1 -timeout=180s ./...` with `TEST_DATABASE_URL`,
  `go vet ./...`, `make build`, `make docs`, gofmt and `git diff --check`: passed.
- `golangci-lint` is unavailable. Production/Render staging smoke and Phase 05
  dedicated verification remain pending; this does not complete those gates.
- No dependency, historical migration, production configuration or external
  deployment change is required. Broker relay remains Phase 09B work.
