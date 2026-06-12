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

**Mergeable-aggregator rule.** Adding a new aggregator that is mergeable means implementing `MergeableAggregator.Merge(other)` and declaring it in `AggregationType.Mergeable()` (`types/types.go`) so it composes correctly under parallel decode (`service/parallel_reduce.go`) and shard reduce (`service/shard_reduce.go`). Both surfaces fold per-worker / per-shard partials in deterministic index order via `mergeShardPartials` + `finalizeMergedPartial`; an aggregator that is registered but not Mergeable will silently force the request down the serial `scanIter` / `shardIter` path. Associative+commutative aggregators (count, sum, min, max, frequency, distinct_count, mode) produce byte-equal merge output; Welford-Pébaÿ aggregators (mean, variance, stddev) use Chan-Welford and stay within ULP of serial. If your aggregator's online state can't be folded associatively, leave `Mergeable()` returning false — both parallel paths gate on `processing.CanMergeRequest` and will fall through cleanly. See `skills/cohort-schema-design.md` (Parallel decode) for the gate composition and observed perf characteristics.

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

## Adding a chain-stage predicate

`ProcessChain` (`pulse.ProcessChain`, `pulse_process_chain`, `pulse api process-chain`) executes a linear pipeline whose stages all pass `processing.CanChainRequest`. Tighten or relax the gate as follows:

1. Edit `processing/chain.go` — `CanChainRequest` calls `CanMergeRequest` first, then layers chain-specific exclusions (`aggregatorEmitsScalar`). Add a new exclusion branch when an operator is mergeable but its emit shape would break the synthesised f64/categorical_u32 schema.
2. Mirror the rule in `descriptor/chain.go` — `chainGateOK` is the predict-side equivalent. Keep them in lockstep; a divergence makes predict pass requests that runtime later rejects.
3. Update `descriptor/capabilities_chain.go` — `processChainCapability()` carries the manifest-facing allowlists and `RejectionRules` strings. Regenerate `descriptor/testdata/manifest.json` via `go test ./descriptor/ -update -run Golden` after edits.
4. Add a failing-gate test in `service/chain_test.go` and a matching predict test in `descriptor/chain_test.go`.
5. Skim `skills/getting-started.md` and `skills/mcp-integration.md` for any operator allowlist that needs adjustment.

### Whole-chain overlays (dual-slot, E6)

`ChainRequest.Overlays []*ChainOverlaySpec` is the whole-chain overlay surface — overlays here execute AFTER every stage finalises (NOT between stages). Per-stage overlays continue to ride the universal `ChainStage.Request.Overlays []OverlaySpec` slot.

- `ChainOverlaySpec` (in `types/chain.go`): `Name string`, `Kind OverlayKind`, `Ref StageRef`, `Target StageRef`, `Scope OverlayScope`, `Params map[string]any`.
- `StageRef` (in `types/chain.go`): XOR `{Index *int, Name string}`. `Index` is a pointer so `0` is meaningful — the canonical "stage 0" call site sets `Index = &zero`, not `Index` unset. The downstream validator (lands in E6-S3 / E6-S7 / E6-S11) enforces "exactly one of Index / Name".
- `OverlayRef.Stage` (in `types/overlay.go`) is the same `*StageRef` — there is exactly one `StageRef` declaration in the codebase. The legacy `OverlayStageRef` identifier is a type alias to `StageRef` and is deprecated.
- `ChainResponse.Overlays []*OverlayLayer` reuses the universal `OverlayLayer` wrapper from `types/overlay.go` (one entry per `ChainRequest.Overlays` spec in matching index order).
- Canonical-hash coverage is data-driven (`types/hash.go`): the slot's `omitempty` tag means overlay-free chain requests hash byte-identically to the pre-E6-S2 form; populated overlays fold into the hash automatically. Locked by `TestChainCanonicalHash_OverlayFreeByteIdentity` / `TestChainCanonicalHash_OverlaysIncluded`.
- Whole-chain handler dispatch + per-kind validation land in E6-S3 / E6-S7 / E6-S11. E6-S2 ships the types only.

## Adding a shard (managing a shard archive)

When an embedder wants to manage a multi-shard `.pulse` archive (a zip-archive cohort that fans out across N standalone `.pulse` shards under union semantics):

