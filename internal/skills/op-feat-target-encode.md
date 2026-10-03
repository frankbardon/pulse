---
name: op-feat-target-encode
description: Replace each categorical value with the smoothed mean of a numeric Target field for that category. Leakage trap — see Gotchas.
kind: operator
category: FEAT
operator: FEAT_TARGET_ENCODE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, feature-pipeline, leakage-risk, leakage-safe, pre-filter]
---

Feature operators emit derived columns; no `Response.Components`.

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

## Gotchas

- **TARGET LEAKAGE TRAP — `PULSE_FEAT_TARGET_LEAKAGE_RISK`.** Means use EVERY row's target (test rows, own row). Predict warns (errors under `--strict`) without a preceding `FEAT_TRAIN_TEST_SPLIT`, but the encoder reads NO split column: the split only silences it, and a `split == 0` filter keeps leaked means (pre-filter). See `feature-engineering`.
- Missing `target`, non-numeric `target`, or `smoothing < 0` → `PROCESSING_CONFIG`.
- GLOBAL-PASS: PrePass tallies per-category (sum, count) + global (sum, count), Finalize freezes `globalMean`, EmitRow is O(1). Streamable via `iter.Reset()`; file-backed iterators pay a second I/O.
- Zero non-null targets → every row null. Category with all targets null → `globalMean`.

## See

- `pulse_examples_search tags=[leakage-safe]`, `tags=[leakage-risk]`
- Skills: `feature-engineering` (target-leakage trap), `op-feat-train-test-split`, `op-feat-frequency-encode`
