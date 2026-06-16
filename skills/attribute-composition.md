---
name: attribute-composition
description: ATTR_* composition rules — slot ordering, two-pass attributes, ATTR_FORMULA expression environment, ATTR vs FEAT distinction. Topical design; per-ATTR detail lives in atomic op-attr-* skills.
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

Entry shape: `{type, field?, label, params?, expression?}`. `type` is the ATTR constant (`ATTR_FORMULA`, `ATTR_ZSCORE`, `ATTR_REG_FITTED`, ...); `field` is the source for single-source ATTRs; `label` names the new column.

## Composition rules

1. **Labels must be unique** across the slot. Collision → `PROCESSING_CONFIG`.
2. **No in-slot chaining.** `ATTR_FORMULA` cannot reference another attribute's label. To stage a derived value into another formula, use Compose / ProcessChain.
3. **Two-pass attributes** (`ATTR_ZSCORE`, `ATTR_TSCORE`, `ATTR_NORMALIZED`, `ATTR_PERCENTILE`, `ATTR_REG_FITTED`, `ATTR_REG_RESIDUAL`, `ATTR_REG_LEVERAGE`) need a pre-pass over filter-passing rows to compute aggregate statistics (mean / stddev / min/max / quantile / OLS coefficients) before pass 2 emits per-row output. The orchestrator handles this transparently — no full-dataset buffering.
4. **Row-local attributes** (`ATTR_FORMULA`, `ATTR_DATE_PART`, `ATTR_SET_POPCOUNT`, `ATTR_SET_HAS`) stream — one pass, no state.
5. **Null inputs propagate.** Null source → null output for most ATTRs. `ATTR_FORMULA` is stricter — referencing a null field raises `PROCESSING_RUNTIME` unless guarded by `??`.
6. **Output is coerced to a scalar.** Numbers pass through; `bool` → `1.0` / `0.0`; anything else errors. ATTRs do not emit Rich payloads.

## ATTR vs FEAT

Both add columns. Split:

| Aspect | `attributes` (ATTR_*) | `features` (FEAT_*) |
|---|---|---|
| Position | After filterers, before groups | Before filterers |
| Sees post-filter rows | yes | no |
| Output column | `label` (rebindable) | fixed per FEAT |
| Use for | per-record scoring, formulas, regression diagnostics | encoding (one-hot, target, frequency), transforms (log, sqrt, bucketize), date features |

Rule: if a downstream filterer or test needs the derived value filterable, use FEAT. Otherwise ATTR. They coexist — FEAT output is in scope for both `filterers` and `attributes`.

## `ATTR_FORMULA` expression environment

Evaluates an `expr-lang/expr` (v1.17.x) string per row.

- **Field refs** by bare name. Numeric → number. Categorical → dictionary string (use `==` / `in`, not the index). `set_*` → sorted `[]string` of selected labels; helpers `contains`, `has_any`, `has_all`, `has_none`, `popcount`, `set_union`, `set_intersect`, `set_diff`, `set_xor` consume naturally.
- **Operators**: arithmetic (`+ - * / % **`), comparison, logical (`and or not`), membership / pattern (`in`, `contains`, `startsWith`, `endsWith`, `matches`), range (`..`), nil-coalesce (`??`), ternary.
- **Functions**: numeric (`abs`, `ceil`, `floor`, `round`, `min`, `max`, `sum`, `mean`, `median`), cast (`int`, `float`, `string`), collection (`len`, `keys`, `values`, `concat`, `sort`, `uniq`, `filter`, `map`, `reduce`, `all`, `any`), string (`join`, `split`, `replace`, `trim`, `lower`, `upper`, `hasPrefix`, `hasSuffix`, `indexOf`), JSON / time (`toJSON`, `fromJSON`, `now`, `date`, `duration`). **No** `sqrt` / `log` / `exp` / `pow` / trig — use `**` for powers (`x ** 0.5`) or pre-compute upstream.
- **Extensions.** `pulse.Options.Extensions.ExprFunctions` injects custom functions; `LookupTables` reaches `lookup(table, keys...)`. Per `extension-points`.
- **Null fields are omitted** from the env. Referencing one without `??` raises `PROCESSING_RUNTIME` and drops the row.

Two common patterns:

```json
{"type": "ATTR_FORMULA", "field": "weight_kg", "label": "bmi",
 "expression": "weight_kg / (height_m ** 2)"}
```

```json
{"type": "ATTR_FORMULA", "field": "brand", "label": "is_premium",
 "expression": "brand in [\"Apple\", \"Samsung\"] ? 1 : 0"}
```

## Streamability

Two-pass ATTRs stream via the two-pass orchestrator (`iter.Reset()`). Buffer-forcing happens only when combined with a window, a buffered grouper (`GROUP_QUANTILE`, `GROUP_DATE`), or another buffered op. Predict reports per-slot streamability under `data.streamable_reasons`. Full table: `request-envelope` and `streaming-and-watching`.

## Components

Attributes emit scalars; they do not produce `Response.Components`. The Components family covers aggregations, groupers, filterers, crosstab, and run — not per-row derived columns. To audit attribute output, read it from `Response.Data` or wrap the attribute in an aggregation.

## Gotchas

- No in-slot chaining — `ATTR_FORMULA` cannot reference another attribute's label.
- Categorical fields appear as strings; arithmetic on them errors. Branch on the label and emit a number.
- Two-pass ATTRs against shard archives: pre-pass is per-shard for `Mergeable` paths and across-shard union for buffered paths. See `cohort-schema-design`.
- ATTR output is f64 by default; declare `decimal128` source explicitly if precision matters — see `financial-cohorts`.

## See

- Per-ATTR recipes: `pulse_examples_search tags=["feature-engineering"]`, `tags=["regression"]`, `tags=["outlier-detection"]` plus atomic `op-attr-<name>` for each ATTR.
- `aggregation-design` — when to aggregate instead of derive a column.
- `feature-engineering` — FEAT_* pre-filter column production.
- `request-envelope` — slot keys, streamability, smart defaults.
- `extension-points` — `ExprFunctions` + `LookupTables` injection.
- `streaming-and-watching` — per-slot streamability classification.
