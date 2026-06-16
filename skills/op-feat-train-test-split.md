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

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ratios` | list[float] | (required) | Two- OR three-element ratio vector. Must sum to `1.0` within `1e-6`. |
| `seed` | int | `0` | RNG seed; same seed produces byte-identical assignments. |
| `stratify` | field name (string) | — | Optional categorical field; ratios are applied per category for class balance. |

Two-element ratios produce train + val (no test). Three-element ratios produce all three splits.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | not consulted — assignments are positional / per-shuffle. May be omitted. |
| `params.stratify` | `categorical_u8` / `u16` / `u32` (rejects non-categorical) |

## Output

One `u8`-valued `f64` column written to `Label` (default `split`). Constants surfaced from `processing/feature`:

```go
feature.SplitTrain = 0
feature.SplitVal   = 1
feature.SplitTest  = 2
```

## Gotchas

- `ratios` length outside `[2, 3]` → `PROCESSING_CONFIG`. Any negative ratio → `PROCESSING_CONFIG`. Sum off `1.0` by more than `1e-6` → `PROCESSING_CONFIG`.
- Non-categorical `stratify` → `PROCESSING_CONFIG`.
- GLOBAL-PASS: PrePass collects row count + stratify keys, Finalize materialises the assignment table (O(rows) memory), EmitRow yields the precomputed assignment in PrePass order. EmitRow over-call (more rows than PrePass) → `PROCESSING_INTERNAL`.
- Streamable via `iter.Reset()`; file-backed iterators pay a second I/O.
- Stratified mode hashes deterministically per group using `seed + len(out)*indices[0]+1` — different groups don't collapse to identical shuffles.
- LEAKAGE-SAFE WIRING: place THIS operator BEFORE any `FEAT_TARGET_ENCODE` to suppress `PULSE_FEAT_TARGET_LEAKAGE_RISK`. Then `FILTER_INCLUDE` on `split == 0` to scope downstream training-only computations.

## See

- `pulse_examples_search tags=[leakage-safe]`, `tags=[feature-engineering]`
- Skills: `feature-engineering` (train / test / split semantics), `op-feat-target-encode`, `op-filter-include`
