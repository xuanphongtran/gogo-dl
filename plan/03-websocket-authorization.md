# 03 — WebSocket Authorization and Protocol Safety

**Priority:** P0  
**Status:** Done
**Depends on:** 01, 02

## Goal

Prevent authenticated users from subscribing to or broadcasting into rooms they are not authorized to access.

## Scope

- Authorize room subscription against server-side membership data.
- Reject unknown event types, invalid room IDs, and invalid payload shapes.
- Prevent client-originated events from impersonating persisted message or server lifecycle events.
- Define typed inbound commands separately from outbound events.
- Return a stable error event without exposing internal details.
- Preserve one-reader/one-writer ownership and keep Hub database I/O non-blocking.

## Acceptance criteria

- [ ] Non-members cannot join, observe, or publish to a room.
- [ ] Membership revocation removes active subscriptions within documented consistency bounds.
- [ ] Client messages cannot choose authoritative user identity or server-generated fields.
- [ ] Malformed and oversized input is rejected safely.
- [ ] Authorization lookup does not block the Hub event loop.
- [ ] Tests cover unauthorized join, forged events, disconnect, slow consumer, and shutdown under `-race`.

## Open decisions

- Authorize during upgrade, through a service callback, or through a pre-authorized subscription command.
- Define behavior when membership changes while a socket remains connected.
