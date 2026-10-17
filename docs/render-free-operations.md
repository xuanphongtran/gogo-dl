# Render Free operation checks

Run one Go Web Service instance with Neon PostgreSQL. Local Hub state and limits
match this topology; distributed 09B–09D remain deferred. The application workers
run only while the web process runs. Spin-down, cold starts and worker lag are
accepted; no continuous synthetic monitor or keepalive service is required.

## One-shot checks

Run this from a development machine with Go 1.23+, after deploying the desired
commit. The command exits nonzero on a failed check. It performs one pass and does
not retry forever or create application records:

```sh
make smoke SMOKE_ARGS='-url https://gogo-dl.onrender.com'
```

Checks: `/health`, `/livez`, `/readyz`, `/readyz/realtime` have their expected JSON
and HTTP 200; the public `/metrics` endpoint returns 404. Output contains check
names, status codes and elapsed times. The default 120-second per-request timeout
allows a cold start; override it with `-timeout 30s`. Redirects fail validation.
This check wakes a sleeping Free service, so run it manually as needed.

For authenticated recovery, use an existing staging user who belongs to a small
test room. Store that user's fresh access token in a private file outside the
repository (for example `/tmp/gogo-smoke-token`, mode 0600). Do not place a token
in command arguments, a URL, terminal output, Git or this document.

```sh
make smoke SMOKE_ARGS='-url https://gogo-dl.onrender.com -token-file /tmp/gogo-smoke-token -room 7 -origin https://your-allowed-frontend.example'
```

The authenticated pass checks profile, inbox and room history. It opens a WS
connection using an Authorization header, joins the room, verifies its presence
snapshot, closes, then opens a new connection and rejoins. The origin must be
configured in both `CORS_ALLOWED_ORIGINS` and `WS_ALLOWED_ORIGINS`; without a frontend, use a specifically allowed
test origin before this staging check. It creates no messages, memberships,
notifications or read markers. Each connection has a bounded snapshot wait and
frame limit; failures expose no response bodies or tokens.

## Redeploy / spin-down recovery

1. Prepare an existing staging room with a known message and an existing mention
   notification owned by the test user. Capture their IDs through the authorized
   API. Use recent records: recovery checks inspect the latest 100 entries and
   the inbox has a 30-day retention policy.
2. Run the authenticated command with `-expect-message 42
   -expect-notification 9` added to the flags. Save only the check output and the
   deployed commit/version; output contains neither token nor message text.
3. Redeploy through Render, or observe natural spin-down/wake. Run the same command
   once after recovery, refreshing the access token if it expired. Verify the
   expected IDs and both WS joins still pass. A new connection after a planned
   client close tests reconnect handling; it alone does not prove interruption
   handling during an actual Render redeploy.
4. Confirm startup migration completion and worker recovery in Render logs. WS
   subscriptions, presence/typing and local rate buckets reset after restart;
   clients rejoin and fetch durable state through REST. Record first-request
   latency and notification backlog without treating observations as an SLA.

Render redeploy/cold-start timing, browser reconnect behavior and frontend origin
setup require real staging verification. Local tests and a public probe pass do
not satisfy those gates.

## Durable worker backlog

Run these read-only counts through an authorized database session; never print a
DSN or user/message content:

```sql
SELECT d.purpose, d.state, count(*) AS jobs,
       max(clock_timestamp() - o.occurred_at) AS oldest_intent_age
FROM domain_outbox_deliveries d
JOIN domain_outbox o ON o.event_id = d.event_id
WHERE d.state <> 'done'
GROUP BY d.purpose, d.state
ORDER BY d.purpose, d.state;
```

After wake, eligible notification jobs resume; an abandoned claim is reclaimed
after lease expiry. Disabled/muted recipients, removed generations and deleted
sources are suppressed; retry limits may produce dead letters. Cleanup resumes
only with its configured R2 adapter, retaining its delay and fencing policy.
Investigate dead letters before any explicit replay; do not mark them delivered.
Pending `broker` intents are expected while 09B is deferred. Monitor growth;
do not delete/acknowledge them to disguise delivery or delete the shared outbox.

## Failure and rollback

- If `/livez` is healthy but `/readyz` fails, inspect safe application logs and DB
  connectivity/migration state. `/health` alone does not prove DB readiness.
- If only realtime readiness fails, inspect Hub startup/drain; recover through
  authenticated REST while investigating. Do not increase the instance count.
- Roll back to a known compatible binary via Render and keep the schema/durable
  intents. Do not run migration down or clear tables on the hosted database.
