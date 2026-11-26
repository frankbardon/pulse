# Protocol Versions

**Audience:** operators and embedders checking that their MCP client and
Pulse agree on a protocol revision, and planning upgrades.

## Revisions served

Pulse's MCP server is built on the official
[Go SDK](https://github.com/modelcontextprotocol/go-sdk) and adds no
restriction: it speaks every revision the pinned SDK supports. With
go-sdk v1.8.0 (the version `go.mod` pins) that is:

| Revision | Handshake |
|---|---|
| `2026-07-28` | the SDK's newer negotiation, outside `initialize` |
| `2025-11-25` | `initialize` (the default answer, see below) |
| `2025-06-18` | `initialize` |
| `2025-03-26` | `initialize` |
| `2024-11-05` | `initialize` |

`TestSupportedProtocolVersionsPinned` (`mcp/gosdk`) holds this set and
this page to the SDK, so an SDK bump that changes it fails until both
are updated on purpose.

## Negotiation

- **`initialize` with a listed revision before `2026-07-28`:** the server
  answers with the same revision.
- **`initialize` with any other value** (unknown, future, or
  `2026-07-28`): no error. The server answers `2025-11-25`, its newest
  `initialize`-era revision, and the client disconnects if it cannot
  speak it (the MCP lifecycle rule).
- **`2026-07-28` clients** negotiate through the SDK's newer mechanism;
  methods that exist only at that revision are refused on an older
  session.

The answer is the `protocolVersion` in the `initialize` result; the
hand check on [Your First MCP Session](../getting-started/first-mcp-session.md)
prints it.

## Transports

- **stdio:** `pulse mcp`, and `mcpserve.Serve` / `ServeStdio` for
  embedders. The only transport the CLI ships.
- **Streamable HTTP:** embedder-supplied. Mount Pulse with
  `gosdk.Register` on your own go-sdk server and serve it through the
  SDK's HTTP handler ([Serving MCP](../library/mcp-serving.md)). On that
  server you may also narrow the revisions with the SDK's
  `ServerOptions.SupportedProtocolVersions`; `pulse mcp` and `mcpserve`
  never narrow.
- **SSE:** not supported.

## Upgrade policy

- go-sdk updates arrive through the weekly dependency updates and are
  reviewed like any change.
- A bump that **adds** a revision is additive and ships in any release.
- A revision is **not dropped** within v1 without a documented
  deprecation first: a release note and a row on this page marking it
  deprecated, at least one minor release before the release that drops
  it. The MCP row of the
[v1 stability policy draft](https://github.com/frankbardon/pulse/blob/main/docs/roadmap/v1.0.0-api-and-release/02-stability-policy.md)
states the same promise.

Tool names, input fields, resource URI schemes and prompt names follow
the stability policy independently of the protocol revision.
