```yaml
name: crosstab-margin-aggregations
description: Auxiliary margin-only figures on a crosstab — a second base beside the cell metric in one scan, the cell-admission rule, present semantics and the display-flag gate
type: guide
kind: design
applies_to: process, compose, predict
covers: [Crosstab, CrosstabComponents, margin_aggregations]
requires: [capability:crosstab]
```

# Crosstab margin aggregations

`crosstab.cell` is ONE aggregation. `crosstab.margin_aggregations` (optional, additive) carries extra aggregations evaluated into the row / column / grand margin accumulators **only, never into a cell**. Reach for it when a table needs a second figure per margin read against the cells — canonically an unweighted respondent base beside a weighted metric, or a distinct-respondent count beside a sum — without a second scan.

```jsonc
{"crosstab": {
  "rows": [...], "columns": [...],
  "cell": {"type": "<weighted metric>", "field": "spend", "label": "wspend"},
  "margins": {"rows": true, "columns": true, "grand": true},
  "margin_aggregations": [{"type": "<count>", "field": "respondent_id", "label": "base"}]
}}
```

Manifest: `crosstab.supports_margin_aggregations` + `crosstab.margin_aggregation_rules`.

## Validation

- Effective label = `label`, else `TYPE_field`; unique across the slot **and** distinct from the cell's, because figures are keyed by label. Faults: `PULSE_CROSSTAB_MARGIN_AGG_INVALID` (null entry / no type), `PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL`; an unknown operator is refused.
- Declared on a crosstab that DISPLAYS no margin ⇒ `PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED` **warning** — the request runs, the figures have nowhere to land.

## The admission rule

**A record contributes to an auxiliary ONLY IF IT CONTRIBUTED TO A CELL.** This is the least guessable property of the surface. Two exclusions follow, and they are the entire difference from the cell's own margins beside it:

- a record whose CELL FIELD IS NULL;
- a record whose axis key a grouper `include` list EXCLUDED.

The cell's own margins count both. Deliberate, no knob: an auxiliary is a base the cells beside it are read against, so it must see the records they saw; a cohort-wide base answers a different question while looking identical. **Reading an auxiliary's `n` as a cohort count is wrong SILENTLY** — every figure still renders, only the base is off.

Both execution arms (fused and buffered) apply the rule identically; nothing in the response says which ran, and nothing needs to.

## Where the figures land

On `Response.Components.Crosstab`:

| Key | Indexed by |
|---|---|
| `row_margin_aggregations[r]` | `RowKeys` order |
| `column_margin_aggregations[c]` | `ColumnKeys` order |
| `grand_total_aggregations` | — |

Each is a map keyed by effective label; each entry is `{value, present, components}`. `components` = the floor `{n, n_null}` over ADMITTED records plus that operator's own component keys — so a distinct-sum auxiliary exposes its `distinct_count` per margin slot beside the scalar: two figures off one scan.

- They sit BESIDE `row_margin_components` (the CELL aggregator's margin, which admits every filter-passing record routed to the row), never inside it. Merging the two would file differently-based figures under one roof.
- **`present` is load-bearing, not a zero value.** A slot admitting no record carries `present: false` and NO `value` key — an aggregator over an empty set has no defined output, and a fabricated `0` would be indistinguishable from a real one. Its `components` still carry the floor, whose `n = 0` is true.
- Declaring no auxiliary emits none of the three keys.

## The display-flag gate

Emission rides the DISPLAY flags alone (`margins.rows` / `.columns` / `.grand`), the rule the cell's own margin figures follow. A normalize direction does **not** count: normalize makes the engine compute a margin as a denominator, and an auxiliary is never a denominator. So `margins` all false + `normalize: row|column|total` + a declared auxiliary does the work and emits nothing — predict warns `PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED`.

**Expecting figures and the block is absent? Check the display flag first.**

## Fused execution

An auxiliary must be mergeable and non-decimal for the crosstab to fuse; otherwise the request runs buffered — the auxiliary is never dropped. Its margin-reducibility class is not consulted: it has no cells to reduce from.

## See

[`crosstab-guide`](crosstab-guide.md) (axes, margins, normalize) · [`response-components`](response-components.md) (floor, crosstab block).
