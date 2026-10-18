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

Feature operators emit derived columns; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `target` — string, required. Numeric field whose grouped mean replaces the category.
- `smoothing` — float, default `0.0`, `>= 0`: `(n * mean_cat + s * mean_global) / (n + s)`.
- `fit_on` — boolean expression (expression-filter syntax, null → not training) choosing the TRAINING rows, e.g. `"split == 0"`. Reads the schema and EARLIER features' columns; a later or own label, or a non-bool result, is `PULSE_FORMULA_COMPILE_ERROR`. Only training rows feed the means; EVERY row is encoded; unseen category → training global mean. Absent: every row trains.
- `folds` — `0` (off) or `2..100`: each training row is encoded from the training rows OUTSIDE its fold (null when none hold an outcome); other rows get the full-training value.
- `rng_seed` — int, only with `folds`: seeds the fold deal (training rows in order, blocks of k, balanced ±1).

## Inputs

`Field` — `categorical_u8`, `categorical_u16`, `categorical_u32`, or `packed_bool` (levels `"0"`/`"1"`). `params.target` — numeric (categorical rejected at construction).

## Output

One `f64` column at `Label` (default `TARGET_<field>`): the (smoothed) mean of `target` over the training rows sharing the categorical value (out-of-fold for a training row under `folds`).

<!-- generated: reading-the-output -->

## Gotchas

- **LEAKAGE — `PULSE_FEAT_TARGET_LEAKAGE_RISK`** on every encoder WITHOUT `fit_on` (predict and process `warnings`, Compose slots too; strict refuses before any record): its means include test rows and the row's own target, and a prior split or `split == 0` filter changes nothing. `folds` alone keeps a reworded warning. Holdout outcomes never move a `fit_on` encoding.
- `folds` 1 / >100, `rng_seed` without `folds`, blank `fit_on`, missing `target` or `smoothing < 0` → `PROCESSING_CONFIG`; a `Field` of any other type, or a categorical / `set_*` `target`, → `PULSE_FIELD_TYPE_UNSUPPORTED` (predict and runtime).
- GLOBAL-PASS, streamable via `iter.Reset()` — unless `field`, `target` or a `fit_on` name is an earlier feature's column: then buffered (`streamable_reasons`).
- Zero training targets → every row null. Weights are ignored.

## See

- `pulse_examples_search tags=[leakage-risk]`
- Skills: `feature-engineering` (target-leakage trap), `op-feat-train-test-split`, `expression-language` (`fit_on`), `op-feat-frequency-encode`
