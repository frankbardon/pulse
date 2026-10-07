# CLAUDE.md

Pulse is a self-describing tabular data processing engine. Ships as a Go library (`github.com/frankbardon/pulse`) and a CLI (`cmd/pulse/`). Library primary; CLI thin adapter.

**Design principles**

- **Library-first.** `pulse.go` is the public API — `New`, `Open` (+ `Cohort.Reader`), `NewCohortBuilder`, `Process`, `Compose`, `ComposeParallel`, `ProcessStream`, `ProcessChain`, `Import`, `Export`, `Convert`, `Inspect`, `InspectEnvelope`, `InspectBytes`, `Predict`, `PredictBytes`, `Sample`, `Facet`, `Synth`, `Profile`, `CountRecords`, `Lookup`, `BuildIndex`, `VerifyIndex`, `ListIndexes`, `DropIndex`, `CohortArtifacts`, `WidenSetField`, `Dedup`, `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`, `InitFeatureProfile`, `CheckFeatureProfile`, `DiffFeatureProfile`, `DescribeFeatureProfile`, `ExampleFeatureProfiles`, `ExampleFeatureProfile`, `Glossary`, `Intents`, `Ontology`, `Skills`, `Skill`, `Version`. **The CLI never contains business logic.**
- **Self-describing.** Every `.pulse` file carries its schema in the header. `internal/descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `internal/skills/` embeds an atomic-per-surface pack (`op-*` / `tool-*` / `type-*`) plus ~20 topical design skills via `//go:embed *.md`; the filesystem walk + frontmatter parse is the source of truth.
- **Embedder-extensible.** `pulse.Options.Extensions` registers custom operators, expr functions, named tables, skills and examples. Predict, manifest, MCP and runtime treat them identically to built-ins.
- **Embedder-first, consumer-agnostic.** Library embedders are a first-class audience: the public Go surface is sized for them, and real downstream usage catalogs are valid evidence for what it must cover. Pulse never depends on a consumer — no reverse imports, no consumer names in contract docs or code, and every public symbol is justified by a general embedder use case, not one consumer's convenience. Harnesses discover Pulse via `pulse manifest --json` + the embedded skills.

**Where the detail lives.** Contributor recipes are the mdBook Internals chapter under `docs/src/internals/` — one `adding-*.md` per extension point plus `regenerating-goldens.md`, `debugging-predict.md`, `wiring-mcp-client.md`, `extension-points.md`. Long-form contract prose lives under `.claude/reference/` — **see "Reference Docs" at the bottom for the index and which file each kind of work requires loading.**

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if trigger fires without required update.

One row per category: trigger → companions → gates. **The exhaustive per-slot table is `.claude/reference/update-demand.md` — load it before touching any contract below.** ATOMIC = `TestOperatorHasAtomicSkill` + `TestAtomicSkillHasRequiredSections` + `TestSkillTokenBudget`. A **bold filename** in column 2 is a `.claude/reference/` file that must be LOADED BEFORE the change, not merely updated after it.

