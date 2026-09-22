# 04 — API and Abuse Protection

**Priority:** P0  
**Status:** Ready
**Depends on:** 01, 03
**Detailed spec:** [spec/04-api-hardening.md](../spec/04-api-hardening.md)
**Execution plan:** [plan/04-api-hardening-execution.md](./04-api-hardening-execution.md)

## Goal

Make public HTTP and WebSocket boundaries safe for internet-facing deployment.

## Scope

- Enforce an environment-driven WebSocket origin policy instead of accepting every origin.
- Add request body, message size, connection, and request-rate limits.
- Normalize and validate usernames, emails, room names, and message content.
- Return stable client-safe validation errors.
- Prevent token and query-string leakage in logs.
- Add HTTP server header and timeout review, including slow-client behavior.

## Acceptance criteria

- [ ] Disallowed WebSocket origins fail before upgrade.
- [ ] Rate limits distinguish authentication, write-heavy, and connection endpoints.
- [ ] Oversized bodies and messages fail with stable errors and bounded resource use.
- [ ] Logs contain request correlation fields but no credentials or raw tokens.
- [ ] Limits are configurable, documented, and have safe production defaults.
- [ ] Negative and boundary tests cover every new limit.

## Out of scope

- Full external WAF configuration.
- Behavioral spam classification.
