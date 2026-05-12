# Development Setup

**Audience:** new contributors getting their first PR ready.

This page is the short version. The fuller treatment of the repo's
conventions, CI gates, and Update Demand lives in the [Internals
section](../internals/architecture.md) and in
[`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md)
at the repository root.

## Clone

```bash
git clone https://github.com/frankbardon/pulse.git
cd pulse
```

## Tooling

Pulse needs only the Go toolchain — there is no Node, Python, or
container build. Install Go 1.24+ (see `go.mod` for the canonical
version).

The repo also uses `staticcheck` for `make lint`; it is auto-installed on
first run via `go run`.

## Common targets

| Command | What it does |
|---|---|
| `make build` | Builds the CLI binary to `bin/pulse` (default goal) |
| `make test`  | Runs `go test ./...` |
| `make fmt`   | Runs `go fmt ./...` |
| `make vet`   | Runs `go vet ./...` |
| `make lint`  | Runs `go vet` then `staticcheck ./...` |
| `make cover` | Runs tests with coverage; outputs `coverage.out` |
| `make clean` | Removes `bin/` and `coverage.out` |

A `.env` file at the repo root is auto-loaded and exported, so
`PULSE_DATA_DIR` and any other `PULSE_*` env vars can live there for
local development.

## Run the binary you just built

```bash
make build
./bin/pulse --version
./bin/pulse --json | head -20
```

The CLI tree itself is mapped in the [CLI Tour](../getting-started/cli-tour.md).

## Where things live

The package layout is documented at [Internals → Package
Layout](../internals/packages.md). Two pointers worth knowing on day
one:

- **Public facade:** [`pulse.go`](https://github.com/frankbardon/pulse/blob/main/pulse.go) —
  every Go embedder API lives here.
- **CLI internals:** [`internal/cli/`](https://github.com/frankbardon/pulse/tree/main/internal/cli) —
  one file per command group; never put processing logic here.

## Read this before writing code

- [Style Guide](style.md)
- [Testing Conventions](testing.md)
- [Pull Request Process](pr-process.md)
- [The Update Demand](../internals/update-demand.md) — what doc/skill
  updates ride alongside what code changes.
