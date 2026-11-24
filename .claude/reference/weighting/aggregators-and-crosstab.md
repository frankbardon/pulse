# Weighting — Aggregator classification, Crosstab

Slice of `.claude/reference/weighting.md` — load the index first, then only the slice(s) your change touches.

## Aggregator classification

Weighted meaning of each weight-aware aggregator (the class itself is `internal/weighting.aggregatorClasses`):

| Operators | Weighted meaning |
|---|---|
| `AGG_COUNT` | Σw over value-present rows (a float) |
| `AGG_SUM`; `AGG_AVERAGE`, `AGG_WEIGHTED_MEAN` | Σwx; Σwx/Σw |
| `AGG_VARIANCE` / `AGG_STDDEV` | population m2_w/Σw; `params.ddof` 1: m2_w/(Σw − 1) (`null` when Σw ≤ 1) |
| `AGG_WELFORD` | sample m2_w/(Σw − 1); `N` stays the contributing ROW count |
| `AGG_SKEWNESS` / `AGG_KURTOSIS` | weighted population moments |
| `AGG_MEDIAN` / `AGG_PERCENTILE` | Hmisc `wtd.quantile` (below). Ordered-categorical slots: the lowest level whose cumulative Σw reaches p·Σw (within 1e-9 relative), scale-free |
| `AGG_MODE` / `AGG_MODE_COUNT` | the max-Σw value / its Σw |
| `AGG_FREQUENCY` | Σw of matches; share Σw_match/Σw |
| `AGG_RATIO` | Σw·num / Σw·den |
| `AGG_SET_FREQUENCY`, `AGG_SET_CARDINALITY_SUM`, `AGG_SET_CARDINALITY_AVG` | Σw per member; Σw·card; Σw·card/Σw |
| `AGG_CI_LOWER`, `AGG_CI_UPPER` | weighted mean ∓ z·√(s²/N*), s² on w* ("Weighted inference") |
| `AGG_MAPE`, `AGG_RMSE`, `AGG_MAE` | Σw·loss / Σw over rows with both sides present (MAPE: non-zero actual); RMSE = root of the pooled MSE; floor keys follow `field` (the actual) |
| `AGG_AUC` | Σᵢⱼ wᵢwⱼψ / (Σw₊·Σw₋), ψ = 1 / ½ / 0. Both classes sorted by (score, weight) before any sum, so record order never moves a bit. DeLong `se` / `ci_*` are `null` under ANY weight (`se_note` = `PULSE_AUC_DELONG_WEIGHTED`); `n_pos` / `n_neg` stay raw |
| `AGG_VEC_MEAN`, `AGG_VEC_SUM` | per member Σw·x / Σw or Σw·x over rows whose member answered with valid w > 0; `element_n` raw, `element_sum_weights` per-member Σw |
| `AGG_VEC_SD` | per member the SAMPLE sd on w*: √(M2_w/(Σw − 1)) frequency, √(M2_w/(Σw − Σw²/Σw)) probability; `null` when N* ≤ 1 |

Every weighted aggregator: w ≡ 1 is bit-identical to unweighted, integer frequency weights equal the expanded rows, a row with an invalid or zero weight contributes nothing.

**The table is `internal/weighting.aggregatorClasses`** (`TestClassify_Complete` holds it total over `types.AllAggregationTypes()`; the same `operatorClasses` table classes tests, regressions, attributes, groupers and windows). Classes: `ClassAware`, `ClassFrequencyOnly`, `ClassNotWeightable`, `ClassRefuse`. Manifest `weight_aware` / `weight_kinds` are stamped from it (`internal/descriptor/capabilities_weight.go`). **Explicit** means a slot weight (or `weight_field`) or the request weight; only `Options.DefaultWeight` is a default — windows can only be explicit through the request. A not-weightable operator is skipped under a default and `PROCESSING_CONFIG` `{slot, operator, field}` under an explicit weight. Overlays are classed in `internal/descriptor` off their Inferential flag ("Refusal rules"); extension operators by their `WeightAware` declaration ("Extension contract").

