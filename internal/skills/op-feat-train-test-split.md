---
name: op-feat-train-test-split
description: Deterministic split-assignment field (0=train, 1=val, 2=test) for ML workflows.
kind: operator
category: FEAT
operator: FEAT_TRAIN_TEST_SPLIT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, feature-pipeline, pre-filter]
---

Feature operators write derived fields before filters run; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ratios` | list[float] | (required) | 2 → train + val; 3 → all three. Sum `1.0` (±`1e-6`). |
| `seed` | int | `0` | Same seed ⇒ byte-identical assignments. Random only. |
| `stratify` | string | — | Categorical or `packed_bool`; ratios per level. Random only. |
| `mode` | enum | `random` | `random` shuffles; `temporal` cuts at a time boundary. |
| `order_by` | string | — | Temporal only: `date`, `datetime` or unsigned integer field. |

## Inputs

`field` unused (optional). `stratify`: `categorical_*` / `packed_bool`. `order_by`: `u4`–`u64`, `date`, `datetime`.

## Output

One derived field at `label` (default `split`): `0` train, `1` val, `2` test.

<!-- generated: contract -->

## Gotchas

- Bad `ratios` (length, negative, sum) → `PROCESSING_CONFIG`.
- Random: global pass, streams (a file-backed cohort is read twice); stratified groups seed distinctly.
- Temporal: earliest rows → train, latest → test; ties never straddle a boundary (it snaps to the nearest value edge). Null `order_by` → null split (in no partition). Buffered. `seed` / `stratify` set, `order_by` missing, or `order_by` outside temporal → `PROCESSING_CONFIG`.
<!-- feature: FEAT_TARGET_ENCODE -->
- Place `FEAT_TARGET_ENCODE` AFTER the split with `fit_on: "split == 0"`, else `PULSE_FEAT_TARGET_LEAKAGE_RISK`.
<!-- /feature -->

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-target-encode`, `op-filter-include`
