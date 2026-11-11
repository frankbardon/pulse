```yaml
name: op-overlay-symmetry
kind: operator
category: OVERLAY
operator: OVERLAY_SYMMETRY
description: Paired-change test on a before × after crosstab — McNemar (2 × 2) or Bowker (k × k): are moves one way as common as moves back?
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Test of symmetry on a before-by-after crosstab of the same units: are moves between two states as common one way as back?

Questions it answers:

- Did more customers move up a tier than down between the two snapshots?
- Did the same respondents switch toward the brand more often than away from it between waves?

Use something else:

- `OVERLAY_MARKOV` when you want where the flow leads over several periods, not a test.

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

| Name | Type | Default | Description |
|---|---|---|---|
| `correct` | bool | `true` | (R's `mcnemar.test`): 2 × 2 continuity correction `(\|n_12 − n_21\| − 1)²`, clamped at 0; inert on k × k and under `exact`. |
| `exact` | bool | `false` | Exact p (small discordant counts): 2 × 2 McNemar, R `binom.test(n_12, n_12 + n_21)`; k × k Bowker. |
| `conf_level` | float | `0.95` | `exact` 2 × 2 only: odds-ratio interval coverage, in (0, 1). |

## Host shape

MATRIX crosstab of PAIRED units: earlier-state rows × later-state columns, one grouper each, count cells. States align by key; a one-axis state is a zero row / column. A long panel needs no pre-pivot: previous-state rows ([`op-attr-previous-state`](op-attr-previous-state.md)) × the state.

## Output

SCALAR χ² = Σ_{i<j} `(n_ij − n_ji)² / (n_ij + n_ji)`. `Summary.Parameters`: `df` (pairs kept), `states`, `discordant`, `pairs_dropped`, `continuity_corrected`. `exact` (`exact: 1`, no `df`): 2 × 2 statistic / scalar `n_12` + discordant `odds_ratio` `n_12 / n_21`, `odds_ratio_ci_lower` / `_upper` (at `conf_level`, echoed: `binom.test` `conf.int` → `p / (1 − p)`); k × k the χ², exact conditional p.

## Reading the output

- `scalar`: The symmetry chi-square, also in summary.statistic: over every pair of states, the squared gap between moves one way and moves back, divided by all moves between the two. 0 means every pair moved both ways equally; read summary.p_value rather than the raw value.
- `summary.statistic`: The same figure as scalar: the symmetry chi-square, or n_12 under params.exact on a 2x2.
- `summary.parameters.df`: Degrees of freedom: the number of pairs of states with at least one move between them (k(k - 1) / 2 for k states when none is empty).

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- An empty pair (`n_ij + n_ji = 0`) is dropped, lowering `df` (R gives NaN). None kept ⇒ statistic / p `null`.
- `exact` odds: `n_21 = 0` ⇒ ratio and upper `null` (+Inf); `n_12 = 0` ⇒ ratio and lower `0`; no discordant unit ⇒ all `null`.
- Pair expected `(n_ij + n_ji) / 2 < 5` ⇒ one `PULSE_OVERLAY_EXPECTED_LOW` (never under `exact`).
- `exact` refusals: probability-weighted cell ⇒ `PULSE_WEIGHT_UNSUPPORTED`; a discordant count > 1e-7 off whole ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`; k × k past 2^22 terms ⇒ `PULSE_OVERLAY_PARAM_MISSING` `exact_too_large`. `conf_level` (same code): no `exact` ⇒ `needs_exact`, k × k ⇒ `needs_2x2`, outside (0, 1) ⇒ `out_of_range`. k × k ones: runtime only.
- Weighted: Σw table, Kish-scaled under probability (+ `sum_weights`, `n_eff`).
- Grouper counts differ ⇒ `PULSE_OVERLAY_MATRIX_NOT_SQUARE`; non-count / negative cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`; an undefined (NaN) cell is absent (0).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md), [`op-overlay-markov`](op-overlay-markov.md), [`op-overlay-retention`](op-overlay-retention.md), [`op-attr-previous-state`](op-attr-previous-state.md).
