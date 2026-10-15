# Frontend handoff priority fixes — 2026-10-15

Branch: `fix/fe-handoff-priority`, based on `develop` at `20f20a0`.
Scope: priority CORS and private-room invitation findings from `BACKEND_HANDOFF.md`.
The original frontend handoff is preserved. Local verification does not close
its deployed/browser acceptance gates.

## Implemented

- CORS permits `Idempotency-Key` alongside the existing request headers for
  configured origins; unconfigured origins remain denied.
- Browser JavaScript can read `Retry-After` and `X-Request-ID`, with
  `Content-Length` still exposed. Tests exercise an actual rate-limit response.
- After membership removal, a manager's invite creates a new pending invitation
  ID. Concurrent invite retries reuse that pending invitation.
- Acceptance of a historical accepted invitation returns 409 when membership is
  missing. It cannot recreate access. Retrying acceptance while membership exists
  remains idempotent.
- Invitation creation/response lock users before rooms/invitations. Room locking
  serializes invitation retries and acceptance with manager removal. Notification
  records remain scoped to the membership generation; rejoin does not restore
  old inbox entries or permit delayed old-generation delivery.

No migration, new dependency or environment variable is required.

## Local verification

Go 1.23.12 and PostgreSQL 16 were run in disposable containers with an isolated
`gogo_handoff_test` database and private schemas. Existing local application and
PostgreSQL containers were not changed.

- Regression evidence against the original implementation: allowed origins lacked
  the idempotency/exposed headers; replaying an accepted invitation restored
  removed membership instead of rejecting it.
- CORS tests cover both frontend origins from the handoff, OPTIONS for the message
  path, real 429 headers and rejection of an unconfigured origin.
- PostgreSQL integration covers initial acceptance/retry, simultaneous old
  acceptance and removal, concurrent re-invites, denial before fresh acceptance,
  fresh membership generation, and old/delayed notification isolation.
- `gofmt` applied to changed Go files.
- `CGO_ENABLED=1 go test -race -count=1 ./...` passed, with
  `TEST_DATABASE_URL` configured for the isolated database.
- `go vet ./...` passed.
- `go build -buildvcs=false -o /tmp/gogo-fe-handoff-server ./cmd/server` passed.
  VCS stamping was disabled because the container Git ownership check rejected
  the host-mounted checkout; compilation and linking were verified.

The host has no Go installation. The initial builder lacked a C compiler;
GCC/musl development tools were installed only in the disposable check container
for the successful race run. `golangci-lint` is unavailable.

## Deployment and frontend acceptance still required

1. Deploy this branch's changes through the usual reviewed Git workflow.
2. From each configured frontend origin, verify OPTIONS accepts
   `authorization,content-type,idempotency-key`.
3. Run direct two-account browser acceptance without the temporary proxy:
   mention send, explicit lost-response retry with identical key/payload,
   canonical message identity and one private inbox effect.
4. Verify private-room remove → re-invite → pending → accept. Old acceptance
   must not restore membership; old inbox entries must stay absent after rejoin.
5. Trigger a controlled staging rate limit and verify the browser reads both
   response headers and applies its cooldown behavior.

Attachments/scanner readiness, disposable account cleanup, historical 502 logs,
final-host WebSocket reconnect and the cursor documentation mismatch remain
separate follow-ups. No staging data or account was changed by these fixes.
