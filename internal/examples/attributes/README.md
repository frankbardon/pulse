# Attribute Examples

Runnable JSON requests demonstrating each `ATTR_*` operator. Attributes
run after filtering and add per-record derived columns that downstream
groups, aggregations, and windows can reference.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./internal/examples/attributes/run-all.sh
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
| `10_date_part_hour.json` | `ATTR_DATE_PART` | Local wall-clock hour of a `datetime` (`part: hour`, request `time_zone`) |
| `11_code_in_top_box.json` | `ATTR_CODE_IN` | 1/0 code-list indicator; its weighted mean per crosstab cell is the share of the whole base (top-box) |
| `12_mahalanobis.json` | `ATTR_MAHALANOBIS` | Multivariate outlier score (D² and its chi-square p) across age and income; `fit_on` fits on a subset while scoring every row |
| `13_pc_score.json` | `ATTR_PC_SCORE` | Standardized principal component scores (unrotated PC1 and a varimax-rotated PC2 fitted on the control arm) averaged by treatment |
| `14_factor_score.json` | `ATTR_FACTOR_SCORE` | Factor scores (regression on a minres fit, Bartlett on an ML fit) of one factor behind three measures, averaged by treatment; a Heywood case warns |
| `15_vector_battery.json` | `ATTR_FORMULA` | A named vector read by formulas: the mean of the valid members when at least 2 of 3 answered (else null), and a flat-profile (straight-lining) flag `vsd(...) == 0` |

See `pulse skills show attribute-composition` for the full operator
contract, the `ATTR_FORMULA` allowlist, and composition rules.
