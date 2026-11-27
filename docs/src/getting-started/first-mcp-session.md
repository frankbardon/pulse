# Your First MCP Session

**Audience:** users who want an AI client to analyse `sales.pulse` from
[Your First Cohort](first-cohort.md). `pulse mcp` serves the cohorts
under `PULSE_DATA_DIR` to any MCP client over stdio.

## 1. Check the server by hand

No AI client is needed for this step. From the data directory (where
`sales.pulse` is), send the MCP handshake, a tool listing and one tool
call on stdin; the server writes one JSON-RPC line per request:

```bash
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"shell","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
    '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pulse_process","arguments":{"cohort":{"filename":"sales.pulse"},"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"revenue","label":"total_revenue"}]}}}'
  sleep 2; } | pulse mcp > replies.ndjson
```

The `sleep 2` keeps stdin open while the server answers: the server stops
when its input closes, and without the pause it exits before it replies.
A status line (`pulse mcp: serving over stdio …`) goes to stderr.

Each reply carries the `id` of its request; they can arrive out of order.
With `jq`:

```bash
jq -c 'select(.id == 1) | .result.serverInfo' replies.ndjson    # {"name":"pulse","version":…}
jq 'select(.id == 2) | .result.tools | length' replies.ndjson    # how many tools this server mounts
jq -r 'select(.id == 3) | .result.content[0].text' replies.ndjson
```

The tool count depends on the build and its
[feature profile](../library/feature-profiles.md), so count it rather
than expect a number. The last line is the response of
[Your First Analysis](first-analysis.md), as text:
`{"data":[{"region":"east","total_revenue":63.989999999999995},…`. A tool
call's `arguments` are the tool's input itself; for `pulse_process` that
is the request object. Which protocol revisions Pulse speaks:
[Protocol Versions](../mcp/protocol.md).

## 2. Register it with a client

Steps 2 and 3 need an MCP client. For Claude Code:

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
