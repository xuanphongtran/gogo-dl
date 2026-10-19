# Lightweight processing implementation plan

SPEC: [08B lightweight processing](../spec/08b-lightweight-processing.md).
Execute inline; preserve both repositories, no commit/push/provisioning requested.

1. Standalone scanner: add regression tests for /v2/process and sanitize/clamav
   modes; observe RED. Add bounded processing/result contract, strict extension /
   media matching, JPEG/PNG rewriting and TXT checks, one shared processing slot.
   Preserve /v1/scan, auth, request deadlines and shutdown. Default static image;
   retain original ClamAV image as Dockerfile.clamav. Update Compose/env/docs.
2. Backend: RED v2 client/worker tests; add bounded typed Artifact response and
   input/output verification. Worker promotes output via unchanged R2 adapter,
   stores new artifact metadata through fenced FinishScan. Migration12 keeps
   original quota/hash untouched, backfills historical ready, guards rollback.
3. PostgreSQL tests: clean migration12/11 upgrade, transformed hash/size, stale
   leases/cancel/member fences and cleanup; preserve existing regression coverage.
4. Run Go race/vet/build in both repos, isolated PostgreSQL only; actual 512MiB
   sanitizer container clean/image/rejection/memory checks and real R2 smoke.
   Review changes, record limits and exact deployment configuration. Upload stays
   closed, no ClamAV requirement for sanitize mode, no fake antivirus claims.


## Observed verification

- RED: missing processor contract tests failed compilation before implementation.
  Image/text policy and backend artifact validation tests then passed.
- Backend full `go test -race -count=1 -timeout=240s ./...` with an owned PostgreSQL16
  tmpfs test database, `go vet ./...`, `make build`: passed. Includes source/output
  identity preservation, migration11→12 backfill and rollback guard, stale lease
  recovery after transformed promotion, cancellation/member fencing and cleanup.
- Real local sanitizer in512MiB container: probes, auth, v1 unavailable,
  HTML/SVG/PDF/binary/type-spoof rejection,4M-pixel RGBA16 rewriting, deterministic
  retry, over-pixel rejection,10MiB text boundary and graceful SIGTERM passed.
  Post-test memory observed50–59MiB; this is not peak or a production guarantee.
- Real R2 with backend adapter + local sanitizer: TXT/PNG/JPEG quarantine retrieval,
  artifact processing, final create-only promotion/readback, idempotent retry and
  owned-object cleanup passed. No personal DB used, no admission opened.
- Final independent review found response-writing escaped single-slot memory
  admission. Blocking-writer regression reproduced second request200 rather than
 429; slot now spans response encoding/write. Review also required explicit old
  worker draining before migration12; rollout docs updated.

Hosted Render sanitizer deployment/cold starts, Render R2 IP allowlist/egress,
browser CORS/late-write/grace and full attachment download/message lifecycle stay
open. Sanitization does not promise malware detection (including EICAR TXT).
No commit/push or hosted provisioning requested. No new dependency.

Final follow-up: blocking-writer regression now passes; scanner full race/vet/build
and Python harness unit checks pass. Final static image passed512MiB container
gate again (post-test52MiB, no OOM). Optional Dockerfile.clamav image was built and
real ClamAV v2 clean passthrough/EICAR rejection passed; legacy v1 probes/auth/
clean/digest/EICAR acceptance passed. Reload/stale/restart gates were not repeated
because their engine implementation is unchanged. No hosted deployment performed.

## Upload admission follow-up

User requested opening uploads. Add RED config/service/HTTP tests, then remove
unconditional config rejection and require active configured scan worker for
opt-in upload. Add explicit NewUploadService constructor, wired only when the
validated upload flag is true; preserve existing closed constructor call sites.
Validate extension/media pair before reservation. Verify authenticated201/ticket,
closed503, invalid400, unauthorized/forbidden/not-found/conflict and signing503;
PostgreSQL admission idempotency and membership remain covered by repository tests.
Regenerate Swagger, update README/env operational steps, run full DB/race/vet/build.
No additional schema or scanner changes and no message attachment acceptance.


Admission verification: RED tests reproduced unconditional config rejection and
missing enabled service constructor. Focused tests now pass enabled201/default503,
invalid extension/type400 without reservation, unauthorized401, room403/404,
conflicting retry409, signing503 without internal error leakage. Dedicated local
PostgreSQL16 test passes public ticket persistence, same-key retry with unchanged
expiry/one reservation, private-room denial and complete→scanning. Final admission
review reported no important findings; browser bucket CORS remains unverified and
is documented as a rollout check. No additional migration was needed for admission.

Final admission checks: `go test -race -count=1 -timeout=240s ./...` with dedicated
TEST_DATABASE_URL, `go vet ./...`, `make build`, pinned swag1.8.12 generation and
`git diff --check` all passed. No hosted environment flags were changed. Default
.env.example remains false; operators opt in with both upload/scan flags true.
