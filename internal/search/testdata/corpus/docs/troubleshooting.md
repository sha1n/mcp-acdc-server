# Troubleshooting

Symptoms, in the order support sees them.

## The server will not start

Ledger refuses to start when `ledger.yaml` holds an unknown key. The startup
error names the offending key and the line it appears on.

## Requests fail with 401

The bearer token is missing, malformed, or revoked. Confirm the token reaches
the server in the `Authorization` header before suspecting the control plane.

## Requests fail with 403

The token authenticated, but its scopes do not cover the operation. This is a
scope problem, not an authentication problem.

## Queries are slow

A range query that spans many segments reads each of them. Check whether the
background merger has fallen behind, and whether the append rate has outgrown
what a single replica can seal. The merger lag dashboard answers both.
