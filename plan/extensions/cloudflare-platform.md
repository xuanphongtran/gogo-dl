# Cloudflare Platform Extension Plan

**Status:** Proposed — documentation only; no implementation or provisioning
**Date:** 2026-10-12
**Specification:** [Cloudflare extension](../../spec/extensions/cloudflare-platform.md)

## Goal and boundaries

Evaluate and incrementally adopt Cloudflare infrastructure while keeping the Go
API on Render Free and PostgreSQL on Neon. Workers Paid around USD 5/month is an
acceptable base subscription; total usage, scanner and telemetry costs still
require a measured budget decision.

This plan is an optional extension. It does not change the order, status or scope
of [the primary roadmap](../README.md), [08](../08-rich-messaging.md) or
[09](../09-scale-observability.md). The primary plans remain authoritative until
an explicitly accepted adoption decision reconciles their provider/topology
requirements with this extension. CF labels below identify extension milestones,
not new product phases. No milestone is complete merely because it is documented.

**Proposed architecture:** Go/Neon own domain state and transactions; R2 owns
private bytes; Workers/Queues orchestrate jobs and event references; Durable
Objects coordinate sockets and ephemeral state. Hyperdrive is optional metadata
access for Workers. No Go API rewrite or database migration to D1 is planned.

**Proposed stack:** existing Go stack; TypeScript Workers, SQLite Durable Objects,
Queues, R2 and optional Hyperdrive. ClamAV in Containers is a scanner proof
candidate, not a selected dependency or service.

**Global constraints:** persist before emitting, enforce current authorization,
fail closed on uncertain membership, retain REST recovery, separate outbox
purposes, bound retries and fanout, redact secrets/content, and preserve rollback.
Do not install packages, enable billing or provision infrastructure as part of
this documentation task. Review focus is privacy during revocation, recovery after
queue expiry, resource ownership, cold starts and actual monthly cost.

## Relationship to existing phases

| Extension milestone | Related work | Adoption gate |
|---|---|---|
| CF-0: baseline and decisions | 05, 08, 09 | Accepted extension and compatible source contracts |
| CF-1: R2 and scanner proofs | Unfinished 08B | Verified private exact-byte lifecycle and selected scanner |
| CF-2: job orchestration | 08B/08C workers, 09 Free operations | Same durable effects, retry safety and no idle keepalive |
| CF-3: durable transport | Deferred 09B | Accepted receipts, ordering and PostgreSQL replay |
| CF-4: realtime canary | Deferred 09B/09D | Authorization, revocation, hibernation and reconnect proof |
| CF-5: shared ephemeral coordination | Deferred 09C | Strict admission and preserved Phase 07 semantics |
| CF-6: telemetry and cost | 09A/09D | Proven collector paths and measured budget |
| CF-7: staged adoption | 09D alternative rollout | Agreed Cloudflare topology, rollback and failure evidence |

CF-1 can be investigated independently of realtime. CF-2 precedes CF-3, and
CF-3 precedes CF-4; CF-5 follows the realtime coordination proof. CF-6 measurements
start with each proof and become a release gate. CF-7 requires the relevant gates
for its chosen cohort, not unrelated unfinished attachment functionality.
Phase 05 verification is required before realtime cutover. Render remains one
instance; a Cloudflare rollout must explicitly reconcile the primary 09D
multi-instance acceptance criteria rather than claim they have been satisfied.

## CF-0 — Freeze the baseline and adoption decisions

- [ ] Inspect the merged baseline and existing `internal/outbox`, `internal/ws`,
  `internal/chat`, notification/attachment workers and 08/09 contracts.
- [ ] Record an adoption decision under `spec/extensions/` listing accepted scope,
  conflicts with the primary plans, TypeScript ownership and deferred components.
- [ ] Define budget thresholds, who owns accounts/alerts, test environments and
  credential boundaries. A USD 5 subscription is not a universal spending cap.
