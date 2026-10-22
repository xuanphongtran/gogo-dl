# Release preparation

**State: draft candidate; not tagged or published.** No new release version is
chosen here. Swagger/AsyncAPI version labels describe the API document, not a
new Git release. Open work is maintained in [plan/pending.md](../plan/pending.md).

## Scope

The candidate contains phases 01–08, 09A single-instance operations and embedded
startup migrations. Features include roles/private-room invitations, message
revision/tombstones, presence/typing/read state, search, mentions/private inbox,
R2 attachment processing/binding/download/cleanup, probes/telemetry/drain and safe
startup diagnostics. Frontend completion is user-confirmed, not a new browser
execution report.

Use **one backend instance** with PostgreSQL and, when uploads are enabled,
private R2 plus the existing lightweight processor. There is no broker, shared
presence/typing/rate store, cross-node fanout or multi-instance readiness claim.
ClamAV is indefinitely deferred and is not required for this candidate.

## Compatibility and configuration

- Migration head13; migration SQL is embedded. No manual CLI is required at
  startup. DB/dirty-schema failures must exit without HTTP admission.
- Before schema13 bound traffic, drain old backend **and cleanup** processes.
  Older binaries can mishandle attached retention/cancel semantics. Keep a backup
  and compatible rollback binary; do not force down migrations across bindings.
- Build/run `./cmd/server` as a package, including startup helpers. Example:

  ```bash
  CGO_ENABLED=0 go build -o bin/gogo-dl ./cmd/server
  ./bin/gogo-dl
  ```

- Production injects secrets and exact frontend CORS/WS origins; use the direct
  Neon endpoint for this topology. Render does not automatically map `PORT` to
  `SERVER_PORT`: set the latter explicitly to 10000.
- Upload admission defaults off. Enabling requires all R2 credentials, processor
  URL/token and both upload/scan flags true. Compose uses `configs/.env`; a host
  process additionally loads `.env.local`. This is deployment configuration,
  not a reason to modify committed development defaults.
- Images may change hash/size through rewriting. Quotas/retries use source bytes;
  bound-message metadata/downloads describe processed output. `verified` is a
  processing-policy result, not an antivirus guarantee.
- Signed GET links are bearer credentials valid 60s; revocation is not immediate
  for an already-issued link. File-only messages may become blank after uploader
  deletion while historical text/deleted-user semantics are preserved.
- Swagger/AsyncAPI UI is disabled in production. Keep committed generated assets
  synchronized; use the YAML/JSON artifacts when reviewing the production API.

## Finalize the release

1. Resolve [REL-01–03](../plan/pending.md#before-tagging--publishing-the-candidate):
   select the exact commit/version and attach test/DB/build/contract evidence.
   [verification.md](verification.md) records existing observations and gaps.
2. Review [CHANGELOG.md](../CHANGELOG.md), the [API guide](api-guide.md) and
   [attachment contract](attachments.md). Set the release version only when agreed.
3. Complete [DEP-01–03](../plan/pending.md#before-promoting-to-a-hosted-environment)
   for the deployment target: backup, env, coordinated upgrade and actual hosted
   readiness/redeploy evidence. Local rehearsals do not satisfy these gates.
4. Create a tag/release and deploy through the reviewed Git workflow only when
   requested. This documentation cleanup does not create a tag, push or publish.
5. On deployment, verify health/readiness and safe startup fields, authenticated
   REST/WS recovery, private metrics denial and durable worker progress. Use the
   [deployment guide](deploy-render-neon.md) and [operations runbook](render-free-operations.md).

Sustained capacity, private telemetry ownership and production PITR/object recovery
remain explicit operation follow-ups. No production SLO is claimed by this draft.
