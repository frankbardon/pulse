# Architecture — package layout and the MCP layer split

CLAUDE.md keeps only the always-load half of the architecture (the public package list and a pointer here). This file is the long form: where every package lives, which ones are public, how the narrowed "noun" packages are built, and how the MCP layer splits. **Load it before moving a package, adding a public package or symbol, or touching any import-boundary gate.**

## Public vs internal

Under Go module semantics every non-`internal/` package is public API, and `v1.0.0` freezes every exported identifier in it. The classification is decided in `docs/roadmap/v1.0.0-api-and-release/00-public-surface.md` (Decisions); the migration from the earlier layout is `docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md`.

```
PUBLIC (frozen at v1.0.0 — TestPublicAPIGolden guards every exported shape)
pulse (root)        the facade: New, Options, Pulse + methods, root aliases, extension registration
types/              wire payload — Go AND JSON names frozen
errors/             CodedError, every code constant, Lookup/ByDomain/Search, codeMetadata (left whole)
encoding/           schema nouns + ungrouped (0x01) raw-byte primitives; RecordLocator = geometry only
descriptor/         result + envelope types only (Envelope, Manifest, PredictResult, InspectResult, …)
io/                 alias facade: jobs, reports, Reader/Writer interfaces, Format + factory
synth/              alias facade: Spec/Profile/Options/Result + Synth, SynthBytes, ProfileFile, …
mcp/gosdk/          the ONLY go-sdk importer: Register, Config, URI/prompt constants
mcpserve/           Serve, ServeStdio, Options
extend/             extension-authoring contract: Record, Rows, operator factories + instance interfaces (leaf; TestExtendImportBoundary)

INTERNAL
cmd/pulse/                 the only binary; buildApp() defines the CLI leaf tree
internal/cli/              flags, formatting, envelopes — no business logic
internal/service/          orchestration (Process, Compose, shards, indexes, facets, …)
internal/descriptor/       manifest/predict/inspect/schema builders + capabilities_*.go — NO-EXECUTE
internal/encoding/         codec remainder: archive, groups, decode plans, index + index manifest, widen
internal/encodingbridge/   init-installed hooks from public encoding to its internal twin
internal/iocore/           the Reader/Writer + optional-interface contracts public io aliases
internal/io/               jobs, inference, transfer; internal/io/<fmt>/ adapters
                           (csv|tsv|ndjson|jsonarray|jsonshared|arrow|parquet|excel|spss)
                           + helpers exportoverlay, nullcell, settristate, setwide
internal/synth/            generator, profile capture, structural rules, fidelity
internal/template/         request templating (import ceiling: stdlib + types + errors)
internal/mcp/              SDK-free MCP core; internal/mcp/toolmeta/ leaf metadata
internal/skills/           embedded skill pack (//go:embed *.md)
internal/examples/         embedded runnable requests
internal/fs/               afero config (fs.Default, fs.NewMemMap)
internal/imports/          managed-imports manager (TTL, sidecars)
internal/daterange/        compiled {label,start,end} model for the date-range operators
internal/spsssidecar/      SPSS sidecar path helpers used by root sidecar_*.go
internal/facadebridge/     init-installed hooks from the root to mcp/gosdk (replaces Service())
internal/processing/       operator engine: operators, crosstab, joins, registry, ExtensionRegistry
internal/processing/{feature,window}/     FEAT_* pre-filter engineers, WIN_* operators
internal/processing/{regression,arena}/   REG_* engine, arena allocator
internal/buildinfo/        VERSION injected by ldflags, read by pulse.Version()
internal/apigolden/        TestPublicAPIGolden + testdata/public_api.txt
internal/embeddersmoke/    nested module (own go.mod) compiled by `make smoke` in CI
internal/shardfixtures/, internal/spsstest/, internal/tools/pkgsplit/   test + tooling support
docs/                      mdBook source (docs/book/ is generated)
```

The pack is still ADDRESSED pack-relative in prose — `skills/<stem>.md` — and resolved under `internal/skills/` by `TestSkillsCoverAllCrossReferences`; the examples are addressed as `examples/<dir>/*.json` the same way.

