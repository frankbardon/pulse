---
name: synth-correlations
description: Synth spec `correlations` — the Gaussian-copula construction, which marginals and field types may participate, how rank and Pearson correlation survive, and how unmeasured matrix entries are completed and reported.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-schema, synth from-profile, correlations, copula]
requires: [capability:synth]
---

# Synth correlations

### Pairwise correlations

`correlations` is a list of `{a, b, rho}` realized by a Gaussian copula (`internal/synth/copula.go`): correlated standard normals `u` via Cholesky → `p_i = Φ(u_i)` → each field's OWN quantile `Q_i(p_i)`. It runs after every pair stage. A profile supplies it from `conditional.numeric_pairs` when captured, else from the top-K `pairwise` list (`synth-profile-capture`).

- **Supported marginals:** `normal` / `uniform` / `lognormal` / `exponential` / `mixture` / `bernoulli` / `discrete` (`fieldMoments` validates, `quantileFor` builds `Q_i`). Anything else in `correlations` → `SERVICE_VALIDATION`. `|rho| ≥ 1` rejected at validation.
- **A participant must also be a SCALAR field TYPE, and that predicate is DERIVED, never transcribed.** `isNumericFieldType` reads `fieldTypeFromName`: every declarable non-`categorical_*`, non-`set_*` type qualifies (`u4`…`u64`, `f32`/`f64`, `date`, `decimal128`, `packed_bool`) and nothing else. A hand-written list silently dropped a whole type out of `Spec.Correlations` once — do not reintroduce one. The boundary is TYPE, not distribution: a `date`'s `uniform_date` is refused one call later, NAMING the distribution.
- **`normal` reduces exactly to `mean + std*u`.** Otherwise the copula targets RANK correlation exactly and preserves each field's own marginal (mean, std AND skew); realized PEARSON attenuates for a wide-variance non-normal marginal, and `min`/`max` clamping distorts further. Keep sigma modest if Pearson must land tightly.
- **A step/staircase participant (`bernoulli`, `discrete`) holds its marginal EXACTLY and attenuates the realised correlation**, costing rank as well as Pearson once ties dominate (`synth-marginals`).
- The `mixture` arm exists for the MODEL draw, not for `correlations`: a `--fit-shape` field is excluded from the value-scale matrix either way (pre-claimed when unmodelled, permanently refused when modelled — its structure rides `residual_correlations`, `synth-models`).
- **A modelled field never joins `correlations`.** Realizing it would overwrite the model's output; capture residual correlations instead. The value-scale and residual-scale arms therefore fire on DISJOINT field sets.
- **Unmeasured entries: ASSUME AND RECORD.** `correlations` is a LIST, the Cholesky needs a MATRIX, so every unnamed pair is filled with 0 (drawn independent) and `buildCorrelator` WARNS with the count. Refusal was rejected — an incomplete list is the normal input. `cholesky`'s ridge also reports its accumulated diagonal jitter, which pulls realized correlations toward zero. Neither warning changes a number. The residual correlator shares this one factorization and completion policy.

`Spec.Correlations` is ONE joint claimant across all participants (a single Cholesky draw): losing one participant to an earlier claim excludes only that field, and the correlator rebuilds from the survivors (`synth-conflicts`).

Gates: `TestSynth_CorrelationReconstructionWithinTolerance`, `TestSynth_CopulaPreservesLognormalMarginal`.

## See

- `synthetic-data` — spec shape and distribution registry.
- `synth-models` — residual-scale correlation for modelled fields.
- `synth-fidelity-report` — the `pairwise` recovery section.
