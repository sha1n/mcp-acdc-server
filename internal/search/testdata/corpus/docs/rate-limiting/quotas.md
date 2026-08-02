# Request throttling

Ledger sheds load before it degrades. Each tenant gets a token budget that
refills continuously, and a burst allowance on top of it.

## Budgets

A budget is expressed in requests per second per tenant. Exhausting the budget
yields `429` with a `Retry-After` header naming the wait in whole seconds.

## Bursts

The burst allowance absorbs short spikes without touching the steady-state
budget. It refills only while the tenant is below its steady-state consumption.
