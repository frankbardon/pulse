---
name: expression-language
description: The per-row expression environment shared by the formula attribute and the expression filter — field binding by type, operators, built-in functions, set helpers, null handling, embedder functions and lookup tables. Not request templating.
type: guide
kind: design
applies_to: process, compose, predict
covers: [expression, expr-lang, formula, set-helpers, vector-functions, lookup, ExprFunctions, coef]
---

# Expression language

Two operators evaluate an `expr-lang/expr` string once per row: the formula attribute (a new column) and the expression filter (truthy keeps the row). One environment serves both; only null handling differs.

## Field binding

A field is named bare; what it binds depends on its schema type:

| Field type | Binds as | Write |
|---|---|---|
| numeric (`u*`, `f*`) | number | `weight_kg / (height_m ** 2)` |
| `categorical_*` | dictionary STRING, never the index | `brand == "Apple"`, `region in ["NA", "EU"]` |
| `set_*` | sorted `[]string` of member labels | `"a" in tags`, `has_any(tags, "a", "b")` |
| `decimal128` | exact decimal — operators and numeric functions (`decimal-formulas`) | `price / cost > 1.2`, `float(price) * 1.5` |
| `date` / `datetime` | epoch days / seconds; `weekday` `month_index` `date_trunc` `to_day` (`calendar-formulas`) | `month_index(order_day)` |

Arithmetic on a categorical errors; branch on the label.

## Operators and functions

- **Operators**: arithmetic (`+ - * / % **`), comparison, logical, membership / pattern (`in`; string-only `contains`, `startsWith`, `endsWith`, `matches`), range (`..`), nil-coalesce (`??`), ternary.
- **Functions**: numeric (`abs`, `ceil`, `floor`, `round`, `min`, `max`, `sum`, `mean`, `median`), cast (`int`, `float` — a decimal's f64 escape, `string`), collection (`len`, `keys`, `values`, `concat`, `sort`, `uniq`, `filter`, `map`, `reduce`, `all`, `any`), string (`join`, `split`, `replace`, `trim`, `lower`, `upper`, `hasPrefix`, `hasSuffix`, `indexOf`), JSON / time (`toJSON`, `fromJSON`, `now`, `date`, `duration`).
- **Set helpers**: `has_any`, `has_all`, `has_none`, `popcount`, `set_union`, `set_intersect`, `set_diff`, `set_xor`. `contains` is a string keyword, not a set helper: use `"x" in tags`.
- `%` on a field is float `math.Mod` (dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.

## Vector and maths built-ins

A vector is a literal list, `[5, 5, 1, 1]` or `[q1, q2, q3]`; a `nil` element is NaN.<!-- feature: capability:matrices --> A request's named vector binds by name, `vmean(battery)` (null member → NaN); `cosine` / `dist` on a poorly fitting `kind` warns (`vector-metrics`).<!-- /feature --> Reductions skip NaN; none valid (or empty) → NaN, `vsum` too.

| Function | Result |
|---|---|
| `vsum` `vmean` `vmin` `vmax` `norm` (L2) | float |
| `vcount` | int count of valid elements |
| `vsd` | sample SD (ddof 1); < 2 valid → NaN |
| `dot` `dist` `cosine` (vector pair) | pairwise-complete; `dist` (euclidean) rescales by p/p_observed; `cosine` is the similarity, zero norm → NaN; unequal lengths → `PROCESSING_VECTOR_LENGTH_MISMATCH` (build + predict) |
| `argmax` `argmin` | 0-based int index; none valid → NaN |
| `vcenter` `vzscore` `vnormalize` (mode `"l1"` / `"l2"`, case-sensitive) | a vector, only inside a call: `vmax(vzscore([q1, q2, q3]))`; top-level, a wrong arity or a literal bad mode → `PROCESSING_CONFIG` (build + predict) |
| `sqrt` `log` (natural) `log1p` `exp` `pow(score, 2)` | IEEE scalar (`sqrt(-1)` NaN); all an overlay formula admits |
| `hill(score, 40, 2)` (x, k, s) | `x^s / (x^s + k^s)`; null → null; `x < 0`, `k`/`s <= 0` → NaN |
| `jaccard` `dice` `overlap` `hamming` (two sets: field or `["a", "b"]`, matched by label; a dictionary-less set pairs only with another) | float, `hamming` int; null set → NaN; two empty → NaN, `hamming` 0 |
| `ref` (table, key) | a reference profile, aligned by label<!-- feature: capability:matrices --> (`vector-metrics`)<!-- /feature --> |

A non-finite result is `null` on the wire. Reserved names: an embedder `ExprFunctions` entry using one → `PULSE_EXTENSION_NAME_RESERVED`.

## Null handling

A null field binds `nil`. Guard with `x ?? 0` or `x == nil ? a : b`. An unguarded `nil`, by host:

- **Formula** — arithmetic on `nil` raises `PROCESSING_RUNTIME`; a `nil` result is a null value.
- **Filter** — `==` is false, `!=` true; `>` / `+` / `len` on `nil` make the predicate UNKNOWN and the row is dropped.

Compiled once, predict included: bad syntax, an unknown name or a type error is `PULSE_FORMULA_COMPILE_ERROR` pre-run (`details.compile_error`); derived columns are numbers.

## Embedder extensions

`pulse.Options.Extensions.ExprFunctions` adds functions; `LookupTables` is reachable as `lookup(table, keys...)` — an unknown table is `PULSE_LOOKUP_TABLE_UNKNOWN`, a missing key `PULSE_LOOKUP_MISS`; `ReferenceVectors` as `ref`.

<!-- feature: capability:compose_fits -->
## Bound models

`coef(fit, term[, stat])` reads a fit's figure, full precision: `term` a predictor, `"(intercept)"` or a registered model's own coefficient; `stat` `point` (default), `lower` / `upper` (at `conf_level`) or `se`. E.g. `coef("m", "tv") * tv`. Codes: `PULSE_MODEL_UNKNOWN`, `_TERM_UNKNOWN`, `_STAT_UNKNOWN` (literal args: predict too; a registered model's `fits` term: run only), `_INTERVALS_UNAVAILABLE` (no SEs). Missing: NaN; a failed fit nulls a formula (`PULSE_FIT_UNAVAILABLE`), refuses a filter. An embedder `coef` shadows it.
<!-- /feature -->

## Patterns

```json
{"label": "bmi", "expression": "weight_kg / (height_m ** 2)"}
```

A formula's result is coerced to a scalar (`bool` → `1.0` / `0.0`); a filter's must be `bool`: typed non-bool fails compile, untyped `PROCESSING_RUNTIME`.

## Gotchas

- A formula sees source fields, feature output and EARLIER attribute labels, only.
- `total(expr)` — cohort-wide post-filter sum, ONLY in the formula attribute (`PULSE_FORMULA_TOTAL_SITE` in a filter or overlay formula); not overridable by `ExprFunctions`. Weights, the extra scan, recipes: `formula-totals`.
- Filters run before attributes, so a filter expression cannot read a derived column — restate it over source fields.

## See

- `attribute-composition` — where the formula attribute sits among attributes.
- `aggregation-design` — filter chaining and per-stage counters.
- `formula-totals` — `total(expr)` in a formula.
<!-- feature: capability:templates -->
- `request-templating` — the request-authoring parameters this language is not.
<!-- /feature -->
- `docs/src/internals/extension-points.md` — `ExprFunctions` + `LookupTables`.
