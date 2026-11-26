# Weighting — Floor keys, Operation-order exactness rule, Reference fixtures

Slice of `.claude/reference/weighting.md` — load the index first, then only the slice(s) your change touches.

## Floor keys

The universal aggregator floor `{n, n_null}` gains three keys, all `omitempty` and present ONLY on a weighted slot:

- `sum_weights` — Σw over the rows that contributed (spelling from `AGG_WEIGHTED_MEAN`; no `w_sum` alias);
- `n_eff` — Kish effective n, (Σw)²/Σw², `kind: probability` only;
- `n_weight_invalid` — rows excluded by validation.

`n` / `n_null` keep their value-presence meaning; counts stay raw ints (`CellCounts`, margin counts, grouper `total_n`, metadata row counts). Absence of `sum_weights` is the only per-slot "unweighted" signal — no `weighted` marker, no skip warning; predict's per-slot status is the explanation. Typed fields `AggregationComponents.SumWeights` / `NEff` / `NWeightInvalid` are pointers so a weighted zero still marshals; tallied by `processing.WeightFloor` beside `{n, n_null}` and merged slot-wise by both reducers; declared `optional: true` right after `{n, n_null}` in every weight-aware aggregator's ComponentSchema (not on non-aware operators). Extensions get them from the orchestrator and never emit them. Floor prose: `.claude/reference/response-components.md`.

## Operation-order exactness rule

Weighted formulas are written so that w = 1 reproduces the unweighted operation order bit for bit — `(w·δ)/Σw`, never `(w/Σw)·δ` — and the unweighted path is untouched when no weight is set. Unity parity is therefore BYTE-identical, never a tolerance.

**Reproducible sums.** Σw, Σw·x, Σw² of the core weighted aggregators (count, sum, average, weighted mean), of the floor (`sum_weights`, `n_eff`) and weighted `AGG_RATIO`'s sums are binned reproducible sums (`internal/reprosum`; `AddWeighted` rounds each product, an FMA barrier): bit-identical on every arm, worker count and shard split, and the floor's `sum_weights` equals `AGG_WEIGHTED_MEAN`'s own bit for bit. At w = 1 the binned Σw is the exact n. Welford moments keep a running Σw and stay within ULP across arms (`.claude/reference/execution-modes/shards-and-merge.md` (Parallel shards)).

Parity gates (`internal/service/weight_parity_test.go`, plus `TestWeightedCore_UnityParity` per aggregator):
- `TestWeightUnityParity` — all-ones weights marshal byte-identically to unweighted (weight-only keys shed) for every weight-aware operator × every execution arm × weight source (request, slot, default).
- `TestWeightFrequencyExpansionParity` — integer weights equal physically duplicated rows run unweighted (exact for counts and integer sums, `sum_weights` exactly the expanded `n`, else 1e-12 relative; p-value-bearing rows 1e-10).
- `TestWeightProbabilityQuantileScaleInvariance` — probability has no expansion identity, so its arm is scale invariance (weights × 0.37 and other factors give the same figures, totals scale by the factor).
- Coverage: every surface's operator table is held equal to the manifest `weight_kinds` through `assertWeightKindCoverage` (`internal/service/weight_coverage_test.go`) — a manifest (operator, kind) without a row, or a row under an unadvertised kind, fails. So an operator flipped to aware owes a parity row. An arm applies when the UNWEIGHTED request reaches it, and the weighted twin must engage the same arm.

**Test slots.** One row per weighted tier-1 test, run under the kinds the manifest advertises. Unity: byte-identical once `sum_weights` / `n_eff` are pinned to `n` and shed. Only buffered and streaming apply (tests never fan out).

**Regression slots.** `regParityOps`, one row per regression variant and per `ATTR_REG_*` / score / SEM-score attribute. Attribute values are read back per group through a weight-following `AGG_AVERAGE` or (leverage: a row's is w × one copy's) an opted-out `AGG_SUM`. `converged_iters` and raw `n_obs` uncompared.

**Score attributes and quantile groupers.** `internal/service/weight_groupers_parity_test.go`: `GROUP_QUANTILE` on `groups[0]` and each crosstab axis, read back through opted-out counts. Frequency: each bucket opens where the expanded rows' bucket opens, but counts / highs are not the expansion's (a row spanning a cut lands whole). Only the buffered arm applies.