| If you change... (category) | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator, attribute, filterer, grouper, feature operator, window operator, statistical test (`TEST_*`), regression (`REG_*`), synth distribution or overlay kind (`OVERLAY_*`) | `skills/op-<category>-<kebab>.md` + the matching `internal/descriptor/capabilities_*.go` + an `internal/examples/` `_meta.operators` tag | ATOMIC, the category's `TestSkillsCoverAll*` + `TestManifest*Complete`, `TestEveryOperatorHasAnExampleTag` |
| A registered matrix operator (`MAT_*`), the `Request.Vectors` / `Request.Matrices` / `Response.Matrices` slots or `MatrixValues` | **`matrix-and-vectors.md`** + `skills/op-mat-<kebab>.md` + `internal/descriptor/capabilities_matrix.go` + a `features.go` row on `capability:matrices` + an example tag + the payload-schema golden | ATOMIC, `TestSkillsCoverAllMatrixOps`, `TestManifestMatrixOpsComplete`, `TestEveryOperatorHasAnExampleTag` |
| A registered MCP tool (add/remove) | `skills/tool-<kebab>.md` (strip `pulse_`) + `internal/mcp/toolmeta/meta.go` | ATOMIC, `TestSkillsCoverAllMCPTools` |
| A registered field type, a `.pulse` format or shard-archive change, a sidecar file, an SPSS surface, or projected decode | **`byte-layout.md`** + `skills/type-<kebab>.md` + `skills/cohort-schema-design.md` + CLAUDE.md "Byte-layout invariants" | ATOMIC, `TestSkillsCoverAllFieldTypes`, `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + ≥1 Fixup — and an `internal/descriptor/error_owners.go` owner entry | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete`, `TestErrorOwners_Complete` |
| A `--json` envelope, `format_version` (currently `"1.1"`), or any payload-reachable `types` slot | CLAUDE.md "Output Format Contract" + `docs/src/contract/payload-schema.md` + regenerate `descriptor/testdata/payload-schema.json` | `TestPayloadSchemaGolden`, `TestClaudeMdMentionsFormatVersion` |
| `ComposedResponse` shape, the Compose facade return type, the `compose --json` `data` wrapping, or `OverlayLayer.Warnings` | CLAUDE.md "Output Format Contract" + `skills/overlay-system.md` (Per-layer warnings section) + the reference row's sites | `TestComposedResponse_OverlayFreeByteIdentical` |
| A registered I/O format (`internal/io/<fmt>/` adapter) | the registry / factory / CLI / capability sites in the reference row + `skills/tool-import.md` + `docs/src/internals/adding-io-format.md` | `TestFromExt_Matrix`, `TestManifestImportCapability` |
| A CLI leaf (add/remove) | the `docs/src/cli/flags.md` command index + `skills/session-bootstrap.md` for an agent-relevant flag + a `commandBindings` entry (`internal/descriptor/features.go`) | `TestSkillsCoverAllCliLeaves`, `TestCommandBindings_Complete` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| An environment variable | CLAUDE.md "Build / Env" + `skills/session-bootstrap.md` | `TestClaudeMdMentionsAllEnvVars` |
| `Response.Components` shape, a per-operator `ComponentSchema`, an Extension registration's `ComponentSchema`, or `CrosstabSpec.MarginAggregations` on either arm | **`response-components.md`** + CLAUDE.md "Output Format Contract" + `skills/response-components.md` + the operator's atomic skill + `internal/descriptor/capabilities_*.go` + `docs/src/internals/extension-points.md` + `skills/crosstab-guide.md`. **A record reaches an auxiliary margin only if it reached a CELL — state that rule wherever the figures appear** | `TestClaudeMdMentionsComponentsContract`, `TestExtensions_ComponentSchemaParity`, `TestCrosstab_BufferedAuxMarginMatchesFused` |
| The request-template document model, variable/target sets, or `$var` / `{{}}` / `$when` | **`request-templating.md`** + `skills/request-templating.md` + `docs/src/library/request-templating.md` + CLAUDE.md "Request templating" | `TestTemplatePackage_ImportBoundary` |
| Any `synth/` / `internal/synth/` surface — capture, `SpecFromProfile`, `Spec`, generation, structural rules, `--suggest-rules`, fidelity report | **`synthetic-data.md`** + `skills/synthetic-data.md` + `skills/synth-models.md` + `skills/synth-structural-rules.md` | `TestSkillsCoverAllSynthDistributions` |
| A feature (operator, capability, I/O format, MCP tool or prompt) or the feature-profile model | **`feature-profiles.md`** + a `features.go` row (`Since` + dependency edges) + `docs/src/library/feature-profiles.md`; never a runtime skill | `TestFeaturesHaveSince`, `TestProfileDependenciesComplete` |
| A `multiplicity` block, `Options.DefaultMultiplicity`, its adjusted outputs, predict `p_values` or the fold | `update-demand.md` Multiplicity row (load first) + `skills/multiplicity-correction.md` + p-emitting atomic `## Params` | `TestMultiplicityNoneIsIdentity` |
| A registered operator (any category) → its `Purpose` (+ `Interpretation` if inferential); the intent taxonomy, glossary, virtual skills or extension guidance hook | **`guided-analysis.md`** + `builtinPurposes` / `builtinInterpretations` | `TestSkillsCoverAllPurposes`, `TestInterpretationCoversOutputs`, `TestManifestGuidanceBudget` |
| A skill file's stem, frontmatter, required sections or budget; a feature name in a skill (fence it) | **`skill-pack.md`** + the file itself | ATOMIC, `TestSkillsCoverFeatureFences` |
| `Request.Weight`, a per-slot `weight` (null ≠ absent), `Options.DefaultWeight`, an operator's weight class, or the weighted floor keys | **`weighting.md`** + `skills/weighting.md` + the operator's atomic skill + **`response-components.md`** + the payload-schema golden | the weighting row in `update-demand.md` |
| Any Request slot, Response slot, capability block, or Execution-mode wiring | the per-slot row in `update-demand.md` | per-slot suites cited there |

Between them the rows carry every word `TestUpdateDemandTableCovers` checks; keep it that way when editing one.

## Architecture

**Public packages** (frozen at v1.0.0, `TestPublicAPIGolden`): root `pulse`, `types`, `errors`, `encoding` (schema nouns + ungrouped raw-byte primitives), `descriptor` (result/envelope types), `io` + `synth` (alias facades over `internal/io` / `internal/synth`), `mcp/gosdk`, `mcpserve`, `extend` (operator-authoring API), `linalg` (FMA-free reference kernels + gonum-backed decompositions). Everything else is under `internal/` — engine `internal/processing`, orchestration `internal/service`, NO-EXECUTE `internal/descriptor`, `internal/io/<fmt>` adapters. **Contract: `.claude/reference/architecture.md` (full tree, facade technique, root aliases, `io` boundary, `linalg` split, MCP split) — load it before moving a package or adding a public symbol.**

