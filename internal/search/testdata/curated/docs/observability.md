---
name: Observability
description: Metrics, traces, and structured logs emitted by the server.
keywords:
  - metrics
  - tracing
  - dashboards
---

# Observability

The server emits structured logs on stderr and exposes a metrics endpoint. Both
are on by default and neither can be disabled at runtime.

## Metrics endpoint

The endpoint serves the Prometheus text format. Series are labelled by tenant,
so cardinality grows with the tenant count rather than with traffic.

## Traces

Spans are emitted over OTLP when an endpoint is configured. The server
propagates incoming trace context and never samples it away.
