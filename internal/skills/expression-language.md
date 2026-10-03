---
name: expression-language
description: The per-row expression environment shared by the formula attribute and the expression filter — field binding by type, operators, built-in functions, set helpers, null handling, embedder functions and lookup tables. Not request templating.
type: guide
kind: design
applies_to: process, compose, predict
covers: [expression, expr-lang, formula, set-helpers, lookup, ExprFunctions]
---

# Expression language

Two operators evaluate an `expr-lang/expr` (v1.17.x) string once per row: the formula attribute (the result becomes a new column) and the expression filter (a truthy result keeps the row). Both compile through one environment, so everything below holds for both; only null handling differs. Per-operator params live in their atomic `op-*` skills.

This is NOT request templating: `$var` / `{{}}` / `$when` are substituted into the request before decode (`request-templating`); an expression runs over row fields at execution time. There is no interop.

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
- **No** `sqrt` / `log` / `exp` / `pow` / trig — use `**` for powers (`x ** 0.5`), or derive the column upstream with a feature transform.
- `%` on a field is float `math.Mod` (dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.

## Null handling

A null field binds `nil`. Guard with `x ?? 0` or `x == nil ? a : b`. What an unguarded `nil` does depends on the host:

- **Formula** — arithmetic on `nil` raises `PROCESSING_RUNTIME`; no number is invented.
- **Filter** — `==` is false, `!=` true; `>` / `+` / `len` on `nil` make the predicate UNKNOWN and the row is dropped.

The program compiles once per request: bad syntax or a type error is `PROCESSING_RUNTIME` at build, before any row is read.

## Embedder extensions

`pulse.Options.Extensions.ExprFunctions` merges custom functions into the environment; `LookupTables` is reachable as `lookup(table, keys...)` — an unknown table is `PULSE_LOOKUP_TABLE_UNKNOWN`, a missing key `PULSE_LOOKUP_MISS`. Both hosts see them. Recipe: `docs/src/internals/extension-points.md`.

## Patterns

```json
{"label": "bmi", "expression": "weight_kg / (height_m ** 2)"}
```

```json
{"label": "is_premium", "expression": "brand in [\"Apple\", \"Samsung\"] ? 1 : 0"}
```

A formula's result is coerced to a scalar (`bool` → `1.0` / `0.0`); a filter's must be `bool` — a non-bool on a non-null row is `PROCESSING_RUNTIME`.

## Gotchas

- No chaining: an expression sees source fields (and pre-filter feature output), never another attribute's label. Stage via Compose / ProcessChain.
- Filters run before attributes, so a filter expression cannot read a derived column — restate it over source fields.

## See

- `attribute-composition` — where the formula attribute sits among attributes.
- `aggregation-design` — filter chaining and per-stage counters.
- `request-templating` — the request-authoring parameters this language is not.
- `docs/src/internals/extension-points.md` — `ExprFunctions` + `LookupTables`.
