# Deployment

**Audience:** operators standing up Pulse as a CLI server, an MCP
process under an AI client, or an embedded Go library inside a larger
binary.

Pulse is a single static Go binary. There is no install command, no
config file, and no daemon — every deployment story is some shape of
"put the binary somewhere, set `PULSE_DATA_DIR`, run it".

> **LLM agents using MCP:** see the `session-bootstrap` skill via
> `pulse_skills_get` for the MCP-side wiring details. This page covers
> the operator side.

## Mode 1: Standalone CLI

```bash
go install github.com/frankbardon/pulse/cmd/pulse@latest
export PULSE_DATA_DIR=/var/data/pulse
pulse --version
```

That's the full install. The CLI tree is mapped in the
[CLI Tour](../getting-started/cli-tour.md).

## Mode 2: MCP stdio server (Claude Desktop, Claude Code, generic MCP clients)

`pulse mcp` runs the Model Context Protocol over stdio. AI clients
launch the process, speak MCP over its standard streams, and shut it
down on session close.

The full wiring guide is in the `session-bootstrap` skill. Quick
reference for Claude Desktop:

```jsonc
// ~/Library/Application Support/Claude/claude_desktop_config.json
{
  "mcpServers": {
    "pulse": {
      "command": "/usr/local/bin/pulse",
      "args": ["mcp"],
      "env": {
        "PULSE_DATA_DIR": "/var/data/pulse"
      }
    }
  }
}
```

For Claude Code (`~/.claude.json`) and other clients the shape is the
same — see the `session-bootstrap` skill (`pulse skills show
session-bootstrap`) for the canonical recipes.

Flags worth knowing:

| Flag | Default | Purpose |
|---|---|---|
| `--data-dir`        | from `PULSE_DATA_DIR` | Override the cohort base directory |
| `--bind-on-open`    | `true` | Register session-scoped JSON-schema-bound tool variants on successful `pulse_inspect`. Disable for clients that bind tool schemas themselves. |

See [`pulse mcp`](../cli/mcp.md) for the full command page.

## Mode 3: Embedded Go library

```go
import "github.com/frankbardon/pulse"

p, err := pulse.New(pulse.Options{
    DataDir: "/var/data/pulse",
})
```

When embedding, you can bypass `PULSE_DATA_DIR` entirely by passing
`DataDir` (as above) or a custom `afero.Fs`. See [Library
Embedding](../library/overview.md) for the full surface.

## Production hardening

- **Filesystem permissions.** `pulse mcp` reads everything under
  `PULSE_DATA_DIR`. Treat the directory as the trust boundary — run
  the process as a user that can only read what it should serve.
- **Stdio plumbing.** MCP transports stderr too. Pulse writes a
  one-line startup notice (`pulse mcp: serving over stdio...`) on
  stderr and never logs request/response payloads, so MCP clients can
  surface stderr without leaking data. `--log-level` records also go
  to stderr only and carry no row data; stdout stays pure JSON-RPC.
- **Metrics port.** `--metrics-addr host:port` is the only way
  `pulse mcp` opens a port; the `/metrics` endpoint is unauthenticated,
  so bind loopback or a private interface.
- **Resource limits.** Streaming aggregations stay memory-bounded;
  buffered request shapes (window operators, median/percentile,
  decimal/geo paths) can materialise large intermediate row sets.
  Use `pulse api predict` to check `Streamable` before running an
  unfamiliar request — see [Performance Notes](performance.md).
- **No mutating background state.** Pulse never writes to a cohort
  during `process`/`compose`. The only write paths are `import`,
  `export`, `synth`, `profile`, and `cohort filter` — explicit by
  flag.

## Upgrades

Drop in a new binary and restart the MCP process (or the calling
client). The `.pulse` file format carries a one-byte version field
(`0x01` or `0x02`; every current writer still emits `0x01` unless a
schema needs a `0x02` feature). Old files stay readable forever; files
written by a newer binary that introduces a version this one does not
know are rejected loud at parse time, not silent at row decode — so a
binary that predates `0x02` refuses `0x02` cohorts, and must be upgraded
before it reads cohorts written by a `0x02`-writing release. See [Header Layout](../format/header.md).
