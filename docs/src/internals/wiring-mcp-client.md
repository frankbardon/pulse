# Wiring Pulse into an MCP Client

**Audience:** Pulse internals contributors and integrators wiring the
`pulse mcp` server into an MCP-aware client (Claude Desktop, Claude
Code, any stdio MCP host).

`pulse mcp` serves the facade over stdio: one tool per facade method
plus the skills, examples, errors, import and label tools (the
manifest's `mcp_tools` block is the live list — never count it by
hand), the `pulse://` and `pulse-skill://` resources and the prompts.
Wiring it into a client takes three steps: build, configure, restart.

## 1. Build the binary

```bash
make build
```

Put `bin/pulse` on the client's `PATH`, or name it by absolute path in
the client's configuration.

## 2. Configure the client

Add an `mcpServers.pulse` entry that runs `pulse mcp` and exports
`PULSE_DATA_DIR` (or passes `--data-dir`):

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

Where each host keeps that file, and Claude Code's `claude mcp add`
one-liner, are in [MCP Integration → Wiring into a host](../mcp/index.md#wiring-into-a-host).
The other `pulse mcp` flags (`--feature-profile`, `--return`,
`--no-cohort-scan`, `--metrics-addr`, `--bind-on-open`) are in the
[CLI reference](../cli/mcp.md).

`--bind-on-open` (default `true`) re-registers the action tools after a
successful `pulse_inspect` or `pulse_import` with JSON Schemas whose
field-name parameters are enums of the cohort's fields. Pass
`--bind-on-open=false` for a client that binds schemas itself. The
trigger, what is constrained and the limits (the latest inspect wins;
an HTTP server shared by several sessions shares one tool set) are in
[MCP Integration → Schema-bound enums](../mcp/index.md#schema-bound-enums).

## 3. Restart the client

Once the client reloads its MCP configuration, the Pulse tools
(`pulse_manifest`, `pulse_inspect`, `pulse_predict`, `pulse_process`, …),
resources and prompts appear in its lists.

## Verification

```bash
bin/pulse mcp --help
bin/pulse --json | jq .data.mcp_tools
```

If the client has an MCP inspector, point it at the configured server
and confirm it exposes every tool the manifest's `mcp_tools` block
lists.
