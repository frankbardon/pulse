---
id: U12
slug: weighting-inferential
title: "Significance tests and models are correct on weighted survey data"
track: Statistical integrity
size: L
status: done
depends_on: [U11]
soft_depends_on: []
blocks: []
todo_items: [55, 56, 59]
branch: weighting-inferential
---

# U12 — weighting-inferential

**Outcome:** Significance tests and models are correct on weighted survey data.

**Track:** Statistical integrity · **Size:** L · **Depends on:** [U11](U11-weighting-descriptive.md) · **Unblocks:** —

## Summary

Extend weighting to inference: weighted `TEST_*` and significance overlays using Kish `n_eff` for probability weights, weighted attributes (`ATTR_ZSCORE`/`PERCENTILE`/`NORMALIZED`), weighted `GROUP_QUANTILE`, and weighted regressions (WLS / weighted IRLS). Completes the weighting gates and fixtures.

> **Already shipped (v0.39.1, forward-ported to `main`; status unchanged, nothing ticked here).** The weighted pairwise two-means z overlay `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` exists, with a required `n_basis` param: `weights` (`n = Σw`) or `kish` (`n = n_eff`). It reads `m2_weighted`, `sum_weights_sq`, `weighted_variance` and `n_eff` off `AGG_WEIGHTED_MEAN` cell Components. The remaining overlays, tests, attributes and regressions shipped with this unit ("Shipped: deviations and decisions" below). The existing `sum_weights` key versus the planned `w_sum` naming is U11's to reconcile — **resolved by U11: the spelling is `sum_weights`, no `w_sum` alias**; read `w_sum` below as `sum_weights`.

## References

**Theme documents (read before starting):**
- [statistical-integrity 01 — Weighting](../v1.0.0-statistical-integrity/01-weighting.md) — What becomes weight-aware (inferential rows), kind=probability / Kish n_eff

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#55** (4. Statistical integrity › Weighting) Weighted tests and significance overlays with Kish `n_eff`
- [x] **#56** (4. Statistical integrity › Weighting) Weighted attributes, `GROUP_QUANTILE`, regressions
- [x] **#59** (4. Statistical integrity › Weighting) `TestWeightUnityParity`, `TestWeightFrequencyExpansionParity`, reference fixtures; `weighting.md` skill

## Scope

**In scope**
- Weighted tests + significance overlays with `n_eff`
- Weighted attributes, `GROUP_QUANTILE`, `REG_*`
- Windows refuse explicit weights
- Reference fixtures for t-test and χ²

**Out of scope**
- Design-based variance

## Epics & stories

> As shipped the unit ran as six epics — E1 the kind dimension and the weighted moment and count tests (with the shared N* / w* rule and `PULSE_WEIGHT_LOW_NEFF`), E2 frequency-weighted rank, exact and distribution tests, E3 inferential overlays on weighted crosstabs, E4 weighted regressions and regression attributes, E5 the exact normal critical value, weighted CI bounds, z / t scores and quantile buckets, E6 documentation, roadmap and the statistics review — not the E1–E2 outline below, which is kept as the original plan. Size grew from M to L accordingly.

Each epic is a vertical slice. Commit with `feat|fix|perf|test(weighting-inferential/E<n>-S<m>): …`; close each epic with `milestone(weighting-inferential/E<n>): vertical slice complete — <epic title>`.

### E1 — Inference uses the right sample size
- S1: `n_eff` plumbing; weighted `TEST_*`
- S2: weighted significance overlays (prop-z, t, z, pairwise, χ²)

### E2 — Row scores and models honour weights
- S1: weighted attributes + `GROUP_QUANTILE`
- S2: weighted regressions
- S3: parity gates extended; reference fixtures

## Acceptance criteria

- [x] Weighted t-test and χ² match published reference results on fixtures (R 4.6.1 on the expansion for `frequency`; the closed form cross-checked with statsmodels / scipy for `probability` — `TestWeightReferenceValues/tests`)
- [x] Under `kind: probability`, overlay summaries report `n_eff` and p-values use it (`Summary.Parameters`; `TestWeightOverlayProbabilityScaleInvariance`, `TestWeightReferenceValues/overlays`)
- [x] Unity/frequency parity gates cover every weight-aware operator (per manifest `weight_kinds`, through `assertWeightKindCoverage`: aggregators, tests, regressions, attributes, groupers, overlays)
- [x] Windows given an explicit weight refuse with `PROCESSING_CONFIG` (shipped by U11: a REQUEST weight on a window is `PROCESSING_CONFIG`, the default skipped — `TestWeight_WindowsAndUnaffectedMatchPredict`)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Parity gates extended to all weight-aware operators

## Update Demand companions

