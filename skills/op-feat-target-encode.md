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

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | string | (required) | Numeric field whose grouped mean replaces the category. |
| `smoothing` | float | `0.0` | Additive prior weight toward the global target mean; `>= 0`. |

```
encoded = (count_cat * mean_cat + smoothing * mean_global) / (count_cat + smoothing)
```

`smoothing=0` → unsmoothed per-category mean. Larger values pull rare categories toward the cohort mean.

## Inputs

`Field` — `categorical_u8`, `categorical_u16`, `categorical_u32`. `params.target` — numeric (categorical rejected at construction).

## Output

One `f64` column at `Label` (default `TARGET_<field>`): the (smoothed) mean of `target` over rows sharing the categorical value.

## Gotchas

- **TARGET LEAKAGE TRAP — `PULSE_FEAT_TARGET_LEAKAGE_RISK`.** Encoding the whole cohort mixes validation / test signal into training rows. Predict warns (errors under `--strict` / `Options.Strict: true`) when a `FEAT_TARGET_ENCODE` has no preceding `FEAT_TRAIN_TEST_SPLIT` in the same slate. Fix: split upstream, then filter to train-only OR scope the encoder to train rows.
- Missing `target`, non-numeric `target`, or `smoothing < 0` → `PROCESSING_CONFIG`.
- GLOBAL-PASS: PrePass tallies per-category (sum, count) + global (sum, count), Finalize freezes `globalMean`, EmitRow is O(1). Streamable via `iter.Reset()`; file-backed iterators pay a second I/O.
- Zero non-null targets → every row null. Unseen-at-encode-time category (only via a re-scan over mutated data) → `globalMean`.

## See

- `pulse_examples_search tags=[leakage-safe]`, `tags=[leakage-risk]`
- Skills: `feature-engineering` (target-leakage trap), `op-feat-train-test-split`, `op-feat-frequency-encode`
