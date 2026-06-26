# Adding an MCP Tool

**Audience:** Pulse internals contributors adding a new tool to the
embedded MCP server (`internal/mcp/`).

Each MCP tool wraps one slice of `pulse.Pulse` and surfaces it over
stdio / SSE / Streamable HTTP transports. The tool count is fixed at
ten (`pulse_inspect`, `pulse_predict`, `pulse_process`, `pulse_compose`,
`pulse_sample`, `pulse_facet`, `pulse_facet_schema`, `pulse_manifest`,
`pulse_errors_lookup`, …) plus the two resource schemes
(`pulse://*.pulse`, `pulse-skill://*`). Adding a new tool means
extending the registry, registering the metadata, optionally binding a
field-aware JSON Schema, and updating the MCP-integration skill.

## 1. Implement the handler

Implement the new tool handler in `internal/mcp/`. The handler signature
matches the existing tools — accept the parsed request, call the
facade method on `pulse.Pulse`, return the result + envelope. Register
the handler in `RegisteredTools()` (`internal/mcp/tools.go`).

## 2. Register tool metadata

Add the tool's name + description in `mcp/toolmeta/meta.go`.
The `mcp/toolmeta` package is imported by `descriptor/` (which assembles
the manifest), so this is the leaf-metadata package that lets the
descriptor surface the tool without importing `internal/mcp`.

## 3. Field-name parameters (optional)

If the new tool has field-name parameters (e.g. a `field: string`
argument that takes a cohort field name), add a per-tool JSON Schema
builder in `internal/mcp/schema_bind.go` + an entry in `Bind`. After
`pulse_inspect` succeeds against a cohort the orchestrator binds
session-scoped variants of every schema-aware tool whose JSON Schema
constrains field-name parameters to the inspected cohort's actual
fields.

Schema-binding parity is enforced by:

- `TestMCPSchemaBinding_RemovesInvalidFields`
- `TestMCPSchemaBinding_AllFieldsInFiltererEnum`
- `TestMCPSchemaBinding_SampleAndFacetFieldEnum`
- `TestMCPSchemaBinding_InspectSucceedsRegistersBindings`
- `TestMCPSchemaBinding_BindOnOpenFalse`

The transport caveat: stdio sessions in mcp-go v0.52.0 do not
implement `SessionWithTools`, so binding is a no-op there. SSE and
Streamable HTTP transports honour it. See the MCP integration skill
for the configuration recipe.

## 4. Update the session-bootstrap skill

Add a section to `skills/session-bootstrap.md` covering the new
tool's purpose, request shape, response shape, and (if applicable)
the Schema-bound enums it exposes after `pulse_inspect`.
`TestSkillsCoverAllMCPTools` enforces presence by name.

## 5. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllMCPTools
go test ./descriptor/ -run TestManifestMCPToolsComplete
go test ./internal/mcp/ -run TestMCPSchemaBinding
```

The Update Demand row for MCP tools covers all of these in one PR;
see [The Update Demand](update-demand.md).