**Weight kinds.** `ClassFrequencyOnly` computes a weighted figure under `kind: frequency` only (exact on the expanded rows): Pulse has no probability form because none is standard, or the design-based test needs design information Pulse lacks and is not the expansion test (rank tests vs `survey::svyranktest`). A probability weight in force — slot, request or default — is `PULSE_WEIGHT_UNSUPPORTED` naming the kind, `{slot, operator, field, kind, supported_kinds: ["frequency"]}`; `weight: null` opts out. `weighting.KindsOf(op)` drives the kinds; `IsAware` is "has a kind", so the engine stamps a frequency-only slot like an aware one. **A class flips to aware / frequency-only only in the change that lands its weighted computation** — earlier, it would run unweighted silently. Permanent refusals carry `details.reason` and say `<slot>: <op> cannot be weighted: <reason>`; a pending `ClassRefuse` says `has no weighted form yet`. Reasons: `weighting.RefusalReason`; slot-level reasons in the resolver (`weightSlot.reason`: every post-test; a regression with `resample: bootstrap` or `selection`); the overlay class table. Gates: `TestKindsOf`, `TestRefusalReasons_AreRefused`, `TestResolveWeights_*`, root `TestWeight_*MatchesPredict`. Test seam for stand-in flips: `weighting.OverrideClassForTest`.

**Manifest `weight_kinds`.** `omitempty` on `descriptor.Operator`, `TestMeta` (tier 1 only, never a post-test), `RegressionMeta` and `OverlayCapability`. `weight_aware` is set exactly where `weight_kinds` is non-empty. Both are absent when `capability:weighting` is hidden. `TestManifest_WeightAware`, `TestManifest_WeightKindsFollowClassFlip`, `TestManifest_WeightingHidden`.

**Core formulas.** Presence via `FieldPresent` (set columns included). Σw = 0 ⇒ mean, variance, quantiles, mode and shape moments are NaN (`null` on the wire). Variance streams / merges by the weighted Welford-West recurrence (`mergeWelfordWeighted`, Chan over weight totals) and buffers by the unweighted twin's two-pass form. Weighted `AGG_AVERAGE` components add `sum_weighted`, `weighted_mean`, `m2_weighted`, `sum_weights_sq`, `weighted_variance`, declared `optional: true`; `AGG_WEIGHTED_MEAN` keeps its own seven-key map.

