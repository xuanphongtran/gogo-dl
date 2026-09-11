# 02 — Data Integrity and Transactions

**Priority:** P0  
**Status:** Proposed  
**Depends on:** 01

## Goal

Ensure schema constraints and multi-step operations cannot leave invalid or partial state.

## Scope

- Resolve foreign-key deletion behavior where `ON DELETE SET NULL` conflicts with non-null ownership columns.
- Define explicit account-deletion semantics for rooms and messages.
- Make room creation and creator membership insertion atomic.
- Replace ignored persistence errors with explicit handling.
- Use typed PostgreSQL error codes for unique and constraint violations.
- Replace touched `SELECT *` queries with explicit column lists.

## Acceptance criteria

- [ ] A new up/down migration expresses the chosen deletion semantics.
- [ ] Account deletion succeeds or fails with a documented domain error; it never leaves partial state.
- [ ] Room creation rolls back when creator membership cannot be inserted.
- [ ] Constraint violations map to stable application errors without string matching.
- [ ] Clean-database and upgrade-path integration tests pass.
- [ ] Existing migration files remain unchanged.

## Open decisions

- Retain deleted-user message history with nullable authors, use a tombstone user, or delete authored messages.
- Transfer, archive, or delete rooms owned by a deleted user.
