---
name: synth-profile-capture
description: What `pulse profile create` captures per field and per pair (conditional pairs, thin-pair warning, run-continuation, shape fitting), how `SpecFromProfile` reconstructs each field, and how categorical joint structure is generated.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [profile create, synth from-profile, conditional, fit-shape, run-continuation, SpecFromProfile]
requires: [capability:synth]
---

# Synth profile capture

Capture via `pulse profile create` (`Pulse.Profile`); synth via `pulse synth from-profile`. Every section below is additive and `omitempty`: a capture without its flag emits no key and is byte-identical to a pre-flag document.

## Profile mode

Per field:

- Numeric: mean, std, min, max, optional percentiles, null-rate, plus `discrete` (per-level histogram) for a capped integer column (`synth-marginals`). Categorical: top-K + frequencies, cardinality, null-rate. Date: range, weekday histogram, null-rate. `set_*`: `synth-set-fields`.
- Pairwise: strongest `|rho|` (capped by `--correlation-top-k`).
- `--conditional` (all under `conditional.`): `conditional.numeric_pairs` `{a,b,rho,n}` row-aligned, `n` the TRUE co-occurrence count (both fields non-null on one row) — preferred over `pairwise` when present, falling back unchanged when absent, so no older document is invalidated. `conditional.categorical_pairs` `{a,b,cells,n}` over at most 10,000 rows by genuine Algorithm-R reservoir sampling (`--seed`), NOT first-N, so a block-ordered source is unbiased; two caps compose — each field's `--top-k` collapses to `"other"` FIRST, then `ContingencyCellCap` (128) folds the tail into one merged `("other","other")`. `conditional.categorical_numeric_pairs` `{a,b,categories,n}`, one online `{category,mean,std,n}` per category (no reservoir cap, no second joint cap).
- **Thin-pair warning:** `n < 30` (`internal/synth.MinPairObservations`) still SHIPS — never refused — with a warning naming the pair (`thinPairWarning`). Every pair kind reuses this one mechanism, and thin model levels share its line shape.
- `--fit-models` (`models`) and `--residual-correlations` (`residual_correlations`, requires `--fit-models`): `synth-models`.
- `--suggest-rules <path>` (rule candidates, written to their own file, never into the profile): `synth-structural-rules`.
- `--run-continuation` (`run_continuation`): per-field share of adjacent row pairs whose on-wire bytes + null bit repeat — exactly the run-skip decode's hit rate — plus `overall`, `high_fields` (rate ≥ 0.75) and `advice` (`low:` below 0.5, pointing at an upstream `ORDER BY` of the parent key). A ROW-ORDER fact, not a distribution: `SpecFromProfile` never reads it. It needs every record, so it is not in `pulse inspect`. Pairs never span a shard; a shard archive profiles as one stream against the canonical schema (an `archive.pulse#shard.pulse` anchor is not resolved).
- `--fit-shape` (`numeric.shape`, numeric only): a 2-component Gaussian mixture kept only on a genuine BIC improvement; `SpecFromProfile` then emits `mixture` instead of `normal`. Fit rules and how it composes with a model: `synth-shape-fit`.

Capture rides ONE cohort scan; no flag adds a read.

## Reconstruction (`SpecFromProfile`)

| Captured field | Rebuilt as |
|---|---|
| `packed_bool` | `bernoulli` (`synth-marginals`) |
| integer with ≤64 observed levels | `discrete` (`synth-marginals`) |
| other numeric | `normal` clamped to observed min/max, or `mixture` when `shape` is present |
| categorical | `weighted_categorical` |
| date | `uniform_date` |
| `set_*` | `set_bernoulli` |
| nothing summarised (always-null) | a typed `constant` placeholder with `null_rate` 1.0 — every row null |

Unsupported → `PULSE_PROFILE_FIELD_UNSUPPORTED`. Captured pairs land on `Spec.CategoricalPairs`, `Spec.CategoricalNumericPairs`, the set pair slots and `Spec.Correlations`; which of them actually run is arbitrated once (`synth-conflicts`). A numeric that lands a model keeps NO numeric-target conditional pair — the model replaces it, per target (`synth-models`).

`synth from-profile --emit-spec <path>` writes the spec that actually generated — the one place to read which distribution each field reconstructed to (`synth-structural-rules`).

### Categorical joint structure (generation)

`Spec.CategoricalPairs` / `CategoricalNumericPairs` drive two resample steps after every field's own draw and before the correlator: categorical-categorical resamples B from the captured per-`a_value` distribution over B's cells (falling back to B's POOLED marginal for an unseen A value); categorical-numeric resamples numeric B keyed on A's drawn value, clamped to B's observed `[min, max]`. Both are no-ops when `Conditional` was absent at capture — a plain profile reproduces independent-marginal generation byte-for-byte. Each pair is PICK-ONE: it overwrites its target, so several drivers conditioning one numeric is a model's job, not a pair's.

## See

- `synthetic-data` — modes, spec shape, distribution registry.
- `synth-marginals` · `synth-set-fields` · `synth-shape-fit` · `synth-correlations` · `synth-conflicts` · `synth-models` · `synth-determinism`.
- `docs/src/cli/profile-create.md` — every capture flag, with examples.
