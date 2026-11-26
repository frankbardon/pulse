# Filterer Examples

Runnable JSON requests demonstrating each `FILTER_*` operator. Filters
run before attributes, groups, and aggregations to reduce the active
record set.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./internal/examples/filterers/run-all.sh
```

## Catalog

| File | Operator | Demonstrates |
|---|---|---|
| `01_include.json` | `FILTER_INCLUDE` | Whitelist categorical values (`region in [north, south]`) |
| `02_exclude.json` | `FILTER_EXCLUDE` | Blacklist categorical values |
| `03_range.json` | `FILTER_RANGE` | Numeric range `[min, max]` (inclusive) |
| `04_expression.json` | `FILTER_EXPRESSION` | Compound predicate via `expr-lang/expr` |
| `05_set_contains_combinations.json` | `FILTER_SET_CONTAINS_ALL`, `FILTER_SET_CONTAINS_NONE`, `FILTER_SET_EQUALS` | Set filterers on a `set_u8` multi-select, combined with AND |
| `06_null.json` | `FILTER_NULL` | Keep present values (`values: ["is_not_null"]`) of a nullable field |
| `07_truthiness.json` | `FILTER_TRUE` | Keep rows where a `packed_bool` is true |
| `08_falsey.json` | `FILTER_FALSE` | Keep rows where a `packed_bool` is false |
| `09_date_ranges.json` | `FILTER_DATE_RANGES` | Keep dates inside inline labelled ranges |
| `10_expression_params.json` | `FILTER_EXPRESSION` | `panel == params.from_wave`: a request param read by the expression |

`FILTER_INCLUDE`/`FILTER_EXCLUDE` compare against the resolved
dictionary string for categorical fields. `FILTER_RANGE` accepts
numeric strings; predict coerces them. `FILTER_EXPRESSION` does not
require a `field` and supports the same operator/function allowlist as
`ATTR_FORMULA`.