**Weighted percentile / median (Hmisc `wtd.quantile`; `weightedQuantileAggregator`, `internal/processing/aggregator_weighted_shape.go`).** Pairs with value present and valid w > 0, sorted by value; W = Σw; h = p·(W − 1); x₍ₖ₎ is the smallest xᵢ whose cumulative weight is ≥ k + 1 (Hmisc's `approx(..., method = "constant", f = 1, rule = 2)`); interpolate linearly between x₍⌊h⌋₎ and x₍⌈h⌉₎. Matches Hmisc 5.3.0 `wtd.quantile(type = "quantile")` with `normwt = TRUE` for probability and `FALSE` for frequency — to the last few ulps, because the interpolation keeps the unweighted twin's `lo + f·(hi − lo)` form for unity bit-parity.

- **Integer snap.** Every cumulative weight within 1e-9·max(1, |c|) of an integer snaps to it (`snapCumWeights`) before comparison. Why: "≥ the rank" is a knife edge; a cumulative weight one ulp below the rank (summation order, rescale, FMA on one architecture) would silently pick the next order statistic. Pulse returns the exact-arithmetic answer, which can differ from Hmisc's own float output (weights 1.2, 0.3, 0.3 on 10, 20, 30: median 10, Hmisc 20) — `TestWeightedShape_QuantileRankSnap`.
- **Scale rule.** Probability weights are RESCALED to W = n (contributing rows) before ranking, so scaling every weight leaves the figure unchanged; frequency weights stay RAW, so integer weights equal type 7 on the expanded data. Integer probability weights therefore reproduce the expanded rows only if Σw = n — expansion equivalence is the frequency contract (`TestWeightedShape_ProbabilityQuantileScaleInvariant`, `TestWeightedShape_FrequencyQuantileRaw`).
- **FMA.** The rank and interpolation product sit behind explicit `float64` barriers in both twins; an arm64-only fusion once split unity bit-parity at p95.
- **Components** keep the unweighted keys; `position*` are EXPANDED-space indices ⌊h⌋ / ⌈h⌉. Stays buffered (forces the buffered crosstab arm), as unweighted.

**Weighted mode / mode count (`weightedModeAggregator`).** Σw per distinct value; ties go to the smallest value; `tie_count` counts values sharing the max Σw. A value seen only at weight 0 is not a distinct value. Streamable and mergeable as unweighted.

**Weighted skewness / kurtosis (`weightedMomentAggregator`).** g1 = M3 / (W·sd³), g2 = M4 / (W·var²) − 3, var = M2 / W. Streaming folds by Pébaÿ's pairwise update with n_B = w; buffered mirrors the two-pass form. Every product is rounded before its sum (explicit `float64` barriers, `detmath.Pow`) so no architecture fuses; buffered M2 accumulates `(w·d)·d`, bit-equal to `d²` at w = 1.

**`AGG_WEIGHTED_MEAN`.** Weighted `AGG_AVERAGE` under its own type name. `params.weight_field` is a slot weight with `kind: probability`: absent ⇒ inherits; differing from an explicit slot weight ⇒ `PROCESSING_CONFIG` `{slot, operator, field}`; nothing resolves ⇒ `PROCESSING_CONFIG` `{slot, operator}` (such a slot makes predict report `weights`). Deliberately NOT gated by `capability:weighting`. An unstamped slot (direct factory caller) falls back to `weight_field`. Negative / NaN / ±Inf weights are excluded and warned like everywhere else.

**Weighted counting aggregators (`internal/processing/aggregator_weighted_counts.go`, plus `ratioAggregator`).** Rows contribute only with a present value AND valid w > 0 (`validRowWeight`).
- `AGG_FREQUENCY` — `match_count` = Σw of matches; `share` omitted when Σw = 0.
- `AGG_RATIO` — a row with either field null or an invalid / zero weight is skipped; `numerator` / `denominator` are weighted sums; floor keys follow the slot's (ignored) `Field`.
- `AGG_SET_FREQUENCY` — Σw per member (`map[string]float64`); `distinct_labels` stays a count; a member selected only at weight 0 is dropped.
- `AGG_SET_CARDINALITY_*` — an empty mask still contributes its weight to Σw.

All stay streamable and mergeable as declared. No scale question: each is a ratio of weighted sums or a weighted total.

**decimal128.** A weight-aware slot whose value field is `decimal128` (its `Field`, `AGG_RATIO`'s `numerator_field` / `denominator_field`, a model-error aggregator's `prediction_field`) is `PULSE_WEIGHT_UNSUPPORTED` under ANY weight in force, the default included, `{slot, operator, field, value_field, type}`: the decimal path has no weighted form, and skipping the weight would silently mix an unweighted figure into a weighted table. Raised by `ResolveWeights` (`decimalWeightRefusal`), so predict ≡ runtime; opt out with `weight: null`. `AGG_WEIGHTED_MEAN` over decimal is therefore always refused.

Outputs that are integers unweighted (set-frequency map and counts, `match_count`, `sum_cardinality`, `mode_count`) become floats only when weighted — integral, so wire-identical, at w = 1; the manifest type notes say so. Streamable / Mergeable flags never change with weighting.

## Crosstab

Both arms stamp, so every accumulator — cells, row / column / grand margins, partial-depth and `normalize_within` denominators, auxiliary `margin_aggregations` — is built off the stamped spec and recomputed from the WEIGHTED raw rows; normalization divides a weighted cell by its weighted margin. **A record reaches an auxiliary margin only if it reached a CELL** — unchanged by weighting.

- **Counts stay raw.** `CellCounts` and every margin count are row counts, never Σw; Σw is the map's `sum_weights`.
- **Floor keys on the flat maps.** Every cell / margin / auxiliary component map carries `sum_weights` / `n_eff` (probability) / `n_weight_invalid` when THAT slot is weighted, tallied over the same records as its `{n, n_null}`. An operator's own same-named key wins. An auxiliary that admitted nothing reports `sum_weights: 0`.
- **The unweighted base.** A `margin_aggregations` slot with `weight: null` beside weighted cells carries no floor key; an auxiliary's own weight overrides the request's.
- **Warning.** One `PULSE_WEIGHT_INVALID_ROWS` per weight field over the filter-passing rows, on both arms.
- **Fusion.** `crosstabfuse.Decide` reads no weight: a weighted median / percentile stays buffered because the operator is non-mergeable, as unweighted (`TestWeightCrosstabFusionDecision`; predict agrees).
- **Per-class risk.** Each fused accumulator class has its own construction site, so a class that dropped the weight would silently answer the UNWEIGHTED figure while the rest stay right — hence `internal/processing` `TestCrosstab_WeightedFusedMatchesBufferedPerClass`, which asserts fused = buffered AND that the weight moved each class. The fused arm is one serial walk; no partial crosstab state is ever merged.

Gates live in `internal/service/weight_crosstab_test.go` (each arm forced and asserted engaged): unity parity, frequency expansion, fused ≡ buffered (byte-exact for the Σw-sum operators, 1e-12 relative for mean / ratio forms), probability-quantile scale invariance, auxiliary override, invalid rows. A cell whose rows all carry weight 0 makes `AGG_RATIO` 0/0 = NaN on both arms, written `null` (`.claude/reference/response-components.md` (Undefined figures on the wire)).
