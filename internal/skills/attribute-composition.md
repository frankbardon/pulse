---
name: attribute-composition
description: Attribute composition rules — slot ordering, two-pass vs row-local attributes, the formula expression environment, attributes vs features. Topical design; per-attribute detail lives in atomic op-attr-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [ATTR, attributes]
---

# Attribute composition

`attributes` add derived COLUMNS per record. Each entry computes one new value per row from existing fields, extending output without modifying the underlying cohort. Design contract here; per-ATTR detail (formulas, null rules, regression diagnostics) lives in atomic `op-attr-*` skills.

## Slot position

Pipeline order: `features → filterers → attributes → groups → aggregations → windows → sort`. Attributes run AFTER filterers and BEFORE groups:

- Filterers see source fields only — never an attribute label.
- Groupers / aggregators / windows CAN reference an attribute label as `field`.
<!-- feature: ATTR_FORMULA, ATTR_REG_PREDICT -->
- **Post-window phase.** A row-local formula or `ATTR_REG_PREDICT` reading a window label (or an earlier post-window attribute) runs AFTER windows, so a saturation `hill(tv_ad, 800, 1.5)` can feed a regression. Any other attribute reading a window, a `total()` formula, or any outside the windows-over-records shape: `PULSE_ATTRIBUTE_WINDOW_ORDER` (`attribute`, `field`; materialize first). A lag's first rows are null: guard with `?? 0`.
<!-- /feature -->

Entry shape: `{type, field?, label, params?, expression?}`. `type` is the attribute constant; `field` is the source for single-source attributes; `label` names the new column.

## Choosing an attribute

Decide what the new column must say about a row, then pick from the manifest `components.attributes` whose `intents` match (`pulse_skills_get intents`) and read `op-attr-<name>`; its guidance names alternatives.

- **Position against the column** (standard deviations, a 0–1 range, a percentile rank) — a two-pass score. Skewed data or extreme values distort mean-based scores; a rank-based one resists them.
- **Position against a model** (fitted value, residual, leverage) — a regression diagnostic; for the model's own coefficients run a regression instead.
- **A rule over this row's fields** (ratio, flag, recode) — the formula attribute.
- **A calendar part or a multi-select test** — the dedicated row-local attributes.
- **Keeping or dropping rows** is a filter, not an attribute.

## Composition rules

1. **Labels must be unique** — never a schema field, feature output or earlier label. Shadowing → `SERVICE_VALIDATION`, runtime and predict.
2. **Declared order.** A formula reads only EARLIER attribute labels (declared order), never a later one. To stage a value across requests, use Compose / ProcessChain.
3. **Two-pass attributes** — scoring a row against the column or a model — pre-pass the filter-passing rows for aggregate statistics (mean / stddev / min/max / quantile / OLS coefficients), then emit per row. Transparent; no full buffering.
4. **Row-local attributes** (formula, calendar part, set membership / popcount) stream — one pass, no state.
5. **Null inputs propagate.** Null source → null output (`n_null`); a 0/1 membership flag reads `0`. A formula is stricter — arithmetic on a null field raises `PROCESSING_RUNTIME` unless guarded by `??`.
6. **Output is coerced to a scalar.** Numbers pass through; `bool` → `1.0` / `0.0`; a formula's `nil` → null; anything else errors. Attributes do not emit Rich payloads.

## ATTR vs FEAT

Both add columns. Split:

| Aspect | `attributes` (ATTR_*) | `features` (FEAT_*) |
|---|---|---|
| Position | After filterers, before groups | Before filterers |
| Sees post-filter rows | yes | no |
| Output column | `label` (rebindable) | fixed per FEAT |
| Use for | per-record scoring, formulas, regression diagnostics | encoding (one-hot, target, frequency), transforms (log, sqrt, bucketize), date features |

Rule: a derived value a filterer must see is a FEAT; otherwise ATTR. FEAT output is in scope for both.

## Formula expressions

The formula attribute evaluates a per-row `expr-lang` expression: categoricals bind as label strings, `set_*` fields as label lists, a null field as `nil` (guard with `??`). Binding table, operators, functions (vector reductions, scalar maths), set helpers and embedder `ExprFunctions` / `lookup(...)`: `expression-language`.

<!-- feature: capability:matrices, ATTR_FORMULA -->
**Worked example — a rating battery.** Name the items once; each row gets the mean of its valid items when ≥ 6 of 8 answered (else null, never 0) and a straight-lining flag:

```json
"vectors": [{"name": "battery", "fields": ["q*"]}],
"attributes": [
  {"type": "ATTR_FORMULA", "label": "battery_mean", "expression": "vcount(battery) >= 6 ? vmean(battery) : nil"},
  {"type": "ATTR_FORMULA", "label": "flat", "expression": "vsd(battery) == 0"}
]
```

As a filter expression, `vsd(battery) > 0` drops flat responders. A vector named like a field or a feature / attribute / window label is `PULSE_VECTOR_NAME_COLLISION`; one only an expression reads raises no unreferenced warning.
<!-- /feature -->

## Streamability

Two-pass attributes stream via the two-pass orchestrator. Only a window, a buffered grouper or another buffered op forces buffering; predict reports it under `data.streamable_reasons` (`request-envelope`, `streaming-and-watching`).

## Components

Attributes emit scalars, not `Response.Components`; audit them in `Response.Data` or an aggregation.

## Gotchas

- Categorical fields appear as strings; arithmetic on them errors. Branch on the label and emit a number.
- Two-pass attributes on shard archives: pre-pass per shard for `Mergeable` paths, union across shards for buffered (`cohort-schema-design`).
- Output is f64 by default; for `decimal128` precision see `financial-cohorts`.

## See

- Per-ATTR recipes: `pulse_examples_search tags=["feature-engineering"]`, `tags=["regression"]`, `tags=["outlier-detection"]` plus atomic `op-attr-<name>` for each ATTR.
- `aggregation-design` — when to aggregate instead of derive a column.
- `feature-engineering` — FEAT_* pre-filter column production.
- `request-envelope` — slot keys, streamability, smart defaults.
- `expression-language` — the formula / filter expression environment.
- `streaming-and-watching` — per-slot streamability classification.