Docs: <https://frankbardon.github.io/pulse/>.

CLI commands map 1:1 to manifest commands: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `schema`, `mcp`, `widen`, `dedup`, `version`, plus `synth from-schema`, `synth from-profile`, `profile create`, `shard {create,add,remove,list,compact,verify,extract}`, `index {build,list,verify,drop}`, `features {init,check,diff,show}`, `api {process,compose,facet,process-chain,lookup}`. `pulse schema` prints the payload JSON Schema RAW — not envelope-wrapped.

**MCP:** `internal/mcp/` is the SDK-free core (`TestMCPCore_NoSDKImport`); `mcp/gosdk/` is the ONLY go-sdk importer (`Register(server, p, cfg)`). **The manifest is the source-of-truth tool count, never hardcode it**; `pulse://schema` is a RESOURCE, not a tool. **Tools, prompts and resources are registered per instance** — a profiled instance mounts only what it enables, so `gosdk.RegisteredTools()` / `RegisteredPrompts()` stay the GLOBAL lists. Detail: `.claude/reference/architecture.md` (MCP layer split).

## Code Conventions

### Naming

- Pulse-native identifiers. No predecessor references (`TestNoOrbitPrefix`, `TestNoOrbitPrefixes`).
- Module path: `github.com/frankbardon/pulse`. The public `io` package is imported as `pio "..."`.
- Component types: SCREAMING_SNAKE — `AGG_COUNT`, `ATTR_ZSCORE`, `FILTER_INCLUDE`, `GROUP_CATEGORY`, `WIN_LAG`, `FEAT_LOG`, `TEST_T`.
- Error codes: DOMAIN_CATEGORY — `ENCODING_INVALID`, `PROCESSING_CONFIG`, `SERVICE_VALIDATION`, `DATA_FILE`, `CLI_INPUT`, `PULSE_IMPORT_ROW_ERROR`.

### Error handling

Six domains: `CLI`, `DATA`, `ENCODING`, `PROCESSING`, `PULSE`, `SERVICE`. Canonical list in `errors/codes.go` (`allCodes`). Every code needs `codeMetadata` entry (`errors/fixup_metadata.go`) with `Message` + ≥1 `Fixup` template OR `FixupNotApplicable: true`. Per-code prose surfaced via `pulse_errors_lookup` (MCP) / `pulse errors lookup CODE` (CLI). Manifest carries name-only list.

Field descriptions in `.pulse` capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`). Low-quality descriptions emit `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warnings (errors under `--strict`).

### Byte-layout invariants

`.pulse` binary format:

1. **9-byte header:** magic `PULSE\x00\x00\x00` + format version ∈ `{0x01, 0x02}` (`encoding.MagicBytes`, `HeaderSize = 9`); any other byte is `ENCODING_INVALID`. **`ReadHeader` RETURNS the version and every `ReadSchema(r, version)` must be handed it** (`TestReadSchemaCallersThreadHeaderVersion`). The version is a function of schema content, never a flag — `0x02` iff the schema declares a parent group, so an ungrouped write stays `0x01` byte-identically; `0x01` cohorts stay readable forever.
2. **Schema block:** per-field descriptor — name, type byte, **nullable flag byte**, byte offset, bit position, optional description. Under `0x02` a `u64`-length-prefixed extension block of tagged sections follows; an unknown REQUIRED section is refused.
3. **Dictionary blocks:** inline after the schema, for every dictionary-bearing field.
4. **Record data:** fixed-width rows, stride derived from the schema.
5. **Per-record null bitmap (optional).** Present iff any field is nullable (`Schema.HasBitmap()`): a trailing `ceil(field_count / 8)` bytes per record; field `i` → byte `i/8`, bit `i%8` (LSB-first); `1` = null.
6. **Parent groups (`0x02`).** Each distinct member tuple stored ONCE in a schema-block dictionary, rows carry a `u32` index; byte sizes are PHYSICAL, every field index LOGICAL; decode is identical to the ungrouped twin. Written only on request (`--group`, `--elide-constants`).

