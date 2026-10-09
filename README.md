# Pulse

High-performance, self-describing tabular data processing engine written in Go. Ships as a Go library (`github.com/frankbardon/pulse`) and a CLI binary (`cmd/pulse/`). Library is primary; CLI is a thin adapter.

Pulse reads and writes `.pulse` files — a compact binary format with an inline schema, categorical dictionaries, and per-field descriptions. Import from CSV, TSV, NDJSON, JSON array, Parquet, Apache Arrow IPC, or Excel; run aggregations, filters, groupers, windows, features, statistical tests, and regressions; export back to any supported format.

LLM-native by design: every command supports `--json`, every operator declares its accepted types and streamability in the manifest, an embedded skill pack teaches agents how to operate Pulse, an embedded request-example library gives them runnable templates, and `pulse mcp` serves the full surface over Model Context Protocol.

## Installation

### From source

```bash
git clone https://github.com/frankbardon/pulse.git
cd pulse
make build
# Binary at ./bin/pulse
```

### Go install

```bash
go install github.com/frankbardon/pulse/cmd/pulse@latest
```

Requires Go 1.22+.

## Quickstart

### Import a CSV into a .pulse file

```bash
pulse import csv --input data.csv --output data.pulse
```

Schema is inferred from a sample of rows (default 500). To supply an explicit schema:

```bash
# Generate a schema template from your data
pulse import schema-template data.csv > schema.json

# Edit schema.json — adjust types, add descriptions
pulse import csv --input data.csv --schema schema.json --output data.pulse
```

### Inspect the file

```bash
pulse cohort inspect data.pulse --json
```

Returns field names, types, byte offsets, descriptions, and categorical dictionaries (truncated by default; pass `--full-dict` for the full mapping).

### Run an aggregation

Create a request file (`request.json`):

```json
{
  "cohort": {"filename": "data.pulse"},
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "total"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "avg_score"}
  ]
}
```

```bash
pulse api process --request request.json --json
```

### Filter, group, and aggregate

```json
{
  "cohort": {"filename": "data.pulse"},
  "filterers": [
    {"type": "FILTER_RANGE", "field": "score", "values": ["80", "100"]}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "brand"}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "count"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "avg_score"},
    {"type": "AGG_STDDEV", "field": "score", "label": "stddev"}
  ]
}
```

### Smart defaults

If you name a field but omit `type`, Pulse fills in a sensible operator from the schema type — `AGG_SUM` for numerics, `AGG_MODE_COUNT` for categoricals, `GROUP_RANGE` (interval 10) for numerics, `GROUP_CATEGORY` for categoricals, `GROUP_DATE` ("day") for dates. Disable with `--no-defaults` or `pulse.Options{DisableDefaults: true}`. The full rule table lives in `internal/descriptor/defaults.go`.

### Validate before executing

```bash
pulse api predict --request request.json --json
```

Returns the proposed schema, applied defaults, streamability, warnings (e.g., numeric aggregation on a categorical field), and structural errors without touching record data. Pass `--strict` to treat warnings as errors.

### Streaming

For requests Pulse can stream (single-pass aggregations on non-decimal fields, no buffered ops), use `--stream` to get NDJSON one row per line:

```bash
pulse api process --request request.json --stream
pulse api compose --request batch.json --stream
```

Library equivalent: `pulse.ProcessStream(ctx, req)` returns a `RowIter`.

### Parallel compose

```bash
pulse api compose --request batch.json --parallel 4 --json
```

Library equivalent: `pulse.ComposeParallel(ctx, req, pulse.ComposeOptions{MaxWorkers: 4})`. Order-preserving by slot; `--no-fail-fast` aggregates errors instead of cancelling on first failure.

### Export and convert

```bash
pulse export csv --input data.pulse --output results.csv
pulse export parquet --input data.pulse --output results.parquet

pulse convert data.csv data.parquet
pulse convert data.xlsx output.tsv --schema schema.json
```

Format auto-detected from extensions. `convert` does not write an intermediate `.pulse` unless `--keep-pulse=path`.

### Sample rows / facet a field

