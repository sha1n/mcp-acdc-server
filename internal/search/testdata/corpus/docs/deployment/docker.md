# Running Ledger in Docker

The published image is a distroless base with the server binary and nothing
else. There is no shell inside it.

## Image tags

Every release publishes an immutable tag. The floating `latest` tag follows the
newest release and is meant for experiments, not for production.

## Volumes

Mount a volume at the data directory. The container process runs unprivileged,
so the mounted volume must be writable by that user or the server exits before
it binds a port.