20 field types (table: `skills/cohort-schema-design.md`; per type `skills/type-<kebab>.md`): `u4`, `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `categorical_u8`/`u16`/`u32`, `decimal128`, `set_u8`/`u16`/`u32`/`u64`/`u128`/`u256`. `u4` / `packed_bool` are bit-packed (`ByteSize() == 0`). `categorical_*` and `set_*` carry an inline dictionary; a `set_*` value is a bitmask (bit `i` ↔ entry `i`) and an empty mask is "no selection" — distinct from null. **`set_u128` / `set_u256` (≤256 members, a hard ceiling) are refused by the `uint64` value API with `ENCODING_TYPE_MISMATCH`, never truncated.** **`date` is epoch DAYS (`int32`); `datetime` is epoch SECONDS (`int64`) — never interchangeable; swapping them rescales every value by 86,400.** Nullability is orthogonal to type; `decimal128` and `set_*` nulls ride the bitmap only.

**Shard archive variant.** A `.pulse` path may instead be an uncompressed Zip64 **shard archive** (zip magic `PK\x03\x04`, dispatched at `pulse.Open`; a reserved `_schema.pulse` entry + N shards); the single-file format is unchanged and `.pulse.zst` is transport-only. Set fields WIDEN across shards, and every archive rewrite is atomic with a MANDATORY warning.

**Contract: `.claude/reference/byte-layout.md` — load it before any byte-layout change; its "The always-load half, long form" paragraph holds what this section compresses. Each of these fails SILENTLY when got wrong:** the sidecar point-lookup index + its manifest, the SPSS metadata sidecar (`cohort.pulse.spss.json`), SPSS derived columns / export / convert, target-aware export predict, default-on projected buffered decode.

### Smart defaults

When a request slot names a field but omits `Type`, engine infers from schema type. Table in `internal/descriptor/defaults.go` (`defaultRules`).

| Field type | Default aggregation | Default grouper |
|---|---|---|
| numeric (u4/u8/u16/u32/u64, f32/f64, decimal128) | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| categorical_* | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `datetime` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `packed_bool` | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |

`Field.Nullable` orthogonal — never changes inferred operator. Defaults apply only when `Field` set and `Type` empty; never override explicit `Type`; never cross categories; never default tests, filter expressions, attributes, windows, features. Disable via `pulse.Options{DisableDefaults: true}` or `--no-defaults`. Predict always computes `DefaultsApplied`.

**Date-family field types.** `GROUP_DATE`, `GROUP_DATE_RANGES` and `FILTER_DATE_RANGES` accept both `date` and `datetime`; all epoch-day / calendar / zone math lives in `internal/temporal` (`TestNoZoneMathOutsideTemporal`). Long form: `execution-modes.md` (Date-family field types, Time zones); skill `skills/time-zones.md`.

## Output Format Contract

### `--json` envelope

All `--json` CLI output and every descriptor operation use `descriptor.Envelope` — `{format_version, data, request, errors, warnings}`, built with `descriptor.NewEnvelope(data)`.

- `format_version` is always `"1.1"`, bumped from `"1.0"` for the Compose facade lift. **Additive-only: bump only on a backward-incompatible shape change** — new `data` fields do not bump, renames and removals do. Any bump MUST update this section.
- `errors` / `warnings` are `{"code", "message", "details"}` entries, an empty array (never null) when absent. **A FATAL `*errors.CodedError` carries its own code** — every CLI leaf (`pulse api *` included) routes through `writeCodedErrorEnvelope`, which unwraps with `errors.As` and falls back to the placeholder (`PROCESS_ERROR`, …) only for an UNCODED error. Stringifying a coded error into the placeholder makes `errors[0].code` unusable with `pulse errors lookup`. **The overlay family follows the same rule**: every `PULSE_OVERLAY_*` fault is raised with that code as its own `Code`, never `PROCESSING_INTERNAL` with a `details["code"]` echo; `PROCESSING_INTERNAL` is reserved for a caller-side invariant violation with no user-facing code.
- **Undefined figures are `null`.** A non-finite float is JSON `null` in place, key kept (`types.MarshalFinite`); Go keeps NaN. Long form: `.claude/reference/response-components.md` (Undefined figures on the wire).
- `request` is an opt-in echo of the *normalized* request, omitted unless `Options.EchoRequest` / `--echo-request`; its shape follows the operation (one of the five request roots). Streaming skips the echo. Additive `omitempty`; no `format_version` bump.

**Compose envelope.** `pulse api compose --json` `data` is a `ComposedResponse` object `{responses, overlays}`, not the legacy array; `--stream` bypasses the envelope with per-row NDJSON. Long form: `.claude/reference/response-components.md` (Opt-out and the Compose surface).

**Multiplicity outputs.** Opt-in `multiplicity` adds `p_adjusted` + `significant_adjusted` BESIDE the raw p, which never moves. Long form: `execution-modes.md` (Multiplicity).

**Response shaping.** Opt-in `Request.Return {preset, include, exclude, precision}`; an excluded part is ABSENT on the wire (never `null`), output stamped `returned`. Long form: `update-demand.md` (`Request.Return` row), `skills/response-shaping.md`.

**Matrices.** `Request.Vectors` + `Request.Matrices` → `Response.Matrices` (`MatrixResult`, each matrix a dedicated `MatrixValues`, never the crosstab `MatrixPayload`). Long form: `matrix-and-vectors.md` (Matrix slot).

### Response.Components

Every `Response` carries an optional `Components *ResponseComponents` (additive `omitempty`; `format_version` stays `"1.1"`). Mirrors the request shape:

- `Aggregations []AggregationComponents` — one entry per aggregator slot; universal floor `{n, n_null}` + operator-specific `Operator map[string]any` keyed by the manifest schema. Grouped: cohort-wide floor + per-row `Groups` (reference).
- `Groupers []GrouperComponents` — universal floor `{total_n, n_null}` + operator-specific bucket layout.
- `Crosstab *CrosstabComponents` — `CellCounts[r][c]`, `CellComponents[r][c]`, row/column/grand-total margin counterparts, axis-key components. Mirrors `MatrixPayload` coordinate-for-coordinate. Auxiliary `crosstab.margin_aggregations` margin figures extend this bullet — reference below.
- `Filterers []FiltererComponents` — uniform `{n_in, n_out, n_null_input}` across all 11 filterers.
- `Run *RunComponents` — `total_records`, `filtered_records`, `null_records`, `shard_count`, `partial_cohort_reason`. Coexists with `Response.Metadata`: `Metadata` keeps non-numerical run facts (cohort filename); `Run` carries the typed counters.
- `Matrices []MatrixComponents` — one per `Response.Matrices` result: floor `{n, n_null, n_listwise_dropped}` (weight-0 rows count in `n`), pairwise `{min_pair_n, max_pair_n}`, the weighted floor keys, operator map (`ddof`). Rides `capability:matrices`.

Per-operator schemas live in `descriptor.Manifest.ComponentsSchemas.{Aggregators,Groupers,Filterers,Matrices}`, each carrying a mergeability class — `Mergeable` / `Partial` / `None` (`types.ComponentsMergeability`). Streaming chunks emit running state for mergeable operators; non-mergeable ones surface only at terminal flush.

**Opt-out.** `Options.DisableComponents bool` (engine default) + `types.Request.DisableComponents *bool` (per-request, `nil` inherits engine — a request `return` never re-opens an engine-off gate; only explicit `false` does); CLI `--no-components` on `api process` / `process-chain` / `compose`. Disabled leaves `Components` `nil`, byte-identical — `format_version` NOT bumped. A sub-part `return` drops is never COMPUTED (`processing.ComputePlan`; overlays veto).

**Weighted slots** add `omitempty` aggregator-floor keys `sum_weights`, `n_eff` (probability only), `n_weight_invalid` — absent means unweighted; `n` / `n_null` and every count stay raw ints. Long form: `.claude/reference/weighting.md`.

**Long form: `.claude/reference/response-components.md`** — the auxiliary `crosstab.margin_aggregations` figures (ADMISSION rule, `present` semantics, display-flag gate, allocation/emission gap), the full opt-out contract and the Compose per-slot / per-layer surface. Skill: `skills/response-components.md`.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** Use `encoding/json`. Grep-gated by `TestDescriptorNoFmtSprintf`.
- **No hand-built XML/CDATA.** Use `encoding/xml`.
- Use `descriptor.NewEnvelope(data)` for the standard envelope.

### Payload JSON Schema

`internal/descriptor.BuildPayloadSchema()` returns the deterministic JSON Schema (draft 2020-12) for every public payload — the request roots, result shapes and the `Envelope` (open `data` slot). Generated by reflection over `types`, registry-injected enums (`types.All*Types()` / `AllOverlayKinds()` / `AllRegressionTypes()`) and strict unions (`OverlayRef`, `OverlayPayload`); operator `params` stay open. Golden `descriptor/testdata/payload-schema.json`; `$id` version equals `format_version`; root `$comment` carries `feature_set_digest`. **`Pulse.PayloadSchema()` is the instance's view** (enums, slots and roots it hides are absent) and backs `pulse schema` (raw); `pulse://schema` stays full. Detail: `.claude/reference/feature-profiles.md` (Payload schema hiding). Full prose: `docs/src/contract/payload-schema.md`.

