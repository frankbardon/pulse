---
name: op-feat-frequency-encode
description: Replace each categorical value with its observed relative frequency in the cohort.
kind: operator
category: FEAT
operator: FEAT_FREQUENCY_ENCODE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, cardinality-analysis, pre-filter]
---

Feature operators write derived fields before filters run; no `Response.Components`.

<!-- generated: use-when -->

## Params

None; `params` is unused.

## Inputs

`field` (required) — `categorical_u8`, `categorical_u16`, `categorical_u32`, or `packed_bool` (levels `"0"`/`"1"`).

## Output

One derived field at `label` (default `FREQ_<field>`). Value per row = `count[category] / total_non_null` over the FULL cohort (pre-filter). Range `(0, 1]` for non-null inputs.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- GLOBAL-PASS: a first pass tallies categories cohort-wide, the second encodes each row. Streams (a file-backed cohort is read twice).
- Frequencies reflect the RAW cohort because FEAT runs PRE-FILTER. To encode within a filtered subset, write the subset to a new cohort first (`pulse cohort filter`).
- Cohort with zero non-null categories → every row emits `null`.
- Null categories on individual rows emit `null`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`, `tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-one-hot`, `op-feat-target-encode`
