```yaml
name: op-feat-target-encode
description: Replace each categorical value with the smoothed mean of a numeric Target field for that category. Leakage trap — see Gotchas.
kind: operator
category: FEAT
operator: FEAT_TARGET_ENCODE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, feature-pipeline, leakage-risk, pre-filter]
```

Feature operators emit derived columns; no `Response.Components`.

## Use when

Replaces each category with the average outcome of the records in that category, optionally pulled toward the overall average.

Questions it answers:

- How do I give a model a single number for each of a thousand postcodes?
- What is the average sale price of each product category, on every record?

Use something else:

- `AGG_AVERAGE` when you want to report each category's average outcome.

## Params

- `target` — string, required. Numeric field whose grouped mean replaces the category.
- `smoothing` — float, default `0.0`. Additive prior weight toward the global target mean; `>= 0`.

```
encoded = (count_cat * mean_cat + smoothing * mean_global) / (count_cat + smoothing)
```

`smoothing=0` → unsmoothed per-category mean. Larger values pull rare categories toward the cohort mean.

## Inputs

`Field` — `categorical_u8`, `categorical_u16`, `categorical_u32`. `params.target` — numeric (categorical rejected at construction).

## Output

One `f64` column at `Label` (default `TARGET_<field>`): the (smoothed) mean of `target` over rows sharing the categorical value.

## Reading the output

- `value`: The average of the outcome field over records in this record's category, in the outcome's units; with smoothing s, (n * category average + s * overall average) / (n + s), so a category with few records sits nearer the overall average.
  - Caveat: Target leakage: every average includes the record's own outcome and the outcomes of validation and test records, because the encoder reads no split column. Placing FEAT_TRAIN_TEST_SPLIT first does not change a single value, and filtering to split 0 afterwards keeps the leaked figures.

## Gotchas

- **TARGET LEAKAGE — `PULSE_FEAT_TARGET_LEAKAGE_RISK`** on every use (errors under `--strict`). Means use EVERY row's target (test rows, own row): no split column is read, so a prior split or `split == 0` filter changes nothing. See [`feature-engineering`](feature-engineering.md).
- Missing `target`, non-numeric `target`, or `smoothing < 0` → `PROCESSING_CONFIG`.
- GLOBAL-PASS: PrePass tallies per-category + global (sum, count); Finalize freezes `globalMean`. Streamable via `iter.Reset()` (file-backed: second I/O).
- Zero non-null targets → every row null. Category with all targets null → `globalMean`.

## See

- `pulse_examples_search tags=[leakage-risk]`
- Skills: [`feature-engineering`](feature-engineering.md) (target-leakage trap), [`op-feat-train-test-split`](op-feat-train-test-split.md), [`op-feat-frequency-encode`](op-feat-frequency-encode.md)
