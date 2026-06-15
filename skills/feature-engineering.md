---
name: feature-engineering
description: Feature slot semantics — pre-filter ordering (FEAT runs before FILTER), target-leakage trap, train/test split. Topical design; per-FEAT detail lives in atomic op-feat-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [FEAT, features, PULSE_FEAT_TARGET_LEAKAGE_RISK]
---

# Feature engineering

`features` adds DERIVED COLUMNS that the rest of the request can reference by label. The nine `FEAT_*` operators (`FEAT_LOG`, `FEAT_SQRT`, `FEAT_BUCKETIZE`, `FEAT_ONE_HOT`, `FEAT_DATE_FEATURES`, `FEAT_FREQUENCY_ENCODE`, `FEAT_TARGET_ENCODE`, `FEAT_TRAIN_TEST_SPLIT`, `FEAT_POLY`) cover ML-pipeline transforms — log / sqrt, bucketize, one-hot, frequency / target encoding, date decomposition, train/test split, polynomial expansion. Design contract here; per-FEAT detail (formula, null rules, output column naming, polynomial degree caps) lives in atomic `op-feat-*` skills.

## Slot position — pre-filter

Features run **before** filters: `features → filterers → attributes → groups → aggregations → windows → sort`.

Consequences:

- A feature's output column is addressable by every downstream stage (filterers, attributes, groupers, aggregators, windows).
- Bucketize then filter on the bucket column works.
- Train/test/split tags are usable as filter values (`FILTER_INCLUDE` on `split == 0`).
- Features see the RAW record set before filtering. A feature's stats (frequency, target mean) reflect the entire cohort, not the filtered subset.

Opposite ordering from `attributes` — `ATTR_*` runs AFTER filters and sees only filtered rows. Use FEAT when a downstream filter / test must consume the derived column; otherwise prefer ATTR (`attribute-composition`).

## Composition rules

1. **Order matters.** Later FEATs can reference earlier FEATs' labels.
2. **Labels unique** within the slot. Collision → `PROCESSING_CONFIG`.
3. **Per-FEAT naming convention** — bare `field` for one-to-one ops (`FEAT_LOG` → `LOG_<field>`), prefix fan-out for `FEAT_ONE_HOT` / `FEAT_DATE_FEATURES` / `FEAT_POLY`. Per-FEAT detail in atomic skills.
4. **Two operator classes:**
   - **Per-row** (`FEAT_LOG`, `FEAT_SQRT`, `FEAT_ONE_HOT`, `FEAT_DATE_FEATURES`, `FEAT_POLY`, `FEAT_BUCKETIZE` with explicit boundaries) — one-pass.
   - **Global-pass** (`FEAT_FREQUENCY_ENCODE`, `FEAT_TARGET_ENCODE`, `FEAT_TRAIN_TEST_SPLIT`, `FEAT_BUCKETIZE` with quantiles) — precompute sweep then per-row emit drives downstream stages.

## Streamability

Stream-eligible when every `FEAT_*` implements `feature.StreamingComputer` AND the rest is stream-eligible (online aggregators, no groups, attributes, or windows). Per-row FEATs stream record-by-record. Global-pass FEATs precompute then rewind via `iter.Reset()` — slice iterator O(1), file-backed iterator re-reads the file (doubles I/O for global-pass).

`FEAT_TRAIN_TEST_SPLIT` materialises its assignment table during precompute; streaming pays the same O(rows) memory as buffered for the split column. Buffered is the fallback whenever streaming is unsafe. Predict reports per-slot streamability under `data.streamable_reasons`.

## Target-leakage trap — `PULSE_FEAT_TARGET_LEAKAGE_RISK`

`FEAT_TARGET_ENCODE` replaces a categorical with the MEAN of a numeric target over rows sharing that category. Optional smoothing `s` shrinks rare categories toward the global mean: `encoded = (n * mean_cat + s * mean_global) / (n + s)`.

The trap: target-encoding the WHOLE cohort mixes validation / test signal into training rows. Each encoded value reflects every other row's target — including rows the model will be tested against.

**Fix.** Place `FEAT_TRAIN_TEST_SPLIT` BEFORE every `FEAT_TARGET_ENCODE` in the same slate. The encoder then computes per-category means within the train partition only; test / val rows receive the train-derived mean.

Predict surfaces `PULSE_FEAT_TARGET_LEAKAGE_RISK` (warning by default, error under `--strict` / `Options.Strict: true`) when a `FEAT_TARGET_ENCODE` has no preceding `FEAT_TRAIN_TEST_SPLIT`. Recovery: reorder OR document the cohort as fully labelled-and-static.

## Train / test / split semantics

`FEAT_TRAIN_TEST_SPLIT` tags each row in a numeric `split` column. Constants: `feature.SplitTrain=0`, `feature.SplitVal=1`, `feature.SplitTest=2`. Two- or three-element ratio vectors; optional `stratify` field preserves class balance per-partition (per-class deterministic shuffle); `seed` is the shuffle seed (default 0 → deterministic).

Downstream: filter to train-only with `FILTER_INCLUDE` on `split == 0`; group by `split` for per-partition metrics; combine with `FEAT_TARGET_ENCODE` to get the leakage-safe path.

## Components

**Features emit per-record columns; they do not produce `Response.Components`.** The Components family covers aggregations, groupers, filterers, crosstab, and run — not per-row derived columns. To audit a feature column, read it from `Response.Data` or wrap it in an aggregation.

## Gotchas

- `FEAT_ONE_HOT` materialises its column set from the SCHEMA dictionary — predict emits the same set as the executor even when some categories don't appear.
- `FEAT_BUCKETIZE` requires EITHER `params.boundaries` OR `params.quantiles` — not both. Predict raises `PROCESSING_CONFIG`.
- `FEAT_POLY` overflows without standardisation — `Degree=10` on `|x|=100` already yields `1e20`. Centre / standardise predictors first.
- `FEAT_DATE_FEATURES` requires `date`-typed source; rejects `categorical_*`.
- Decimal128 source: `FEAT_LOG`, `FEAT_SQRT`, `FEAT_BUCKETIZE` accept (f64 approximation); categorical-only ops reject. See `financial-cohorts`.
- Features can't reference attribute labels (attributes run after). Stage via Compose / ProcessChain.

## See

- Recipes: `pulse_examples_search tags=["feature-engineering"]`, `tags=["target-encoding"]`, `tags=["polynomial-regression"]`, `tags=["train-test-split"]` plus atomic `op-feat-<name>`.
- `attribute-composition` — when to derive a column AFTER the filter instead.
- `regression-modeling` — `FEAT_POLY` upstream of `REG_OLS` for polynomial regression.
- `request-envelope` — slot keys, streamability, smart defaults.
- `streaming-and-watching` — streaming-vs-buffered pipeline selection.
- `error-code-reference` — `PULSE_FEAT_TARGET_LEAKAGE_RISK` recovery playbook.
