# pulse mcp

**Audience:** operators wiring Pulse into an MCP-aware AI client
(Claude Desktop, Claude Code, generic MCP clients).

`pulse mcp` runs the Model Context Protocol server over stdio. The AI
client launches `pulse mcp` as a subprocess, speaks MCP over its
stdio streams, and shuts it down on session close.

> **LLM agents using MCP:** the agent-side guide is the
> `mcp-integration` skill — fetch it via `pulse_skills_get` for the
> tool catalog and request shapes. This page is for the human setting
> the server up.

## Synopsis

```
pulse mcp [--data-dir PATH] [--bind-on-open] [--no-cohort-scan]
```

The command reads stdin, writes MCP responses on stdout, and writes a
one-line startup notice (and any subsequent diagnostics) on stderr.

## Flags

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--data-dir`     | string | from `PULSE_DATA_DIR` env var | Cohort base directory |
| `--bind-on-open` | bool   | true | Register session-scoped JSON-schema-bound tool variants on successful `pulse_inspect` |
| `--no-cohort-scan` | bool | false | Skip the startup walk that enumerates `.pulse` files as `pulse://` resources (env: `PULSE_MCP_NO_COHORT_SCAN`) |

`--data-dir` is **required** in one of its two forms (env var or
flag). The MCP server fails to start otherwise:

```
data directory required: set PULSE_DATA_DIR or pass --data-dir
```

## --bind-on-open

When a session calls `pulse_inspect` successfully, the server can
register session-scoped tool variants whose JSON Schemas constrain
field-name parameters to the cohort's actual fields. This narrows
the LLM's choices and prevents typos at parameter-binding time.

Default: `true`. Pass `--bind-on-open=false` if your client binds
tool schemas itself.

The binding logic lives in the SDK-free core
[`mcp/bind.go`](https://github.com/frankbardon/pulse/blob/main/mcp/bind.go)
(the per-session server mutation that consumes it lives in the go-sdk
adapter, `mcp/gosdk/bind.go`); see
[Adding an MCP tool](../internals/adding-mcp-tool.md) for the
LLM-facing implications.

## --no-cohort-scan

At startup the server walks the data directory once and registers every
`.pulse` file it finds as an exact-match `pulse://<path>` resource, so
`resources/list` enumerates the available cohorts. On a large, remote or
volatile data root that walk is the most expensive thing startup does,
and the enumeration is not always worth it — a client that already knows
which cohort it wants never reads the list.

`--no-cohort-scan` (or `PULSE_MCP_NO_COHORT_SCAN=1`) skips the walk. The
`pulse://{+path}` resource **template** is registered either way, so
every cohort stays readable by URI:

```
resources/read  pulse://surveys/2026.pulse   # works with or without the scan
resources/list                               # lists cohorts only WITH the scan
```

Nothing else changes: the static `pulse://schema` resource, the
`pulse-skill://` resources, every tool, and `pulse_inspect` (the tool
route to the same header+schema JSON) are unaffected.

Embedders reach the same knob as `gosdk.Config{DisableCohortScan: true}`
or `mcpserve.Options{DisableCohortScan: true}`; both default to scanning.

## Wiring it into Claude Desktop

`~/Library/Application Support/Claude/claude_desktop_config.json`:

```jsonc
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

Restart the client. The Pulse tools (`pulse_manifest`, `pulse_inspect`,
`pulse_predict`, `pulse_process`, `pulse_compose`, `pulse_sample`,
`pulse_facet`, `pulse_import`, `pulse_drop`, `pulse_imports_list`,
`pulse_examples_search`, `pulse_examples_get`, `pulse_errors_lookup`,
`pulse_skills_list`, `pulse_skills_get`) and resources
(`pulse://*.pulse`, `pulse-skill://*`) appear in the tool/resource
list. The per-cohort `pulse://*.pulse` entries come from the startup
scan — see [`--no-cohort-scan`](#--no-cohort-scan).

## Wiring it into Claude Code

`~/.claude.json` (or per-project `.claude.json`):

```jsonc
{
  "mcpServers": {
    "pulse": {
      "command": "/usr/local/bin/pulse",
      "args":    ["mcp"],
      "env":     { "PULSE_DATA_DIR": "/var/data/pulse" }
    }
  }
}
```

The full LLM-side recipe (including resource URIs and the schema
binding details) is in [Adding an MCP tool](../internals/adding-mcp-tool.md)
and [Wiring an MCP client](../internals/wiring-mcp-client.md).

## Exit codes

`pulse mcp` is a long-running process. It exits non-zero only on
fatal startup failure (missing data dir, transport error). Once
serving, an MCP client controls the lifecycle.

## Examples

### Foreground run for debugging

```bash
PULSE_DATA_DIR=/tmp/pulse-data ./bin/pulse mcp
# Stderr: pulse mcp: serving over stdio (data dir: /tmp/pulse-data, bind-on-open: true)
```

### Disable schema binding

```bash
PULSE_DATA_DIR=/tmp/pulse-data ./bin/pulse mcp --bind-on-open=false
```

### Skip the cohort scan on a large data root

```bash
PULSE_DATA_DIR=/mnt/cohorts ./bin/pulse mcp --no-cohort-scan
# Stderr: pulse mcp: serving over stdio (data dir: /mnt/cohorts, bind-on-open: true, cohort-scan: false)
```

Or from an MCP client config, which sets `env` rather than `args`:

```jsonc
"env": { "PULSE_DATA_DIR": "/mnt/cohorts", "PULSE_MCP_NO_COHORT_SCAN": "1" }
```

### Inspect what the server registers

```bash
# Manifest exposes the MCP tool list
pulse --json | jq '.data.mcp_tools[]'
```

## Related

- [How LLMs Use Pulse](../mcp/index.md) — the pointer table from this
  site into the skill pack
- [Adding an MCP tool](../internals/adding-mcp-tool.md) — LLM-side
  wiring, tool catalog, resource schemes, schema binding
- [Deployment](../ops/deployment.md) — production hardening notes
- [Troubleshooting](../ops/troubleshooting.md) — common MCP failure
  modes
