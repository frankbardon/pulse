```yaml
name: op-feat-target-encode
description: Replace each categorical value with the smoothed mean of a numeric Target field over the training rows (fit_on), optionally out-of-fold. Leakage trap — see Gotchas.
kind: operator
category: FEAT
operator: FEAT_TARGET_ENCODE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, feature-pipeline, leakage-risk, pre-filter]
```

Derived column; no `Response.Components`.

## Use when

Replaces each category with the average outcome of the records in that category, optionally pulled toward the overall average.

Questions it answers:

- How do I give a model a single number for each of a thousand postcodes?
- What is the average sale price of each product category, on every record?

Use something else:

- `AGG_AVERAGE` when you want to report each category's average outcome.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Numeric field whose mean replaces the category. |
| `smoothing` | float | `0` | `>= 0`; shrinks toward the global mean. |
| `fit_on` | string | — | Boolean expression picking the TRAINING rows (`"split == 0"`; null → not training) over the schema and EARLIER features (else `PULSE_FORMULA_COMPILE_ERROR`). Every row is encoded; unseen category → training global mean. Absent: every row trains. |
| `folds` | int | `0` | `0` (off) or `2..100`: each training row is encoded from the training rows OUTSIDE its fold (null when none hold an outcome); other rows get the full-training value. |
| `rng_seed` | int | — | Only with `folds`: seeds the fold deal. |

## Inputs

`Field` — see `## Contract` (`packed_bool` levels `"0"`/`"1"`). `params.target` — numeric.

## Output

One `f64` column at `Label` (default `TARGET_<field>`): the (smoothed) mean of `target` over the training rows sharing the categorical value.

## Reading the output

- `value`: The average of the outcome field over the training records (params.fit_on, else every record) in this record's category, in the outcome's units; with smoothing s, (n * category average + s * overall average) / (n + s), so a category with few records sits nearer the overall average.
  - Caveat: Computed over every record of the cohort before any filter runs, so a filtered result still carries figures from the records it filtered out.

## Contract

- Types (`field`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`target`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- **LEAKAGE:** without `fit_on`, `PULSE_FEAT_TARGET_LEAKAGE_RISK` (strict refuses; `folds` alone rewords it) — the trap: [`feature-engineering`](feature-engineering.md).
- `folds` 1 / >100, `rng_seed` without `folds`, blank `fit_on`, missing `target` or `smoothing < 0` → `PROCESSING_CONFIG`; a categorical / `set_*` `target` → `PULSE_FIELD_TYPE_UNSUPPORTED`.
- GLOBAL-PASS, streams — unless `field`, `target` or a `fit_on` name is an earlier feature's column: then buffered (`streamable_reasons`).
- No training target → every row null. Weights are ignored.

## See

- `pulse_examples_search tags=[leakage-risk]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-feat-train-test-split`](op-feat-train-test-split.md), [`expression-language`](expression-language.md), [`op-feat-frequency-encode`](op-feat-frequency-encode.md)