## Reference fixtures

**Oracle policy.** Every weighted figure is pinned to a value an EXTERNAL tool computed, never to Pulse's own output or the helper it calls:

- **`frequency`** = stock software (R base, numpy, scikit-learn) on the `rep()`-EXPANDED rows, run unweighted. Integer frequency weights mean exactly that, so this is the definition, not an approximation.
- **`probability`** = the one rule in CLOSED FORM on w* = w·n_eff/Σw. **This verifies the arithmetic, not the method:** no reference implements Kish-w* inference, and design-based software (R `survey`) gives different SEs by design. Cross-checked against an independent implementation where one exists (statsmodels `DescrStatsW(w*)` / `GLM(freq_weights = w*)`, scipy, Hmisc), and the SAME closed form must reproduce R on the frequency field first (1e-9), so the probability arm inherits R's correctness. Where no reference exists (`REG_BAYES_LINEAR`'s scalar-precision NIG prior) the row is the conjugate posterior in closed form on the expansion, stated as such. Note: OLS probability df = n_eff − q, NOT `lm(weights =)`'s row-count df.
- **Generators never run in CI**; the generated `*_values_test.go` files are never hand-edited. Toolchain, versions and the check: `internal/service/testdata/weight_reference/README.md`.
- **Reproducibility.** Closed-form linear algebra runs on the wheel's BLAS / LAPACK, which moves with the OS, so another machine regenerates those rows a few ulps apart (worst measured 2.2e-14 relative). The committed file is always one whole generator run; `gen_weight_reference.py --check` regenerates in memory and fails beyond 1e-12 relative (`weightRefTol`).
- **Relative tolerances only** (amd64 / arm64 FMA): 1e-12 statistics / estimates / closed forms, 1e-11 closed-form regression figures, 1e-10 p-values, interval bounds, IRLS and coordinate descent.
- **Kinds-agree** (`TestWeightReferenceKindsAgree`): a probability twin `p = c·f` must give the frequency figure for every scale-free point estimate and a different p-value / SE — the guard against a probability arm that silently reads Σw. Mahalanobis / PC / factor scores differ by one constant factor (ratio asserted); quantile cases are skipped (probability weights are rescaled, frequency are not).

`TestWeightReferenceValues` (`internal/service/weight_reference_test.go`) pins every weight-aware aggregator, test, regression, attribute, grouper and overlay figure plus floor keys. Values live in the generated `internal/service/weight_reference_values_test.go` (and siblings), written by `internal/service/testdata/weight_reference/gen_weight_reference.py` (`uv run --no-project gen_weight_reference.py`, then `gofmt -w`). A `coverage` subtest demands a case per manifest `weight_aware` operator per weight field, so a newly aware operator owes a reference as well as a parity row. The generator names the reference used for each figure. Fixture weight fields: `w` (fractional probability with a zero, a negative and a NaN), `f` (integer frequency with zeros, a negative, a NaN and a non-integer), and the probability twin `p = c·f`. Tests and regressions use their own generated fixtures; cases are slot-agnostic (`weightRefInferCase`: a JSON request fragment plus figures keyed by wire path), so a new inferential surface adds generator rows only.

**Significance tests.** Frequency: R on the expansion (`test_reference.R`). Probability: closed form on w*; the χ² fixture is asserted to clear the expected-count guard under every configuration.

**Regressions and regression attributes.** Frequency: R on the expansion (`reg_reference.R`); GLM refit once from its own converged coefficients (glm's SEs otherwise use the last iteration's starting weights, ~1e-8 off). Penalised fits pin β only (scikit-learn, `Ridge(alpha = λ·Σw)`).

**Scores and quantile buckets.** Quantile fixture values are distinct so no tie run straddles a cut; counts and highs follow from an exact rational transcription that refuses a fixture with a cumulative weight within 1e-8 of (but off) an integer.

**Probability quantiles are Hmisc's.** R + Hmisc 5.3.0 `wtd.quantile(normwt = TRUE)` via `hmisc_quantile.R` (refuses any other Hmisc version), cross-checked against an exact-rational transcription at 1e-12. The generator refuses a fixture whose normalized cumulative weight sits on an integer rank: Pulse snaps there but Hmisc does not, so Hmisc's figure is a last-ulp float artifact, not a usable reference.
