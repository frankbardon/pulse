---
name: export-format-selection
description: Pick the right export format — CSV (universal), TSV, NDJSON, JSON array, Parquet (columnar analytics), Arrow IPC (zero-copy handoff), Excel (humans). Use when wiring Process output into a downstream tool or warehouse. Also covers cross-format overlay embedding.
type: guide
applies_to: process, compose
---

# Export Format Selection

<skill_overview>
After processing, Pulse can export results in multiple formats with tradeoffs in type fidelity, file size, tooling compatibility, and categorical representation. Invoke this skill when choosing an export target for a downstream consumer.
</skill_overview>

<reference>
## Format Comparison

| Feature | CSV | TSV | NDJSON | JSON Array | Parquet | Arrow IPC | Excel |
|---------|-----|-----|--------|------------|---------|-----------|-------|
| Type fidelity | Low (all text) | Low (all text) | Medium (JSON types) | Medium (JSON types) | High (native types) | High (native Arrow types) | Medium |
| Null representation | Empty string | Empty string | `null` | `null` | Native null | Native null | Empty cell |
| Categorical | String labels | String labels | String labels | String labels | Dictionary-encoded | Dictionary-encodable | String labels |
| File size | Medium | Medium | Large | Large | Small (compressed) | Small (LZ4 by default) | Medium |
| Streaming | Yes | Yes | Yes | Producer streams; consumers usually parse whole file | No (columnar) | Batch-by-batch | No |
| Human-readable | Yes | Yes | Yes | Yes | No | No | Yes (GUI) |
| Schema included | No | No | No | No | Yes | Yes | No |
</reference>

<reference>
## When to Use Each Format

### CSV

Use CSV when:
- The downstream tool expects comma-separated text (spreadsheets, most data tools)
- Human readability is important
- Type information will be re-inferred by the consumer

Limitations:
- No native null; empty strings are ambiguous
- No type metadata; consumers must infer types
- Quoting rules can cause issues with embedded commas

### TSV

Use TSV when:
- The downstream tool expects tab-separated text
- Data contains many commas (avoiding quoting complexity)
- Otherwise, same tradeoffs as CSV

### NDJSON

Use NDJSON (newline-delimited JSON) when:
- The consumer is a JSON-native tool or API
- You need one-record-per-line streaming
- Null values must be explicitly represented

Limitations:
- Larger file size than CSV due to repeated keys
- No schema metadata in the file itself

### JSON Array

Use JSON Array (`.json`) when:
- The consumer is a browser, REST API, or scripting environment that expects a single top-level array (`JSON.parse`, Python `json.load`, etc.)
- The dataset is small enough that whole-file parsing is acceptable
- You want a single self-contained JSON document rather than line-delimited records

Limitations:
- Most consumers parse the whole array at once; not as memory-friendly as NDJSON for very large datasets
- No schema metadata in the file
- Only flat objects are supported on import (nested objects/arrays are rejected with `PULSE_IMPORT_ROW_ERROR`, same rule as NDJSON)
- The `.json` extension always means "top-level array of flat objects"; NDJSON should use `.ndjson` or `.jsonl`

### Parquet

Use Parquet when:
- The downstream tool supports Parquet natively (Spark, DuckDB, Pandas, Arrow)
- Type fidelity matters (integers stay integers, nulls stay nulls)
- File size matters (Parquet uses columnar compression)
- Categorical fields should remain dictionary-encoded

Parquet is the best format for machine-to-machine data transfer.

### Arrow IPC (Feather V2)

Use Arrow IPC (`.arrow`, `.feather`) when:
- The downstream tool already speaks Arrow natively (Polars, DuckDB, pandas via `pyarrow`, R `arrow`)
- You want zero-copy columnar interchange without the Parquet encode/decode overhead
- You need batch-level streaming on read while still keeping schema metadata in the file
- You want to round-trip a dataset through an Arrow-native pipeline and back into Pulse

The `.arrow` and `.feather` extensions both produce the same Arrow IPC file format. Feather V2 is a re-branding of the IPC file format on disk.

Limitations:
- Not as widely consumed as Parquet by non-Arrow tooling
- Pulse's writer emits all columns as Arrow `String`; consumers that need native numeric Arrow types should round-trip through Parquet or import the data with a typed schema first
- Stream-format Arrow IPC (`.arrows`) is not supported — use the file format only

### Excel

Use Excel when:
- The consumer is a non-technical user working in Microsoft Excel or Google Sheets
- You need a single-file deliverable with formatted output

Limitations:
- Row limit of ~1 million rows
- No native categorical encoding (values appear as strings)
- Larger file size than Parquet
</reference>

<reference>
## Categorical Behavior per Format

- **CSV/TSV**: Categorical fields are exported as their string labels. The dictionary is lost.
- **NDJSON**: Categorical fields appear as string values in JSON.
- **JSON Array**: Categorical fields appear as string values; the dictionary is lost.
- **Parquet**: Categorical fields are exported as dictionary-encoded columns, preserving the encoding.
- **Arrow IPC**: Categorical fields are exported as Arrow `String` columns. Consumers that need dictionary encoding can call `.dictionary_encode()` after read.
- **Excel**: Categorical fields appear as string values in cells.
</reference>

