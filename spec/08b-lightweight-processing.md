# 08B extension: lightweight attachment processing

User-approved scope: replace the default heavy ClamAV deployment with strict
JPEG/PNG/TXT validation and image rewriting, keeping a reversible service boundary.
No PDF/Office/archives/SVG/HTML or new dependencies. Upload admission extension
below supersedes the initial default-closed runtime restriction.

The standalone scanner adds authenticated POST /v2/process. Request is raw bytes
with expected SHA256, declared Content-Type and allowlisted file extension. The
response binds input hash/size and output bytes/hash/size/type to engine, versioned
policy and timestamp. sanitize-v1 rewrites JPEG/PNG from decoded pixels (4M pixel
cap, one slot held through response write, 10MiB input/output); text stays UTF8 nonbinary/plaintext. This is
content sanitization, not a malware verdict. clamav-v1 uses the same API and returns
original bytes only after a clean full scan. Existing /v1/scan is preserved and
unavailable in sanitize mode, never faked clean. Sanitizer needs no signatures.

Backend keeps original reservation size/hash for quota, retries and idempotency;
adds processed hash/size/type/engine/policy for the immutable final object. Worker
verifies input, calls processing, verifies returned metadata/bytes, promotes only
output then atomically records processed metadata with fenced ready transition.
Final object remains deterministic and create-only; different retry artifacts
fail closed instead of overwriting. Changing policy/engine with in-flight promoted
jobs may reject mismatched artifacts; drain before upgrades. Existing ready rows
are backfilled as original-byte clamav-v1. Rollback refuses live transformed rows.

Maximum logical quota remains input-based; output<=10MiB means physical storage
must be monitored independently. Processor is single-slot, no request queue,
bounded HTTP/body/decode/output budgets. Cancellation prevents publication; codecs
have finite pixel/input limits and are not interrupted mid-call. Deployment is a
small static Go image, GOMEMLIMIT soft cap plus container memory limit. Prove
memory in a 512MiB container using maximum-pixel inputs, not only tiny samples.
Render backend needs a reachable HTTPS/private scanner endpoint, never localhost
pointing at a developer laptop. Test locally with R2 using only owned random keys.

Acceptance: metadata/extension/type agreement; strict text, invalid/oversize image,
auth, timeout, cancel, unknown policy and corrupt result rejection; source metadata
unchanged; only re-encoded bytes promoted; ready metadata atomic with lease ACK;
legacy ClamAV endpoint unchanged; mode switch tested. Opt-in ticket admission is
now covered by the user-authorized extension below.

## User-authorized upload admission extension

The user explicitly requested opening uploads after processing implementation.
This supersedes the default-closed requirement for ticket creation only. Runtime
admission remains opt-in: ATTACHMENT_UPLOAD_ENABLED=true requires configured R2,
valid scanner credentials and ATTACHMENT_SCAN_ENABLED=true. Defaults stay closed.
Members can reserve allowed .jpg/.jpeg/.png/.txt declarations, receive a short-lived
create-only quarantine PUT URL, complete, poll private metadata and cancel. Match
filename extension/type at admission. Existing ownership, quota, idempotency and
cleanup deadlines apply unchanged. No automatic cloud provisioning or live config
changes; backend operators enable the flag after deploying scanner v2/migration12.

Download/message binding remain outside this request. Missing browser bucket CORS,
provider egress and cold starts are operational checks, not startup code bans.
The backend never marks files ready before processing. Scanner outages can leave
uploads scanning/retrying; monitor dead-letter scan/cleanup jobs and retained quota.