- [ ] Record unresolved Phase 05 DB/role/revocation checks as release gates.

**Evidence:** a compatibility matrix with no silently weakened invariant;
explicit approval of implementation scope. Do not change main roadmap statuses
as a side effect of this extension's planning or experiments.

## CF-1 — Verify R2 and investigate scanning

**Affected areas:** `internal/attachment`, provider fixtures and the existing 08B
capability records. Put experiment findings in `spec/extensions/`.

- [ ] Prove actual R2 upload CORS, create-only conditions, checksum/size validation,
  immutable quarantine/promotion, scoped download and cleanup behavior.
- [ ] Benchmark a capped ClamAV Containers experiment: signature freshness,
  memory, startup, exact-byte hash binding, clean/malicious/error outcomes and cost.
- [ ] If unsuitable, compare a dedicated Linux scanner and private scanning API
  for price, privacy and operations. Obtain a scanner decision before integration.
- [ ] Keep upload admission closed until scanner and provider gates pass.

**Evidence:** repeatable provider tests and measured scanner report, including
failure cases. An R2 notification never becomes a clean verdict by itself.

## CF-2 — Add bounded job orchestration

**Proposed paths:** `deploy/cloudflare/` with `wrangler.jsonc`, package manifest
and `src/jobs.ts`; Go service-auth middleware and internal job routes under
`internal/middleware` and `internal/httpserver`; existing worker packages,
`internal/config`, `cmd/server/main.go`, `.env.example`, container wiring and README.
These paths are implementation targets, not files created by this plan.

- [ ] Freeze the internal callback contract before coding: method/path, service
  audience, environment, body hash, timestamp, nonce replay policy and rotation.
- [ ] Extract only the existing lease-protected work entry points needed by the
  bridge. Notification/cleanup effects keep one authoritative implementation.
- [ ] Add a Queue consumer that calls bounded Go operations, with idempotency,
  backoff and visible dead letters. Do not ACK an uncertain effect.
- [ ] If needed, grant Hyperdrive a narrowly scoped eligible-work metadata view.
  Schedule a five-minute reconciliation check; contact Render only for real work.
- [ ] Test cold start, timeout after commit, duplicate callback, expired/replayed
  signature, purpose contention and origin outage. Measure Neon wake/compute cost.

**Evidence:** repeated delivery produces one durable effect; no pending work
produces no origin keepalive. Queue retention cannot erase unresolved work.

## CF-3 — Prove durable transport and recovery

**Affected areas:** `internal/outbox`, its producers, new migration pair(s),
`deploy/cloudflare/src/dispatch.ts`, room/user object handlers and protocol tests.
Assign migration numbers against merged history; preserve deployed migrations.

- [ ] Define versioned reference-only envelopes, aggregate sequence, revision,
  environment/incarnation and independent publisher lease/progress.
- [ ] Separate publication from durable acceptance. Persist receipts after the
  target durable handoff; reconcile unresolved intent from PostgreSQL.
- [ ] Implement bounded reorder/gap handling, duplicate detection, retry/DLQ
  inspection and replay after queue retention expires.
- [ ] Explicitly fan out in the dispatcher. Queue consumers are not subscribers
  that each receive every event.
- [ ] Test consumer crash at each handoff, lost receipt, duplicate, reorder,
  unknown schema, stale incarnation and exact-revision hydration after edit/delete.

**Evidence:** recoverable intent across all crash windows, independent purpose
progress, no private content in transport/logs and no false completion on publish.

## CF-4 — Build a realtime canary

**Proposed paths:** `deploy/cloudflare/src/realtime/{user,room}.ts`, ticket issuance
and redemption in Go, route/config wiring, WS contract tests and AsyncAPI docs.

- [ ] Freeze ticket and internal authorization contracts; atomic one-use ticket
  redemption, short expiry, allowed Origin and redacted subprotocol handling.
