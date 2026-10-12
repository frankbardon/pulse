```yaml
name: expression-language
description: The per-row expression environment shared by the formula attribute and the expression filter — field binding by type, operators, built-in functions, set helpers, null handling, embedder functions and lookup tables. Not request templating.
type: guide
kind: design
applies_to: process, compose, predict
covers: [expression, expr-lang, formula, set-helpers, vector-functions, lookup, ExprFunctions, coef]
```

# Expression language

Two operators evaluate an `expr-lang/expr` (v1.17.x) string once per row: the formula attribute (the result is a new column) and the expression filter (a truthy result keeps the row). Both compile through one environment, so everything below holds for both; only null handling differs.

This is NOT request templating: `$var` / `{{}}` / `$when` are substituted into the request before decode ([`request-templating`](request-templating.md)); an expression runs over row fields at execution time.

## Field binding

Fields are referenced by bare name; the value a name binds depends on the schema type:

| Field type | Binds as | Write |
|---|---|---|
| numeric (`u*`, `f*`) | number | `weight_kg / (height_m ** 2)` |
| `categorical_*` | dictionary STRING, never the index | `brand == "Apple"`, `region in ["NA", "EU"]` |
| `set_*` | sorted `[]string` of member labels | `"a" in tags`, `has_any(tags, "a", "b")` |
| `decimal128` | `Decimal128` | — |

Arithmetic on a categorical errors — branch on the label and emit a number.

## Operators and functions

- **Operators**: arithmetic (`+ - * / % **`), comparison, logical (`and or not`), membership / pattern (`in`; string-only `contains`, `startsWith`, `endsWith`, `matches`), range (`..`), nil-coalesce (`??`), ternary.
- **Functions**: numeric (`abs`, `ceil`, `floor`, `round`, `min`, `max`, `sum`, `mean`, `median`), cast (`int`, `float`, `string`), collection (`len`, `keys`, `values`, `concat`, `sort`, `uniq`, `filter`, `map`, `reduce`, `all`, `any`), string (`join`, `split`, `replace`, `trim`, `lower`, `upper`, `hasPrefix`, `hasSuffix`, `indexOf`), JSON / time (`toJSON`, `fromJSON`, `now`, `date`, `duration`).
- **Set helpers**: `has_any`, `has_all`, `has_none`, `popcount`, `set_union`, `set_intersect`, `set_diff`, `set_xor`. `contains` is a string operator keyword, never a set helper — test membership with `"x" in tags`.
- `%` on a field is float `math.Mod` (dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.

## Vector and maths built-ins

A vector is a literal list, `[5, 5, 1, 1]` or `[q1, q2, q3]`; a `nil` element is NaN. A request's named vector binds by name, `vmean(battery)` (null member → NaN); `cosine` / `dist` on a poorly fitting `kind` warns ([`vector-metrics`](vector-metrics.md)). Reductions skip NaN; none valid (or empty) → NaN, `vsum` too.

| Function | Result |
|---|---|
| `vsum` `vmean` `vmin` `vmax` `norm` (L2) | float |
| `vcount` | int count of valid elements |
| `vsd` | sample SD (ddof 1); < 2 valid → NaN |
| `dot` `dist` `cosine` (vector pair) | pairwise-complete; `dist` (euclidean) rescales by p/p_observed; `cosine` is the similarity, zero norm → NaN; unequal lengths → `PROCESSING_VECTOR_LENGTH_MISMATCH` |
| `argmax` `argmin` | 0-based int index; none valid → NaN |
| `vcenter` `vzscore` `vnormalize` (mode `"l1"` / `"l2"`, case-sensitive) | a vector, only inside a call: `vmax(vzscore([q1, q2, q3]))`; top-level → `PROCESSING_CONFIG` |
| `sqrt` `log` (natural) `exp` `pow(score, 2)` | IEEE scalar (`sqrt(-1)` NaN); the only built-ins an overlay formula admits |
| `jaccard` `dice` `overlap` `hamming` (two sets: field or `["a", "b"]`, matched by label; a dictionary-less set pairs only with another) | float, `hamming` int; null set → NaN; two empty → NaN, `hamming` 0 |
| `ref` (table, key) | a reference profile, aligned by label ([`vector-metrics`](vector-metrics.md)) |

A non-finite result is `null` on the wire. These names are reserved: an embedder `ExprFunctions` entry using one → `PULSE_EXTENSION_NAME_RESERVED`.

## Null handling

A null field binds `nil`. Guard with `x ?? 0` or `x == nil ? a : b`. What an unguarded `nil` does depends on the host:

- **Formula** — arithmetic on `nil` raises `PROCESSING_RUNTIME`; a `nil` result is a null value.
- **Filter** — `==` is false, `!=` true; `>` / `+` / `len` on `nil` make the predicate UNKNOWN and the row is dropped.

The program compiles once per request: bad syntax or a type error is `PROCESSING_RUNTIME` at build, before any row is read.

## Embedder extensions

`pulse.Options.Extensions.ExprFunctions` merges custom functions into the environment; `LookupTables` is reachable as `lookup(table, keys...)` — an unknown table is `PULSE_LOOKUP_TABLE_UNKNOWN`, a missing key `PULSE_LOOKUP_MISS`; `ReferenceVectors` as `ref`. Recipe: `docs/src/internals/extension-points.md`.

## Bound models

`coef(fit, term[, stat])` reads a compose fit's figure at full precision: `term` is a predictor or `"(intercept)"`; `stat` is `point` (default), `lower` / `upper` (interval at the fit's `conf_level`) or `se`. Marginal return: `coef("mmm", "tv_sat") * tv / (1 + tv)`. Codes: `PULSE_MODEL_UNKNOWN`, `_TERM_UNKNOWN`, `_STAT_UNKNOWN` (predict checks literal arguments), `_INTERVALS_UNAVAILABLE` (fit has no standard errors). A figure the fit lacks for one term is NaN, never 0. An embedder `coef` shadows it.

## Patterns

```json
{"label": "bmi", "expression": "weight_kg / (height_m ** 2)"}
```

A formula's result is coerced to a scalar (`bool` → `1.0` / `0.0`); a filter's must be `bool` — a non-bool on a non-null row is `PROCESSING_RUNTIME`.

## Gotchas

- A formula sees source fields, feature output and EARLIER attribute labels (declared order), never a later one.
- `total(expr)` — cohort-wide sum of a row-level expression over post-filter rows; allowed ONLY in the formula attribute (`PULSE_FORMULA_TOTAL_SITE` in a filter or overlay formula). One argument, no nesting; unweighted; nulls skipped. Makes the formula two-pass (one extra scan). Not overridable by `ExprFunctions`.
- Filters run before attributes, so a filter expression cannot read a derived column — restate it over source fields.

## See

- [`attribute-composition`](attribute-composition.md) — where the formula attribute sits among attributes.
- [`aggregation-design`](aggregation-design.md) — filter chaining and per-stage counters.
- [`request-templating`](request-templating.md) — the request-authoring parameters this language is not.
- `docs/src/internals/extension-points.md` — `ExprFunctions` + `LookupTables`.
