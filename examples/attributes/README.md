# Attribute Examples

Runnable JSON requests demonstrating each `ATTR_*` operator. Attributes
run after filtering and add per-record derived columns that downstream
groups, aggregations, and windows can reference.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./examples/attributes/run-all.sh
```

## Catalog

| File | Operator | Demonstrates |
|---|---|---|
| `01_zscore.json` | `ATTR_ZSCORE` | Standard score `(x - μ) / σ` per record |
| `02_tscore.json` | `ATTR_TSCORE` | Linear transform `z*10 + 50` (psychometric scale) |
| `03_normalized.json` | `ATTR_NORMALIZED` | Min-max normalize to `[0, 1]` |
| `04_percentile.json` | `ATTR_PERCENTILE` | Per-record percentile rank in the cohort |
| `05_formula.json` | `ATTR_FORMULA` | Arbitrary expression via `expr-lang/expr` (`income / 12`) |
| `06_date_part.json` | `ATTR_DATE_PART` | Extract `year_month` from a date field for grouping |

See `pulse skills show attribute-composition` for the full operator
contract, the `ATTR_FORMULA` allowlist, and composition rules.