### Manifest payload

`internal/descriptor.BuildManifest()` returns the deterministic LLM-bootstrap blob — one fetch per session, client-cached, reachable as `pulse manifest --json` and `pulse_manifest`. Top level: `format_version`, `commands`, `components` (six operator slices), `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes_count` + `error_domains` + `error_codes` (slim), `mcp_tools`, `cohort_types`, `skills`, `extensions`, `intents`, `return_presets`, `feature_set_digest`, plus the capability blocks `Facet`, `Join`, `ProcessChain`, `Crosstab`, `Export`, `Import` (pointers, omitted when hidden) and `Overlays`. Sort-stable; golden at `descriptor/testdata/manifest.json`. Declarations live in `internal/descriptor/capabilities_*.go`; MCP tool metadata in `internal/mcp/toolmeta/meta.go`.

### Predict / Inspect contracts

**Contract: `.claude/reference/predict-inspect.md` — load it before changing predict, inspect, `pulse_inspect` or `CountRecords`.** The always-load half:

- **Predict structural ban:** `internal/descriptor/predict.go` MUST NOT import `internal/service/` or `internal/processing/`. Enforced by `TestPredictNoExecutionImports`. Reads only header + schema, never records.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods plus schema gates (decimal). Runtime parity via the engine's internal streamability gate (`TestPredict_Streamable_MatchesRuntime`). Beside it, `CrosstabFusable` (nil ⇔ no crosstab) + `CrosstabFusionReasons`: the fused-crosstab dispatch answer from the shared `internal/crosstabfuse` rule, instance-aware (`DisableCrosstabFusion`) — `TestPredict_CrosstabFusableMatchesRuntime`.
- **Inspect reads no record:** header + schema + sidecar metadata (`suggested_weight`); dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`; `RecordCount` derives from the file LENGTH. **`Pulse.Inspect` drops `env.Warnings`** — CLI and `pulse_inspect` read `Pulse.InspectEnvelope`.
- **CountRecords header-fast:** no payload decode; the single-file floor division exists ONCE (`encoding.Schema.RecordCountForPayload`, shared with `Inspect`). `Inspect` warns on a truncated tail, `CountRecords` floors SILENTLY — deliberately; never make it error.

### Execution modes (pointers)

**Contract: `.claude/reference/execution-modes.md` — load it before engine work.** Its "Mode index" section lists every mode with its knob and named skill (streaming, projection, parallel Compose / shards / decode, ProcessChain, join, crosstab, facet, filter precompute, point lookup, overlays, matrices).

## Non-Skippable CI Gates

`TestClaudeMdMentionsAllNonSkippableGates` scans every `*_test.go` for eight prefixes — `TestSkillsCover`, `TestClaudeMd`, `TestUpdateDemand`, `TestNoOrbit`, `TestGoldensNot`, `TestPredictNo`, `TestDescriptorNo`, `TestPerPackageCoverage` — and requires each match listed BY NAME below, from any package. Write the gate and its entry together. What each one checks: `.claude/reference/update-demand.md` (The CLAUDE.md CI-gate list, per-gate prose).

- CLAUDE.md hygiene: `TestClaudeMdMentionsFormatVersion`, `TestClaudeMdMentionsAllEnvVars`, `TestClaudeMdMentionsAllNonSkippableGates`, `TestClaudeMdMentionsComponentsContract`, `TestClaudeMdSizeBudget` (**CLAUDE.md ≤ 40,000 bytes — displace prose into `.claude/reference/`, never raise the ceiling**), `TestUpdateDemandTableCovers`, `TestUpdateDemandTableCoversComponents`.
- Predecessor references: `TestNoOrbitPrefix`, `TestNoOrbitPrefixes`.
- Descriptor contracts: `TestPredictNoExecutionImports`, `TestDescriptorNoFmtSprintf`, `TestGoldensNotHandEdited`, `TestPerPackageCoverageFloors`.
- Skill coverage: `TestSkillsCoverAllComponents`, `TestSkillsCoverAllFieldTypes`, `TestSkillsCoverAllWindowTypes`, `TestSkillsCoverAllMCPTools`, `TestSkillsCoverAllSynthDistributions`, `TestSkillsCoverAllRegressions`, `TestSkillsCoverAllMatrixOps`, `TestSkillsCoverAllOverlayKinds`, `TestSkillsCoverAllPurposes`, `TestSkillsCoverShardingTopics`, `TestSkillsCoverAllCliLeaves`, `TestSkillsCoverAllOperatorComponents`, `TestSkillsCoverProfileGet`, `TestSkillsCoverFeatureFences`, `TestSkillsCoverAllCrossReferences`.
- Atomic-skill structure / budget / example tag (not prefix-matched): `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestOperatorHasAtomicSkill`, `TestEveryOperatorHasAnExampleTag`.

Other load-bearing gates (`TestManifest*Complete`, `TestStreamability_*`, `TestExtensions_*`, `TestExamples_*`, `TestShardArchive*`, the per-mode suites): `.claude/reference/update-demand.md` (Other load-bearing contract gates).

## Build / Env

`make build` (default; injects `VERSION` from `git describe` via ldflags into `internal/buildinfo`, read by `pulse.Version()`), `test`, `fmt`, `vet`, `lint`, `cover`, `clean`, `dist` (6-platform archives + `checksums.txt` into `dist/`), `docs`, `docs-serve`, `docs-clean`. A `v*.*.*` tag push runs `release.yml`: `ci.yml` via `workflow_call`, then `make dist`, assets attached to the GitHub Release (`-` tags pre-release). A `.env` at repo root is auto-loaded. `make lint` = `go vet` + `staticcheck`, and must pass before any push.

**Environment variables** — one line each; `pulse.Options` always overrides:

- `PULSE_DATA_DIR` — base directory for `.pulse` cohorts, used by `fs.Default()`. The only required env var; bypass with `Options{DataDir}` or `Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdir under the fs root (default `imports`), honoured by `imports.Manager`; sidecar read settings (`source_tz`, `dst_policy`, …): `skills/session-bootstrap.md`.
- `PULSE_IMPORT_TTL` — default TTL for managed imports: Go duration (`24h`), day form (`7d`), or `pin`. Default `7d`.
- `PULSE_LABEL_TABLES_DIR` — directory whose `*.json` files auto-load as `LabelTables` at `pulse.New` time, keyed by filename. Detail: `skills/label-display.md`.
- `PULSE_RANGE_TABLES_DIR` — same shape for `RangeTables` (bare `{label,start,end}` array or a `{"description","ranges"}` wrapper; filename minus `.json` is the table name), validated through the shared range-compilation pass. A name declared both programmatically and on disk is a hard error.
- `PULSE_MCP_NO_COHORT_SCAN` — `pulse mcp` only (flag `--no-cohort-scan`): skip enumerating `.pulse` files as `pulse://` resources; the template stays, so cohorts stay readable. Library: `gosdk.Config.DisableCohortScan` / `mcpserve.Options.DisableCohortScan`.
- `PULSE_FEATURE_PROFILE` — `pulse mcp` / `mcpserve.NewPulse` only (flag `--feature-profile` wins): OS path to a feature profile.
- `PULSE_TEMPLATES_DIR` — request-template roots, `os.PathListSeparator`-separated, PATH-style precedence (first root wins). **Precedence and the hot-reload phase table: `.claude/reference/request-templating.md` (Directory precedence, Hot-reload lifecycle).**