<rule severity="should" topic="round-trip-preservation">
If you need to round-trip data back into Pulse, use Parquet to preserve categorical encoding.
</rule>

<reference>
## Overlay Embedding

When a Response carries `Response.Overlays []*OverlayLayer` (or a `ChainResponse.Overlays` slice on the whole-chain barrier), the export adapter can embed those layers alongside the host record stream so downstream consumers see the index / share / delta / inferential decorations the engine produced. Overlay embedding is a cross-format export concern — the on-disk `.pulse` byte format is unchanged and never carries overlay payloads. See `skills/overlay-system.md` for the overlay-kind catalog, payload shapes (`scalar` / `series` / `matrix`), and the Request-time wire surface (`Request.Overlays`).

### Per-format behavior

| Format | Overlay carrier | Round-trip | Notes |
|---|---|---|---|
| Arrow IPC | Top-level `LIST<STRUCT>` field named `overlays`, emitted once in the first record-batch. Each list element is one `OverlayLayer` carrying `kind`, `name`, `scope`, `ref` (JSON-marshalled), `shape`, payload, and the layer's `OverlaySummary`. | Exact-byte | The nested struct schema is FIXED across runs — absent payload arms surface as SQL NULL so the same schema is reusable by downstream consumers (DuckDB, polars, Spark). |
| Parquet | Same Arrow `LIST<STRUCT>` schema flows through the `pqarrow` bridge as a top-level `OPTIONAL group`. The overlay group rides in the FIRST row-group and is empty in subsequent row-groups. | Exact-byte | Inherits the writer's compression codec (default `snappy`). Overlay payloads are tiny relative to host data so per-group compression is uniform with host columns. |
| Excel | One sheet per layer named `__overlay_<layer_name>`. Sheet-name length capped at 31 chars (Excel limit); truncation emits a `PULSE_OVERLAY_EXPORT_SHEET_TRUNCATED` warning. Matrix-shape overlays render row × column with `__row_margin` / `__col_margin` headers; series-shape overlays render `key, value, present, statistic, p_value` columns; scalar overlays render a header / value layout with summary rows. | Best-effort (±1 ULP) | NaN / Inf coerce to text strings through Excel's text path; downstream readers should treat overlay values as float64 best-effort. The `__overlay_` sheet-name prefix is RESERVED. |
| NDJSON | Single trailing line `{"_overlays": [...]}` after the last host-record line. The slice is `[]*OverlayLayer` serialised via `encoding/json` — same shape as the descriptor envelope's `data.overlays` slot. | Exact-byte (modulo JSON number normalisation) | The host stream is byte-identical to a pre-overlay NDJSON file UNTIL the trailer. The `pulse api process --stream` streaming path skips the trailer by construction, mirroring the `--json` envelope's "streaming output skips the echo" rule. |
| CSV / TSV | None — warn-and-skip. | None | CSV cannot encode nested overlay payloads cleanly. The dispatcher emits one `PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED` warning at job dispatch when `Response.Overlays` is non-empty AND `IncludeOverlays` does not explicitly opt out; the CSV body is BYTE-IDENTICAL to the same job with overlays stripped. TSV inherits the warn-and-skip surface (the warning code stays CSV-flavoured; fixups call out TSV alongside CSV). |

### Wiring shape

Both `ExportJob.IncludeOverlays *bool` and `ConvertJob.IncludeOverlays *bool` are tri-state pointers:

- `nil` — DEFAULT EMIT. When the host Response carries any overlay layers the adapter embeds them in the format-native sidecar. Output for an overlay-free Response is byte-identical to a pre-overlay `ExportJob`.
- `*true` — explicit emit. Same downstream behaviour as `nil`, but the explicit toggle distinguishes intent in canonical-hash composition so cache keys differ between "default" and "explicit yes".
- `*false` — opt out. Overlays are dropped on export even when the host Response carries layers. Output is byte-identical to a pre-overlay `ExportJob` against the same host result. Also suppresses the `PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED` warning when the target is CSV / TSV.

The pointer shape mirrors the `Includes`-slot precedent — `nil` here means "default: emit when present" rather than "explicit no," because the Go `bool` zero is `false`. Marshalling via `encoding/json` honours `omitempty` so `nil` pointers do not appear in canonical JSON; explicit `*true` and `*false` both serialise as booleans and produce distinct canonical-hash keys.

The companion `Overlays []*types.OverlayLayer` slot on each job carries the layer slice the adapter should embed. `pulse.Export` / `pulse.Convert` populate it from `Response.Overlays`; embedders constructing `io.ExportJob` directly fill the slot themselves. The slot itself is NOT part of canonical-hash composition (cache identity is the export REQUEST, not the response).

### Worked example: Arrow with two overlay layers

