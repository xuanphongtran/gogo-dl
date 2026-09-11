# 07 — Presence, Typing, and Read State

**Priority:** P1  
**Status:** Proposed  
**Depends on:** 03, 05

## Goal

Add responsive collaboration signals without turning ephemeral socket state into unreliable durable state.

## Scope

- Track online presence per user across multiple connections.
- Add throttled typing-started and typing-stopped events.
- Persist a per-user, per-room read cursor for unread counts.
- Define reconnect and stale-connection behavior.
- Expose a room presence snapshot through an authorized path.

## Acceptance criteria

- [ ] Multiple tabs do not mark a user offline until the final connection closes.
- [ ] Typing events are ephemeral, authorized, throttled, and automatically expire.
- [ ] Read cursors advance monotonically and retries are idempotent.
- [ ] Unread counts derive from durable cursors and message IDs.
- [ ] Disconnect and reconnect behavior is tested without timing-dependent sleeps.
- [ ] Privacy rules for presence visibility are documented.

## Out of scope

- Exact last-active analytics.
- Cross-device push notifications, which belong to plan 08.