```bash
pulse api sample --input data.pulse --count 10
pulse api facet --input data.pulse --field brand
```

### Synthetic data

Generate a deterministic synthetic cohort from a schema spec or from a previously-captured profile:

```bash
pulse synth from-schema --spec spec.json --output synth.pulse --seed 42
pulse profile create --input real.pulse --output profile.json --include-correlations
pulse synth from-profile --profile profile.json --output synth.pulse --rows 100000 --seed 42
```

Distributions such as `normal`, `lognormal`, `poisson`, `bernoulli`, `weighted_categorical`, `regex`, `uniform_date` and `monotonic_from` (the manifest's `synth_distributions` block has the full list), pairwise correlations, value constraints. See the `synthetic-data` skill.

## CLI Reference

```
pulse
├── --json [--slim]                       Root manifest (self-description)
├── import
│   ├── csv|tsv|ndjson|jsonarray|         --input FILE --output FILE [--schema FILE]
│   │   parquet|arrow|excel
│   ├── auto SOURCE                       Managed import (auto-detect format)
│   ├── list                              List managed import handles
│   ├── drop HANDLE                       Drop a managed handle
│   ├── predict                           --input FILE [--schema FILE] --json
│   └── schema-template INPUT             Generate editable schema from source
├── export
│   ├── csv|tsv|ndjson|jsonarray|         --input FILE --output FILE
│   │   parquet|arrow|excel
│   └── predict                           --input FILE --format FORMAT --json
├── convert INPUT OUTPUT [--from F] [--to F] [--schema FILE] [--keep-pulse PATH]
│   └── predict INPUT OUTPUT --json
├── cohort
│   ├── inspect PATH [--json] [--full-dict]
│   └── filter --input FILE --output FILE --filter EXPR
├── api
│   ├── process --request FILE [--json] [--stream] [--no-defaults]
│   ├── compose --request FILE [--json] [--stream] [--parallel N] [--no-fail-fast]
│   ├── sample --input FILE --count N
│   ├── facet --input FILE --field NAME
│   └── predict --request FILE --json [--strict]
├── synth
│   ├── from-schema --spec FILE --output FILE [--rows N] [--seed N]
│   └── from-profile --profile FILE --output FILE --rows N [--seed N]
├── profile
│   └── create --input FILE --output FILE [--top-k N] [--include-correlations]
├── skills
│   ├── list [--json]
│   └── show NAME
├── examples
│   ├── search [--query Q] [--tag T ...] [--category C] [--json]
│   └── show NAME [--json]
├── errors
│   ├── lookup CODE [--json]
│   └── list [--domain D] [--query Q] [--json]
└── mcp [--data-dir DIR] [--bind-on-open]   Serve MCP over stdio
```

Every leaf supports `--json` for output wrapped in a `descriptor.Envelope` with `format_version: "1.1"`, `data`, `errors`, and `warnings`. `pulse api compose --json` wraps a `ComposedResponse` `{responses[], overlays[]}` on `data` — not the legacy `[]*Response` array.

## MCP Usage

Pulse ships an MCP (Model Context Protocol) server that exposes the full library surface to AI clients (Claude Desktop, Claude Code, Cursor, any MCP-aware host). One binary, one command:

```bash
pulse mcp --data-dir /path/to/data
```

`PULSE_DATA_DIR` is honored if `--data-dir` is omitted. The server speaks JSON-RPC over stdio (logs to stderr).

### Wiring into a host

For Claude Desktop, add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "pulse": {
      "command": "pulse",
      "args": ["mcp"],
      "env": {
        "PULSE_DATA_DIR": "/Users/me/data"
      }
    }
  }
}
```

For Claude Code:

```bash
claude mcp add pulse --env PULSE_DATA_DIR=/Users/me/data -- pulse mcp
```

Restart the host. Pulse tools appear in the tool list.

### Tool surface

| Tool | Purpose |
|---|---|
| `pulse_manifest` | **Call first.** Self-description: commands, operators, accepted types, tests, regressions, distributions, error codes, MCP tools, cohort field types. Cache once per session. |
| `pulse_inspect` | Read header + schema (no record bytes). Also binds session-scoped field-name enums on action tools. |
| `pulse_predict` | Validate a request against the schema without executing. |
| `pulse_process` | Execute one pre-built request. |
| `pulse_compose` | Execute a batch of requests in one round trip. |
| `pulse_sample` | Return up to N rows for preview. |
| `pulse_facet` | Distinct values for a single field. |
| `pulse_import` | Import a tabular source into a managed `.pulse` handle (TTL-tracked, default 7d). |
| `pulse_dedup` | Deduplicate an existing cohort into parent groups, or suggest groups (read-only). |
| `pulse_recommend` | Turn a question kind (intent ID) into ranked draft requests, predict-validated when bound to a cohort. |
| `pulse_explain` | Say in plain words what a request will do, or what a result found (verdicts, strength bands, caveats). |
| `pulse_drop` | Drop a managed handle. |
| `pulse_imports_list` | Enumerate managed handles with sidecar metadata. |
| `pulse_examples_search` | Search the embedded request-example library by query, tags, category. |
| `pulse_examples_get` | Fetch one runnable example by name. |
| `pulse_errors_lookup` | Per-code Message + Fixup detail (kept out of the manifest for context economy). |
| `pulse_skills_list` / `pulse_skills_get` | Embedded skill pack. |

### Resources and prompts

| URI | What |
|---|---|
| `pulse://<path>` | One per `.pulse` file under the data directory. Read returns `descriptor.InspectResult` JSON. |
| `pulse-skill://<name>` | One per embedded skill. Read returns the markdown body. |

