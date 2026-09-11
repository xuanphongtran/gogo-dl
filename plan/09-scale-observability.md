# 09 — Scale and Observability

**Priority:** P2  
**Status:** Proposed  
**Depends on:** 01–08

## Goal

Operate multiple application instances while retaining reliable diagnostics and clearly defined service objectives.

## Scope

- Introduce a broker-backed cross-instance event path, such as Redis Streams or NATS JetStream, after measuring requirements.
- Define event IDs, deduplication, ordering scope, retry behavior, and failure recovery.
- Add structured request and connection correlation.
- Add metrics for HTTP latency/errors, database pools, active sockets, queue depth, dropped events, and delivery latency.
- Add readiness separate from liveness and graceful connection draining.
- Define dashboards, alerts, and initial SLOs.

## Acceptance criteria

- [ ] Users connected to different instances receive authorized room events.
- [ ] Duplicate and out-of-order delivery behavior is documented and tested.
- [ ] Instance shutdown drains HTTP and WebSocket work within a bounded interval.
- [ ] Readiness reflects critical dependencies without causing restart loops.
- [ ] Operators can diagnose elevated errors and dropped delivery from metrics and logs.
- [ ] Load tests establish capacity assumptions and scaling thresholds.

## Open decisions

- Broker selection based on delivery semantics and operational ownership.
- Required ordering guarantee: per room, per user, or best effort.
