# Repository Guidelines

## Project Structure & Module Organization

`cmd/acdc-mcp/` contains the CLI entry point. Core packages live under `internal/`: `app` coordinates startup, `mcp` exposes protocol capabilities, `search` wraps Bleve, and `content`, `resources`, and `prompts` discover content. Authentication and settings belong in `internal/auth` and `internal/config`; shared types belong in `internal/domain`. Unit tests sit beside their packages. End-to-end coverage lives in `tests/integration/`, with fixtures in `tests/integration/testkit/`. Keep documentation in `docs/` and deployment/content samples in `examples/`.

## Build, Test, and Development Commands

- `make install` tidies and resolves Go modules.
- `make go-build-current` builds the local binary as `bin/acdc-mcp`.
- `make build` cross-compiles supported Linux, macOS, and Windows binaries.
- `make test` runs all unit and integration tests.
- `make coverage` writes `coverage.out` and prints package/function coverage.
- `make lint` runs `go vet`, golangci-lint, and the formatting check.
- `make format` applies simplified `gofmt` formatting.
- `make build-docker` builds `sha1n/mcp-acdc-server:latest`.

Run locally with `bin/acdc-mcp --content-dir ./examples/sample-content`; add `--transport sse` to exercise the HTTP/SSE path.

## Coding Style & Naming Conventions

Use standard Go formatting (tabs from `gofmt`) and idiomatic names: exported identifiers in `PascalCase`, unexported identifiers in `camelCase`, and short lowercase packages. Keep packages focused. Return contextual errors rather than logging deep inside packages. Use `log/slog` for structured logging and `stderr` so stdio protocol traffic remains valid. Run `make format && make lint` before submitting.

## Testing Guidelines

Follow test-driven development: add a failing test before production changes. Use Go's `testing` package and Testify where helpful. Name tests `TestSubject_Scenario`; prefer table-driven cases for validation and parsing. Add unit tests for local behavior and integration tests for observable MCP, authentication, transport, or configuration behavior. CI runs linting and `make coverage`; no fixed threshold is declared, so preserve meaningful behavioral coverage.

## Commit & Pull Request Guidelines

Use Conventional Commits, as reflected in history: `feat(scope): summary`, `fix: summary`, `docs: summary`, or `build(deps): summary`. Mark breaking changes explicitly. Pull requests should explain behavior and contract changes, link relevant issues, list verification commands, and update docs/examples for user-visible configuration or CLI changes. Include screenshots or terminal output only when they clarify observable behavior. Keep each PR focused and ensure CI passes.

## Security & Configuration Tips

Configuration may come from flags, files, or `ACDC_MCP_*` environment variables. Never commit credentials, API keys, generated coverage files, or local content indexes. Preserve the unauthenticated `/health` endpoint contract when changing SSE middleware.
