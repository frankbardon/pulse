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
| `16_scale_score.json` | `ATTR_SCALE_SCORE` | A rating-battery score per row from a kind `scale` vector: the valid-item mean (an out-of-range value is missing and warned), a reverse-keyed item by label, and the prorated sum over inline fields |
| `17_profile_similarity.json` | `ATTR_PROFILE_SIMILARITY` | Each row's similarity to a target profile: the kind `scale` default (correlation) against a literal, and an explicit cosine against a label map matched by label (warned as a poor fit) |
| `18_vec_distance.json` | `ATTR_VEC_DISTANCE` | Each row's distance to a benchmark: the kind `measure` default (std_euclidean, a two-pass fit), mahalanobis fitted on the control arm, and a row-local manhattan over inline fields |
| `19_centroid_reference.json` | `ATTR_VEC_DISTANCE`, `ATTR_PROFILE_SIMILARITY` | References computed from the data: each row's distance to its own arm's centroid (`{centroid: {by}}`) and its similarity to the control arm's centroid (`{centroid: {filter}}`) — a two-pass weighted per-member mean |
| `20_record_reference.json` | `ATTR_VEC_DISTANCE` | Each row's distance to one stored record (`{record: {keys}}`), point-looked-up before the run through the cohort's sidecar index on `id` (built by `fixtures/build.sh`) |
| `21_retention_month_index.json` | `ATTR_FORMULA` | Calendar-month retention on `subscribers.pulse`: `month_index(first_repeat_order) - month_index(signup_date)` and month-1 / within-quarter flags, no hard-coded epoch-day ranges |
| `22_decimal_margin.json` | `ATTR_FORMULA`, `FILTER_EXPRESSION` | Exact money on `decimal128` fields: keep `revenue > cost * 2`, then the margin ratio `(revenue - cost) / revenue` divided exactly at four places |
| `23_date_part_iso_weekday.json` | `ATTR_DATE_PART` | Local ISO weekday number of a `datetime` (`part: iso_weekday`, 1 = Monday .. 7 = Sunday) as a Monday-first grouping key |
| `24_previous_state_transitions.json` | `ATTR_PREVIOUS_STATE` | Each row's plan at the same customer's previous snapshot on the `plan_history.pulse` long panel, typed as `plan`, crossed with `plan` for a from × to count of plan moves |
| `25_sem_score.json` | `ATTR_SEM_SCORE` | Latent scores of a two-wave scalar-invariant SEM (each row scored with its own wave's parameters), regression and Bartlett, averaged by wave — the latent mean shift MAT_SEM reports |
| `26_sem_ordinal_score.json` | `ATTR_SEM_SCORE` | Empirical Bayes modal (EBM) scores of a two-wave ordinal (`estimator: "wlsmv"`) SEM with thresholds, loadings and intercepts equal, averaged by wave — value down and service up in wave2 |
| `27_reg_influence.json` | `ATTR_REG_INFLUENCE`, `ATTR_REG_RESIDUAL` | Cook's distance and DFFITS of an OLS refit (`params.measure`), beside the externally studentized residual (`params.kind: studentized`) |

See `pulse skills show attribute-composition` for the full operator
contract, the `ATTR_FORMULA` allowlist, and composition rules.
