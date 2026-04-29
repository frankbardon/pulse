# Contributing to Pulse

Thanks for your interest in contributing to Pulse! This guide covers the basics.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/<you>/pulse.git`
3. Create a branch: `git checkout -b my-feature`
4. Make your changes
5. Run checks: `make test && make lint`
6. Commit and push
7. Open a pull request

## Development Setup

- Go 1.26+

```bash
make build    # Build CLI binary to bin/pulse
make test     # Run tests
make lint     # Run golangci-lint
make cover    # Run tests with coverage
```

## Code Conventions

- **Library-first.** All processing logic lives in library packages. The CLI in `cmd/pulse/` is a thin adapter.
- **No business logic in `cmd/pulse/`.** Parse flags, call library, format output.
- **Error codes** use `DOMAIN_CATEGORY` format and the typed `errors.Code` system.
- **Component types** use SCREAMING_SNAKE: `AGG_COUNT`, `ATTR_ZSCORE`, `FILTER_INCLUDE`, `GROUP_CATEGORY`.
- **No `fmt.Sprintf` for JSON.** Use `encoding/json` and `descriptor.NewEnvelope`.
- **All file I/O via `afero.Fs`.** Never `os.Open` directly in library code.
- **Predict / inspect / manifest are no-execute.** `descriptor/` MUST NOT import `service/` or `processing/`.

See [CLAUDE.md](CLAUDE.md) for the full set of conventions and contracts.

## The Update Demand

Any change to code, configuration, file format, or public surface MUST update the corresponding skill file(s) and `CLAUDE.md` in the same PR. The Update Demand table in `CLAUDE.md` lists every trigger and its enforcing CI gate. Do not defer doc updates to a follow-up PR.

## Pull Request Guidelines

- Keep PRs focused — one feature or fix per PR
- Include tests for new functionality (TDD: tests first)
- Update skill files and `CLAUDE.md` per the Update Demand
- Run `make test && make lint` before submitting
- Fill out the PR template

## Reporting Bugs

Use the [bug report template](https://github.com/frankbardon/pulse/issues/new?template=bug_report.yml). Include your request JSON, Go version, and OS.

## Suggesting Features

Use the [feature request template](https://github.com/frankbardon/pulse/issues/new?template=feature_request.yml). Describe the problem you're solving, not just the solution you want.
