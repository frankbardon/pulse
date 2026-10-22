---
name: op-feat-target-encode
description: Replace each categorical value with the smoothed mean of a numeric Target field over the training rows (fit_on), optionally out-of-fold. Leakage trap — see Gotchas.
kind: operator
category: FEAT
operator: FEAT_TARGET_ENCODE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, feature-pipeline, leakage-risk, pre-filter]
---

Derived column; no `Response.Components`.

<!-- generated: use-when -->

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

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **LEAKAGE:** without `fit_on`, `PULSE_FEAT_TARGET_LEAKAGE_RISK` (strict refuses; `folds` alone rewords it) — the trap: `feature-engineering`.
- `folds` 1 / >100, `rng_seed` without `folds`, blank `fit_on`, missing `target` or `smoothing < 0` → `PROCESSING_CONFIG`; a categorical / `set_*` `target` → `PULSE_FIELD_TYPE_UNSUPPORTED`.
- GLOBAL-PASS, streams — unless `field`, `target` or a `fit_on` name is an earlier feature's column: then buffered (`streamable_reasons`).
- No training target → every row null. Weights are ignored.

## See

- `pulse_examples_search tags=[leakage-risk]`
- Skills: `feature-engineering`, `op-feat-train-test-split`, `expression-language`, `op-feat-frequency-encode`