## How the narrowed packages are built

Three techniques, chosen per package:

- **Split in place** (`encoding`, `descriptor`). The kept subset stays at the public path; the remainder moved to an `internal/<pkg>` twin with the same package name, so `encoding.X` / `descriptor.X` spellings read the same in prose either way. Methods on kept types that only the remainder needs became functions in the twin (`BuildDecodePlan`, `DecodeGroupEntry`, `NewGroupEntryDecoder`, `GroupSpecOf`, `ReadRecordAt`). The public `encoding` reaches its twin's needs through `internal/encodingbridge` hooks installed at init. `internal/tools/pkgsplit` is the AST rewriter that performed the split.
- **Alias facade** (`io`, `synth`). The whole implementation lives in `internal/io` (+ `internal/iocore`) and `internal/synth`; the public package holds only `type X = internal.X` aliases, re-declared constants and thin function wrappers for the kept subset. An alias freezes its target's fields and methods exactly as if the target were public — which is why `TestPublicAPIGolden` expands every alias.
- **Left whole** (`errors`, `mcpserve`) or **unexported in place** (`mcp/gosdk`).

**Reading package-qualified names.** Because each internal twin keeps its public package's NAME, contributor prose spelling `encoding.X`, `descriptor.X`, `synth.X` or `io.X` may mean the internal twin (`synth.AllDistributions`, `descriptor.BuildManifest`, `encoding.SidecarIndexPath` are all internal). Embedder-facing prose (`docs/src/library/`, README, the skill pack) names only spellings an external module can import; `internal/apigolden/testdata/public_api.txt` is the arbiter.

**Root aliases.** Facade-returned types from internal packages keep their `pulse.X` spelling as aliases (`ComposeOptions`, `Row`, `RowIter`, the shard and index result types, `ImportSpec` / `ImportResult` / `ImportEntry` / `ImportSidecar`, `Example` / `ExampleSummary`, `Template*` / `RenderedTemplate`, `CohesionWarning`, `GroupIndexHeadroom`, `SidecarIndex`, …). `DateRangeSpec`, `MemberSet` and `LoadMemberSetResult` are root-NATIVE types, not aliases. `pulse.go` also re-exports `types.Request` / `Response` / `ComposedRequest` and `synth.Spec` / `Result` / `Options` / `Profile` / `ProfileOptions`.

**The `io` import boundary.** Nothing under `internal/io/**` or `internal/iocore` may import the public `io` — its factory (`io.NewReader`, `NewReaderFromBytes`, `NewWriter`, `NewWriterToBuffer`, typed `io.Format` constants, `FormatFromPath`) imports every adapter, so the reverse edge is a cycle. Adapters import `internal/iocore` for contracts and `internal/io` for jobs. Gated by `TestIOImportBoundary*` (`internal/iocore/boundary_test.go`); when an adapter moves, move its path in `belowFacade` with it.

**Surface guards.** `TestPublicAPIGolden` (blocking) freezes every public package's exported shape, aliases expanded; regenerate with `go test ./internal/apigolden/ -run TestPublicAPIGolden -update` only for an intentional surface change and review the diff. The `apidiff` job in `.github/workflows/api-compat.yml` is advisory against the latest tag (label `api-break-ok` marks an intentional break) and flips to blocking once a stable `v1.0.0` tag exists. `make smoke` builds `internal/embeddersmoke`, an external module that only uses public spellings. Contributor prose: `docs/src/contributing/pr-process.md`.

## CLI

CLI commands map 1:1 to manifest commands — the list is in CLAUDE.md "Architecture". `pulse schema` prints the payload JSON Schema RAW, not envelope-wrapped.

## MCP layer split

