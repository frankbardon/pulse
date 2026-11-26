# Your First MCP Session

**Audience:** users who want an AI client to analyse `sales.pulse` from
[Your First Cohort](first-cohort.md). `pulse mcp` serves the cohorts
under `PULSE_DATA_DIR` to any MCP client over stdio.

## 1. Check the server by hand

Send the MCP handshake and a tool listing on stdin; the server answers
one JSON-RPC line per request on stdout:

```bash
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"shell","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 2; } | pulse mcp | cut -c1-240
```

The first line names the server (`"serverInfo":{"name":"pulse",…}`) and
the protocol revision it agreed; the second lists the tools. A status
line goes to stderr. Which revisions Pulse speaks:
[Protocol Versions](../mcp/protocol.md).

## 2. Register it with a client

For Claude Code:

```bash
claude mcp add pulse --env PULSE_DATA_DIR="$PULSE_DATA_DIR" -- pulse mcp
```

Other clients take the same command, argument and environment in their
config file ([Wiring into a host](../mcp/index.md#wiring-into-a-host)).
Restart the client.

## 3. Ask a question

In a new session, ask for example: *"Using Pulse, what is total revenue
by region in sales.pulse?"* The agent reads the manifest, inspects the
cohort, validates a request with `pulse_predict` and runs it with
`pulse_process`, the same steps as
[Your First Analysis](first-analysis.md). Its playbook is the
`session-bootstrap` skill the server ships.

## Next

- [MCP Integration](../mcp/index.md): tools, resources, prompts,
  configuration and troubleshooting.
- [`pulse mcp`](../cli/mcp.md): flags (feature profiles, limits,
  metrics).
- Serving Pulse from your own Go program: the
  [Embedder Walkthrough](../library/embedder-walkthrough.md).
