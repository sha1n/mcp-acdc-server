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

## Code Style

- Standard Go formatting (`gofmt`) is enforced
- Run `make lint` before committing to check for issues
- Run `make format` to auto-format code
