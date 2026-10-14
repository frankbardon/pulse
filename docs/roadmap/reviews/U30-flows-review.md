# U30 flow overlays review

The committed record of the statistical review of the flow overlays roadmap [U30](../units/U30-matrix-extensions-hardening.md) shipped. It is an input for the human statistics reviewer at [U33](../units/U33-v1-release.md) (#206), beside the [U08](U08-statistics-review.md), [U12](U12-weighting-review.md), [U24](U24-matrix-operators-review.md) and [U25](U25-multivariate-review.md) records.

- **Scope:** `OVERLAY_SYMMETRY` (McNemar's test on a 2 × 2 table, Bowker's test of symmetry on k × k) on a from-state × to-state crosstab of paired observations, and `OVERLAY_RETENTION` (descriptive per from-state stay, switch and churn rates on the same kind of crosstab).
- **Not in scope:** `OVERLAY_MARKOV` (descriptive, U28); the chi-square tail itself (`chiSquareSurvival`, the U08 record); the weight rule (U12 record).
- **Status:** done without a human reviewer. The deterministic gates below bind in CI. The advisory review is one automated reading of the code, the skill and the guidance text. **Human sign-off is deferred to U33 (#206).**

## Method

### Deterministic layers (binding CI gates)

1. **External R oracle.** `scripts/reference/gen_symmetry.R` (run by `make reference`, never by CI) writes `internal/processing/testdata/reference/symmetry.json` from base R `stats::mcnemar.test`. `TestSymmetry_MatchesR` runs every case with the host columns reversed against the rows, so an alignment by position instead of by key fails.
2. **Weights follow the one rule** (`.claude/reference/weighting.md`): the Σw table, scaled to its Kish n_eff under `probability` (the `OVERLAY_CHISQ_MATRIX` rule). The weighted reference generator (`internal/service/testdata/weight_reference/gen_weight_reference.py`, case `ov_symmetry`) checks the frequency arm against R on the `rep()`-expanded rows and the probability arm against the closed form on w*. The overlay parity suites (`weight_overlay_parity_test.go`) prove unity byte parity, frequency = expanded rows and probability scale invariance.
3. **Predict = runtime.** Every refusal (Ref, scope, params, cell aggregation, grouper counts, Level / Within) raises the same code, message and details on both sides (root `TestSymmetry_RefusalsMatchPredict`).
4. **Retention hand oracle.** `OVERLAY_RETENTION` has no R function to compare with: each rate is one division of host cells. `internal/processing/overlay_retention_test.go` pins hand-computed rates on a three-tier table whose columns are reversed against the rows (so the diagonal must align by key), the pooled layer rates, the absorbing-row exclusion, a row-only state, a zero row and an undefined cell. The values are exact decimal literals, because each rate is one correctly rounded division of integers. `internal/service` `TestRetention_FrequencyWeightEqualsExpandedRows` shows the weight rule: integer frequency weights give byte-identical layers to the duplicated rows run unweighted, and `probability` gives the same layer as `frequency`.
5. **Coverage and guidance gates:** `TestSkillsCoverAllOverlayKinds`, `TestOperatorHasAtomicSkill`, `TestEveryOperatorHasAnExampleTag`, `TestSkillsCoverAllPurposes`, `TestInterpretationCoversOutputs`, `TestOverlayInterpretationFieldsHoldAtRuntime` and `TestOverlayPSites_CoverEveryInferentialKind`.

### Oracle files and tolerances

| Oracle file | Reference (R) | Pins | Tolerance |
|---|---|---|---|
| `symmetry.json` | `stats::mcnemar.test(x, correct)`; the empty-pair case summed by hand in base R (R returns NaN there) | statistic, df, p, discordant count, dropped pairs, whether the correction applied (7 cases: Agresti 2007 Table 8.1 with and without the correction, published z² = 17.36; a 3 × 3 and a seeded 4 × 4 Bowker table; one empty pair; a balanced 2 × 2; a diagonal-only 2 × 2) | 1e-12 relative (`symmetryOracleTol`), never exact bits: the p runs through a chi-square tail |
| weighted reference `ov_symmetry` | frequency: the same Bowker sum in R on the expanded rows, cross-checked against `mcnemar.test` when no pair is empty; probability: closed form on w* | statistic, p, df, sum_weights, n_eff, the expected-count warning | 1e-12 relative (`weightRefTol`) |

## Advisory review: formula choices

- **Continuity correction.** Default on, as R's `mcnemar.test(correct = TRUE)`: Edwards' `(|n_12 − n_21| − 1)²` on a 2 × 2 when the two counts differ, never on k × k. `params.correct: false` gives the uncorrected McNemar (Agresti's z²). The corrected gap is clamped at 0. R does not clamp, and that differs only on a non-integer table (a Kish-scaled or `AGG_SUM` host), where R's square would exceed the uncorrected statistic. **Acceptable.** The exact binomial McNemar test, which many texts prefer when `n_12 + n_21 < 25`, is not offered yet (#454, U36). The expected-count warning flags those tables instead.
- **Empty pairs.** A pair of states with no move either way is dropped and df counts the pairs kept. R keeps it, and its statistic becomes NaN. An empty pair carries no information about symmetry. **Decision recorded; the reviewer should confirm.** With no pair kept, the statistic and p are null (nobody changed state).
- **One-axis states.** A state seen on one axis only (nobody started, or nobody ended, in it) is a row or column of zeros, not a refusal. This is the table R would be given after padding. **Acceptable.**
- **Weights.** The test reads Σw counts. Under `probability` the first-order Kish scaling is not a design-based (Rao-Scott style) correction for paired survey data. **Same framing as the U12 record; sign off with it.**

## Advisory review: Purpose and Interpretation

- The Purpose states the paired-data assumption, the null (moves one way as common as moves back, the diagonal not entering), and the rule of thumb of about 10 moves per pair. It sends independent samples to `OVERLAY_CHISQ_MATRIX` and numeric before / after measures to `TEST_PAIRED_T`. **Acceptable.**
- The Interpretation reads the statistic, p, df and the four summary parameters. It says the statistic does not name the lopsided pair or its direction, so readers compare each cell with its mirror. An effect size (for example the McNemar odds ratio `n_12 / n_21` on a 2 × 2) is not reported yet. The owner filed it with the exact test as #454 (U36); see open item 2.

## Advisory review: `OVERLAY_RETENTION`

- **Rates.** Each rate divides by the from-state's total over every column: stay is the cell in the column with the same state name, churn sums the columns named in `params.absorbing`, and switch sums the rest. Each rate is its own sum over the total, never one minus the others, so a reader can check that they add to 1. Without `absorbing`, every move out counts as a switch. **Acceptable.**
- **Absorbing from-states.** A state named in `absorbing` is not reported as a from-state. Its row holds reactivations or stays in the absorbing state, and those are not retention rates. **Decision recorded; the reviewer should confirm.** A caller who wants that row omits `absorbing`.
- **Undefined rates.** A from-state with a zero total (possible with `AGG_SUM` cells) reports null rates and raises a `PULSE_OVERLAY_REF_ZERO` warning. It is never refused and never reported as 0. **Acceptable**, per the effort's undefined-figure decision.
- **Weights.** No weight class. Every rate is a ratio of Σw cells, so it equals the rate on the rows expanded by their weights under either kind. No Kish scaling applies, because no sample size enters. **Acceptable.**
- **Purpose and Interpretation.** The Purpose sends multi-period projection to `OVERLAY_MARKOV`, the directional test to `OVERLAY_SYMMETRY` and every destination's share to `OVERLAY_SHARE_OF_ROW`. The Interpretation reads the stay rate per entry, the switch and churn rates on each entry and on the layer, and the row total. It also says the rates carry no confidence interval. **Acceptable.**

## Open items for the U33 human reviewer (U30 flows)

1. Confirm dropping empty pairs (and the df it implies) over R's NaN.
2. ~~Decide whether an exact binomial McNemar test or an effect size (odds ratio of the discordant counts) should ship before the freeze.~~ Owner decision 2026-10-14: both are wanted but not in U30. They are filed as #454 under [U36](../units/U36-reference-oracles.md): `params.exact` on a 2 × 2 and the discordant odds ratio with its interval, each pinned to R. The reviewer may still mark either one release-blocking.
3. Confirm that `OVERLAY_RETENTION` leaves the from-states named in `absorbing` out of its report.
