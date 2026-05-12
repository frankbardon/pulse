# Installation

**Audience:** new users who want a working `pulse` binary on their PATH.

This page walks through installing Pulse, the prerequisites it needs, and how
to verify the install. Pulse is distributed as a single static Go binary;
there is no installer, no daemon, and no config file.

> **LLM agents using MCP:** see the `getting-started` skill via
> `pulse_skills_get` — it covers session bootstrap rather than local install.

## Prerequisites

| Requirement | Minimum |
|---|---|
| Go toolchain | 1.24 (see `go.mod`) |
| OS | Linux, macOS, or Windows (anywhere Go cross-compiles) |
| Disk | A few MB for the binary; cohort files live wherever you point `PULSE_DATA_DIR` |

`go.mod` is the source of truth for the supported Go version; if it drifts
from this page the `go.mod` value wins.

## Install with `go install`

The fastest path on a developer machine:

```bash
go install github.com/frankbardon/pulse/cmd/pulse@latest
```

This drops a `pulse` binary at `$(go env GOBIN)` (typically `~/go/bin`).
Make sure that directory is on your `PATH`.

Pin a specific release by replacing `@latest` with a tag:

```bash
go install github.com/frankbardon/pulse/cmd/pulse@v0.2.0
```

## Build from source

The same binary, built reproducibly from a checkout:

```bash
git clone https://github.com/frankbardon/pulse.git
cd pulse
make build
# Binary at ./bin/pulse
```

The `Makefile` is documented in [CLAUDE.md → Build / Dev / Test
Workflow](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md#build--dev--test-workflow);
the relevant targets are `make build`, `make test`, `make lint`, and
`make cover`.

## Configure the data directory

Pulse reads and writes `.pulse` files under a base directory called
`PULSE_DATA_DIR`. Most commands accept absolute paths and will work without
it, but `pulse mcp` requires the variable so the MCP server can enumerate
cohorts:

```bash
export PULSE_DATA_DIR=/var/data/pulse
```

The repo `Makefile` auto-loads a `.env` file from the repo root, so you can
also drop `PULSE_DATA_DIR=...` there for local development.

`PULSE_DATA_DIR` is the only required environment variable. See
[Flag Reference](../cli/flags.md) for the full list of CLI flags and
environment knobs.

## Verify

```bash
pulse --version
pulse --json | head -20
```

`pulse --json` prints the root manifest — the full self-description of
commands, components, field types, and embedded skills. If you see a
top-level `format_version: "1.0"` envelope, the install is working.

## Where to go next

- New to the file format and vocabulary? [Your First Cohort](first-cohort.md)
- Want a quick map of every command? [CLI Tour](cli-tour.md)
- Embedding Pulse in a Go program? [Go API Overview](../library/overview.md)
- Wiring Pulse into an MCP-aware client? [`pulse mcp`](../cli/mcp.md)
