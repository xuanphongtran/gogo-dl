# Phase 8A — local verification and query plans

08A is implemented and verified locally on `ft/phase-8a` (2026-10-02).
Production rollout and the independent Phase 05 verification gate remain pending.

## Implementation

- `GET /api/v1/rooms/:id/messages/search` uses Bearer authentication and service
  authorization against room/membership rows held `FOR SHARE` in one transaction.
  Public nonmembers receive 403; private nonmembers/missing rooms receive 404.
- Valid UTF-8 query: trimmed, 1–256 bytes; no NUL; PostgreSQL rejects input with
  zero searchable nodes. `plainto_tsquery('simple', q)` is parameterized.
- Default limit 20, range 1–100, positive exclusive `before`; read one extra match
  to determine `next_before`. Empty pages serialize as `[]` with a null cursor.
- Only live messages in this room; descending IDs. Existing message projection,
  lifecycle revisions and nullable authors are retained; history is unchanged.
- Migration `000008` adds a stored generated vector and partial GIN index. Edits
  and tombstones update the vector in their existing transaction. Down removes
  only this index/column; no historical migration was changed.

Plain text joins surviving terms with AND; punctuation is not advanced syntax.
See [PostgreSQL query parsing](https://www.postgresql.org/docs/16/textsearch-controls.html)
and [generated vector storage](https://www.postgresql.org/docs/16/textsearch-tables.html).

## Verification

Go 1.23.12, PostgreSQL 16.15 (`postgres:16-alpine`), disposable owned database
ending in `_test`. Existing application containers/databases were untouched.

- Service: byte/encoding/bound validation, normalization/defaults, lookahead,
  privacy policy, dependency failure and context propagation.
- HTTP: authenticated success/empty JSON, 400/401/403/404/500, cursor overflow,
  explicit zero bounds and no infrastructure detail exposure.
- Repository: begin/read/parse/select/scan/rows/commit failures and rollback;
  cursor SQL and nullable author projection.
- PostgreSQL: clean migrations, 7→8 backfill, 8 down/reapply, live/deleted rows,
  all-term/punctuation/injection-shaped queries, accented Vietnamese, pagination
  during edits and immediate edit/delete index changes.
- Search-first removal blocks on membership; removal-first search blocks and
  honors its deadline. After removal commits, private search returns 404.
- Full `go test -race -p 2 -count=1 ./...` with PostgreSQL passed; focused final
  search tests passed after review fixes. `go vet -p 2 ./...`, `make build`,
  `make docs`, gofmt and `git diff --check` passed. Linter unavailable.

Historical downgrade tests now target schema 5/6 explicitly so adding migration 8
does not cause them to test the wrong down migration. Concurrent startup expects
the new migration head, version 8.

## Representative local measurement

Shared host: Intel Core i5-13400, approximately 15.4 GiB RAM. PostgreSQL container
has no configured CPU/memory limit. Go tests use `GOMAXPROCS=2`; no production
resource equivalence is claimed. No competing-load or cold-cache experiment.

Fixture: 100,000 messages in two rooms (50,000 each), approximately 200-byte text;
`common` in every message and `rareterm` in every 500th message (100 matches/room).
Room messages are clustered by ID; 50,000 other-room IDs follow the target room.
Run `ANALYZE messages`, then `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` on the actual
first-page projection/filter/order with limit 21, and ten warm service requests.

| Operation | Observed result |
| --- | --- |
| Embedded migration 7→8 on 100,000 existing rows | 1.563 s |
| Selective `rareterm` query | 0.536 ms SQL; 1.704 ms mean service latency |
| Common `common` query | 8.762 ms SQL; 9.205 ms mean service latency |

Selective plan: GIN bitmap index scan on `idx_messages_search_vector`, bitmap heap
scan with room filter, top-N sort by ID, then username lookup. Both rooms' 200
rare matches enter the room filter; 100 are discarded.

Common plan: descending `messages_pkey` scan, room/deletion/vector filter, username
lookup and limit. PostgreSQL chose this over the available `(room_id, id DESC)`
index and discarded 50,000 newer other-room rows. This reveals a common-term cost
that grows with room distribution; the fixture does not prove capacity for much
larger or differently skewed histories. No planner settings were forced.

Reproduce with `TEST_DATABASE_URL` set to a fresh disposable database:

```bash
go test -race -count=1 ./internal/database -run TestPhase8ASearchRepresentativePlans -v
```

The test logs complete JSON plans, buffers, migration time and warm latency.
These are local observations, not production SLOs. Before deployment, measure
with staging data and expected room distributions/concurrent writes. Migration 8
rewrites messages and acquires a table lock; startup's statement budget is 5 s.
A larger/slower database needs a reviewed migration procedure before rollout.
No Neon/Render changes or push were performed.