Both table directories skip Pulse's own sidecars yet hard-fail any other unparseable `*.json`: `.claude/reference/byte-layout.md` (Table-directory sidecar exclusion).

**Knobs.** `pulse.Options` concurrency `ShardWorkers` / `DecodeWorkers` (default `0` ⇒ `NumCPU`, negatives rejected at `pulse.New()`) and overlay knobs `DictPrefixFast` / `MaxPanelTargets`: `.claude/reference/execution-modes.md` (Parallel shards, Parallel buffered Process, Overlays).

Hermetic testing: `fs.NewMemMap()` (`internal/fs`) returns an `afero.NewMemMapFs()`-backed `Config`; no disk I/O.

## Extension Points

`pulse.Options.Extensions` registers embedder operators (eight categories, authored against the public `extend` package), expr functions and named tables at `pulse.New()`; predict, manifest, MCP and runtime treat them as built-ins, and `internal/processing` stays unreachable (`TestExtendImportBoundary`, `TestRootSurfaceNamesNoProcessing`). Names are `<CATEGORY>_<NAMESPACE>_<NAME>`; reserved namespaces and built-in collisions are rejected. **Recipe: `docs/src/internals/extension-points.md`; contract: `.claude/reference/architecture.md` (Extension surface).**

## Feature profiles

