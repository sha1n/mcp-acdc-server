# Command line reference

Every subcommand accepts `--config` and `--log-format`.

## ledger serve

Starts the HTTP server. Blocks until interrupted, then drains in-flight requests
before closing the data directory.

## ledger migrate

Upgrades an on-disk data directory to the current segment format. Refuses to run
against a directory another process holds open.

## ledger verify

Walks every segment and recomputes its checksum. A checksum mismatch is reported
against the segment and the page holding it, and the walk continues, so one bad
checksum does not hide the ones behind it. Exits non-zero if any mismatched.
