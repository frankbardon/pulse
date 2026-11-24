# Response.Components — auxiliary crosstab margins, the opt-out, and the Compose surface

**How to load.** This file is an INDEX: the preamble below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/response-components/` your change touches; find one with `grep -l '<identifier>' .claude/reference/response-components/*.md`. A section-qualified pointer at this file resolves through the section map.

**The shape.** Every `Response` carries an optional `Components *ResponseComponents` (additive `omitempty`; `format_version` stays `"1.1"`) that mirrors the request:

- `Aggregations []AggregationComponents` — one per aggregator slot: the universal floor `{n, n_null}` plus an operator-specific `Operator map[string]any` keyed by the manifest schema. Grouped: a cohort-wide floor plus per-row `Groups`.
- `Groupers []GrouperComponents` — floor `{total_n, n_null}` plus the operator's bucket layout; `fit: "within"` adds per-refit `buckets`.
- `Crosstab *CrosstabComponents` — `CellCounts[r][c]`, `CellComponents[r][c]`, the row / column / grand-total margin counterparts and axis-key components, coordinate-for-coordinate with `MatrixPayload`. Auxiliary `margin_aggregations` figures and multi-measure `Measures` extend it (slices below). A record reaches an auxiliary margin only if it reached a CELL.
- `Filterers []FiltererComponents` — `{n_in, n_out, n_null_input}` on every filterer.
- `Run *RunComponents` — `total_records`, `filtered_records`, `null_records`, `shard_count`, `partial_cohort_reason`. It coexists with `Response.Metadata`: `Metadata` keeps the non-numerical run facts (the cohort file name), `Run` the typed counters.
- `Matrices []MatrixComponents` — floor `{n, n_null, n_listwise_dropped}`, pairwise `{min_pair_n, max_pair_n}`, the weighted floor keys and an operator map.
- `Attributes []AttributeComponents` — per attribute slot that declares a `ComponentSchema`, keyed by `label` + `type`.

Per-operator schemas live in `descriptor.Manifest.ComponentsSchemas.{Aggregators,Groupers,Filterers,Matrices,Attributes}`, each with a mergeability class (`Mergeable` / `Partial` / `None`); streaming chunks emit running state only for mergeable operators. Weighted slots add the `omitempty` floor keys `sum_weights`, `n_eff` and `n_weight_invalid`; `n` / `n_null` stay raw counts.

Load it before changing `Response.Components`, a per-operator `ComponentSchema`, `CrosstabSpec.MarginAggregations` (auxiliary margins) or `CrosstabSpec.Cells` (multi-measure crosstabs) on either execution arm, the Components opt-out knobs, the compute plan, or the Compose per-slot surface.

`format_version` does not move for anything described here: every slot is additive `omitempty`, and a request that declares none of them is byte-identical.

LLM-facing prose: `skills/response-components.md`, `skills/crosstab-margin-aggregations.md`, `skills/crosstab-measures.md`, `skills/streaming-and-watching.md` (per-chunk Components by mergeability class). Parallel reducers: `.claude/reference/execution-modes.md`.

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `response-components/margins-and-opt-out.md` | Auxiliary crosstab margin aggregations (the admission rule, `present`, display gate, allocation vs emission); Multi-measure crosstabs; Opt-out and the Compose surface; Opt-out; Compute plan; Compose surface (v1.1); Compose envelope; Slot label echo; Sweep; Banner; Fits; Batch warnings; Optimize; Post-fit |
| `response-components/notes-and-undefined.md` | Implementation notes displaced from the skill pack; Undefined figures on the wire; Under `Return.precision`; Run-time warnings in the envelope and the stream; Promotion; Streams; Per-slot failures (incl. Compose fits and readers of a failed fit) |
| `response-components/matrix-group-attribute.md` | Matrix components; Per-group aggregation components; Grouper refit buckets; Attribute components |