A **feature profile** (never bare "profile" — synth owns it) is an instance's closed feature allowlist, validated and enforced by `pulse.New` (`PULSE_FEATURE_PROFILE_INVALID` → `_UNKNOWN` → `_DEPENDENCY`): a hidden feature acts never-registered, and manifest, payload schema, errors and MCP show only the instance; every payload carries `feature_set_digest`. `pulse.New` never reads `PULSE_FEATURE_PROFILE`, and no runtime skill mentions profiles. **Contract: `.claude/reference/feature-profiles.md`.**

## Guided analysis

Declared, never executed: intents, per-operator `Purpose`, per-output `Interpretation`, a glossary and `details.effect_size.*` keys. **Prose is pulled, never pushed** (`TestManifestGuidanceBudget`). **Contract: `.claude/reference/guided-analysis.md`.**

## Request templating

Stored parameterised JSON that `internal/template/` renders into a **validated typed request** (import ceiling: `TestTemplatePackage_ImportBoundary`) — a library surface only, no CLI leaf, no MCP tool. **Not expr-lang:** `$var` / `{{}}` / `$when` substitute BEFORE decode. **Contract: `.claude/reference/request-templating.md`** — load it before changing the document model, the variable or target sets, or the substitution syntax; skill `skills/request-templating.md`.

## Synthetic data

**Contract: `.claude/reference/synthetic-data.md` — you MUST load it before changing any `synth/` or `internal/synth/` code, configuration or public surface. Every failure it records was SILENT** — the run succeeded and a number was quietly wrong. `format_version` does NOT move for a synth change (`synth/` types are not payload-reachable). Skills: `skills/synthetic-data.md`, `skills/synth-models.md`, `skills/synth-structural-rules.md`.

## Skill Pack

