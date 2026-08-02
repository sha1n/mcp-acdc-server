# Ledger

Ledger is an append-only event store with an HTTP API. It keeps a durable log of
domain events and serves range queries over that log.

## Installation

Download a release binary, or build from source with `make build`. The binary is
self-contained and needs no runtime dependencies.

## Authentication

Every request must carry a bearer token. Tokens are issued by the control plane
and scoped to a single tenant. The authentication guide covers token rotation
and the full scope vocabulary.

## Configuration

Ledger reads `ledger.yaml` from the working directory. Environment variables
prefixed with `LEDGER_` override values from the file.

## Support

Open an issue if something breaks. Include the server version and the output of
`ledger verify`.
