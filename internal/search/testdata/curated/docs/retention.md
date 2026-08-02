---
name: Retention policy
description: How long Ledger keeps events, and what reclaims the space they held.
keywords:
  - retention
  - compaction
  - pruning
---

# Retention policy

A tenant declares how long its events remain readable. Past that horizon the
events stop answering range queries and become eligible for reclamation.

## Horizons

A horizon is a duration, not a timestamp. Shortening one takes effect on the
next merger pass; lengthening one cannot resurrect events already reclaimed.

## Reclamation

The background merger drops unreachable events as it rewrites sealed segments.
Space returns to the filesystem only when the rewritten segment is durable.