Prompts `pulse-bootstrap` and `pulse-author-request`, plus one guided `pulse-<intent>` prompt per analytic intent (inspect → recommend → confirm → process → explain), are registered for hosts that surface them as slash commands.

### Recommended session flow

1. `pulse_manifest` once at session start. Cache the result — it is deterministic for a binary version and carries every fact needed to author a valid request.
2. `pulse_import` when the user hands the LLM a raw tabular file; skip when the cohort already exists as a managed handle or `.pulse` under `PULSE_DATA_DIR`.
3. `pulse_inspect` on the handle (or path). Reads header + schema only and binds session-scoped field-name enums on the action tools.
4. `pulse_predict` with the authored request. Validates against the schema; each error code carries Fixup metadata so the LLM can repair the request without another round trip.
5. `pulse_process` to execute. Use `pulse_compose` for batching, `pulse_sample` / `pulse_facet` for cheap probes.

### Schema-bound enums

After a successful `pulse_inspect`, the server registers session-scoped variants of the action tools whose JSON Schemas embed enums on field-name parameters — picked from a typed list rather than free-texted. Works on SSE / Streamable HTTP transports; on stdio the session does not support tool overrides, so enums are advisory and `pulse_predict` remains the validation gate. Disable with `--bind-on-open=false`.

The `session-bootstrap` skill (`pulse skills show session-bootstrap`) and the `tool-*` skills are the authoritative reference.

## Embedding Pulse in a Go Application

Pulse is library-first. The CLI is a thin adapter over the public API.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/frankbardon/pulse"
    pio "github.com/frankbardon/pulse/io"
    "github.com/frankbardon/pulse/types"
)

