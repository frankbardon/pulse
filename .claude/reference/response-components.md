# Response.Components — auxiliary crosstab margins, the opt-out, and the Compose surface

**How to load.** This file is an INDEX: the preamble below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/response-components/` your change touches; find one with `grep -l '<identifier>' .claude/reference/response-components/*.md`. A section-qualified pointer at this file resolves through the section map.

Relocated verbatim from CLAUDE.md (section `### Response.Components`). CLAUDE.md keeps the always-load half inline — the `Components *ResponseComponents` shape (the `Aggregations` / `Groupers` / `Crosstab` / `Filterers` / `Run` / `Matrices` / `Attributes` bullets), the universal floor `{n, n_null}`, the `Response.Metadata` naming-collision note, and the per-operator `ComponentsSchemas` projection. Everything below is the long form it points at.

Load it before changing `CrosstabSpec.MarginAggregations` (the auxiliary margin-only surface), `CrosstabSpec.Cells` (multi-measure crosstabs), either execution arm's accumulation of it, the `row_margin_aggregations` / `column_margin_aggregations` / `grand_total_aggregations` figures, the Components opt-out knobs, or the Compose per-slot Components surface — CLAUDE.md's Update Demand table names this file as a required companion for those triggers.

`format_version` does NOT move for anything described here. `Response.Components` is additive `omitempty` throughout; the auxiliary margin figures, the opt-out and the Compose per-layer `Warnings` slot are all additive, and a request that declares none of them is byte-identical to the pre-slot wire form.

## Contents

1. Auxiliary crosstab margin aggregations (`crosstab.margin_aggregations`) — the admission rule, `present` semantics, the display-flag gate, and the allocation/emission gap
   - Multi-measure crosstabs (`crosstab.cells`, U47 E2-S2) — per-measure computation, the MEASURE row fold, `matrix.measures` / `components.crosstab.measures`, pairwise-only overlays
2. Opt-out (`DisableComponents`, `--no-components`)
3. Compose surface (v1.1) and the Compose `--json` envelope
4. Implementation notes displaced from the skill pack
5. Undefined figures on the wire (NaN / ±Inf → `null`)
6. Matrix components (`Components.Matrices`, U16)
7. Per-group aggregation components (`aggregations[*].groups`)
8. Attribute components (`Components.Attributes`, U27 E2-S4)
9. Run-time warnings in the envelope and the stream (U44 E2-S5)
10. Per-slot failures (`types.SlotError`, `PULSE_SLOT_FAILED`, failed Compose fits + their readers + `PULSE_FIT_UNAVAILABLE`, U49 E1-S2 / E1-S3 / E1-S4)

**Skill homes (U10 split).** The LLM-facing prose for this surface lives in these skills: `skills/response-components.md` (floor, five sub-blocks, opt-out, manifest declaration, mergeability classes), `skills/crosstab-margin-aggregations.md` (the auxiliary figures, the admission rule, `present`, the display gate), `skills/crosstab-measures.md` (multi-measure crosstabs) and `skills/streaming-and-watching.md` (per-chunk Components by mergeability class). Contributor-only detail that used to sit in those skills is in section 4 below and in `.claude/reference/execution-modes.md` (parallel reducers, the Welford overlay parity reads).

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `response-components/margins-and-opt-out.md` | Auxiliary crosstab margin aggregations; Multi-measure crosstabs; Opt-out and the Compose surface; Opt-out; Compute plan (U18); Compose surface (v1.1); Compose envelope (relocated from CLAUDE.md "Output Format Contract" at U18 E1-S1); Sweep (U40); Banner (U47 #326); Fits (U42; vector fits U30 E2-S2); Optimize (U60) |
| `response-components/notes-and-undefined.md` | Implementation notes displaced from the skill pack; Undefined figures on the wire; One rule for every JSON payload: a non-finite float (NaN, +Inf, -Inf) is written as JSO…; Under `Return.precision`; Run-time warnings in the envelope and the stream; Promotion (U44 E2-S5); Streams; Per-slot failures |
| `response-components/matrix-group-attribute.md` | Matrix components; Per-group aggregation components; Attribute components |
