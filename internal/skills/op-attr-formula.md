---
name: op-attr-formula
description: Per-row expression evaluation against the record's fields via expr-lang.
kind: operator
category: ATTR
operator: ATTR_FORMULA
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, streaming-friendly]
---

Row-level scalars; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Expression` | string | (required) | `expr-lang/expr` v1.17.x string evaluated per row. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any referenced field type |
| `Label` | required — new column name |

## Output

One `float64` per record (bools → `1.0` / `0.0`); a `nil` result → null (`n_null`). Null binds `nil`: guard `x ?? 0` / `x == nil ? a : b`; unguarded arithmetic on nil → `PROCESSING_RUNTIME`, at runtime only: predict reads no record, so a nullable field that holds a null looks like one that holds none — guard every nullable field.

## Gotchas

- Reads source fields and EARLIER attribute labels (declared order); stage across requests via Compose / ProcessChain.
- **`total(expr)`** (this operator only; lowercase, one argument, no nesting): cohort-wide sum of a row-level `expr` over post-filter rows — the same figure on every row, also when grouped. Weighted by a `frequency` weight in force on the slot (Σw·expr, the expanded-row total; invalid / zero-weight rows add nothing); a `probability` weight → `PULSE_WEIGHT_UNSUPPORTED` on predict and runtime alike (the total would scale with the weights), opt out with `"weight": null`. A formula without `total()` reads no weight (skipped). Null-argument rows skipped. A binned order-free sum: the same bits on every arm and shard split. Empty total → `null`; zero total as divisor → `null`. Pro-rata: `x * 1000 / total(x)`. Subset total: `x * (1 - 1000 / total(week_index >= 52 ? x : 0))`.
- A `total` formula is **two-pass** (like the z-score attribute): streams alone, buffers beside groups / features; `ShardWorkers` / `DecodeWorkers` run it two-phase (a parallel pre-pass, same bits) when ungrouped and no `total()` argument reads another total, else sequentially; `ProcessChain` refuses it (`PULSE_CHAIN_NOT_MERGEABLE`). Predict adds `PULSE_ADVISORY_EXTRA_PASS` per formula (`added_scans`).
- Misuse: `total()` in an expression filter or overlay formula (Compose host too) → `PULSE_FORMULA_TOTAL_SITE`; nested → `_TOTAL_NESTED`; arity ≠ 1 → `_TOTAL_ARITY`. An embedder `ExprFunction` named `total` → `PULSE_EXTENSION_NAME_RESERVED`.
- Compiled once; bad syntax / names / types → `PULSE_FORMULA_COMPILE_ERROR` on predict and at build alike (reason in `details.compile_error`); an earlier attribute or feature output types as a number.
- The text goes in the `expression` slot; `params.expression` is not read — an empty slot is `PROCESSING_CONFIG` on predict and runtime alike.
- Categorical → STRING (`==` / `in`); `set_*` → `[]string` (`"a" in tags`, `has_any(tags, "a", "b")`).
- `decimal128` is exact: `price / cost`, `price * 1.1` and comparisons keep every digit (`decimal-formulas`); the column takes the nearest f64. `float(price)` opts out; `price / 0` → `PULSE_DECIMAL_DIVIDE_BY_ZERO`.
- `date` binds epoch days, `datetime` epoch seconds: `weekday` / `month_index` / `date_trunc` read days, so a bare datetime there → `PULSE_FORMULA_COMPILE_ERROR`; wrap it in `to_day` (request zone; `calendar-formulas`).
- A literal `coef("m", …)` of a compose fit that failed on its data: null on every row + `PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}`; a non-literal fit name reaching it is refused with the fit's code.
- `%` on a field: float `math.Mod` (`score % 1`, dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `attribute-composition`, `expression-language` (maths / vector functions, `ExprFunctions`, `lookup(...)`), `decimal-formulas` (decimal arithmetic), `calendar-formulas` (calendar helpers, `to_day`)
