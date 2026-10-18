# Aggregations Examples

Each request runs against the shared `all_types.pulse` cohort, which carries one column for every one of the 17 supported field types. Together they exercise the type-aware aggregation code paths end to end without needing per-example fixtures.

| File | Touches |
|---|---|
| `01_decimal_sum.json` | `AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX` on `decimal128` |
| `02_decimal_variance.json` | Native decimal128 `AGG_VARIANCE` and `AGG_STDDEV` (banker-rounded sqrt) |
| `04_all_narrow_types.json` | One aggregation each on every narrow numeric type (u8/u16/u32/u64, f32/f64, nullable_*, packed_bool) |
| `05_categorical_breakdown.json` | `AGG_DISTINCT_COUNT`/`AGG_MODE` across categorical_u8/u16/u32 plus `date` |
| `22_vector_sd_per_arm.json` | `AGG_VEC_MEAN` beside `AGG_VEC_SD` (per-member sample sd, `params.expand` element columns) per treatment arm on `experiment.pulse` |
| `23_auc_default_score.json` | `AGG_AUC` (field = a logistic default score, `params.outcome` = the `defaulted` flag; gini and the DeLong interval in components) per plan on `subscribers.pulse` |

```bash
./internal/examples/aggregations/run-all.sh
```