- Atomic skills for every touched test/overlay/attribute/regression
- `weighting.md` inference section
- Purpose/Interpretation updated where weighting changes how output reads (`n_eff`)

## Human inputs & decisions

- Reviewer glance at the `n_eff` semantics (the statistics reviewer who signs off U08's guidance at [U33](U33-v1-release.md), #206). **Done without a human reviewer:** an automated review is recorded in [`reviews/U12-weighting-review.md`](../reviews/U12-weighting-review.md); the human glance joins the U33 #206 sign-off list.

## Inherited from U11

U11 shipped the weight surface, the resolver, validation, the weighted descriptive aggregators, crosstabs and share / index overlays. Contract: `.claude/reference/weighting.md` (Refusal rules is the list below); skill `skills/weighting.md`.

- **The refusal list this unit lifts.** While a weight is in force on the slot (its own `weight`, the request's, or `Options.DefaultWeight`), each of these is `PULSE_WEIGHT_UNSUPPORTED` at predict and runtime, raised by `ResolveWeights` (`internal/descriptor/weight_resolve.go`) and classed in `internal/weighting` (`ClassRefuse`): every built-in `TEST_*` (`tests` and `post_tests`); every `REG_*`; `ATTR_ZSCORE`, `ATTR_TSCORE`, `ATTR_PERCENTILE`, `ATTR_NORMALIZED`; `GROUP_QUANTILE` (groups and crosstab axes); `AGG_CI_LOWER` / `AGG_CI_UPPER`; every overlay kind declared `Inferential: true` (keyed off the flag, sole exemption `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` in `weightExemptOverlays`), on `Request.Overlays` and on Compose (`ComposeOverlayWeightRefusal`). Lifting an entry means moving it to `ClassAware`, adding its unity / frequency-expansion parity row and reference value, and updating the skill and `weighting.md`.
- **Not in the list:** windows stay not-weightable (a REQUEST weight is already `PROCESSING_CONFIG`, the default skipped — this unit's "windows refuse" scope item has shipped). Weight-aware aggregators over `decimal128` stay refused (no weighted decimal path).
- **Spelling.** The floor key is `sum_weights`; `n_eff` (Kish, `probability` only) is on every weighted aggregator slot and crosstab cell / margin map already — the overlays here read it rather than recomputing.
- **#59 is largely in place.** `TestWeightUnityParity`, `TestWeightFrequencyExpansionParity`, the external reference fixtures (`TestWeightReferenceValues`, generator under `internal/service/testdata/weight_reference/`) and `skills/weighting.md` shipped with U11 for the descriptive operators; this unit extends each to the operators it makes weight-aware (their `coverage` subtests demand a row per manifest `weight_aware` operator).
- **Extension precedent.** A WeightAware extension TEST already runs weighted; keep that consistent when the built-in tests become weight-aware.

## Shipped: deviations and decisions

Contract: `.claude/reference/weighting.md` ("Classification by family", "Weighted inference", "Overlays", "Reference fixtures"); agent skill `skills/weighting.md` (Weighted inference); user guide `docs/src/library/weighting.md`; embedder rows and changelog `03-embedder-migration.md` (Changes from U12); statistics review `reviews/U12-weighting-review.md`.

- **One rule.** N* = Σw (`frequency`) or Kish n_eff = (Σw)²/Σw² (`probability`); w* = w·N*/Σw; every weighted formula is the frequency formula on w*. Point estimates are kind-free; SEs, df (fractional) and p-values read N*. Shared code: `internal/weighting/inference.go` (`Basis`, `NStar`, `Scale`, `KishNEff`, `Welford`). Not design-based: unequal weighting only, no strata / clusters / FPC.
- **A kind dimension, not a binary.** `ClassFrequencyOnly` joins aware / not weightable / refused; manifest `weight_kinds` on operators, tests, regressions and overlays drives the coverage harnesses. Frequency-only (rank tests, Fisher, KS, Brown-Forsythe, Bayes linear, the Fisher cell overlay) = exact on the expansion, refused under `probability` naming the kind. Permanent refusals carry `details.reason` (Shapiro-Wilk, Tukey HSD, ANOVA RM, trend, post-tests, `ATTR_PERCENTILE`, resample / selection modifiers, pairwise two-means z with `alternative`, probit t).
- **`ATTR_NORMALIZED` reclassified** not weightable (min-max has no weighted meaning): default skipped, explicit `PROCESSING_CONFIG` — was a refusal.
- **Raw counts stay raw.** `n`, `n_obs`, bucket `count`, `total_n`, `CellCounts` keep row counts; `sum_weights` / `n_eff` ride beside them (test `details`, `RegressionResult`, overlay `Summary.Parameters`, the components floor). Zero-weight rows are excluded from a test's raw `n` (as on the expansion).
- **`PULSE_WEIGHT_LOW_NEFF`** (new code): probability only; tests at their raw-n floor per group, regressions at p + 1 (decision E4-S1).
- **χ² under probability is first-order Kish** (the table scaled to n_eff, then Pearson), explicitly not Rao-Scott; `contingency` / margins report Σw while the statistic, `expected_min`, V and φ read the scaled table.
- **Weighted-host overlays.** A host cell is weighted when it carries `sum_weights`, whatever set the weight — so a host weighted only by its own slot weight is now read on N* (a bug fix: it ran on raw counts). On a weighted host the raw-count, `n_within` and distinct-key `n_source` modes are refused, the weight-sum modes frequency-only (decision E3-S2); a `probability` host built with components disabled refuses the floor-scaled overlays rather than forcing components on (decision E3-S3). `PAIRWISE_PROP_Z` with `n_source` omitted reads N*.
- **`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` unchanged in computation**; predict now reports it `applied`, and its `n_basis` (not the weight's `kind`) still picks n.
- **Regressions.** OLS β is WLS (kind-free), SEs on w* with df = N* − p − 1 — NOT `lm(weights =)`, whose df is the row count; penalties scale by Σw (β invariant to rescaling); GLM = R `glm(weights = w*)` with dispersion fixed at 1; Bayes linear frequency-only. Leverage = w × the expanded-copy leverage. `RegressionResult` gains `sum_weights` / `n_eff` (additive). `ATTR_REG_*` and `ATTR_ZSCORE` / `ATTR_TSCORE` now weight under a default weight (behaviour change).
- **Quantile buckets.** bucket = ⌊⌊C − 1⌋·k/W⌋ on the weighted-quantile rule; a heavy row spanning a cut lands whole in the higher bucket, so under `frequency` the cuts equal the expansion's but counts / highs need not (decision E5-S2). Groups and crosstab axes are now stamped.
- **Exact normal critical value** (U36 #205 fixed here): `TEST_Z_TWO_SAMPLE` / `TEST_PROP_Z` / `TEST_PEARSON_R` intervals ~4.7e-4 relative wider at α = 0.05 (Winitzki replaced by `qnorm`); `AGG_CI_*` moved ~1e-11.
- **KS tie walk fixed** (unweighted too): D no longer compares ECDFs inside a tie run.
- **Extensions keep one flag.** `OperatorMeta` has no `weight_kinds`: a WeightAware extension runs under both kinds (decision, documented; no API added).
- **Oracles.** `frequency` = stock R 4.6.1 / scikit-learn 1.7.2 on the `rep()` expansion; `probability` = the closed form on w*, cross-checked with statsmodels 0.14.5 / scipy 1.16.2 / Hmisc 5.3.0, and required to equal R on the frequency column first. Generators never run in CI.
- **No changelog file.** The repository has none; the migration doc's U12 table is the changelog (precedent set in E3-S3).

## Handed on

| Item | Owner |
|---|---|
| Human glance at the `n_eff` semantics and the lifted formulas: [`reviews/U12-weighting-review.md`](../reviews/U12-weighting-review.md) | U33 (#206) |
| Engine findings of the U12 review awaiting triage: WS-01 (ANOVA F whole-sample Kish scale — high), WS-05 / WS-12 (undefined figures at tiny n_eff), WS-06 (weighted two-means z `n_basis: weights` on a probability host), WS-10 (weighted KS ties), WS-13 (prop-z scaled-expected guard), WS-07 / WS-08 / WS-09 (reason texts) | maintainer triage → U36 / new |
| `OVERLAY_T_VS_REF` / `OVERLAY_Z_VS_REF` over an engine `AGG_WELFORD` series emit an EMPTY layer (the series arm does not parse `processing.WelfordTriple`; pre-existing, weighted or not); a fix must carry `sum_weights` / `n_eff` / `m2` through | U36 |
| Weighted GLM gamma dispersion (fixed at 1, as unweighted) and the weighted rows of the per-output oracles at U36's tolerances | U36 |
| Extension `weight_kinds` declaration (a WeightAware extension is applied under both kinds today) | U34 / new (on demand) |
| `n_source` refusal on a weighted host is wired for the pairwise / panel kinds; other overlay modes keep the predict-only inertness refusal | new |
| Op-overlay atomic skills over the 1200-char soft budget (pre-existing, grown by the weighted-host notes) | new (skill budget tightening) |
| Design-based (strata / cluster / replicate) variance, Rao-Scott χ², probability-weighted rank tests | out of scope |
| Facet weighting, top-level Compose / chain weights, weighted windows, weighted `decimal128` | out of scope (unchanged from U11) |
