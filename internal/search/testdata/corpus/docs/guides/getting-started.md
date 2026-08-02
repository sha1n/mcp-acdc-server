# Getting started

This guide walks from an empty directory to a server that answers queries.

Install the binary, write a minimal `ledger.yaml` naming a data directory, then
run `ledger serve`. The server refuses to start until it can create and lock the
data directory, so a permission problem surfaces immediately rather than on the
first write.

Ask the control plane for a token with the `events:append` scope and append an
event. Read it back with a range query covering the current day. Once that round
trip works, everything else in these documents is a refinement of it.
