---
name: contributor-workflow
description: Step-by-step recipes for contributing to Pulse — adding aggregators, attributes, filterers, groupers, windows, features, statistical tests, synth distributions, I/O formats, MCP tools, error codes, and field types. Also covers porting from external sources, debugging predict mismatches, regenerating goldens, and wiring Pulse into an MCP client. Use when extending the engine or onboarding a Claude Code session that will modify Pulse internals.
type: guide
applies_to: process, compose, sample, facet, inspect, predict, manifest
---

# Contributor Workflow

Recipes for extending Pulse. Each recipe lists the files to touch, the registries to update, the skill to update, and the CI gates to run.

## Adding a new aggregator

1. Define the constant in `types/types.go` (e.g., `AGG_MEDIAN`). Add it to `AllAggregationTypes()`.
2. Add `Streamable()` entry in `types/streamability.go` + table in `types/streamability_test.go`.
3. Implement the aggregator in `processing/` — factory function registered in `aggregatorRegistry` (`processing/registry.go`).
4. Write tests in `processing/aggregator_test.go`.
5. Add a section in `skills/aggregation-guide.md`.
6. Add capability declaration in `descriptor/capabilities_aggregators.go` (params, accepts_types, emits_type, streamable_hint).
7. If the aggregator interacts with categorical fields specially, update `descriptor/predict.go`'s `numericAggregations` map.
8. Update CLAUDE.md "Current registered components" count.
9. Run: `go test ./skills/ -run TestSkillsCoverAllComponents && go test ./descriptor/ -run TestManifestOperatorsComplete && go test ./types/ -run TestStreamability_AggregationsKnown`.

## Adding a new attribute / filterer / grouper / window operator

Same pattern as aggregator. Target skill differs:

| Category | Skill | Capability file |
|---|---|---|
| Attribute (`ATTR_*`) | `skills/attribute-composition.md` | `descriptor/capabilities_attributes.go` |
| Filterer (`FILTER_*`) | `skills/aggregation-guide.md` (filtering section) | `descriptor/capabilities_filterers.go` |
| Grouper (`GROUP_*`) | `skills/grouper-design.md` | `descriptor/capabilities_groupers.go` |
| Window (`WIN_*`) | `skills/window-operations.md` | `descriptor/capabilities_windows.go` |

## Adding a new feature operator (`FEAT_*`)

1. Define `FEAT_X` in `types/types.go`. Add to `AllFeatureTypes()`.
2. Implement in `processing/feature/<name>.go`. Register via `init()` calling `register(types.FEAT_X, newX)`.
3. Implement `feature.StreamingComputer` (`PrePass + Finalize + EmitRow`) if streaming-eligible.
4. Add section to `skills/feature-engineering.md` (params + output column naming).
5. Update `descriptor/predict_feature.go`: validate params and emit output labels in `featureOutputLabels`.
6. Capability declaration in `descriptor/capabilities_features.go`.
7. Update CLAUDE.md count.
8. Run: `go test ./skills/ -run TestSkillsCoverAllComponents && go test ./descriptor/ -run TestPredict_Feature`.

## Adding a new statistical test (`TEST_*`)

1. Define `TEST_X` in `types/types.go`. Add to `AllTestTypes()`.
2. `Streamable()` entry in `types/streamability.go` + test table.
3. Implement test in `processing/test.go` (tier-1 row tests) or register as tier-2 variant.
4. Add section in `skills/statistical-testing.md` (Operator catalog).
5. Capability declaration in `descriptor/capabilities_tests.go` (and `postTestCapabilities` for tier-2).
6. Run: `go test ./types/ -run TestStreamability_TestsKnown && go test ./descriptor/ -run 'TestManifestTestsComplete|TestManifestPostTestsComplete'`.

## Adding a new synth distribution

1. Implement in `synth/`. Register in `synth.AllDistributions()`.
2. Add section in `skills/synthetic-data.md` (Supported distributions).
3. Capability declaration in `descriptor/capabilities_distributions.go`.
4. Run: `go test ./skills/ -run TestSkillsCoverAllSynthDistributions && go test ./descriptor/ -run TestManifestDistributionsComplete`.

## Adding a new I/O format

1. Create `io/<format>/` with reader + writer implementing `io.Reader` and `io.Writer`.
2. Tests in `io/<format>/<format>_test.go`.
3. Wire into `ImportJob` / `ExportJob` if needed.
4. Add or update `skills/export-format-selection.md` (when to use the format).
5. If a CLI flag is added, update `skills/getting-started.md`.

## Adding a new error code

1. Add code constant in `errors/codes.go` + entry to `allCodes` slice.
2. Add entry to `codeMetadata` map in `errors/fixup_metadata.go` — `Message` + `Fixups[]` (or `FixupNotApplicable: true`).
3. Surface via `pulse_errors_lookup` MCP tool / `pulse errors lookup CODE` CLI — no skill file edit needed.
4. Run: `go test ./errors/ -run 'TestCodesHaveFixups|TestErrorsLookup' && go test ./descriptor/ -run 'TestManifestErrorCodesComplete|TestManifest_ErrorCodesSlim'`.

## Adding a new field type

1. Define `FieldType` constant + `ByteSize()` method.
2. Schema reader case in `encoding/`.
3. Update `descriptor/capabilities_*.go` for operator `accepts_types` references.
4. Add to "All field types" table in `skills/cohort-schema-design.md`.
5. Add categorical-vs-numeric routing in `descriptor/predict.go` if applicable.
6. Run: `go test ./skills/ -run TestSkillsCoverAllFieldTypes`.