```go
// Suppose Process produced a crosstab Response with two overlay layers:
//
//   resp.Overlays = []*types.OverlayLayer{
//     {Name: "index", Kind: types.OverlayIndexVsMargin,  Scope: types.OverlayScopeCell,   Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: ...}},
//     {Name: "chisq", Kind: types.OverlayChisqMatrix,    Scope: types.OverlayScopeMatrix, Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: ...}},
//   }

emit := true
job := &pio.ExportJob{
    Source:          "/path/to/cohort.pulse",
    Target:          arrowWriter, // overlay-aware adapter
    IncludeOverlays: &emit,        // explicit emit (distinct hash key vs nil default)
    Overlays:        resp.Overlays, // populated by pulse.Export from Response.Overlays
}
if _, err := job.Run(ctx); err != nil {
    return err
}
```

Resulting Arrow file:

- Host record-batches stream as usual, byte-identical to an `IncludeOverlays=false` export of the same host result.
- Top-level schema gains an `overlays` `LIST<STRUCT>` field; the FIRST record-batch carries a 2-element list (one struct per layer), subsequent record-batches carry an empty list.
- Each struct entry inlines `name`, `kind`, `scope`, `ref`, `shape`, the matching payload arm (`matrix` for `index`, `scalar` for `chisq`), and the layer-level `summary` block.
- A reader reconstructs `[]*types.OverlayLayer` by reading the first non-empty `overlays` list, dispatching on `shape`, and copying each layer back into the slice — exact-byte equivalent to the original.

### Default opt-out

Setting `IncludeOverlays` to a pointer at `false` (e.g. `optOut := false; job.IncludeOverlays = &optOut`) produces output byte-identical to a pre-overlay `ExportJob` against the same host result. Use the explicit opt-out when targeting CSV / TSV and you want to suppress the `PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED` warning while keeping the same CSV body.

### CanonicalHash interaction

`ExportJob.IncludeOverlays` and `ConvertJob.IncludeOverlays` participate in `ExportJob.Hash()` / `ConvertJob.Hash()` canonical-hash composition (the same hash that keys intermediate `.pulse` materialisations via `ConvertJob.KeepPulseAt`). Two jobs sharing `Source`, `Includes`, `Labels`, and format-specific knobs but differing in `IncludeOverlays` resolve to DISTINCT hash keys so a cache that promotes overlay-stripped outputs does not stall an overlay-bearing request. The `Overlays` slot itself is NOT in the hash composition — cache identity is the export REQUEST shape, not the response payload.

### Cross-references

- `skills/overlay-system.md` — overlay-kind catalog (`OVERLAY_INDEX_VS_MARGIN`, `OVERLAY_CHISQ_MATRIX`, etc.), payload-shape contract (`scalar` / `series` / `matrix`), Request-time wire surface (`Request.Overlays` / `Response.Overlays`), and the host-shape rules every overlay obeys.
- `PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED` — warning-class code with fixups pointing at Arrow / Parquet / Excel / NDJSON targets and the `IncludeOverlays=false` opt-out. Look up via `pulse errors lookup PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED` or the `pulse_errors_lookup` MCP tool.
- `.planning/result-overlay-system/research/export-embedding-shape.md` — authoritative per-format wire shape (Arrow / Parquet schema, Excel per-sheet layouts, NDJSON trailer rules), round-trip equivalence rules, and the open-questions punt list for future stories.
</reference>

<rule severity="note" topic="field-projection">
## Projecting Fields on Export

`ExportJob.Includes []string` (and `ConvertJob.Includes`) restricts the output to the named source-schema fields. Nil / empty selects every field — pre-projection behaviour, unchanged. Output column order always follows the source schema, never the include order, so the on-disk byte layout stays the source of truth and downstream consumers see a stable layout regardless of CLI invocation order.

CLI surface: the `pulse export {csv|tsv|ndjson|jsonarray|parquet|arrow|excel}` subcommands accept a repeatable `--include` flag (e.g. `--include country --include amount`). Unknown names return `PULSE_EXPORT_FIELD_UNKNOWN` — run `pulse inspect` first to discover valid field names.

Label interaction: `--labels field=table:augment` still emits the sibling `<field>_label` column, but only when the source field is itself included. Excluding a source field also drops its augment sibling — the sibling has nothing to attach to. Replace-mode bindings apply only to included fields. Convert: when `KeepPulseAt` is set, the intermediate `.pulse` file always carries the full source schema — projection is an output-time overlay, not an on-disk schema change.

Embedder API: set `pio.ExportJob{Includes: []string{"a", "b"}}` (or `pio.ConvertJob`); duplicates dedupe; the in-memory order matches schema order in the emitted row.
</rule>

<rule severity="note" topic="no-mcp-tool">
## Export has no MCP tool today

The export and convert operations are CLI / library facilities — there is no `pulse_export` MCP tool. When advising on format selection, decide based on the table above and point a human at the relevant mdBook chapter for invocation:

- CSV / TSV / NDJSON / JSON Array — see the export chapters under https://frankbardon.github.io/pulse/cli/
- Parquet — same.
- Arrow IPC / Feather V2 — produced via the convert command. See https://frankbardon.github.io/pulse/cli/
- Excel — same.

Validation before writing is available from the CLI side (`pulse export predict`). It is not surfaced through MCP today.
</rule>
