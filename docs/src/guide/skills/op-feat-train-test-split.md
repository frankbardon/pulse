```yaml
name: op-feat-train-test-split
description: Deterministic split-assignment column (0=train, 1=val, 2=test) for ML workflows.
kind: operator
category: FEAT
operator: FEAT_TRAIN_TEST_SPLIT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, feature-pipeline, pre-filter]
```

Feature operators emit derived columns; no `Response.Components`.

## Use when

Labels every record train (0), validation (1) or test (2) by a seeded shuffle or by time, to check a model on records it never learned from.

Questions it answers:

- How do I hold back 20% of records to test a model on?
- Can I split records so each class keeps its share in train and test?

Use something else:

- `capability:sample` when you just want a random subset of rows to look at.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ratios` | list[float] | (required) | 2 → train + val; 3 → all three. Sum `1.0` (±`1e-6`). |
| `seed` | int | `0` | Same seed ⇒ byte-identical assignments. Random only. |
| `stratify` | string | — | Categorical or `packed_bool`; ratios per level. Random only. |
| `mode` | enum | `random` | `random` shuffles; `temporal` cuts at a time boundary. |
| `order_by` | string | — | Temporal only: `date`, `datetime` or unsigned integer field. |

## Inputs

`Field` unused. `stratify`: `categorical_*` / `packed_bool`. `order_by`: `u4`–`u64`, `date`, `datetime`.

## Output

`f64` column `Label` (default `split`): `0` train, `1` val, `2` test.

## Contract

- Types (`stratify`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Bad `ratios` (length, negative, sum) → `PROCESSING_CONFIG`.
- Random: global pass (O(rows)), streams via `iter.Reset()`; stratified groups seed distinctly.
- Temporal: earliest rows → train, latest → test; ties never straddle a boundary (it snaps to the nearest value edge). Null `order_by` → null split (in no partition). Buffered. `seed` / `stratify` set, `order_by` missing, or `order_by` outside temporal → `PROCESSING_CONFIG`.
- Place `FEAT_TARGET_ENCODE` AFTER the split with `fit_on: "split == 0"`, else `PULSE_FEAT_TARGET_LEAKAGE_RISK`.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-feat-target-encode`](op-feat-target-encode.md), [`op-filter-include`](op-filter-include.md)
