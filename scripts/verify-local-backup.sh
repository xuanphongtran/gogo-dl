#!/usr/bin/env bash
# Owns only newly named Docker fixtures. Keeps the stopped DB and its volume.
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
fixture="gogo-backup-$(date +%s)-$$"
database_container="${fixture}-pg"
data_volume="${fixture}-data"
module_cache=${RECOVERY_MODULE_CACHE:-gogo-dl-scan-gomod}
build_cache=${RECOVERY_BUILD_CACHE:-gogo-dl-scan-gocache}
artifacts=$(mktemp -d /tmp/gogo-backup.XXXXXX)
database_started=false
cleanup() {
  if [[ $database_started == true ]]; then
    docker stop "$database_container" >/dev/null
    printf 'Retained stopped fixture: %s; volume: %s\n' "$database_container" "$data_volume"
  fi
  printf 'Verification fingerprints: %s\n' "$artifacts"
}
trap cleanup EXIT

# Module downloads use the normal Docker network before DB isolation. The repo
# is read-only and no application env/credentials are supplied to this process.
docker run --rm -v "$repo_root:/app:ro" -w /app \
  -v "$module_cache:/go/pkg/mod" golang:1.23 go mod download
docker run -d --name "$database_container" --network none \
  --label io.gogo.fixture=backup-rehearsal \
  -e POSTGRES_PASSWORD=local-recovery-fixture-only -e POSTGRES_DB=source_test \
  -v "$data_volume:/var/lib/postgresql/data" postgres:16-alpine >/dev/null
database_started=true
for attempt in {1..30}; do
  if docker exec "$database_container" pg_isready -h 127.0.0.1 -p 5432 -U postgres -d source_test >/dev/null 2>&1; then break; fi
  if [[ $attempt == 30 ]]; then printf 'PostgreSQL fixture did not become ready\n' >&2; exit 1; fi
  sleep 1
done

run_go() {
  docker run --rm --network "container:$database_container" \
    -v "$repo_root:/app:ro" -v "$artifacts:/artifacts" -w /app \
    -v "$module_cache:/go/pkg/mod" -v "$build_cache:/root/.cache/go-build" \
    -e CGO_ENABLED=1 -e RECOVERY_SERVER_BINARY=/artifacts/server \
    -e 'RECOVERY_DATABASE_URL=postgres://postgres:local-recovery-fixture-only@127.0.0.1:5432/source_test?sslmode=disable' \
    -e 'RECOVERY_RESTORED_DATABASE_URL=postgres://postgres:local-recovery-fixture-only@127.0.0.1:5432/restored_test?sslmode=disable' \
    golang:1.23 "$@"
}

run_go sh -c 'go build -buildvcs=false -race -o /artifacts/server ./cmd/server && go test -buildvcs=false -tags=integration -race -v -count=1 -timeout=120s -run "TestServerProcessRecovery|TestRecoveryURLSafety" ./integration/recovery'

# Source application has stopped. Never dump a personal DB or restore over one.
started_at=$SECONDS
docker exec "$database_container" pg_dump -U postgres -d source_test \
  --format=custom --file=/var/lib/postgresql/data/recovery.dump
printf 'dump_seconds=%d\n' "$((SECONDS-started_at))"
docker exec "$database_container" createdb -U postgres restored_test
started_at=$SECONDS
docker exec "$database_container" pg_restore -U postgres -d restored_test \
  --exit-on-error --single-transaction --no-owner /var/lib/postgresql/data/recovery.dump
printf 'restore_seconds=%d\n' "$((SECONDS-started_at))"

# Compare every user table (including exact row values) and every sequence.
# Emit counts/digests only, never individual password hashes or row bodies.
cat >"$artifacts/fingerprint.sql" <<'SQL'
SELECT format(
  'SELECT %L,count(*),coalesce(md5(string_agg(to_jsonb(r)::text,E''\n'' ORDER BY to_jsonb(r)::text)),'''') FROM %I.%I r;',
  schemaname||'.'||tablename,schemaname,tablename)
FROM pg_tables WHERE schemaname='public' ORDER BY tablename
\gexec
SELECT format('SELECT %L,last_value,is_called FROM %I.%I;',
  schemaname||'.'||sequencename,schemaname,sequencename)
FROM pg_sequences WHERE schemaname='public' ORDER BY sequencename
\gexec
SQL
for database in source_test restored_test; do
  docker exec -i "$database_container" psql -XAt -v ON_ERROR_STOP=1 -U postgres -d "$database" \
    <"$artifacts/fingerprint.sql" >"$artifacts/$database.fingerprint"
done
diff -u "$artifacts/source_test.fingerprint" "$artifacts/restored_test.fingerprint"
printf 'PASS all table data and sequence fingerprints match\n'
run_go go test -buildvcs=false -tags=integration -race -v -count=1 -timeout=90s \
  -run '^TestRestoredServer$' ./integration/recovery
printf 'PASS isolated PostgreSQL backup/restore rehearsal (not production RPO/RTO)\n'
