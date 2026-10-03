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

Entry shape: `{type, field?, label, params?, expression?}`. `type` is the attribute constant; `field` is the source for single-source attributes; `label` names the new column.

## Choosing an attribute

Decide what the new column must say about a row, then pick from the manifest `components.attributes` entries whose `intents` match (`pulse_skills_get intents`: `prepare`, `segment`, `data_quality` for outlier flags, `drivers` for model diagnostics) and read `op-attr-<name>`; each operator's guidance names its alternatives.

- **Position against the column** (standard deviations, a 0–1 range, a percentile rank) — a two-pass score. Skewed data or extreme values distort mean-based scores; a rank-based one resists them.
- **Position against a model** (fitted value, residual, leverage) — a regression diagnostic; for the model's own coefficients run a regression instead.
- **A rule over this row's fields** (ratio, flag, recode) — the formula attribute.
- **A calendar part or a multi-select test** — the dedicated row-local attributes.
- **Keeping or dropping rows** is a filter, not an attribute.

## Composition rules

1. **Labels must be unique** — never a schema field, feature output or earlier label. Shadowing → `SERVICE_VALIDATION`, runtime and predict.
2. **No in-slot chaining.** A formula cannot reference another attribute's label. To stage a derived value into another formula, use Compose / ProcessChain.
3. **Two-pass attributes** — anything scoring a row against the column or a model — need a pre-pass over filter-passing rows to compute aggregate statistics (mean / stddev / min/max / quantile / OLS coefficients) before pass 2 emits per-row output. The orchestrator handles this transparently — no full-dataset buffering.
4. **Row-local attributes** (formula, calendar part, set membership / popcount) stream — one pass, no state.
5. **Null inputs propagate.** Null source → null output for most attributes. A formula is stricter — referencing a null field raises `PROCESSING_RUNTIME` unless guarded by `??`.
6. **Output is coerced to a scalar.** Numbers pass through; `bool` → `1.0` / `0.0`; anything else errors. Attributes do not emit Rich payloads.

## ATTR vs FEAT

Both add columns. Split:

| Aspect | `attributes` (ATTR_*) | `features` (FEAT_*) |
|---|---|---|
| Position | After filterers, before groups | Before filterers |
| Sees post-filter rows | yes | no |
| Output column | `label` (rebindable) | fixed per FEAT |
| Use for | per-record scoring, formulas, regression diagnostics | encoding (one-hot, target, frequency), transforms (log, sqrt, bucketize), date features |

Rule: if a downstream filterer or test needs the derived value filterable, use FEAT. Otherwise ATTR. They coexist — FEAT output is in scope for both `filterers` and `attributes`.

## Formula expressions

The formula attribute evaluates a per-row `expr-lang` expression: categoricals bind as label strings, `set_*` fields as label lists, a null field as `nil` (guard with `??`, or unguarded arithmetic raises `PROCESSING_RUNTIME`), and there is no `sqrt` / `log` / `exp`. Binding table, operators, functions, set helpers and embedder `ExprFunctions` / `lookup(...)`: `expression-language`.

## Streamability

Two-pass attributes stream via the two-pass orchestrator (`iter.Reset()`). Buffer-forcing happens only when combined with a window, a buffered grouper, or another buffered op. Predict reports per-slot streamability under `data.streamable_reasons`. Full table: `request-envelope` and `streaming-and-watching`.

## Components

Attributes emit scalars, not `Response.Components` (which covers aggregations, groupers, filterers, crosstab, run). To audit attribute output, read `Response.Data` or wrap the attribute in an aggregation.

## Gotchas

- Categorical fields appear as strings; arithmetic on them errors. Branch on the label and emit a number.
- Two-pass attributes against shard archives: pre-pass is per-shard for `Mergeable` paths and across-shard union for buffered paths. See `cohort-schema-design`.
- Attribute output is f64 by default; declare `decimal128` source explicitly if precision matters — see `financial-cohorts`.

## See

- Per-ATTR recipes: `pulse_examples_search tags=["feature-engineering"]`, `tags=["regression"]`, `tags=["outlier-detection"]` plus atomic `op-attr-<name>` for each ATTR.
- `aggregation-design` — when to aggregate instead of derive a column.
- `feature-engineering` — FEAT_* pre-filter column production.
- `request-envelope` — slot keys, streamability, smart defaults.
- `expression-language` — the formula / filter expression environment.
- `streaming-and-watching` — per-slot streamability classification.
