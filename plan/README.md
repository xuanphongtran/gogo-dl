# Roadmap

Status as of 2026-10-22. This page tracks implementation; it does not declare a
published release or a hosted rollout successful.

| Phase | Scope | Implementation |
| --- | --- | --- |
| 01 | Test foundation | Complete locally |
| 02 | Data integrity and transactions | Complete locally |
| 03 | WebSocket authorization | Complete locally |
| 04 | API/origin/resource limits | Complete locally |
| 05 | Membership, roles and invitations | Complete locally |
| 06 | Message edit/delete and revision events | Complete locally |
| 07 | Presence, typing and personal read state | Complete locally |
| [08](08-rich-messaging.md) | Search, attachments and mentions/inbox | Implemented; local verification recorded |
| [09A](09-scale-observability.md) | Single-instance observability and recovery | Implemented; local verification recorded |
| 09B / 09C / 09D | Broker, cross-node state and distributed rollout | Not implemented; deferred |
| [Deployment migrations](10-serverless-migrations.md) | Embedded migrations and safe startup failure | Implemented; local verification recorded |

Frontend completion is user-confirmed. R2/lightweight processing is implemented;
no further provider/scanner verification is scheduled in this documentation task.
ClamAV is deferred indefinitely and is not a release prerequisite.

## Next work

All open work is tracked in [pending.md](pending.md), with release/deployment
gates separate from future functionality. In particular, the current release
scope has one instance and requires no broker. Future distributed contracts are
retained in [spec/09](../spec/09-scale-observability.md), not promised at runtime.

- [Release scope and procedure](../docs/release.md)
- [Verification record](../docs/verification.md)
- [Contract index](../spec/README.md)

Completed execution checklists and superseded scanner proposals were removed to
avoid conflicting status reports. Their history remains in Git. Current specs,
API contracts and operational runbooks remain maintained.
