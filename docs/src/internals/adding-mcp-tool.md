# Adding an MCP Tool

**Audience:** Pulse internals contributors adding a new tool to the
embedded MCP server. The MCP layer is split into the SDK-free core
(`mcp/`), the go-sdk adapter (`mcp/gosdk/`, the only package importing
the MCP SDK), and the leaf metadata package (`mcp/toolmeta/`).

Each MCP tool wraps one slice of `pulse.Pulse` and surfaces it over
stdio / Streamable HTTP transports. The catalog covers one tool per
facade method (`pulse_inspect`, `pulse_predict`, `pulse_process`,
`pulse_compose`, `pulse_sample`, `pulse_facet`, `pulse_facet_schema`,
`pulse_manifest`, `pulse_errors_lookup`, …) plus the skills / examples /
import / label tools, and the two resource schemes (`pulse://*.pulse`,
`pulse-skill://*`). Treat the manifest (`pulse manifest`) as the
source-of-truth count — never hardcode a number. Adding a new tool
means extending the core catalog, registering the metadata, optionally
binding a field-aware JSON Schema, and updating the MCP-integration
skill.

## 1. Implement the handler

Implement the new tool handler in the SDK-free core (`mcp/handlers.go`).
The handler is a typed function over `*pulse.Pulse`: accept the typed
`In` struct, call the facade method, return the typed `Out` (coded
errors surface verbatim as `{code, message, details}`). Add the tool's
`ToolDescriptor` to the core catalog (`mcp/tools.go`) so `Tools(cfg)`
emits it; the go-sdk adapter (`mcp/gosdk/`) mounts whatever the catalog
returns via `gosdk.Register`.

## 2. Register tool metadata

Add the tool's name + description in `mcp/toolmeta/meta.go`.
The `mcp/toolmeta` package is imported by `descriptor/` (which assembles
the manifest) and by the core, so this is the leaf-metadata package that
lets the descriptor surface the tool without importing the MCP layer or
the SDK.

## 3. Field-name parameters (optional)

If the new tool has field-name parameters (e.g. a `field: string`
argument that takes a cohort field name), add a per-tool JSON Schema
builder in `mcp/bind.go` + an entry in `Bind`. After `pulse_inspect`
succeeds against a cohort the adapter binds session-scoped variants of
every schema-aware tool whose JSON Schema constrains field-name
parameters to the inspected cohort's actual fields. `mcp/bind.go` is
pure (no MCP SDK); the per-session server mutation that consumes the
schemas lives in the adapter (`mcp/gosdk/bind.go`).

Schema-binding parity is enforced by:

- `TestMCPSchemaBinding_RemovesInvalidFields`
- `TestMCPSchemaBinding_AllFieldsInFiltererEnum`
- `TestMCPSchemaBinding_SampleAndFacetFieldEnum`
- `TestMCPSchemaBinding_DedupAndSort`
- `TestMCPSchemaBinding_NilSchema`

The transport caveat: bind-on-inspect works on the single stdio session
(post-serve `AddTool`/`RemoveTools` auto-emits `list_changed`); there is
no per-session override for shared HTTP servers — a documented
limitation. See the MCP integration skill for the configuration recipe.

## 4. Update the session-bootstrap skill

Add a section to `skills/session-bootstrap.md` covering the new
tool's purpose, request shape, response shape, and (if applicable)
the Schema-bound enums it exposes after `pulse_inspect`.
`TestSkillsCoverAllMCPTools` enforces presence by name.

## 5. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllMCPTools
go test ./descriptor/ -run TestManifestMCPToolsComplete
go test ./mcp/ -run TestMCPSchemaBinding
```

The Update Demand row for MCP tools covers all of these in one PR;
see [The Update Demand](update-demand.md).
