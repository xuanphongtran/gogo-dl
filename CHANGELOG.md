# Changelog

## Unreleased

Single-instance candidate; release version/tag not assigned.

### Features

- Public/private rooms, invitations, owner/moderator/member roles and revocation.
- Message editing, revision conflicts, deletion tombstones and lifecycle events.
- Authorized presence/typing and durable personal read cursors.
- Room-scoped PostgreSQL search, typed mentions, preferences and private inbox.
- Private R2 upload tickets, JPEG/PNG/TXT processing, atomic file-only/message
  attachments, authorized short-lived downloads and lifecycle cleanup.
- Private telemetry, health/readiness probes, bounded drain and startup migration
  diagnostics with no raw secrets/errors in logs.
- Isolated process restart, DB outage, backup/restore and PostgreSQL crash tools.

### Compatibility

- Schema13; drain older backend/cleanup binaries before accepting bound files.
- Build the package `./cmd/server`; uploads require explicit configured opt-in.
- Signed downloads are60-second bearer links; transformed metadata differs from
  source metadata. Processing verification is not an antivirus guarantee.

### Outside this release

Broker/cross-node09B–09D and ClamAV are deferred. No distributed-delivery or
production capacity/PITR guarantee is introduced. See
[pending work](plan/pending.md) and [release preparation](docs/release.md).
