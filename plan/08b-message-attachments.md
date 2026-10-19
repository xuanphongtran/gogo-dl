# 08B message attachments execution plan

SPEC: [message attachments](../spec/08b-message-attachments.md).
Inline execution, test first, preserve scanner repo unchanged. One final review.

1. Schema13 and service/normalization RED tests; add typed DTO/feature-aware repo
   constructor and atomic claims in existing mention send transaction. Preserve old
   schema fixtures and source quotas; conditional no-text send with max5 files/key.
2. Add locked download repository/service/handler and R2 GET signing60s; metadata
   projection across send/retry/history/search/edit/delete. No URL in projections.
3. Add reusable transactional cleanup helper; message/account lifecycle and orphan
   sweeper; pin user before room/member/message/attachment locks. Prevent attached
   cancel/TTL GC, include bounded best-effort account-removal WS refresh and intents.
4. Dedicated PostgreSQL tests for upgrade13, claims/races/rollback, quotas, lifetime,
   authorization, deletion and metadata; focused unit/HTTP/signing tests. Final
   race/vet/build/docs, independent review, regressions for important findings.

Decisions: new constructors opt into schema13 to preserve earlier-schema tests.
Original input bytes remain logical quota basis as in processing integration;
output identity is used for download and message DTO. Hard room deletion has no
public API today; retain FK tombstones and reconcile orphans instead of adding a
speculative room endpoint. Backend emits post-commit best-effort events; broker
intents are durable evidence, not a claim of distributed delivery on Render Free.

## Execution evidence — 2026-10-19

Branch: `ft/phase-8b-message-attachments`, based on `be81987`. No commit,
push or hosted rollout requested for this work. Scanner project unchanged.

### Implemented

- Schema13 and bound-aware repositories, max5 atomic ready claims, normalized
  retry hashing, file-only messages and processed metadata on REST/WS projections.
- Authorized final GET signing60s, safe response headers, no-store tickets,
  no provider keys/URLs in Message DTOs.
- Attached retention/cancel rules, message/account transactional cleanup intents,
  user/room/member/message/file lock ordering and orphan sweep reconciliation.
- Account postcommit subscription revocation and authoritative message refresh;
  old schema constructors/fixtures retain existing behavior.
- Swagger/AsyncAPI regenerated, FE contract at `docs/attachments.md`, README and
  main phase8 progress reconciled. Hosted Swagger remains old until deployment.

### Verification observed

- Initial RED: normalized send dropped attachment IDs; missing cleanup intent;
  search omitted metadata; missing-account error mapping; deleted user received
  account-removal refresh before revocation. Focused regressions passed after fixes.
- PostgreSQL16 disposable tmpfs DB `gogo_binding_test`: full race-enabled suite
  passed during implementation, then additional focused integration tests passed:
  binding rollback/concurrent claims/projection parity, account and hard-delete
  orphans, signing vs membership removal, account delete vs send, real local WS
  binding payload and ordinary account revocation.
- Fresh review found request cancellation could skip committed-account revocation.
  `TestCommittedAccountRevocationWaitsForHubDespiteCanceledRequest` failed before
  the fix, then passed with `TestAccountAttachmentRevocationSurvivesRequestCancellation`
  under race detector. No new Hub maps or background goroutines introduced.
- Final `go test -buildvcs=false -race -count=1 -timeout=180s ./...`,
  `go vet ./...`, `make build` passed in Docker Go1.23 **without**
  `TEST_DATABASE_URL` (DB tests skipped). This final run includes the cancellation
  fix and generated docs. A prior concurrent docs-generation run failed the stale
  HTML check; generation completed and the sequential final suite passed.
- Real R2 `TestProcessingRealR2`, local lightweight sanitizer: TXT/PNG/JPEG passed
  quarantine→processing→immutable final→retry plus signed final GET, output bytes,
  verified Content-Type, Content-Disposition and no-store. Only random owned keys
  were created/cleaned. This is provider proof, not a hosted new-backend smoke.
- Node22 `make docs-asyncapi` and pinned swag1.8.12 generation passed.
- `git diff --check` passed. Host Go absent; Docker used. golangci-lint is not
  installed, so lint was not run; no lint pass is inferred from vet.

### Remaining gates and rulings

- Automatic approval review rejected downgrade/force DB test execution twice,
  including after read-only proof of private tmpfs/no-public-port fixture. Per
  AGENTS.md explicit authorization for destructive DB operations, stopped further
  DB reset/migration tests and asked the user. Approval is still pending.
- Added (compiled/skipped without a DSN) schema12→13→12→13 preservation, actual
  transactional down-SQL live-binding guard, and attached/pending quota regression
  tests. They are **not executed against PostgreSQL** yet. The guard test rolls
  back its transaction; no Force bypass is required. Final full DB rerun is also
  pending authorization, not claimed complete by the unit-only final suite.
- Ruling: original bytes remain quota basis; processed bytes identify downloads —
  preserves the v2 trust model; FE must not use upload hash for output verification.
- Ruling: preserve deleted-user message history even if its last file disappears —
  no fabricated replacement content or unrelated message deletion; FE may show a
  blank historical file-only message after uploader deletion.
- Ruling: mandatory postcommit revocation gets a synchronous owned5s context using
  WithoutCancel; database refresh keeps the HTTP context — cancellation cannot skip
  normal revocation and does not extend request-scoped DB work indefinitely.
- Ruling: schema13 is a coordinated upgrade; stop old backend/cleanup binaries
  before binding traffic — older binaries can mishandle attached expiry/cancel.
  No zero-downtime mixed-version downgrade is promised; live down guard must stand.
- Hosted end-to-end binding/download/edit/delete/recovery smoke requires deployment
  of this branch. Browser fetch downloads require GET in R2 CORS; no bucket policy
  was changed in this task. ClamAV remains deferred indefinitely, not a blocker.

Final independent review: cancellation finding verified fixed; no remaining important
findings. Reviewer did not run Go tests (host Go absent); author Docker evidence above.
