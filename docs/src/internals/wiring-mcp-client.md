# Wiring Pulse into an MCP Client

**Audience:** Pulse internals contributors and integrators wiring the
`pulse mcp` server into an MCP-aware client (Claude Desktop, Claude
Code, any custom MCP host).

Pulse ships an embedded MCP server that exposes the public facade
through ten tools (one per facade method plus `pulse_facet_schema`)
and two resource schemes (`pulse://*.pulse`, `pulse-skill://*`).
Wiring it into a client is a three-step process: build, configure,
restart.

## 1. Build the binary

```bash
make build
```

The resulting `bin/pulse` must be on the client's `PATH` (or
referenced by absolute path in the client's configuration).

## 2. Configure the client

Add an `mcpServers.pulse` entry to the client's configuration file
(`claude_desktop_config.json` for Claude Desktop, `~/.claude.json`
for Claude Code) running `pulse mcp` and exporting `PULSE_DATA_DIR`:

```json
{
  "mcpServers": {
    "pulse": {
      "command": "/abs/path/to/bin/pulse",
      "args": ["mcp"],
      "env": {
        "PULSE_DATA_DIR": "/abs/path/to/your/cohorts"
      }
    }
  }
}
```

The `--bind-on-open` flag (default `true`) controls whether successful
`pulse_inspect` calls trigger registration of session-scoped tool
variants whose JSON Schemas constrain field-name parameters to the
cohort's actual fields. Pass `--bind-on-open=false` for clients that
bind themselves:

```json
{
  "command": "/abs/path/to/bin/pulse",
  "args": ["mcp", "--bind-on-open=false"]
}
```

## 3. Restart the client

After the client reloads its MCP configuration the Pulse tools
(`pulse_inspect`, `pulse_predict`, `pulse_process`, …) and resources
(`pulse://*.pulse`, `pulse-skill://*`) appear in the tool / resource
list.

## Schema-bound enums

The full configuration recipe — including the schema-bound enums
section that describes the inspect trigger, the multi-file limitation
(latest inspect wins), and the transport-support caveat (stdio
sessions in mcp-go v0.52.0 do not implement `SessionWithTools`, so
binding is a no-op there; SSE and Streamable HTTP transports honour
it) — lives in the [MCP integration
skill](https://github.com/frankbardon/pulse/blob/main/skills/mcp-integration.md).

## Verification

```bash
bin/pulse mcp --help
bin/pulse manifest --json | jq .mcp_tools
```

If the client supports an MCP tool inspector, point it at the
configured server and confirm ten tools and two resource schemes are
exposed.
