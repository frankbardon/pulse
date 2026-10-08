```yaml
name: feature-engineering
description: Feature slot semantics — choosing a transform, pre-filter ordering (features run before filters), per-row vs global-pass cost, target-leakage trap, train/test split. Topical design; per-feature detail lives in atomic op-feat-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [FEAT, features, PULSE_FEAT_TARGET_LEAKAGE_RISK]
```

# Feature engineering

`features` adds DERIVED COLUMNS that the rest of the request can reference by label — the ML-pipeline transforms: variance-stabilising (log / sqrt), binning, categorical encoding (one-hot, frequency, target), date decomposition, polynomial expansion and train/test assignment. Per-operator detail (formula, null rules, output column naming, accepted types, degree caps) lives in atomic `op-feat-*` skills.

## Choosing a feature

`pulse_skills_get intents` → `prepare` (every feature serves it), narrowed by `distribution_shape` (taming a long tail), `relationship` (curvature for a model), `composition` / `segment` (encoding categories, assigning partitions), `change_over_time` (calendar parts). Keep the manifest `components.features` entries carrying it, then read `op-feat-<name>`; each one's guidance names its alternatives. The deciding questions:

- **Must a filter, grouper or test downstream consume the column?** Then it is a feature; if only post-filter rows matter, derive an attribute ([`attribute-composition`](attribute-composition.md)).
- **A reported table, not a model input?** Display bins are groupers; a category's average outcome is an aggregation.
- **Encoding a categorical** — few categories → one column each; many → a single numeric code (frequency, or target — mind the leakage trap below).
- **Does it read the whole cohort?** That sets its cost class (below) and leakage risk.

## Slot position — pre-filter

Features run **before** filters: `features → filterers → attributes → groups → aggregations → windows → sort`.

- A feature's output column is addressable by every downstream stage (filterers, attributes, groupers, aggregators, windows) — bucketize then filter on the bucket; filter on a split tag.
- Features see the RAW record set before filtering. A feature's stats (frequency, target mean, quantile cutpoints) reflect the entire cohort, not the filtered subset.

## Composition rules

1. **Order matters.** Later features can reference earlier features' labels.
2. **Labels unique** within the slot. Collision → `PROCESSING_CONFIG`.
3. **Naming** — one-to-one ops emit one column (`<PREFIX>_<field>` unless labelled); fan-out ops (one-hot, date parts, polynomial) emit a prefixed column family. The atomic skill names them; predict reports the post-feature schema.
4. **Two cost classes:**
   - **Per-row** — the value depends only on this row (log / sqrt, one-hot, date parts, polynomial, bucketize with explicit boundaries). One pass.
   - **Global-pass** — the value depends on the whole cohort (frequency / target encoding, train/test assignment, bucketize by quantiles). A precompute sweep, then per-row emit.

## Streamability

Stream-eligible when every feature implements the streaming computer interface (embedder-authored features implement `extend.StreamingFeatureComputer`) AND the rest is stream-eligible (online aggregators, no groups, attributes, or windows). Per-row features stream record-by-record. Global-pass features precompute then rewind via `iter.Reset()` — slice iterator O(1), file-backed iterator re-reads the file (doubles I/O).

Train/test assignment materialises its table in precompute — O(rows) memory either way. Predict reports per-slot streamability under `data.streamable_reasons`.

## Target-leakage trap — `PULSE_FEAT_TARGET_LEAKAGE_RISK`

Target encoding replaces a categorical with the MEAN of a numeric target over rows sharing that category. Optional smoothing `s` shrinks rare categories toward the global mean: `encoded = (n * mean_cat + s * mean_global) / (n + s)`.

The trap: each encoded value averages EVERY row's target — test / val rows and the row's own included. The encoder reads no split column and features run pre-filter, so a `split == 0` filter keeps the leaked means.

Predict surfaces `PULSE_FEAT_TARGET_LEAKAGE_RISK` (warning; error under `--strict` / `Options.Strict: true`) on EVERY target encoder — a preceding train/test split changes no value, so it does not silence it. Train-only means: a separate request averaging the target grouped by the category, filtered to `split == 0`, mapped back yourself.

## Train / test / split semantics

The train/test split feature tags each row in a numeric `split` column. `0` = train, `1` = val, `2` = test; two- or three-element ratios, optional `stratify` (per-class shuffle keeps class balance), deterministic `seed`.

Downstream: keep `split == 0` with a value filter for train-only; group by `split` for per-partition metrics. Same seed + same rows in the same order ⇒ same labels.

## Components

**Features emit per-record columns, not `Response.Components`.** To audit a feature column, read it from `Response.Data` or wrap it in an aggregation.

## Gotchas

- Fan-out column sets come from the SCHEMA (a one-hot column per dictionary entry, even for categories absent from the rows), so predict and the executor agree without a scan.
- Predict refuses mutually exclusive params (bucketize: boundaries XOR quantiles) as `PROCESSING_CONFIG` and a source type outside the manifest `accepts_types` as `SERVICE_VALIDATION` — check it before using a `decimal128` or categorical source ([`financial-cohorts`](financial-cohorts.md)).
- Polynomial expansion overflows unstandardised — `Degree=10` on `|x|=100` yields `1e20`. Standardise first.
- Features can't reference attribute labels (attributes run after). Stage via Compose / ProcessChain.

## See

- Recipes: `pulse_examples_search tags=["feature-engineering"]`, `tags=["target-encoding"]`, `tags=["polynomial-regression"]`, `tags=["train-test-split"]` plus atomic `op-feat-<name>`.
- [`attribute-composition`](attribute-composition.md) — when to derive a column AFTER the filter instead.
- [`regression-modeling`](regression-modeling.md) — polynomial columns upstream of a linear fit.
- [`request-envelope`](request-envelope.md) — slot keys, streamability, smart defaults.
- [`streaming-and-watching`](streaming-and-watching.md) — streaming-vs-buffered pipeline selection.
- `pulse_errors_lookup` — `PULSE_FEAT_TARGET_LEAKAGE_RISK` recovery playbook.
