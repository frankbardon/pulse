---
name: op-feat-train-test-split
description: Deterministic split-assignment column (0=train, 1=val, 2=test) for ML workflows.
kind: operator
category: FEAT
operator: FEAT_TRAIN_TEST_SPLIT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, feature-pipeline, leakage-safe, pre-filter]
---

Feature operators emit derived columns; no `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ratios` | list[float] | (required) | Two elements → train + val; three → all three splits. Must sum to `1.0` within `1e-6`. |
| `seed` | int | `0` | Same seed ⇒ byte-identical assignments. |
| `stratify` | string | — | Optional categorical field; ratios applied per category for class balance. |

## Inputs

`Field` not consulted — assignments are positional / per-shuffle; may be omitted. `params.stratify` — `categorical_u8` / `u16` / `u32` only.

## Output

One `u8`-valued `f64` column at `Label` (default `split`), from `processing/feature`: `feature.SplitTrain = 0`, `feature.SplitVal = 1`, `feature.SplitTest = 2`.

## Gotchas

- `ratios` length outside `[2, 3]`, any negative ratio, a sum off `1.0` by more than `1e-6`, or a non-categorical `stratify` → `PROCESSING_CONFIG`.
- GLOBAL-PASS: PrePass collects row count + stratify keys, Finalize materialises the assignment table (O(rows) memory), EmitRow yields it in PrePass order. EmitRow over-call (more rows than PrePass) → `PROCESSING_INTERNAL`.
- Streamable via `iter.Reset()`; file-backed iterators pay a second I/O.
- Stratified mode hashes per group with `seed + len(out)*indices[0]+1`, so groups do not collapse to identical shuffles.
- LEAKAGE-SAFE WIRING: place this BEFORE any `FEAT_TARGET_ENCODE` to suppress `PULSE_FEAT_TARGET_LEAKAGE_RISK`, then `FILTER_INCLUDE` on `split == 0` to scope downstream training-only work.

## See

- `pulse_examples_search tags=[leakage-safe]`, `tags=[feature-engineering]`
- Skills: `feature-engineering` (train / test / split semantics), `op-feat-target-encode`, `op-filter-include`
