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

References: [Render Free](https://render.com/docs/free),
[WebSocket reconnect/shutdown](https://render.com/docs/websocket),
[Phase 9 plan](../plan/09-scale-observability.md).