`internal/mcp/` is the SDK-free core (typed In/Out structs, reflected JSON schemas, typed handlers over `*pulse.Pulse`, strict-decode, bind classification — gated by `TestMCPCore_NoSDKImport`). `mcp/gosdk/` is the ONLY package importing the go-sdk; its `Register(server, p, cfg)` mounts the core catalog onto a caller-supplied server, and `pulse mcp` builds a bare server and calls it. `mcp/gosdk` reaches the instance's extension snapshot through `internal/facadebridge`, never through an exported engine handle. `internal/mcp/toolmeta/` holds the leaf metadata both `internal/descriptor` and the core import. One tool per facade method plus skills/examples/errors/import/label tools — **the manifest is the source-of-truth count, never hardcode it** — and two resource schemes (`pulse://`, `pulse-skill://`); `pulse://schema` serves the payload JSON Schema as a RESOURCE, not a tool. Cohort resources are ENUMERATED by a startup walk of the data root, suppressible with `gosdk.Config.DisableCohortScan` / `mcpserve.Options.DisableCohortScan` / `pulse mcp --no-cohort-scan` — the `pulse://` template stays mounted, so a disabled scan costs enumeration only, never readability. Payload tools take the structured request at top level, outputs are typed-wrapped, coded errors surface as `{code, message, details}`.

## Extension surface

Embedders author operators against the public `extend` package and register them through `pulse.Options.Extensions` (root `extensions*.go`). `extend` is a leaf: it imports only `encoding`, `types` and `errors` (`TestExtendImportBoundary`, `TestExtendImportBoundary_NoTransitiveEngine`), and no public root signature names an `internal/processing` type (`TestRootSurfaceNamesNoProcessing`). At `pulse.New`, `extensions_adapt.go` lifts each `extend` operator onto the engine interfaces, forwarding every optional sibling explicitly (a streamable operator that also carries `ComponentsFunc` still streams). Engine-only capabilities — `MetaWindow`, `ExtensionAware`, `KeyFor`, the merge hooks — are deliberately absent from `extend` (`TestExtendOmitsEngineOnlyCapabilities`).

- **Snapshot.** `pulse.New` builds the read-only `internal/descriptor.ExtensionsSnapshot` and passes it into `PredictOptions.Extensions` and `mcp.BindWithExtensions` (`mcp/gosdk` reaches it via `internal/facadebridge`), keeping `internal/descriptor/` free of `internal/service/` and `internal/processing/`. It carries `OperatorMeta.FansOut` (manifest `fans_out`, grouper-only); runtime twin is the engine's extension-registry fan-out map. Both feed `types.CheckPairwiseSlabPartitionWith`, which owns the ONE order: built-in first, then the extension side, and a name known to NEITHER passes (it cannot execute, so it can produce no wrong number).
- **Lazy table resolution.** A grouper factory's `(grp, schema)` signature cannot reach the registry, so a named `table:` resolves through the engine-internal `ExtensionAware` `SetExtensions` hook, threaded by `ApplyGrouperExtensions` through every construction site. Inline sources resolve eagerly.
- **Streamability follows the declaration.** Extension aggregators, groupers, attributes and row tests route on the DECLARED `Streamable` flag (runtime, predict and the snapshot agree: `TestExtensions_StreamabilityFollowsDeclaration`); `Streamable:false` runs buffered even when the value is online-capable. A grouper declaring `Streamable:true` without a keying sibling is `PULSE_EXTENSION_STREAMABLE_MISMATCH`. Feature streamability is decided by the returned value and is not probed. A built-in ZSCORE + `GROUP_CATEGORY` request predicts `Streamable=false`, matching runtime.
- **Components emission.** `ComponentsFunc` is probe-validated; an operator's own `Components()` method still emits when none is registered, for every emitting category (intentional deviation, NOT probe-validated).
- **Limits.** Extension operators are never mergeable (parallel shard / decode run them serially); grouped Components lack extension per-operator figures; single-key extension groupers take fused crosstab through an adapter-synthesized `KeyFor` (still omitted from `extend`); a `two_pass` extension attribute keeps a crosstab buffered, as the built-in two-pass set does. Extension aggregators are admitted on `decimal128` targets (they read `DecimalValue`) and stream there per their declared `Streamable` flag — `UpdateRow` sees decimal fields via `DecimalValue`; built-ins over decimal stay buffered, and every decimal target runs serial.
- **FieldInputs.** The projection extractor (`NeededFields`, internal) reads each registration's optional `FieldInputs`; absent widens the retained set to `*`.
