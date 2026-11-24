# CLAUDE.md

Pulse is a self-describing tabular data processing engine: a Go library (`github.com/frankbardon/pulse`, public API in `pulse.go`) and a thin CLI (`cmd/pulse/`). Docs: <https://frankbardon.github.io/pulse/>.

## Design principles

- **Library-first.** Business logic lives behind the library facade. `cmd/pulse/` parses flags, builds library objects, calls them and formats output — nothing else.
- **Self-describing.** Every `.pulse` file carries its schema in its header. Predict, inspect and the manifest (`internal/descriptor/`) never execute a request and never read records.
- **Embedder-first, consumer-agnostic.** Library embedders are a first-class audience; they extend Pulse through `pulse.Options.Extensions`, and predict, manifest, MCP and runtime treat extensions exactly like built-ins. Pulse never imports, names or special-cases a consumer: every public symbol is justified by a general embedder use case.
- **Agents discover Pulse** through `pulse manifest --json` and the embedded skill pack (`internal/skills/`, addressed `skills/<stem>.md`). The filesystem walk is the skill index — never create a `skills/index.json` or hardcode a registered count.

## The Update Demand

A change to Pulse code, configuration, file format or public surface ships with its companions in the same PR: the skill file(s), capability declarations, docs and regenerated goldens that describe it, plus its tests. A companion deferred to a follow-up PR does not get written.

To find the companions for a change:

1. Run `make sites ARGS="op <TYPE>"`, `"new-op <category>"` or `"slot <request|response> <name>"`. It prints the file checklist, the gates to rerun and the goldens to regenerate.
2. Load `.claude/reference/update-demand.md` (an index) and only the slice your change triggers.
3. For the areas in "Reference Docs" below, load the named file before you start — several of them record failures that were silent.

The gates enforce most companions; a failing gate names what is missing. `make docs` regenerates `docs/src/guide` — never hand-edit it.

**CLAUDE.md is not a companion.** It holds only what every session needs. Edit it when one of those rules changes; put everything else in the matching `.claude/reference/` file, skill or book page. Where an older doc says to update a CLAUDE.md section, update the reference file that section points at.

## Architecture

**Public packages** (frozen at v1.0.0 by `TestPublicAPIGolden`): root `pulse`, `types`, `errors`, `encoding`, `descriptor`, `io` and `synth` (alias facades over `internal/`), `mcp/gosdk`, `mcpserve`, `extend` (operator-authoring API), `linalg`, `observe` (+ `/otel`, `/prom`), `optimize`, `mcmc`. Everything else is under `internal/`: the engine in `internal/processing`, orchestration in `internal/service`, the no-execute descriptors in `internal/descriptor`, format adapters in `internal/io/<fmt>`. Load `.claude/reference/architecture.md` before moving a package or adding a public symbol.

**MCP:** `internal/mcp/` is SDK-free; `mcp/gosdk/` is the only go-sdk importer. Tools, prompts and resources register per instance (a feature-profiled instance mounts only what it enables). The manifest is the source of truth for the tool count — never hardcode it.

## Code conventions

- Pulse-native names only; no predecessor references (`TestNoOrbit*`). The public `io` package is imported as `pio`.
- Component types are SCREAMING_SNAKE (`AGG_COUNT`, `WIN_LAG`, `TEST_T`). Extension names are `<CATEGORY>_<NAMESPACE>_<NAME>`.
- Error codes are `DOMAIN_CATEGORY` across six domains (`CLI`, `DATA`, `ENCODING`, `PROCESSING`, `PULSE`, `SERVICE`), listed in `errors/codes.go`. Every code needs a `codeMetadata` entry in `errors/fixup_metadata.go` (Message + at least one Fixup, or `FixupNotApplicable`) and an owner in `internal/descriptor/error_owners.go`.
- JSON is built with `encoding/json`, never `fmt.Sprintf`; XML with `encoding/xml`. Every `--json` output goes through `descriptor.NewEnvelope`.
- Filesystem access goes through `afero.Fs` (tests use `fs.NewMemMap()`), never `os` directly.
- A slot that names a field but omits `Type` gets a smart-default operator from `internal/descriptor/defaults.go`; zone math lives only in `internal/temporal`.

## Byte-layout invariants

The `.pulse` binary format. These fail silently when wrong; load `.claude/reference/byte-layout.md` before touching the format, a field type, a sidecar or the shard layout.

