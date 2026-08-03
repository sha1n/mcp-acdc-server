# Development Guide

This guide covers building, testing, and contributing to `mcp-acdc-server`.

## Prerequisites

- [Go](https://go.dev/doc/install) 1.24+
- [Make](https://www.gnu.org/software/make/)
- [golangci-lint](https://golangci-lint.run/welcome/install/) (for linting)
- [Docker](https://docs.docker.com/get-docker/) (optional, for container builds)

## Building from Source

```bash
# Clone the repository
git clone https://github.com/sha1n/mcp-acdc-server.git
cd mcp-acdc-server

# Install dependencies
make install

# Build for your current platform
make build-current

# Build for all platforms (Linux, macOS, Windows)
make build
```

The compiled binary is located at `bin/acdc-mcp`.

## Building Docker Image

```bash
make build-docker
```

## Makefile Reference

| Command | Description |
|---------|-------------|
| `make install` | Tidy Go modules |
| `make build` | Build binaries for all platforms |
| `make build-current` | Build for current OS/Arch only |
| `make build-docker` | Build Docker image |
| `make test` | Run all tests |
| `make coverage` | Run tests with coverage report |
| `make bench` | Run the search measurement benchmarks |
| `make lint` | Run linters (go vet, golangci-lint, format check) |
| `make format` | Format source files |
| `make clean` | Remove build artifacts |

## Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
make coverage
```

## Search Benchmarks

`make bench` reports index build time, query latency, segment count and Go
heap for the in-memory and on-disk indexes at several batch sizes. It is a
measurement instrument, not a regression gate — it asserts nothing about
timings or sizes.

The default corpus (`internal/search/testdata/corpus`) is far too small to
compare anything: every batch size collapses into a single batch, so both
index kinds report one segment. Point it at a real corpus instead, and
optionally repeat that corpus to reach a larger scale:

```bash
make bench BENCHFLAGS="-corpus=$HOME/docs -corpus-dup=2"
```

`BENCHFLAGS` is forwarded to the test binary. When invoking `go test`
directly, the package path must come **before** these flags:

```bash
# Works
go test ./internal/search/ -run '^$' -bench=. -corpus-dup=2

# Fails with a misleading "no Go files in ."
go test -corpus-dup=2 ./internal/search/
```

`-corpus` and `-corpus-dup` are registered by the test binary rather than by
`go test`, so `go test` consumes the unknown flag itself and never sees the
package path that follows it.

## Code Style

- Standard Go formatting (`gofmt`) is enforced
- Run `make lint` before committing to check for issues
- Run `make format` to auto-format code
