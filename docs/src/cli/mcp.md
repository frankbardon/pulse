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
pulse mcp [--data-dir PATH] [--bind-on-open]
```

The command reads stdin, writes MCP responses on stdout, and writes a
one-line startup notice (and any subsequent diagnostics) on stderr.

## Flags

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--data-dir`     | string | from `PULSE_DATA_DIR` env var | Cohort base directory |
| `--bind-on-open` | bool   | true | Register session-scoped JSON-schema-bound tool variants on successful `pulse_inspect` |

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

The binding logic lives in
[`internal/mcp/schema_bind.go`](https://github.com/frankbardon/pulse/blob/main/internal/mcp/schema_bind.go);
see [Adding an MCP tool](../internals/adding-mcp-tool.md) for the
LLM-facing implications.

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
list.

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
