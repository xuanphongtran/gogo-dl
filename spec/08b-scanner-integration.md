# 08B — Processing service boundary

**Status:** implemented; current backend uses authenticated `POST /v2/process`.
[Lightweight policy](08b-lightweight-processing.md) defines output semantics;
[binding/download](08b-message-attachments.md) defines schema13 usage.
ClamAV is an optional compatible engine, indefinitely deferred for operation.

## Transport and trust

- Scan defaults off. Enabling requires R2 credentials, `SCANNER_URL` and a
  distinct `SCANNER_API_TOKEN` of32–512 printable nonspace ASCII characters.
- Use HTTPS; HTTP is restricted to validated loopback/private IPs, `.internal`
  and single-label private service endpoints. Reject URL auth/path/query/fragment,
  redirects and environment proxies for authenticated service traffic.
- POST raw bytes with `Content-Type: application/octet-stream`, Content-Length,
  Bearer auth, `X-Content-SHA256`, `X-File-Content-Type` and `X-File-Extension`.
  The22s client budget and request context bound I/O; input is1 byte–10MiB.
- Require HTTP200 with `Content-Type: application/json`, strict JSON with no extra fields/trailing values,
  at most14MiB encoded response and1 byte–10MiB decoded output. Independently verify
  exact input/output size/hash/type, accepted engine/policy, verdict and timestamp
  within5 minutes in either direction. Unknown or unverifiable output fails closed.
- 422 is terminal rejection;429/5xx/network and invalid responses are unavailable,
  not accepted/clean; the bounded worker retry budget applies. Never fall back to
  original bytes when processing fails. No credentials/URLs/reports in public DTOs.

## Persistence, promotion and cleanup

One sequential worker uses a60s scan lease and45s job budget; claims commit
before network I/O. Cleanup/notification/future-broker leases remain independent.
Fenced finalization rechecks live token, room/member references and attachment
state/deadline. Cancel/removal/stale leases cannot produce a ready attachment.
No provider I/O occurs in transactions or inside the Hub event loop.

Pin quarantine key/ETag and conditionally read exact bytes; verify actual length,
SHA256 and format instead of trusting HEAD or client metadata. Preserve original
size/hash for quotas and retry identity. Validate processed bytes independently,
then create immutable deterministic `final/<reservation UUID>` output. A412 retry
must read/compare the existing final object; never overwrite conflicting output.
The ready transition, processed metadata and lease acknowledgement are atomic.

Retain quarantine/final identities across failed finalization and parent cascades.
Cleanup removes both possible objects before releasing quota/acknowledging
completion. Ready-unattached files expire after24h; bound retention, cancellation,
authorized metadata and download follow the schema13 contract.

Legacy `/v1/scan` remains a compatibility endpoint, unavailable in sanitize mode;
it must never manufacture a clean antivirus verdict. Its historical deployment
proposal is not a release dependency. Current open work is in
[pending](../plan/pending.md), not in obsolete scanner execution checklists.