- Before restoring data, stop application writes, restore a consistent DB/object
  backup, and verify authorized history/inbox and cleanup references. Record actual
  RPO/RTO. This guide does not provision backups or claim a tested recovery time.

## Recovery decision and evidence record

Use the following sequence for a manual incident drill. Perform destructive
restore work only after explicit approval, against a separate recovery target
first. A read-only probe pass never authorizes changing hosted data.

1. Record deployed commit, UTC incident time, last known successful write and
   observed symptoms. Distinguish a sleeping origin from database failure using
   startup logs and the separate liveness/readiness probes.
2. If schema migration failed, inspect version/dirty status using an authorized
   read-only session. Resolve the migration cause before restarting; do not clear
   dirty flags, force migration versions or run down migrations as a shortcut.
3. For database recovery, select an approved backup/restore point and record the
   potential loss window. Restore to an isolated target; check migration version,
   authorized history/inbox and membership before changing the application DSN.
   Keep credentials out of command arguments, shell history and evidence files.
4. Reconcile object state before allowing the recovered service to issue provider
   deletes. PostgreSQL rollback cannot recreate an R2 object already deleted by
   a later cleanup operation. Keep the recovery target's R2 credentials absent
   until object references, quarantine deadlines and cleanup intents are checked.
   A restored `deleted` checkpoint with missing bytes is consistent; a restored
   live reference to missing bytes needs investigation, not a fabricated upload.
5. Fence the previous writer/service before promoting the target. Run one
   application instance, confirm migrations/readiness, then compare authorized
   REST recovery IDs. Check workers only after the DB/object reconciliation gate.
6. Observe eligible work, retry backoff and dead letters. Do not accelerate hosted
   leases or retry schedules using the fixture-only SQL from tests. Retain all
   broker intents until distributed delivery is explicitly implemented.
7. Record first-request latency, oldest eligible job age, recovery completion time,
   observed data loss, and the rollback decision. RTO ends when authorized API and
   required recovery checks pass; process startup alone is insufficient.

Use bounded aggregate-only queries to distinguish due work from intentional
backoff. Run through an authorized session; do not export object keys, file names,
message text, tokens or DSNs:

```sql
BEGIN READ ONLY;
SET LOCAL statement_timeout = '5s';
SELECT purpose, state, count(*) AS jobs,
       count(*) FILTER (
         WHERE (state = 'pending' AND available_at <= clock_timestamp())
            OR (state = 'leased' AND lease_until <= clock_timestamp())
       ) AS eligible_jobs,
       max(attempts) AS highest_attempts
FROM domain_outbox_deliveries
GROUP BY purpose, state
ORDER BY purpose, state;

SELECT purpose, last_error, count(*) AS dead_jobs
FROM domain_outbox_deliveries
WHERE state = 'dead'
GROUP BY purpose, last_error
ORDER BY purpose, last_error;
COMMIT;
```

The [existing local baseline](../plan/09-scale-observability.md#one-process-local-smoke-baseline)
records one-process test workload, resources and DB pool settings. It establishes
neither Render Free capacity nor a cold-start budget. For a staging capacity
observation, agree a bounded workload first and record room/connection counts,
request rate, HTTP/WS latency, errors/drops, memory, pool wait and job lag. Stop at
the agreed error/resource threshold; do not infer multi-instance guarantees.
No staging load test is part of the read-only smoke command.

### Cleanup restart regression

`internal/database/cleanup_recovery_integration_test.go` exercises the actual
cleanup worker with PostgreSQL and a deterministic object-store fake. It models
an abandoned lease before deletion, deletion without an acknowledgement, and a
provider outage after restart. A replacement worker uses a new connection pool;
the test checks expiry/fencing, backoff, idempotent delete, released quota,
retained tombstone identity and untouched scan/notification/broker progress.
The fixture advances only its own timestamps instead of waiting for real leases.

Run with `TEST_DATABASE_URL` pointing only to a disposable, empty database whose
name ends in `_test`:

```sh
go test -race -count=1 ./internal/database \
  -run TestCleanupWorkerRecoversAfterConnectionAndWorkerRestart -timeout 90s
```

This is local connection/worker restart evidence. It does not simulate a
PostgreSQL process crash, verify actual R2 DELETE semantics, prove an object
backup exists, or satisfy Render redeploy/spin-down and browser acceptance gates.

References: [Render Free](https://render.com/docs/free),
[WebSocket reconnect/shutdown](https://render.com/docs/websocket),
[Phase 9 plan](../plan/09-scale-observability.md).
