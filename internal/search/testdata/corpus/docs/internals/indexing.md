# How indexing works

Nothing here is a public contract. It changes between releases.

## Segments

An append lands in the open segment. When the open segment reaches its size
bound it is sealed, and a fresh one takes its place. Sealed segments are
immutable.

## Segment compaction

A background merger rewrites adjacent sealed segments into one. Compaction
reclaims space held by superseded events and shortens the read path for range
queries that span a long stretch of the log.

## Checksums

Every segment carries a digest over its payload. `ledger verify` recomputes each
digest; the reader validates them lazily as it touches pages.
