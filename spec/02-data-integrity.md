# 02 — Data Integrity and Transactions Specification

**Related plan:** [plan/02-data-integrity.md](../plan/02-data-integrity.md)  
**Status:** Proposed  
**Priority:** P0

## Objective

Make schema constraints and multi-step mutations express the same domain rules
and prevent partial room creation, ambiguous account deletion, and hidden
constraint failures.

## Durable deletion decisions

These decisions resolve the open choices in the high-level plan for this
implementation slice:

### Account deletion

- Account deletion is a hard delete for the `users` row.
- A user with rooms they own cannot be deleted yet. The service returns a
  stable `409 Conflict` domain error explaining that ownership must be
  transferred or the room must be archived first. Ownership transfer belongs
  to plan 05.
- Room ownership therefore remains non-null and uses explicit delete
  restriction behavior.
- Room memberships are deleted automatically with the user.
- Message history is retained. The message author reference becomes nullable
  when the user is deleted, and history renders a stable deleted-user label.

This policy avoids silently deleting conversations and avoids leaving a room
without an owner before ownership-transfer behavior exists.

### Foreign-key actions

The next migration must make the following constraints explicit:

- `rooms.created_by`: `NOT NULL`, `ON DELETE RESTRICT`.
- `room_members.room_id`: `ON DELETE CASCADE`.
- `room_members.user_id`: `ON DELETE CASCADE`.
- `messages.room_id`: `ON DELETE CASCADE`.
- `messages.user_id`: nullable, `ON DELETE SET NULL`.

The existing migration must not be edited. Add a new up/down migration and
verify both clean installation and upgrade from the current schema.

## Mutation transaction boundaries

### Create room

Creating a room and inserting the creator's membership is one atomic use case:

1. Begin a transaction using the request context.
2. Insert the room and return its ID/timestamp.
3. Insert the creator membership.
4. Commit only after both writes succeed.
5. Roll back on every failure path, including context cancellation.

The service must never return a successfully created room if creator membership
was not persisted.

The transaction belongs in the repository method that represents the complete
use case, for example `CreateRoomWithMember`, rather than being split across
two independent repository calls.

### Delete account

Account deletion is also one transaction:

1. Check ownership and return the stable conflict error if owned rooms exist.
2. Delete the user within the transaction.
3. Let the declared foreign-key actions detach message authors and remove
   memberships.
4. Commit or return a wrapped infrastructure error.

The foreign key remains the final race-safe guard against a room being created
for the user between an application check and the delete.

## Error mapping

PostgreSQL constraint errors must be classified with `*pq.Error` and the
SQLSTATE code, not substring matching. At minimum:

- `23505` → stable conflict error;
- `23503` → stable domain conflict or not-found error according to the use
  case;
- other database errors → wrapped internal error with operational context.

Client responses must not expose SQL statements, constraint names, DSNs, or
driver error text.

## Query contract

All touched queries must list columns explicitly. In particular:

- user lookups list the user model columns;
- room lookups/listing list room columns;
- message history uses an explicit message projection and a `LEFT JOIN` for
  nullable authors;
- scans check every returned error;
- rows are closed and `rows.Err()` is checked;
- `RowsAffected()` is checked where a missing target changes the outcome.

## Compatibility

- Existing REST paths and successful response status codes remain unchanged.
- Deleted-message authors are the one documented response-shape exception:
  `user_id` may be `null` and `username` is a stable deleted-user label.
- Cursor pagination remains ID-based and must continue to return deterministic
  ordering.

## Required tests

- Clean application of all migrations.
- Upgrade from the current schema without editing migration `000001`.
- Creator membership rollback when the second insert fails.
- Account deletion with no owned rooms.
- Account deletion rejected while the user owns a room.
- Message history retained after author deletion.
- Duplicate username/email mapped through SQLSTATE `23505`.
- Context cancellation and transaction rollback on repository failure.

## Acceptance criteria

- A new up/down migration expresses the deletion policy.
- Room creation is atomic.
- Account deletion is atomic and follows the documented conflict policy.
- No touched query uses `SELECT *`.
- Constraint violations use typed PostgreSQL classification.
- Clean-database and upgrade-path integration tests pass.
- Existing migration files remain unchanged.

## Out of scope

- Room ownership transfer and moderation roles; those belong to plan 05.
- Soft-delete users or messages.
- Message edit/delete lifecycle; that belongs to plan 06.
