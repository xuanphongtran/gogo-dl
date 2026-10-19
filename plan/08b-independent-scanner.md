# Independent ClamAV Scanner Implementation Plan

> **For agentic workers:** Use `superpowers:executing-plans` to implement this plan task by task after design/plan approval. Do not start implementation from this planning request. Use checkbox steps to record actual evidence; commits require user authorization.

**Status:** Proposed — planning only; no runtime, schema or deployment change.

**Goal:** Deliver a portable, independently deployable, authenticated byte-scanning service for the future 08B integration.

**Architecture:** New Go binary wraps local clamd over Unix INSTREAM; freshclam maintains signatures. Scanner owns no DB, cloud credentials or durable queue. Backend integration and opening uploads are separate release work.

**Tech stack:** Go 1.23, standard library HTTP/Unix sockets, existing zerolog, supported pinned ClamAV Docker image, container init/supervision, Docker Compose; optional paid Render Private Service later.

**Spec:** [Independent scanner design](../spec/08b-independent-scanner.md). Read it alongside [08B invariants](../spec/08-rich-messaging.md#5-upload-scan-and-cleanup-invariants).

## Global constraints

- Only scanner deliverables in this plan; preserve existing staging-document edits.
- No new Go module/dependency, chat-domain import, schema, backend configuration or main-server wiring.
- Upload admission remains closed; do not consume pending attachment_scan jobs.
- Input is raw bytes, 1–10,485,760 bytes, known Content-Length and lowercase 64-hex expected SHA-256; never accept paths or URLs.
- One scan slot, no application queue; request 20 s, engine scan at most 15 s, readiness 2 s, shutdown drain 25 s.
- Signature age at most 72 h; timestamps over 5 min in the future fail closed; container timezone UTC.
- Error/unknown/partial/limited/stale scan never becomes clean; no content, hash or credential logs.
- No public clamd port; local bind loopback, hosted private network; HTTPS/tunnel outside Render.
- A scanner-ready milestone does not complete Phase 08B or authorize paid provisioning.

## Review focus

1. Short/oversized streams and checksum mismatch: never submit/approve incomplete bytes (Tasks 1–2).
2. Limit alerts or malformed engine replies disguised as success: never emit clean (Tasks 1–2).
3. Reload/stale signatures and concurrent overload: no stale clean, unbounded queue or leaked slot (Task 2).
4. Disconnect/SIGTERM/child failure: cancel I/O, reap children, stop accepting work (Tasks 3–4).
5. Docker signatures and networking differ from local fakes: empty/persisted volume, real engine limits and no exposed clamd tested (Tasks 4–5).

## File map

| Files to create/modify | Responsibility |
| --- | --- |
| `cmd/scanner/main.go` | Scanner-only composition root, signals and HTTP lifecycle |
| `internal/scanner/config.go`, `config_test.go` | Separate validated scanner environment |
| `internal/scanner/clamd.go`, `clamd_test.go` | Context-aware Unix protocol, bounded replies and loaded metadata |
| `internal/scanner/service.go`, `service_test.go` | Admission, exact bytes/hash, freshness/verdict rules |
| `internal/scanner/handler.go`, `handler_test.go` | Auth, raw request contract and safe error mapping |
| `internal/scanner/server.go`, `server_test.go` | HTTP limits and bounded shutdown |
| `deploy/scanner/Dockerfile`, `entrypoint.sh`, `clamd.conf`, `compose.yaml`, `.env.example` | Separate runtime image, supervision, signatures and local deployment |
| `scripts/scanner-acceptance.sh` | Bounded real-engine acceptance; secrets from files/env only |
| `.github/workflows/scanner.yml` | Path-filtered real-engine checks and scanner image build |
| `Makefile`, `docs/scanner-operations.md`, `README.md` | Additive scanner commands, deployment and contract guide |

No modifications to existing backend Dockerfile, migrations, attachment workers or
main-server Config are expected. New scanner `.env` files must be ignored and
excluded from image build context before local use; only `.env.example` is tracked.

## Task 1 — Unix engine adapter and configuration

**Interfaces produced in `internal/scanner`:**

- `LoadConfig(getenv func(string) string) (Config, error)` uses only the four scanner env vars in SPEC.
- `DatabaseStatus` fields: `EngineVersion string`, `DatabaseVersion string`, `BuiltAt time.Time`.
- `EngineResult` fields: `Verdict string` (`clean` or `infected`). Limits/errors return errors, not a verdict.
- `Engine` interface: `Status(context.Context) (DatabaseStatus, error)` and `Scan(context.Context, []byte) (EngineResult, error)`.
- `NewClamd(socketPath string) *Clamd`; `Clamd` implements `Engine`.
- Typed/sentinel errors for unavailable, timeout and scan-limit outcomes; wrap underlying causes with `%w`.

- [ ] Write table-driven config tests: loopback default, mandatory token >=32 bytes, Unix path, max-age >0 and <=72h, invalid durations. Do not borrow backend config/secrets.
- [ ] Write fake-Unix-daemon tests: `TestClamdINSTREAMExactBytes`, `TestClamdReplyClassification`, `TestClamdCancellationClosesConnection`, `TestClamdStatusParsing`.
  Assert big-endian chunks/zero terminator and exact bytes; fragmented successful reply parses; missing terminator/EOF/unknown/oversized >4096-byte response fails; FOUND detects; `Heuristics.Limits.Exceeded` returns scan-limit error; timeout/cancel releases connection.
- [ ] Run `go test ./internal/scanner -run 'Test(Config|Clamd)' -count=1`; observe expected missing-code failures before implementing.
- [ ] Implement config and Unix client. Bound each read/write/dial by context and the SPEC budget; actively close blocked I/O on cancellation, join any helper goroutine. Do not use IDSESSION or path commands.
- [ ] Parse supported VERSION response with strict bounded fields and UTC timestamp; unrecognized format fails readiness. Confirm actual supported image format during Task 4, without guessing a successful fallback.
- [ ] Run focused tests under `-race`; record results. Review diff and commit this task only if commits are authorized.

## Task 2 — Verified request service and HTTP contract

**Consumes:** Task 1 `Engine`, errors, `Config` and `DatabaseStatus`.

**Interfaces produced:**

- `Request` fields: `SizeBytes int64`, `SHA256 string`.
- `Result` JSON fields exactly `verdict`, `sha256`, `size_bytes`, `engine`, `scanned_at` from SPEC.
- `NewService(engine Engine, maxAge time.Duration, now func() time.Time) *Service`.
- `(*Service).Scan(ctx context.Context, body io.Reader, req Request) (Result, error)`.
- `(*Service).Ready(ctx context.Context) error`, `(*Service).Drain()` (idempotent).
- `NewHandler(service *Service, token string) http.Handler` registers only the three SPEC routes.

- [ ] Add `TestScanExactDigestAndLength`: good bytes return same hash/count; mismatched digest, truncated body, empty/overlimit body never produce clean. Assert `Engine.Scan` sees only validated bytes; fake receives no call on mismatch.
- [ ] Add `TestScanFreshnessAndReload`: age exactly72h accepted when configured72h, older rejected; future >5min, malformed/missing status and changed pre/post database versions never produce clean; fake clock avoids sleeps.
- [ ] Add `TestScanBusyAndCancellation`: hold engine with a channel; second call gets busy without reading its body; cancellation and all failures release slot; drain rejects new calls; no slot starvation after error.
- [ ] Add `TestScanHTTPContract`: auth absent/wrong=401, digest invalid=400, unknown length=411, >10MiB=413, encoding/type=415, digest mismatch=422, engine limit=422, busy=429/Retry-After=1, dependency failure=503, timeout=504, valid clean/infected=200. Error JSON contains no verdict/raw engine text; responses no-store and <=4KiB.
- [ ] Run focused tests, observe RED, implement bounded buffer/hash, single-slot admission, pre/post freshness and error mapping, then verify GREEN under race detector.
- [ ] Use constant-time credential comparison (e.g. compare fixed-size token digests); never consume body before auth/header validation/admission. Never log hashes or content.
- [ ] Run full `go test -race -count=1 ./...` and `go vet ./...`, using isolated TEST_DATABASE_URL for existing integration tests when available; report unavailable DB instead of claiming a full DB pass. Commit only when authorized.

## Task 3 — Standalone binary and lifecycle

**Consumes:** Task 1 Config/Clamd and Task 2 Service/Handler.

**Produces:** `NewServer(cfg Config, service *Service, handler http.Handler) *http.Server` and `Run(ctx context.Context, cfg Config) error` in `internal/scanner/server.go`; signal-aware `cmd/scanner/main.go` invokes Run. No backend imports/startup.

- [ ] Add `TestServerTimeouts` asserting the exact HTTP constants in SPEC; add `TestServerDrainCancelsOutstandingIO` with controlled engine and listener barriers, bounded deadlines, no arbitrary sleeps.
- [ ] Add startup tests for invalid config and occupied listen address; failure must return operational errors without token disclosure.
- [ ] Observe RED; implement manual wiring, live/ready/draining, graceful shutdown. Use an explicitly owned bounded shutdown context only for server lifecycle; do not detach scan I/O from request cancellation.
- [ ] Make errors stop the process with safe structured logs. HTTP listener startup must not require PostgreSQL/R2. Initial engine absence keeps readiness503 until it loads.
- [ ] Verify focused race tests; `go build -o /tmp/gogo-scanner ./cmd/scanner`; run process-level SIGTERM check with fake daemon and record exit/child/socket cleanup. Commit only when authorized.

## Task 4 — Portable container and actual ClamAV acceptance

**Consumes:** scanner binary and SPEC engine config. **Produces:** a runnable image/local Compose with no backend services.

- [ ] Select a currently supported official ClamAV release, verify availability and licence notices, resolve/pin its image digest in Dockerfile and record engine version. No floating latest tag; do not invent a digest in advance.
- [ ] Add failing acceptance cases before container wiring: cold start/not-ready, missing/wrong token, clean sample, EICAR sample, size boundary and engine limit, engine absent, signature reload/staleness, SIGTERM and child failure.
- [ ] Build Go binary in its own builder stage; copy it into the separate ClamAV runtime. Use `tini` as PID1 and a small Bash supervisor (`wait -n`, explicit traps, child PID ownership) for wrapper/clamd/freshclam. If these OS packages are not present, install/pin through the chosen distro; no extra Go module.
- [ ] Foreground each child; fail the container when any critical child exits; ensure one updater/daemon only (do not also run the image's default supervisor). Forward TERM, allow <=25s drain, stop/reap all children on success or failure. Run scan processes without root; startup may initialize volume ownership before dropping privileges.
- [ ] Set SPEC clamd bounds, UTC, socket permissions, bounded temp storage and disabled core dumps. Keep freshclam signature integrity checks; persist `/var/lib/clamav`. Empty storage starts unready until initial download/load succeeds; startup retries respect upstream download limits.
- [ ] Compose publishes wrapper at `127.0.0.1:8091:10000` only and passes SCANNER_LISTEN_ADDR explicitly. No clamd TCP, no cloud/DB credentials, no content volume; local token from ignored private env file. Exclude all scanner env files from Docker build context.
- [ ] Run real engine checks with enough RAM, both empty and reused signature storage. Generate official EICAR test bytes only at runtime in memory/private temp; clean fixtures after use and never send them to chat/R2.
- [ ] Real limit/reload/freshness tests must exercise actual daemon behavior, using isolated test configuration/fixtures when production bounds are impractical to trigger; separate wrapper-fake evidence from actual engine evidence. Confirm AlertExceedsMax maps to failure and VERSION parser matches loaded metadata. Database staleness proof must concern the loaded engine, not merely a modified file timestamp.
- [ ] Measure startup, warm scan, reload RSS peak, temp usage and SIGTERM duration. If no sufficient-memory Docker environment is available, leave actual-engine acceptance open. Commit only when authorized.

## Task 5 — CI, operator guide and release handoff

**Consumes:** runnable standalone container and prior tests. **Produces:** reproducible scanner checks and deploy instructions, no live hosting changes.

- [ ] Add `make build-scanner` (binary `bin/gogo-scanner`) and `make scanner-check` (invoke `scripts/scanner-acceptance.sh` against explicitly supplied local/private URL); preserve `make build` and backend CI behavior.
- [ ] Acceptance script uses bounded requests, secret-safe output, no shell tracing, and cleanup traps; wrong/missing token, clean/EICAR/hash mismatch and probes checked. Never print tokens or response content/hash. Use a token file/env, not command-line token arguments.
- [ ] Add path-filtered scanner workflow for relevant scanner/runtime files plus go.mod/go.sum and the acceptance script. Run race/vet/build and real-container acceptance on sufficient-memory runner. Signature download failure is a reported failed/incomplete engine gate, not a mocked success. Avoid caching secret env/fixtures in CI artefacts.
- [ ] Write `docs/scanner-operations.md`: contract/status mapping, local commands/env, signature update/stale alert, auth rotation, restart/rollback, resource measurements, child supervision, EICAR handling and no-content-log policy.
- [ ] Document Render dashboard creation as a future manual step: paid Private Service, same region, Dockerfile path `deploy/scanner/Dockerfile`, root build context, internal port10000, safe token, persistent signatures, >=4GB RAM. Verify actual cost and platform startup/probe behavior before provisioning; an image build is not deployment evidence.
- [ ] Document HTTPS/tunnel requirement for non-Render hosts. Show future backend configuration conceptually only; do not add unused backend env vars or activate scan consumers.
- [ ] Run gofmt on changed Go, `go test -race -count=1 ./...`, `go vet ./...`, `make build`, `make build-scanner`, `git diff --check`, meaningful scanner integration checks and lint when installed. Record every unavailable gate.
- [ ] Review authorization/limits/cancellation/protocol/loaded-freshness against SPEC; fix important findings with regression tests. Summarize actual evidence and remaining 08B gates. Commit only when authorized.

## Future integration handoff — separate implementation slice

After standalone acceptance, write/approve the backend 08B integration plan. It
must include exact-byte conditional R2 read and promotion capabilities, strict
image/text validation, typed HTTP client/no redirects/response limits, service auth,
scan-purpose claim and fenced finalization, lease/work budgets, new schema for final
identity/verified states, upload/download/message binding and lifecycle cleanup.
Keep admission closed until real provider, scan, failure/retry and authorization
checks pass. Reuse outbox retry policy; do not build a second durable scanner queue.

## Completion gates

- [ ] SPEC and plan reviewed; implementation method selected.
- [ ] Standalone service contracts pass unit/race and real engine acceptance.
- [ ] Portable image and child shutdown measured; no public clamd or credentials in artefacts.
- [ ] CI, environment example and operator docs verified.
- [ ] Render paid hosting and cost explicitly approved if requested; otherwise deployment pending.
- [ ] 08B integration/provider/activation gates remain explicitly open.

Planning verification: source contracts reviewed; no application code, dependency,
container or paid service created by writing this plan. Implementation tests above
are future commands, not results from this planning session.
