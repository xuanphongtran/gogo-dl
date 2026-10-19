> Updated by [lightweight processing](08b-lightweight-processing.md): v2 artifacts
> replace the same-byte-only worker contract. The original integration below
> describes the ClamAV v1 baseline.

# 08B scanner integration implementation plan

**SPEC:** [Backend scanner integration](../spec/08b-scanner-integration.md).
**Execution:** Inline, test-first; preserve existing uncommitted planning files.

1. Add RED client/provider tests; implement typed scanner client, bounded object
   reads, format/hash validation and create-only same-byte promotion.
2. Add migration11 and scan repository with fenced prepare/finalize, then worker
   regression tests and implementation. Cleanup must cover both deterministic keys.
3. Wire validated opt-in config, process-owned cancellation/join, environment and
   operations docs. Public uploads/message attachment acceptance remain closed.
4. Run focused and full race/DB/vet/build tests; review the full change, fix
   important findings with regressions and record unverified provider/hosted gates.

Expected new files: internal/scannerapi/client.go, scan_worker.go,
scan_repository.go, verification.go and tests; migration11 up/down; config tests;
database integration tests. Existing R2, cleanup, outbox, model/service, main,
README/env/Compose receive minimal synchronized edits. No new dependency.

Acceptance: successful verified bytes become ready exactly once; rejected or
unverifiable bytes never do; crashes/duplicate jobs cannot overwrite bytes, publish
unauthorized files, erase cleanup identity or consume another outbox purpose.
45s job fits60s scan lease; all final mutations require a live token and current
state/membership. Existing10MiB/quotas and default-closed admission remain.


## Implementation and verification

Implemented on `ft/phase-8b-scanner-integration`, without committing. Scanner HTTP
client, bounded R2 reads and conditional promotion, migration11, fenced repository,
worker, dual-key cleanup, configuration and shutdown wiring are complete.

Verification uses Go1.23.12 and an isolated PostgreSQL16 tmpfs database, not Neon:
`go test -race -count=1 -timeout=240s ./...`, `go vet ./...`, and `make build` pass.
Tests cover migration10→11, stale lease reclaim, authorization/cancel fencing,
clean/infected verdicts, promotion before lease loss, broker-purpose isolation,
and cleanup of both objects. Cleanup regression failed against the previous
worker; HTML-as-text regression failed before MIME validation was added.

One final independent review found no important issues. Its conflicting-final
object coverage suggestion is implemented. Remaining minor limitations: image
suffix checks do not prove complete decoder consumption (all bytes are still
scanned); worker warnings could include safe event identifiers for diagnosis.
No hosted-provider behavior was judged without credentials.

Real R2 conditional requests/CORS/late writes and hosted scanner smoke remain
unverified. Message binding, downloads and remaining lifecycle release gates are
not part of this slice. Upload admission remains closed. No dependencies added.