1. **Header (9 bytes):** magic `PULSE\x00\x00\x00` + a version byte in `{0x01, 0x02}`; anything else is `ENCODING_INVALID`. The version is a function of schema content, never a flag: `0x02` iff the schema declares a parent group, a vector or a default zone field. `ReadHeader` returns the version and `ReadSchema` must be handed it.
2. **Schema block:** per field — name, type byte, nullable flag, byte offset, bit position, optional description. Under `0x02` a length-prefixed block of tagged extension sections follows; an unknown required section is refused.
3. **Dictionaries** for every categorical / set field, then **fixed-width rows**, then an optional per-record **null bitmap** (field `i` → byte `i/8`, bit `i%8`, `1` = null) when any field is nullable.
4. **Field types** (22; table in `skills/cohort-schema-design.md`): `date` is epoch DAYS (`int32`) and `datetime` epoch SECONDS (`int64`) — never interchangeable. A `set_*` value is a bitmask whose empty mask means "no selection", distinct from null; `set_u128` / `set_u256` are refused by the `uint64` value API, never truncated. Size `vec_*` fields with `Schema.FieldWidth`, never from the type.
5. **Shard archive:** a `.pulse` path may instead be an uncompressed Zip64 archive (magic `PK\x03\x04`) holding a reserved `_schema.pulse` plus up to 65,535 shards. Set fields widen across shards; nullability and the zone default never do. Every rewrite is atomic and warns.

## Output Format Contract

- Every `--json` output and descriptor operation returns `{format_version, data, request, errors, warnings}`. `format_version` is `"1.1"` and is additive-only: new `data` fields do not bump it; renames and removals do.
- `errors` / `warnings` are arrays of `{code, message, details}`, empty (never null) when absent. A fatal `*errors.CodedError` reports its own code. Exit status follows the envelope: errors → 1, otherwise 0, usage → 2.
- A non-finite float is JSON `null` with its key kept; read back, `null` decodes to NaN, never 0.
- A regression / test / matrix slot that fails on its data keeps its identity and `error`, drops its figures, and warns `PULSE_SLOT_FAILED`; config faults stay fatal.
- `Response.Components` (optional) carries the counts behind each figure; disabling it leaves output byte-identical. Long form: `.claude/reference/response-components.md`.
- The payload JSON Schema (`pulse schema`) and the manifest (`pulse manifest --json`) are deterministic and pinned by goldens under `descriptor/testdata/`.

## Testing

- Tests land in the same commit as the code they cover. For each key new test, revert one production line and confirm it fails.
- Never hand-edit a golden: regenerate with `-update`, then rerun without it. Use `go test -count=1` — a cached pass can hide a stale golden.
- A test taking 1 s or more goes in a `*_heavy_test.go` file tagged `//go:build heavy`.
- Results must be bit-identical across worker counts and architectures. Kernels are FMA-free (`float64(a*b)`); never pin gonum-decomposition bits in a fixture.

## Build / Env

`make build`, `test` (fast tier), `test-heavy` (both tiers), `smoke`, `lint`, `cover`, `docs`, `sites`. Run `make lint` (with `GOTOOLCHAIN=go1.26.1`) before every push. CI runs by hand once on the finished branch, before merge (`docs/src/contributing/local-ci.md`).

`PULSE_DATA_DIR` is the one required environment variable; `pulse.Options` always overrides the environment. Every `PULSE_*` variable is documented in `docs/src/library/options.md`.

## What NOT to Do

- Do not import `internal/service/` or `internal/processing/` from `internal/descriptor/` (`TestPredictNoExecutionImports`).
- Do not put business logic in `cmd/pulse/`.
- Do not add a component without registering it in `internal/processing/registry.go` and `types.All*Types()`.
- Do not mutate `Response.Data` or the crosstab matrix from an overlay; overlays write only `Response.Overlays[i]`.
- Do not raise a size ceiling to make a file fit (`TestClaudeMdSizeBudget` and the reference-doc ceilings); move the prose instead.

## Reference Docs

Long-form contracts live in `.claude/reference/`. Load the named file BEFORE the work. A file with a same-named subdirectory is an index: load it, then only the slice(s) your change triggers (never `history.md`).

| File | Load before... |
|---|---|
| `agent-practices.md` | any delegated or agent work |
| `architecture.md` | moving a package, adding a public package or exported symbol, touching an import-boundary gate |
| `update-demand.md` | touching any contract; adding a Request slot, Response slot, capability block or execution mode |
| `byte-layout.md` | the `.pulse` format, a field type, shard archives, a sidecar, SPSS, projected decode |
| `execution-modes.md` | engine work in `internal/service/` or `internal/processing/` |
| `response-components.md` | `Response.Components`, a `ComponentSchema`, crosstab margin aggregations |
| `predict-inspect.md` | predict, inspect, `pulse_inspect`, `CountRecords` |
| `request-templating.md` | the request-template model, variables or substitution syntax |
| `synthetic-data.md` | any `synth/` or `internal/synth/` change |
| `feature-profiles.md` | adding any feature, or feature-profile code |
| `skill-pack.md` | adding or restructuring a skill file, its frontmatter, sections or budget |
| `weighting.md` | any weight surface |
| `guided-analysis.md` | intents, `Purpose`, `Interpretation`, glossary, effect-size keys |
| `matrix-and-vectors.md` | `linalg/`, matrix operators, vectors |
| `observability.md` | the `observe` package, metrics, log attributes, hooks, OTLP |

Contributor recipes (one `adding-*.md` per extension point) are in `docs/src/internals/`.