func main() {
    ctx := context.Background()

    p, err := pulse.New(pulse.Options{DataDir: "/var/data"})
    if err != nil {
        log.Fatal(err)
    }

    // Import a CSV. The io factory picks the adapter from a typed format.
    src, err := pio.NewReader(pio.FormatCSV, p.Fs(), "input.csv", pio.ReaderOptions{})
    if err != nil {
        log.Fatal(err)
    }
    importJob := pio.NewImportJob(src, "dataset.pulse")
    report, err := p.Import(ctx, importJob)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Imported %d rows\n", report.RowsImported)

    // Run an aggregation.
    resp, err := p.Process(ctx, &pulse.Request{
        Cohort: &types.Cohort{Filename: "dataset.pulse"},
        Aggregations: []*types.Aggregation{
            {Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(resp.Data)

    // Inspect a file.
    result, err := p.Inspect(ctx, "dataset.pulse")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Fields: %d\n", result.FieldCount)

    // Validate before execution.
    prediction, err := p.Predict(ctx, &pulse.Request{
        Cohort: &types.Cohort{Filename: "dataset.pulse"},
        Aggregations: []*types.Aggregation{
            {Type: types.AGG_SUM, Field: "revenue"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Warnings: %v\n", prediction.Warnings)
}
```

### Public facade

`pulse.Pulse` exposes: `New`, `Open`, `Process`, `ProcessStream`, `Compose`, `ComposeParallel`, `Import`, `ImportFile`, `Drop`, `Imports`, `SweepImports`, `ResolveImport`, `Export`, `Convert`, `Inspect`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`, `Manifest`, plus example/error lookup helpers.

### Custom filesystem

Pulse accepts any `afero.Fs` for testing or non-local backends:

```go
import "github.com/spf13/afero"

// In-memory filesystem for testing
p, _ := pulse.New(pulse.Options{
    FS: afero.NewMemMapFs(),
})
```

`afero.NewMemMapFs()` gives hermetic tests a complete in-memory filesystem — no disk I/O.

### Operator catalogue

The manifest is the source of truth for what is registered — `pulse manifest --json` (or the `pulse_manifest` MCP tool) lists every operator with its params and capabilities. This README keeps categories and representative members only, never counts:

- **Aggregators** (`AGG_*`): COUNT, SUM, AVERAGE, MIN, MAX, MEDIAN, STDDEV, PERCENTILE, FREQUENCY, DISTINCT_COUNT, WEIGHTED_MEAN, RATIO, set aggregators (SET_FREQUENCY, SET_UNION, …), …
- **Attributes** (`ATTR_*`): ZSCORE, TSCORE, NORMALIZED, FORMULA, PERCENTILE, DATE_PART, regression diagnostics (REG_FITTED, REG_RESIDUAL, …), set attributes, …
- **Filterers** (`FILTER_*`): INCLUDE, EXCLUDE, RANGE, EXPRESSION, NULL, DATE_RANGES, set-membership filters (SET_CONTAINS_ANY, …), …
- **Groupers** (`GROUP_*`): CATEGORY, RANGE, ROUNDED, DATE, DATE_RANGES, QUANTILE, set groupers, …
- **Windows** (`WIN_*`): LAG, LEAD, ROW_NUMBER, RANK, RUNNING_SUM, MOVING_AVG, EWMA, PCT_CHANGE, …
- **Features** (`FEAT_*`): LOG, SQRT, BUCKETIZE, ONE_HOT, TARGET_ENCODE, DATE_FEATURES, TRAIN_TEST_SPLIT, POLY, …
- **Statistical tests** (`TEST_*`): tier-1 row tests (T, WELCH, CHISQ, ANOVA_F, KS, PEARSON_R, MANN_WHITNEY_U, FISHER_EXACT, SHAPIRO_WILK, …) and tier-2 post-tests (TUKEY_HSD, TREND, summary-statistic variants)
- **Regressions** (`REG_*`): OLS, GLM, BAYES_LINEAR — with `Resample` / `Selection` modifiers and `FEAT_POLY` composition covering the common textbook regression variants
- **Synth distributions**: listed under `synth_distributions` in the manifest

## LLM Skill Pack

Pulse bundles a skill pack that teaches LLM agents how to operate it. Skills are embedded via `//go:embed` — no external files. `pulse skills list` (or the manifest's `skills` block) is the authoritative listing; this README does not keep a count.

### Discovering skills

```bash
pulse skills list
pulse skills list --json
pulse skills show session-bootstrap
```

### Bundled skills

The pack has two shapes. **Atomic** skills cover one registered surface each, and their name says which: `op-<category>-<kebab>` per operator (`op-agg-sum`, `op-group-date`, `op-test-t`, …), `tool-<kebab>` per MCP tool (`tool-process`, `tool-predict`, …) and `type-<kebab>` per field type (`type-categorical-u8`, `type-set-u256`, …). **Topical** skills cover cross-cutting design:

| Skill | Purpose |
|---|---|
| `session-bootstrap` | Canonical MCP session order — start here |
| `request-envelope` | Request shapes, slot keys, smart defaults, streamability |
| `cohort-schema-design` | Field-type selection, nullability, shard archives |
| `aggregation-design` | Aggregator + filterer slot semantics |
| `attribute-composition` | `ATTR_*` slot ordering and the formula environment |
| `grouper-design` | Multi-grouper composition, smart defaults per type |
| `window-design` | `WIN_*` partition / order / frame semantics |
| `feature-engineering` | Pre-filter `FEAT_*` ordering and the leakage trap |
| `statistical-testing` | Tier-1 tests vs tier-2 post-tests, assumption gates |
| `regression-modeling` | `REG_*` operators, modifiers, textbook-name mapping |
| `crosstab-guide` | Rows × columns grids, margins, normalization, fused vs buffered |
| `compose-requests` | Batched requests against one cohort, parallel Compose |
| `process-chain` | Source-rooted linear pipelines |
| `join-design` | Pushdown hash joins |
| `facet-design` | Per-field summaries and facet overlays |
| `overlay-system` | Overlay composition and host wiring |
| `pairwise-n-sources` | Sample-size vocabulary for pairwise overlays |
| `response-components` | `Response.Components` — the parts behind every figure |
| `label-display` | Display labels at output time |
| `streaming-and-watching` | Request hashing, streaming, `Watch`, derived cohorts |
| `request-templating` | Stored parameterised requests |
| `financial-cohorts` | `decimal128` money semantics |
| `spss-cohorts` | SPSS `.sav` / `.zsav` import and `.sav` writing |
| `synthetic-data` | `synth from-schema` / `from-profile` entry point; routes to the focused `synth-*` skills (capture, marginals, set fields, correlations, conflicts, fidelity report, determinism) |
| `synth-models` | `--fit-models` entry point; routes to selection, draw, residual correlations and the two recovery sections |
| `synth-structural-rules` | Synth spec `rules[]` and `constraints[]` entry point; routes to expressions, nulls, validation, claims and profile-derived rules |

### From Go

The skill pack is embedded in the binary (`internal/skills/`) and is not a
Go import. Reach it through `pulse skills list` / `pulse skills show <name>`,
the MCP skill tools and `pulse-skill://` resources (mounted by
`gosdk.Register` or `mcpserve`), or the `skills` block of
`(*pulse.Pulse).Manifest`.

The root manifest (`pulse --json`) includes a `skills[]` array so agents can discover available skills in one call.

## .pulse File Format

Binary, self-describing, fully transportable:

- **9-byte header**: magic bytes (`PULSE\x00\x00\x00`) + format version (`0x01`, or `0x02` when the schema declares a parent group)
- **Schema block**: field count, per-field descriptors (type, name, byte offset, bit position, source column index, optional description capped at 1000 bytes)
- **Dictionary blocks**: one per categorical field (string-to-integer mapping stored inline)
- **Record data**: fixed-width binary records, one per row

Field types (the manifest's `cohort_types` block is the authoritative list; per-type detail lives in the `type-*` skills):

| Type | Bytes | Notes |
|---|---|---|
| `u4` | 0 | 4-bit unsigned; bit-packed |
| `u8`, `u16`, `u32`, `u64` | 1, 2, 4, 8 | Unsigned integers |
| `f32`, `f64` | 4, 8 | IEEE 754 floats |
| `date` | 4 | Days since Unix epoch |
| `datetime` | 8 | Seconds since Unix epoch |
| `packed_bool` | 0 | Bit-packed; shares bytes with adjacent packed fields |
| `categorical_u8`, `categorical_u16`, `categorical_u32` | 1, 2, 4 | Dictionary-encoded strings |
| `decimal128` | 16 | Fixed precision/scale; banker's rounding |
| `set_u8` … `set_u64`, `set_u128`, `set_u256` | 1–8, 16, 32 | Multi-select bitmask over an inline dictionary |

Nullability is orthogonal to type: any field can be nullable, carried by a per-record null bitmap. Categorical width auto-selected from sample cardinality during import. Bit-packed types report `ByteSize() == 0` — they share bytes with adjacent packed fields. Schema reader rejects unknown type bytes at parse time with `ENCODING_INVALID`.

## Configuration

Environment variables (`pulse.Options` always overrides; a `.env` at the repo root is auto-loaded):

```bash
export PULSE_DATA_DIR=/path/to/data        # Base directory for .pulse cohorts (fs.Default); bypass with Options{DataDir} or Options{FS}
export PULSE_IMPORTS_DIR=imports           # Managed-imports subdir under the fs root (default "imports")
export PULSE_IMPORT_TTL=7d                 # Default TTL for managed imports: Go duration ("24h"), day form ("7d") or "pin"; default 7d
export PULSE_LABEL_TABLES_DIR=/path/labels # Directory whose *.json files auto-load as LabelTables at pulse.New, keyed by filename
export PULSE_RANGE_TABLES_DIR=/path/ranges # Same shape for RangeTables ({label,start,end} array or {"description","ranges"} wrapper)
export PULSE_MCP_NO_COHORT_SCAN=1          # pulse mcp only: skip the startup cohort enumeration; pulse:// URIs stay readable
export PULSE_TEMPLATES_DIR=/a:/b           # Request-template roots, PATH-style precedence (first root wins)
```

Embedders override per-instance via `pulse.Options{DataDir, ImportsDir, ImportTTL, FS, LabelTablesDir, RangeTablesDir, TemplateDirs}`; the cohort-scan switch is `gosdk.Config.DisableCohortScan` / `mcpserve.Options.DisableCohortScan` (CLI `pulse mcp --no-cohort-scan`). No config files.

## Output Format Contract

All `--json` output is wrapped in a `descriptor.Envelope`:

```json
{
  "format_version": "1.1",
  "data": { ... },
  "errors": [],
  "warnings": []
}
```

`format_version` is `"1.1"` (bumped from `"1.0"` for the Compose facade lift — `pulse api compose --json` now wraps a `ComposedResponse` object `{responses[], overlays[]}` on `data` instead of a raw `[]*Response` array). Errors/warnings use `{"code", "message", "details"}` entries (empty array when absent, never null). Additive-only: new `data` fields don't bump the version; renames or removals do.

## Development

### Build

```bash
make build    # Binary at ./bin/pulse
make test     # go test ./...
make fmt      # gofmt
make vet      # go vet
make lint     # staticcheck (auto-installed via go run)
make cover    # Coverage report
make docs     # Regenerate the Analysis Guide (docs/src/guide) and build mdBook
make dist     # 6-platform release archives + checksums.txt in ./dist (VERSION=v1.2.3)
make contrib  # vet + staticcheck + tidy check + test the contrib/ adapter modules
make clean    # Remove artifacts
```

A `.env` at repo root is auto-loaded by the Makefile.

### Run tests

```bash
go test ./...
go test ./processing/...
go test ./internal/service/... -v -run TestProcess
go test ./encoding/... -fuzz FuzzPulseFileHeader -fuzztime 30s
```

### Project structure

```
pulse/
├── pulse.go                Public facade
├── cmd/pulse/              CLI binary (thin adapter)
├── types/                  Request/response structs + streamability table
├── errors/                 Typed error codes (CodedError system)
├── encoding/               Schema nouns + ungrouped raw-byte primitives
├── descriptor/             Result + envelope types (manifest, predict, inspect)
├── io/                     Import/export jobs, Reader/Writer, io.Format factory
├── synth/                  Synthetic data specs, profiles, Synth
├── mcp/gosdk/, mcpserve/   Mount Pulse's MCP tools / run a ready-made server
├── extend/                 Public extension-authoring contract (Record, Rows, operator interfaces)
├── docs/                   mdBook source (GitHub Pages)
└── internal/               Everything else: service, descriptor and encoding
                            builders, io/<fmt> adapters, operator engine, synth engine, mcp core,
                            skills, examples, fs, imports, template, cli
```

Only the packages above `internal/` are importable; the full tree is in
`.claude/reference/architecture.md`.

Documentation: <https://frankbardon.github.io/pulse/>.

## License

MIT. See [LICENSE](LICENSE).
