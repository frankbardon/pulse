# Adding an MCP Tool

**Audience:** Pulse internals contributors adding a new tool to the
embedded MCP server. The MCP layer is split into the SDK-free core
(`internal/mcp/`), the go-sdk adapter (`mcp/gosdk/`, the only package importing
the MCP SDK), and the leaf metadata package (`internal/mcp/toolmeta/`).

> **Run the site checklist first.** `make sites ARGS="mcp-tool <name>"` (`inspect` or `pulse_inspect`; a new name gives the template) prints every table that lists tools (`toolmeta.Meta`, `mcpToolBindings`, the core catalog), the tool's sites and the gates below. See [Finding Change Sites](finding-change-sites.md).

Each MCP tool wraps one slice of `pulse.Pulse` and is served over
stdio (`pulse mcp`) or an embedder-supplied HTTP transport. The catalog
covers one tool per facade method (`pulse_inspect`, `pulse_predict`,
`pulse_process`, …) plus the skills / examples / import / label tools,
and the two resource schemes (`pulse://*.pulse`, `pulse-skill://*`).
The manifest (`pulse --json`, `mcp_tools`) lists them — never hardcode a
number. Adding a tool means extending the core catalog, registering its
metadata and feature binding, optionally binding a field-aware JSON
Schema, and writing its atomic skill.

## 1. Implement the handler

Implement the new tool handler in the SDK-free core (`internal/mcp/handlers.go`).
The handler is a typed function over `*pulse.Pulse`: accept the typed
`In` struct, call the facade method, return the typed `Out` (coded
errors surface verbatim as `{code, message, details}`). Add the tool's
`ToolDescriptor` to the core catalog (`internal/mcp/tools.go`) so `Tools(cfg)`
emits it; the go-sdk adapter (`mcp/gosdk/`) mounts whatever the catalog
returns via `gosdk.Register`.

## 2. Register tool metadata

Add the tool's name + description in `internal/mcp/toolmeta/meta.go`.
The description opens with one `CALL …` sentence saying when to call the
tool, and that sentence is the first line of the tool skill's
`## When to use` section, word for word
(`TestToolDescriptionsLeadWithWhenToUse`).
A clause about one feature inside a sentence about others goes in a
skill-pack feature fence (`<!-- feature: capability:matrices -->…<!-- /feature -->`),
so an instance hiding that feature drops the clause rather than the
whole sentence; the lead sentence carries none
(`TestToolDescriptionFences_Valid`).
The `internal/mcp/toolmeta` package is imported by `internal/descriptor/` (which assembles
the manifest) and by the core, so this is the leaf-metadata package that
lets the descriptor surface the tool without importing the MCP layer or
the SDK.

Bind the tool to the feature (or core surface) that owns it in
`mcpToolBindings` (`internal/descriptor/features.go`): tools register per
instance, so a feature profile that hides the feature drops the tool.
`TestFeaturesHaveSince` fails on an unbound or twice-bound tool.

## 3. Field-name parameters (optional)

If the new tool has field-name parameters (e.g. a `field: string`
argument that takes a cohort field name), add a per-tool JSON Schema
builder in `internal/mcp/bind.go` + an entry in `Bind`. After `pulse_inspect`
succeeds against a cohort the adapter binds session-scoped variants of
every schema-aware tool whose JSON Schema constrains field-name
parameters to the inspected cohort's actual fields. `internal/mcp/bind.go` is
pure (no MCP SDK); the per-session server mutation that consumes the
schemas lives in the adapter (`mcp/gosdk/bind.go`).

Schema-binding parity is enforced by:

- `TestMCPSchemaBinding_RemovesInvalidFields`
- `TestMCPSchemaBinding_AllFieldsInFiltererEnum`
- `TestMCPSchemaBinding_SampleAndFacetFieldEnum`
- `TestMCPSchemaBinding_DedupAndSort`
- `TestMCPSchemaBinding_NilSchema`
- `TestBindForInstance_PayloadSchemaParity` — every payload-schema key under
  a bound request root is in the bound schema (allowlisted omissions carry a
  reason), and no bound key is absent from the instance's payload schema

The transport caveat: bind-on-inspect works on the single stdio session
(a same-name `Server.AddTool` replaces the tool and go-sdk emits
`list_changed`); go-sdk has no per-session tool set, so a shared HTTP
server binds per session only when its `NewStreamableHTTPHandler`
factory builds one server per session (see [MCP Serving](../library/mcp-serving.md)).

## 4. Write the tool skill

Write the atomic skill `skills/tool-<kebab>.md` (the tool name minus
`pulse_`; frontmatter, required sections and budget in
`.claude/reference/skill-pack.md`; copy `skills/tool-facet-schema.md`).
Its `## When to use` first line is the description's lead sentence.
Mention the tool in `docs/src/mcp/` too: the coverage matrix needs a
hand-written mdBook mention for every tool.

## 5. Run the gates

```bash
go test -count=1 ./internal/skills/ -run 'TestOperatorHasAtomicSkill|TestAtomicSkillHasRequiredSections|TestSkillTokenBudget|TestSkillsCoverAllMCPTools|TestToolDescriptionsLeadWithWhenToUse'
go test -count=1 ./internal/descriptor/ -run 'TestManifestMCPToolsComplete|TestFeaturesHaveSince|TestToolDescriptionFences_Valid'
go test -count=1 ./internal/mcp/... -run 'TestMCPSchemaBinding|TestBindForInstance'
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/
make docs && make doc-checks
```

The Update Demand row for MCP tools covers all of these in one PR;
see [The Update Demand](update-demand.md).