The pack under `internal/skills/` (addressed `skills/<stem>.md`, embedded via `//go:embed *.md`) is the LLM surface: **atomic** files whose stem encodes the surface — `op-<category>-<kebab>.md`, `tool-<kebab>.md` (strip `pulse_`), `type-<kebab>.md`; frontmatter `name:` = stem — and **topical** `kind: design` files. The filesystem IS the manifest: never create a `skills/index.json`, never hardcode a registered count. **Contract (required sections, budgets, stems, routing, add-a-skill): `.claude/reference/skill-pack.md`.**

## What NOT to Do

- **Do not import `internal/service/` or `internal/processing/` from `internal/descriptor/`.** Predict/inspect/manifest are no-execute; `TestPredictNoExecutionImports` fails.
- **Do not hand-edit golden files.** Regenerate: `go test ./descriptor/ -run 'Test.*Golden' -update`.
- **Do not defer a skill or CLAUDE.md update** to a follow-up PR; it will not happen.
- **Do not add implementation without tests in the same PR.** TDD is a hard rule here.
- **Do not use `fmt.Sprintf` for JSON/XML.** Use `encoding/json` + `descriptor.NewEnvelope(data)`.
- **Do not raise `claudeMdSizeCeiling` to make CLAUDE.md fit.** Move the long form into `.claude/reference/` and leave the always-load half plus a pointer — that is the whole point of the gate.
- **Do not add a component without updating the registry** (`internal/processing/registry.go`) + `types.All*Types()`.
- **Do not bypass `afero.Fs`** — it defeats `fs.NewMemMap()` and the custom-storage extension hook.
- **Do not put business logic in `cmd/pulse/`.** The CLI parses flags, constructs library objects, calls methods, formats output.
- **Do not bypass overlay typing via direct payload mutation.** Overlays are read-only siblings keyed to host coordinates — never mutate `Response.Data` / `Response.Crosstab.Matrix` in a handler; the fold writes `Response.Overlays[i]` only.

## Reference Docs

`.claude/reference/*.md` is where CLAUDE.md's long form lives. **This is the complete index.** CLAUDE.md carries only the always-load half of each contract — load the named file BEFORE the work, not after. Most are also required companions in the Update Demand table, which makes loading them binding.

| File | Holds | Load before... |
|---|---|---|
| `architecture.md` | package tree, facade narrowing, `io` boundary, surface guards, MCP layer split, extension surface | moving a package, adding a public package or exported symbol, or touching an import-boundary gate |
| `update-demand.md` | the per-slot trigger table, per-gate prose, the non-prefix-matched gates | touching ANY contract; add a row for a new Request slot, Response slot, capability block or execution-mode wiring |
| `byte-layout.md` | format version, parent groups, wide sets, shard archives, sidecars, SPSS, projected decode | changing the `.pulse` format, a field type, shard-archive layout, any sidecar file, an SPSS surface, or the projection contract |
| `execution-modes.md` | wiring prose per mode, every overlay host, multiplicity, time zones | engine work (`internal/service/`, `internal/processing/`) or adding an execution mode |
| `response-components.md` | auxiliary crosstab margins (ADMISSION rule), opt-out, Compose surface + envelope, undefined figures | changing `Response.Components`, a per-operator `ComponentSchema`, or `CrosstabSpec.MarginAggregations` on either arm |
| `predict-inspect.md` | inspect's envelope split, truncated-tail warning, `CountRecords`' silence, the predict import ban | changing `predict.go` / `inspect.go`, `Pulse.Inspect` / `InspectEnvelope`, `pulse_inspect`, or `CountRecords` |
| `request-templating.md` | file wrapper, substitution forms, variable types, precedence, hot-reload phases, `PULSE_TEMPLATE_*` codes | changing the request-template document model, the variable or target sets, or the substitution syntax |
| `synthetic-data.md` | the whole `synth/` contract | ANY `synth/` change — every failure it records was SILENT |
| `feature-profiles.md` | feature kinds, the feature table, `Since`, dependencies, the profile model, codes | adding any feature, or touching feature-profile code |
| `skill-pack.md` | frontmatter, required sections, budgets, stems, add-a-skill | adding or restructuring a skill file, frontmatter keys, a family's required sections, or a budget |
| `weighting.md` | the weight surface (null ≠ absent), resolution, weight classes, refusals, floor keys, op-order exactness | touching ANY weight surface |
| `guided-analysis.md` | intents, `Purpose`, `Interpretation`, glossary, virtual skills, effect-size keys, budget gates | touching any guidance registry, an effect-size key, an overlay `Inferential` flag or the extension `Purpose` hook |
| `matrix-and-vectors.md` | `linalg` backend policy, tolerance rules, `CoMoment`, the blocked merge, the matrix slot | touching `linalg/`, its callers or the blocked merge |

`TestClaudeMdSizeBudget` caps CLAUDE.md at 40,000 bytes so a new contract DISPLACES long form into that directory; never raise the ceiling. `.claude/reference/*.md` is inside `TestSkillsCoverAllCrossReferences`' corpus — a heading renamed in moved text still breaks the pointer that names it.
