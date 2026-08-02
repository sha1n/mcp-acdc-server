# Configuration

Ledger takes settings from a YAML file, from environment variables, and from
command line flags. Every setting is available through all three.

## The ledger.yaml file

The server reads `ledger.yaml` from its working directory unless `--config`
names another path. Unknown keys are rejected at startup rather than ignored.

## Environment variables

Every setting has an environment variable spelled `LEDGER_` followed by the
upper-case setting name. Nested settings join their segments with an underscore.

## Precedence

Flags beat environment variables, which beat the YAML file, which beats the
built-in defaults. Precedence is resolved once at startup and never re-read.