## Adding a facet capability variant

The `pulse.FacetSchema` endpoint sits behind `descriptor.FacetCapability`. To
add a new top-level facet behaviour (e.g. a streaming auto-range histogram, a
new aggregation kind on numeric fields, a new contribution-style accumulator):

1. Extend `types.FacetRequest` / `types.FacetResult` (`types/facet.go`) with
   the new fields. Keep JSON tags backward-compatible — additive only.
2. Implement the per-row accumulation in `service/facet_rich.go`, dispatching
   off the schema field type via `newKindAccumulator`.
3. Add the capability flag in `descriptor/capabilities_facet.go` so the
   manifest exposes the new behaviour to LLM agents.
4. Mirror the validator: `descriptor/facet.go::ValidateFacet` runs without
   importing `service` / `processing`, so any new structural rule lands here
   as a `SERVICE_VALIDATION` error or advisory warning.
5. Update the JSON Schema builder in `internal/mcp/schema_bind.go`
   (`buildFacetSchemaRequestSchema`) so the LLM sees the new fields.
6. Update `skills/facet-design.md` and (when relevant) the example fixtures
   under `examples/facet/`. Run `go test ./skills/ ./examples/ ./descriptor/`.

## Adding a new MCP tool

1. Implement handler in `internal/mcp/`. Register in `RegisteredTools()` (`internal/mcp/tools.go`).
2. Add name + description in `internal/mcp/mcptools/meta.go`.
3. If tool has field-name parameters, add per-tool JSON Schema builder in `internal/mcp/schema_bind.go` + entry in `Bind`.
4. Add section in `skills/mcp-integration.md` (Tool surface + Schema-bound enums if applicable).
5. Run: `go test ./skills/ -run TestSkillsCoverAllMCPTools && go test ./descriptor/ -run TestManifestMCPToolsComplete && go test ./internal/mcp/ -run TestMCPSchemaBinding`.

## Wiring Pulse into an MCP client

1. Build the binary: `make build`. The resulting `bin/pulse` must be on the client's `PATH` (or referenced absolutely).
2. Configure the client (Claude Desktop's `claude_desktop_config.json` or Claude Code's `~/.claude.json`) with an `mcpServers.pulse` entry running `pulse mcp` and exporting `PULSE_DATA_DIR`. The `--bind-on-open` flag (default true) controls whether successful `pulse_inspect` calls trigger registration of session-scoped tool variants whose JSON Schemas constrain field-name parameters to the cohort's actual fields. Pass `--bind-on-open=false` for clients that bind themselves.
3. Restart the client. Pulse tools (`pulse_inspect`, `pulse_predict`, `pulse_process`, etc.) and resources (`pulse://*.pulse`, `pulse-skill://*`) appear in the tool/resource list.
4. See `skills/mcp-integration.md` for the full configuration recipe, including the "Schema-bound enums" section that describes the inspect trigger, the multi-file limitation (latest inspect wins), and the transport-support caveat (stdio sessions in mcp-go v0.52.0 do not implement `SessionWithTools`, so binding is a no-op there; SSE / Streamable HTTP transports honor it).

## Debugging a predict mismatch

1. Run `pulse predict --json < request.json` against your `.pulse` file.
2. Check the envelope's `errors` and `warnings` arrays.
3. Common issues: field-name typo (`SERVICE_VALIDATION`), numeric aggregation on categorical field (`PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`), low-quality description (`PULSE_FIELD_DESCRIPTION_LOW_QUALITY`).
4. Use `pulse inspect --json <file.pulse>` to see actual schema fields and types.
5. Predict reads only the header and schema. If predict reports valid but execution fails, the bug is in the processing layer — not predict.

## Regenerating goldens

Golden files live in `descriptor/testdata/` and end with a `// golden-hash: <sha256>` line.

```bash
go test ./descriptor/ -run 'Test.*Golden' -update
go test ./descriptor/ -run TestGoldensNotHandEdited  # verify hashes
```

**Never hand-edit golden files** — `TestGoldensNotHandEdited` will fail.

## Running CI gates locally

```bash
# All tests
go test ./...

# Descriptor contract gates
go test ./descriptor/ -run 'TestPredictNoExecution|TestDescriptorNoFmtSprintf|TestGoldensNotHandEdited'

# Skill coverage gates
go test ./skills/ -run 'TestSkillsCoverAll|TestSkillsManifestConsistent|TestSkillsFrontmatter'

# Hygiene gate (no predecessor references)
go test . -run TestNoOrbit

# CLAUDE.md enforcement gates
go test . -run 'TestClaudeMd|TestUpdateDemandTable'
```

## Porting workflow

When porting functionality from an external source into Pulse:

1. Identify source behavior and destination Pulse package.
2. Write Pulse-native tests first in the destination package (all identifiers Pulse-native — no predecessor references).
3. Run `go test ./...` and confirm new tests fail with informative messages. If they pass, the test is wrong — fix it.
4. Port the implementation. Refactor for Pulse-native idioms.
5. Re-run tests. Iterate until green.
6. Update target skill file(s) per the Update Demand. Run skill-coverage gates locally.
7. Update CLAUDE.md if the change touches a contract, env var, format version, or registered surface.
8. Run `go test . -run TestNoOrbit` and confirm zero matches before opening the PR.

## Testing with in-memory filesystem

Use `fs.NewMemMap()` for hermetic tests. It returns a `fs.Config` backed by `afero.NewMemMapFs()` — no disk I/O.