- [ ] Prototype user/room DO roles preserving one connection with multiple rooms.
  Hydrate only data authorized for the current membership generation.
- [ ] Prove room control ordering and user delivery fences across DO boundaries.
  Test removal, account deletion, stale joins and delayed controls concurrently.
- [ ] Recover attachments/checkpoints after hibernation; fail closed until
  authorization is revalidated. Bound slow consumers and reorder buffers.
- [ ] Canary only after privacy proof; one authoritative transport per cohort.
  Test browser reconnect, REST catch-up and Worker/Render failure independently.

**Evidence:** current revocation and privacy invariants hold under failure.
If they cannot be preserved, retain Go sockets and document the blocker.

## CF-5 — Coordinate ephemeral state and limits

**Affected areas:** DO coordination, `internal/ws`/`internal/chat` integration and
Phase 07 contract tests; new config must be documented and validated.

- [ ] Preserve presence, typing and read-state semantics; PostgreSQL keeps durable
  read state. Specify lease expiry, alarms, recovery and connection accounting.
- [ ] Enforce strict user admission through the authoritative user DO. Prove
  concurrent joins, disconnect loss, hibernation and restart do not leak leases.
- [ ] Keep KV caches and edge rate limits outside strict quota/authorization
  decisions. Evaluate other strict scopes separately before claiming support.

**Evidence:** multiple edge connections preserve limits and no stale presence
survives its declared expiry bound. Ephemeral loss cannot change durable read state.

## CF-6 — Establish telemetry and a measured budget

- [ ] Measure requests/CPU, DO activity/storage, queue retries, R2 operations,
  Neon activity and scanner resources under idle, typical and hot-room workloads.
- [ ] Define bounded labels, redaction, alerts, retention and operational owners.
- [ ] Prove where existing Go metrics/traces go. Evaluate an external collector
  only if Cloudflare cannot supply the required ingestion/scraping capability.
- [ ] Record monthly estimates and failure amplification; agree thresholds before
  expanding canaries. Alerts alone are not a hard spending limit.

**Evidence:** correlated Go/edge traces and recoverable job/queue indicators,
with a cost report distinguishing base subscriptions from usage.

## CF-7 — Adopt in staging and retain rollback

- [ ] Approve the Cloudflare replacement criteria for the chosen 09 slices and
  document reconciliation with the primary plan before enabling them.
- [ ] Run staging sleep/wake, queue expiry/replay, DO hibernation/crash, hot-room,
  stale-control, slow-client, reconnect and scanner-outage drills as applicable.
- [ ] Disable edge admission, close/drain canary sockets, restore Go routing and
  fetch durable history/inbox. Retain schema, intents and receipts for recovery.
- [ ] Verify rollback and late-event incarnation fences before expanding cohorts.
  Record capability evidence separately from primary roadmap completion.

**Evidence:** reproducible failure/recovery and rollback runbooks, accepted costs
and no unverified privacy or retention assumptions.

## Verification required during implementation

For each Go change, run focused behavioral tests, then `gofmt`,
`go test -race -count=1 ./...`, `go vet ./...` and `make build`. Use an isolated
PostgreSQL test database for transaction/migration/recovery checks and test both
clean install and upgrade paths. Never use personal or production data.

For the Worker component, first approve necessary dependencies and lock versions.
Use Node 22 via `nvm use 22`, then `npm ci`, the project's defined test/typecheck
scripts and `wrangler deploy --dry-run`. A dry run is not deployment evidence;
real provider tests require separately authorized staging resources.

Review the complete diff, run `git diff --check`, check documentation links and
update API/AsyncAPI/configuration documentation for changed contracts. Record
exact results and unavailable checks; do not claim provider verification from mocks.

## Current handoff

Only the extension specification and plan are proposed. No main plan status,
runtime behavior, dependency, migration or deployed resource has changed.
The next step is CF-0 review and explicit selection of an implementation slice.
