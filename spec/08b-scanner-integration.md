> Updated by [lightweight processing](08b-lightweight-processing.md): v2 artifacts
> replace the same-byte-only worker contract. The original integration below
> describes the ClamAV v1 baseline.

# 08B — Backend scanner integration

## Scope

Integrate the independent scanner HTTP v1 with the existing attachment outbox.
Read and validate exact R2 quarantine bytes, scan, conditionally create and verify
the same bytes under a deterministic server-only `final/<uuid>` key, then persist
`ready`. Upload admission remains closed; message binding/download and actual
provider rollout are separate gates. No new dependencies or hosted resources.

## Contracts and ownership

- Opt-in `ATTACHMENT_SCAN_ENABLED=false`; enabled requires all R2 credentials,
  `SCANNER_URL` and random `SCANNER_API_TOKEN` (32–512 printable nonspace ASCII).
- HTTPS URL, or HTTP only for loopback/private IPs, `.internal` or single-label private service endpoints; no URL auth,
  query, fragment, path, redirects or environment proxy for service credentials.
- Scanner POST raw bytes, Content-Length, expected SHA256; 22s timeout, response
  at most4KiB, strict JSON/verdict/hash/count/engine/time validation. Invalid200,
  auth, timeout, overload, outage never mean clean.422 is terminal rejection;
  429/5xx/network failures retry via existing eight-attempt outbox budget.
- One sequential scan worker. Independent scan lease60s, full work budget45s;
  claims commit before network I/O. Existing cleanup/notification/broker leases
  remain30s. Cancellation does not acknowledge or detach I/O.
- Verify HEAD identity and bounded conditional GET (If-Match); actual hash/length,
  full PNG/JPEG decode with <=25megapixels and UTF8 nonbinary plain text. No HEAD
  or metadata-only clean decision. Scanner independently binds bytes/hash/count.
- Promotion uses the verified in-memory buffer, create-only PUT and conditional
  destination verification; retry412 requires reading and comparing existing
  bytes. Never promote the latest mutable source or overwrite final bytes.
- Final identity is `final/` plus validated quarantine UUID. This is determined
  at reservation time without losing identity in parent cascades/DB failures.
  Cleanup deletes both possible keys before releasing quota/acknowledging deletion.
- New migration adds ready/scan metadata; ready implies successful fenced scan
  finalization and cleanup-after=24h. Safe public metadata exposes verified only
  for ready. No final keys, reports or secrets in DTOs/events/logs.
- Prepare/finalize lock scan delivery, room, membership, attachment in that order;
  recheck live token, references/state, current membership and cleanup deadline.
  Cancel/removal/expired lease cannot become ready. No network I/O in transactions.
- Complete on ready is idempotent; cancel ready schedules existing cleanup.
  Sweeper continues to reclaim orphaned/expired/rejected/ready-unattached rows.
- Production admission remains rejected even with scanner configured until real
  R2 create-only/CORS/late-write/grace and full attachment lifecycle gates pass.

## Verification

Regression tests: client response/auth/redirect/timeout/privacy; provider signing,
conditional read, immutability/retry conflict; worker clean/infected/invalid bytes,
outage/cancel and finalize failure; PostgreSQL upgrade/down guard, ready/rejected,
stale token/reclaim, cancel/removal and independent purpose progress. Run full
race/vet/build using disposable PostgreSQL, never personal Neon. Real R2/scanner
hosted calls require separately supplied endpoints/credentials; disclose absence.
