# Adding a Facet Capability Variant

**Audience:** Pulse internals contributors extending the
`pulse.FacetSchema` endpoint with a new top-level facet behaviour — a
streaming auto-range histogram, a new aggregation kind on numeric
fields, a new contribution-style accumulator.

The facet endpoint sits behind `descriptor.FacetCapability` and runs
through `service/facet_rich.go`. New behaviours land in five files in
lockstep — request type, accumulator dispatch, capability flag,
validator, MCP JSON Schema builder — plus the facet-design skill.

## 1. Extend the request / result types

Add the new fields to `types.FacetRequest` and `types.FacetResult` in
`types/facet.go`. Keep JSON tags backward-compatible — additive only.
Renames or removals trigger a `format_version` bump per the [Output
Format Contract](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md#output-format-contract);
new fields use `omitempty` and do not bump.

## 2. Implement per-row accumulation

Add the per-row accumulation in `service/facet_rich.go`, dispatching
off the schema field type via `newKindAccumulator`. The accumulator
contract — `Add(value)`, `Finalize()`, `Result()` — runs per-row
during the facet pass.

## 3. Capability flag

Add the capability flag in `descriptor/capabilities_facet.go` so the
manifest exposes the new behaviour to LLM agents.
`TestManifestFacetCapability` enforces parity between the capability
block and the runtime surface.

## 4. Validator

`descriptor/facet.go::ValidateFacet` runs without importing
`service` / `processing` (it is structurally no-execute, governed by
`TestPredictNoExecutionImports`). Any new structural rule lands here
as a `SERVICE_VALIDATION` error or an advisory warning.

## 5. MCP JSON Schema builder

Update the JSON Schema builder in `internal/mcp/schema_bind.go` —
the `buildFacetSchemaRequestSchema` function — so the LLM sees the
new fields in the `pulse_facet_schema` tool surface.
`TestMCPSchemaBinding_SampleAndFacetFieldEnum` enforces parity.

## 6. Update the facet-design skill

Update `skills/facet-design.md` with the new behaviour's request
shape, output shape, and any worked example. When the behaviour
warrants a runnable fixture, add it under `examples/facet/` and
update the example metadata. `TestExamples_*` enforces the fixture
contract.

## 7. Run the gates

```bash
go test ./skills/ ./examples/ ./descriptor/
go test ./service/ -run TestFacet
go test ./internal/mcp/ -run TestMCPSchemaBinding
```

The Update Demand row for facet-capability changes covers all of
these in one PR; see [The Update Demand](update-demand.md).
