# Authentication

Ledger authenticates every request with a bearer token issued by the control
plane. A token carries a tenant identifier and a set of scopes.

## Bearer tokens

A bearer token is an opaque string. Send it in the `Authorization` header of
every request:

```
Authorization: Bearer <token>
```

Tokens are validated against the control plane on first use and cached for the
remainder of their lifetime. A token that fails validation yields `401`.

## Token rotation

Rotate a token without downtime by issuing the replacement before revoking the
predecessor. Both tokens authenticate while the old one remains valid.

### Overlapping validity windows

The control plane accepts an overlap of up to seven days. During the overlap
both tokens resolve to the same tenant, so a caller can migrate at its own pace.

### Revoking a predecessor

Revocation takes effect within one cache lifetime. Callers still presenting the
revoked token begin to see `401` responses.

## Scopes

A scope names one capability: `events:append`, `events:read`, or `admin`. A
token without the matching scope is rejected with `403`, not `401`.
