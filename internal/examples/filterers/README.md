# Filterer Examples

Runnable JSON requests demonstrating each `FILTER_*` operator. Filters
run before attributes, groups, and aggregations to reduce the active
record set.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./examples/filterers/run-all.sh
```

## Catalog

| File | Operator | Demonstrates |
|---|---|---|
| `01_include.json` | `FILTER_INCLUDE` | Whitelist categorical values (`region in [north, south]`) |
| `02_exclude.json` | `FILTER_EXCLUDE` | Blacklist categorical values |
| `03_range.json` | `FILTER_RANGE` | Numeric range `[min, max]` (inclusive) |
| `04_expression.json` | `FILTER_EXPRESSION` | Compound predicate via `expr-lang/expr` |

`FILTER_INCLUDE`/`FILTER_EXCLUDE` compare against the resolved
dictionary string for categorical fields. `FILTER_RANGE` accepts
numeric strings; predict coerces them. `FILTER_EXPRESSION` does not
require a `field` and supports the same operator/function allowlist as
`ATTR_FORMULA`.