1. **Create the archive** from one or more existing single-file `.pulse` shards. First include seeds the canonical schema; remaining includes are validated against it via structural cohesion + the dict prefix rule. Atomic temp + rename:

   ```bash
   pulse shard create q1_2019.pulse \
       --include 20190101.pulse \
       --include 20190108.pulse \
       --include 20190115.pulse
   ```

2. **Append a shard** to an existing archive. Validates cohesion + dict prefix, grows the canonical dict if needed (rewriting `_schema.pulse` before placing the new shard), then in-place appends the payload:

   ```bash
   pulse shard add q1_2019.pulse 20190122.pulse
   ```

3. **List shards** inside an archive (reads `_schema.pulse` + central directory, prints basenames + per-shard record counts):

   ```bash
   pulse shard list q1_2019.pulse
   ```

4. **Verify** by re-validating every shard's header + cohesion against the canonical schema:

   ```bash
   pulse shard verify q1_2019.pulse
   ```

5. **Compact** to reclaim orphan bytes (e.g. after `pulse shard remove`) and refresh canonical metadata (`aggregate_record_count`, `shard_count`):

   ```bash
   pulse shard compact q1_2019.pulse
   ```

Read-side commands (`pulse api process`, `pulse api compose`, `pulse api sample`, `pulse api facet`, `pulse inspect`, `pulse predict`) accept shard-archive paths transparently — see `skills/cohort-schema-design.md` (Sharded cohorts) for the union semantics and memory multiplier.

**Concurrency caveat:** Pulse does **not** provide writer locking. Two processes running `pulse shard add` against the same archive race; last writer wins, earlier writer's shard is lost. Sharding is single-writer by design — the caller owns concurrency control (orchestrator coordination, external advisory lock, or a single-writer architecture).

For maintainers extending the sharding internals, the implementation surface lives in `encoding/archive.go` (Zip64 read/write + EOCD), `encoding/schema_doc.go` (`_schema.pulse` parser/writer), `encoding/cohesion.go` (structural + dict-prefix validators), `service/shard_iter.go` (multi-shard row iterator), `service/shard_reduce.go` (parallel reducer for mergeable ops), `service/shard_admin.go` (create/add/remove/list/extract), `service/shard_compact.go`, `service/shard_verify.go`, `service/anchor_overlay.go` (anchor-syntax overlay), and `internal/cli/shard.go` (CLI thin adapter).

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

## Adding a new `afero.Fs` implementation

When you wire a new `afero.Fs` whose virtual paths ultimately resolve to a regular on-disk file (for example a copy-on-read overlay that caches remote objects into a local tempdir, a base-path wrapper, or any prefix-translating shim), implement the `service.RealPather` capability interface on the fs (or on a wrapper around it):

```go
// service.RealPather — defined in service/fs_probe.go.
type RealPather interface {
    RealPath(name string) (string, error)
}
```

The returned path MUST be `os.Open`-able at the moment of the call and the bytes MUST be identical to what `fs.Open(name).Read` would yield. The signature deliberately matches `*afero.BasePathFs.RealPath` so that wrapper is detected automatically.

Why this matters: `service.resolveRealPath` is the eligibility probe that decides whether the streaming iterator engages the mmap fast path. Without `RealPather`, the probe falls through to the `*afero.OsFs` check and then to the `afero.ReadFile` slow path. **Failure to implement this interface silently disables the mmap optimization — no error, no warning, just a regression in scan throughput on cold-cache wide cohorts.** The mmap policy and probe order are documented at `skills/cohort-schema-design.md` ("Iterator mmap policy") and the rationale for omitting an open-and-inspect fallback is inline at `service/fs_probe.go`.

The regression gate is the `countingFs` test family in `service/` (e.g. `TestCountingFs_*`) — a wrapper that fails the test if `Process` calls `afero.ReadFile` on a single-file cohort path when the fs advertises a real path. If you add a new wrapper and the gate flips red, the wrapper is almost certainly missing `RealPather`.

For caches that materialize the file lazily (CoR overlays), advertise `RealPath` only after the local copy is on disk; return a non-nil error during the in-flight download window so the probe declines and the iterator falls back to `afero.ReadFile` for that call.

Hermetic tests that need to exercise the non-mmap path keep using `fs.NewMemMap()` — `MemMapFs` does not satisfy `RealPather` and the probe correctly declines.
