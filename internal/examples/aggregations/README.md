# Aggregations Examples

`AGG_*` aggregations over the shared fixture cohorts (see [the fixture table](../README.md#available-cohorts)): decimal, narrow-numeric, categorical, set and vector fields, shape moments, nulls, intervals, model error and ordered categoricals. Each file's `_meta.description` says what it shows; `pulse examples search --category aggregations` lists them.

| File | Touches |
|---|---|
| `01_decimal_sum.json` | `AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX` on `decimal128` (`all_types.pulse`) |
| `02_decimal_variance.json` | Native decimal128 `AGG_VARIANCE` and `AGG_STDDEV` (banker-rounded sqrt) |
| `04_all_narrow_types.json` | One aggregation on each narrow numeric type (u8/u16/u32/u64, f32/f64, u4, packed_bool) |
| `05_categorical_breakdown.json` | `AGG_DISTINCT_COUNT` / `AGG_MODE` across categorical_u8/u16/u32 plus `date` |
| `06_sharded_anchor.json` | One shard of a shard archive by anchor (`transactions_sharded.pulse#wave1.pulse`) |
| `07_set_card_issuers.json` | Every `AGG_SET_*` aggregator on a `set_u8` multi-select after `FILTER_SET_CONTAINS_ANY` |
| `08_welford_components.json` | `AGG_WELFORD` row value and its operator components |
| `09_categorical_mode_count.json` | `AGG_MODE_COUNT` beside `AGG_MODE` per city |
| `10_shape_moments.json` | Story: `AGG_STDDEV`, `AGG_SKEWNESS`, `AGG_KURTOSIS` on income |
| `11_null_count.json` | `AGG_NULL_COUNT` beside `AGG_COUNT` on a nullable field |
| `12_percentile_p95.json` | `AGG_PERCENTILE` (p95) against the mean and max |
| `13_range_spread.json` | `AGG_RANGE` beside `AGG_STDDEV` |
| `14_ratio_arpu_per_treatment.json` | `AGG_RATIO` (revenue per session-minute) per arm |
| `15_weighted_mean.json` | `AGG_WEIGHTED_MEAN` beside `AGG_AVERAGE` |
| `16_zscore_grouped.json` | `AGG_ZSCORE` per region (the figures are its components) |
| `17_confidence_interval.json` | Story: `AGG_CI_LOWER` / `AGG_CI_UPPER` around the mean |
| `18_distinct_sum_per_subject.json` | `AGG_DISTINCT_SUM` once per subject against `AGG_SUM` |
| `19_value_frequency.json` | `AGG_FREQUENCY` of one value per city |
| `20_model_error_per_region.json` | `AGG_MAPE` / `AGG_RMSE` / `AGG_MAE` against an `ATTR_REG_FITTED` prediction |
| `21_vector_profile_per_arm.json` | `AGG_VEC_MEAN` / `AGG_VEC_SUM` per arm |
| `22_vector_sd_per_arm.json` | `AGG_VEC_MEAN` beside `AGG_VEC_SD` (per-member sample sd, `params.expand` element columns) per arm |
| `23_auc_default_score.json` | `AGG_AUC` (field = a logistic default score, `params.outcome` = the `defaulted` flag; gini and the DeLong interval in components) per plan |
| `24_missing_by_channel.json` | Story: `AGG_COUNT` beside `AGG_NULL_COUNT` on the nullable `income` / `age` fields per intake channel |
| `25_constant_target.json` | `AGG_CONSTANT` (an `f64` target from `params.target`, a literal `date`) beside `AGG_SUM` per arm |
| `26_ordered_plan_tier.json` | Ordered-categorical `AGG_MEDIAN` / `AGG_MIN` / `AGG_PERCENTILE` (`params.levels`, R type 1 over level ranks; the figure is the level's dictionary index) per arm |

```bash
./internal/examples/aggregations/run-all.sh
```
