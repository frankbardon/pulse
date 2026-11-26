# Deployment

**Audience:** operators running Pulse as a CLI, as an MCP process under
an AI client, or as a library inside a larger binary.

Pulse is one Go binary with no installer, config file or daemon. Every
deployment is some shape of "put the binary somewhere, set
`PULSE_DATA_DIR`, run it" ([Installation](../getting-started/installation.md)).

> **LLM agents using MCP:** the `session-bootstrap` skill (via
> `pulse_skills_get`) covers the session. This page covers the operator
> side.

## Mode 1: Standalone CLI

```bash
go install github.com/frankbardon/pulse/cmd/pulse@latest
export PULSE_DATA_DIR=/var/data/pulse
pulse --version
```

The CLI tree is mapped in the [CLI Tour](../getting-started/cli-tour.md).

## Mode 2: MCP stdio server

`pulse mcp` serves MCP over stdio: the AI client launches it, speaks
JSON-RPC over its standard streams and stops it when the session ends.
Client configuration (Claude Desktop, Claude Code, other stdio hosts) is
on [MCP Integration](../mcp/index.md#wiring-into-a-host); every flag is
on [`pulse mcp`](../cli/mcp.md). The ones an operator sets most:

| Flag | Purpose |
|---|---|
| `--data-dir` | The cohort base directory (default `PULSE_DATA_DIR`) |
| `--feature-profile` | Mount only the operators, tools and capabilities a [feature profile](../library/feature-profiles.md) lists |
| `--limit name=value`, `--memory-limit` | Resource ceilings ([Tuning Limits](../library/tuning-limits.md)) |
| `--return` | Response preset for a tool call without its own `return` block |
| `--metrics-addr` | Prometheus `/metrics` endpoint ([Observability](../library/observability.md)) |
| `--bind-on-open=false` | Turn off schema binding after `pulse_inspect` / `pulse_import` |

## Mode 3: Embedded Go library

```go
import "github.com/frankbardon/pulse"

p, err := pulse.New(pulse.Options{
    DataDir: "/var/data/pulse",
})
```

`pulse.Options` always overrides the environment: set `DataDir` or a
custom `afero.Fs` and `PULSE_DATA_DIR` is not read. Start at the
[Embedder Walkthrough](../library/embedder-walkthrough.md); serving MCP
from your own binary is [Serving MCP](../library/mcp-serving.md).

## Production hardening

- **Filesystem.** `pulse mcp` reads every cohort under the data
  directory and writes there too: managed imports (`imports/`) and the
  outputs of `pulse_materialize`, `pulse_rake` and `pulse_dedup`. Run it
  as a user that can touch only what it should serve, and hide the
  writing tools with a feature profile where agents must not write.
  `pulse_import` reads absolute source paths only inside its jail
  (the server's working directory by default).
- **Stdio plumbing.** Stdout is pure JSON-RPC. Pulse writes one startup
  line (`pulse mcp: serving over stdio ...`) to stderr; `--log-level`
  records also go to stderr and never carry request, response or row
  data.
- **Metrics port.** `--metrics-addr host:port` is the only way
  `pulse mcp` opens a port. `/metrics` is unauthenticated, so bind
  loopback or a private interface.
- **Resource limits.** Streaming requests stay memory-bounded; buffered
  shapes (windows, medians and percentiles, rank statistics) hold rows in
  memory. `pulse api predict` reports streamability and the memory
  estimate before a run, and `--limit` / `--memory-limit` cap what one
  request may use ([Performance Notes](performance.md),
  [Tuning Limits](../library/tuning-limits.md)).
- **Reads never write a cohort.** `process`, `compose`, `predict`,
  `sample` and `facet` never change a cohort; a read of a managed import
  only slides its TTL sidecar. Cohorts change only through commands that
  say so (`import`, `cohort filter` / `zone`, `api materialize`, `rake`,
  `dedup`, `widen`, `shard`, `index`, `synth`), and every rewrite is
  atomic.

## Upgrades

Drop in the new binary and restart the MCP process (or the client that
launches it). Every `.pulse` file carries a format version byte (`0x01`,
or `0x02` when the schema needs it). Old files stay readable; a file in
a version the binary does not know is refused at header parse, never
misread, so a binary must be at least as new as the release that wrote
the cohorts it reads. See [Header Layout](../format/header.md).
