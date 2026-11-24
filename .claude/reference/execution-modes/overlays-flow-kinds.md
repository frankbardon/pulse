# Execution modes — overlays: the paired-flow kinds (MARKOV, SYMMETRY, RETENTION)

Per-topic slice of the execution-modes reference. Index (Mode index + section map): `.claude/reference/execution-modes.md` — load it first, then only the slice(s) your engine work touches.

## Overlays — paired-flow kinds: `OVERLAY_MARKOV`, `OVERLAY_SYMMETRY`, `OVERLAY_RETENTION`

Continues the `## Overlays` section of `.claude/reference/execution-modes/overlays.md` via `.claude/reference/execution-modes/overlays-matrix-kinds.md`. All three read a from-state (rows) × to-state (columns) crosstab on the MATRIX (crosstab) host, are implicit-margin and index-aware in `applyOverlaysPlanned`. The shared Crosstab spec refusals are in `overlays-matrix-kinds.md` (Spec refusals — one rule per host).

Shared rules:

- **States align BY KEY**, never by position (`overlayhost.MarkovStateName`: a one-element string key is itself, any other key its JSON).
- A different grouper count per axis is `PULSE_OVERLAY_MATRIX_NOT_SQUARE` on predict AND runtime (`MarkovArityRefusal`, details `{kind, row_groupers, column_groupers, index}`).
- Cells: `NonnegativeCellRefusal` / `NonnegativeValueRefusal`; an absent or undefined (NaN) cell = 0.
- `absorbing` is one shared decoder (non-empty, distinct). A name that matches no host state is refused at runtime only (`MarkovStateRefusal`) because predict reads no record.
- Every product is FMA-free (explicit `float64()` around it).

**`OVERLAY_MARKOV`.** Markov chain of a transition crosstab. Scope `matrix`, descriptive (no p-site, no weight class: every output row-normalizes, so a weighted Σw table is read as is). `internal/processing/overlay_markov.go`.

- **Square host.** States are the row keys in row order, columns aligned by key. Differing key sets are a runtime-only refusal (`MarkovKeySetRefusal`, `{kind, row_only, column_only, index}`) because a crosstab emits only observed keys — an absorbing state with no observed move out is missing from the row axis.
- **Declared-absorbing synthesis.** When there is no row-only key and EVERY column-only key is named in `params.absorbing`, each gets a synthesized unit row (appended after the observed rows, in column order — known by declaration, not invented), reported by one `PULSE_OVERLAY_REF_ZERO` `{kind, axis: "row", states, reason: "absorbing_row_synthesized"}` plus `summary.parameters.synthesized_rows` (absent when 0, so unsynthesized output is byte-identical). An observed row that is also declared absorbing is NOT rewritten. Any other one-axis key keeps the refusal.
- < 2 states or a zero total is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. A zero row becomes the unit row (absorbing) with one `PULSE_OVERLAY_REF_ZERO` `{kind, axis: "row", rows, states, reason: "zero_row_absorbing"}`.
- **Params** (`overlayhost.ParseMarkovParams`, strict, `PULSE_OVERLAY_PARAM_MISSING`): `steps` (default `[1,3,6,12]`, distinct integers 1–1000, at most `MarkovMaxStepCount` 64 so one spec cannot ask for unbounded k × k parts), `absorbing`, `initial` (`"margin"` = row totals / grand, or a share map normalized by its sum; omitted states 0).
- **Outputs.** Primary MATRIX P; parts `step_<n>` (Pⁿ by binary powering), `distribution_<n>` (π₀ Pⁿ, with `initial`), `steady_state` and `absorption_time`. Summary parameters `states`, `classes`, `irreducible`, `period` (irreducible only), `steady_state_iterations` / `_converged`.
- **`steady_state`:** power iteration from uniform, `max|Δ| < 1e-12` within 10,000 steps; slower chains fall back to the exact `linalg.Solve` of π(I − P) = 0, Σπ = 1 (`steady_state_converged` 0 says which). Only for one communicating class with period 1; else one `PULSE_OVERLAY_MARKOV_NOT_ERGODIC` `{reason: reducible, classes}` / `{reason: periodic, period}` and no part.
- **`absorption_time`** (with `absorbing`): expected steps to FIRST reach the declared set (a first-passage time, since declared rows are not rewritten), `(I − Q)⁻¹·1` via `linalg.FactorLU` over the non-absorbing states certain to be absorbed. A state that can reach a closed class outside the set is NaN → `null` with one `PULSE_OVERLAY_MARKOV_NOT_ERGODIC` `{reason: absorption_not_certain, states}`. A numerically singular I − Q drops the part with a `PULSE_MATRIX_SINGULAR` WARNING carrying linalg's details plus `kind` / `part` — the layer still ships, never a silent NaN.
- Interpretation addresses only the fixed-name parts (`steady_state`, `absorption_time`); `step_<n>` / `distribution_<n>` are named by params. Oracle: `scripts/reference/gen_markov.R` (`markovchain`).

**`OVERLAY_SYMMETRY`.** Test of symmetry for a crosstab of PAIRED observations — McNemar on 2 × 2, Bowker on k × k (R `mcnemar.test`). Scope `matrix`, INFERENTIAL (SCALAR payload = χ², `Summary{Statistic, PValue, Parameters}`); joins the Level / Within-zero gate. `internal/processing/overlay_symmetry.go`.

- **States** are row keys in row order, then column-only keys in column order; a one-axis state is a zero row / column — never a refusal (unlike MARKOV).
- Spec refusals are shared, predict AND runtime: a set `Ref` is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, a scope other than `matrix` `PULSE_OVERLAY_SCOPE_UNSUPPORTED`. < 2 states or a zero total is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- **Params** (`overlayhost.ParseSymmetryParams`, strict): `correct` (boolean, default `true` = R's default), `exact`, `conf_level` (default 0.95, needs `exact` on 2 × 2).
- **Statistic.** Over pairs i < j, `s = n_ij + n_ji`, `χ² = Σ (n_ij − n_ji)² / s`, `df` = the pairs with `s > 0`. **An empty pair is DROPPED** (R keeps it as 0/0 = NaN; an empty pair carries no information); none kept ⇒ statistic / p / scalar `null`, `df` 0. On 2 × 2 with `correct` and `n_12 ≠ n_21`: Edwards' `max(0, |d| − 1)²` — the clamp differs from R only on a non-integer table (Kish-scaled / `AGG_SUM`), where R's `(|d| − 1)²` can exceed it. p = the `TEST_CHISQ` tail.
- **`exact` on 2 × 2:** R `binom.test(n_12, n)` + `odds_ratio` n_12 / n_21 and `odds_ratio_ci_lower` / `_upper` at `conf_level` (n_21 = 0 → `null`).
- **`exact` on k × k** is the exact conditional Bowker test (`statdist.BowkerExactP`): given each pair's total m, n_ij ~ Binomial(m, ½) independently; statistic = the uncorrected χ² bit for bit, p = P(X² ≥ obs) by exact convolution (values merged at 1e-12 relative, an outcome counted when X²·(1 + 1e-7) ≥ obs — `fisher.test`'s relErr). On one pair it equals `binom.test`. Runtime-only refusals (states and counts are data): an explicit `conf_level` ⇒ `PULSE_OVERLAY_PARAM_MISSING` `needs_2x2`; more than `BowkerExactMaxTerms` (2^22) enumeration terms ⇒ the same code, `exact_too_large` `{states, terms, limit}` — never a silent χ².
- Summary parameters `df`, `states`, `discordant` (Σ off-diagonal), `pairs_dropped`, `continuity_corrected`, plus the weighted floor (the exact k × k arm carries `exact` and no `df` / odds / `conf_level`). Any pair with `s / 2 < 5` raises ONE `PULSE_OVERLAY_EXPECTED_LOW` `{kind, low_expected_pairs, zero_pairs, expected_min}`.
- **Weights:** `ClassAware`, the `OVERLAY_CHISQ_MATRIX` rule — the Σw table, scaled to its Kish n_eff under probability (so a components-disabled probability host is the hidden-floor `PROCESSING_CONFIG`).
- **Multiplicity:** one p per layer; predict `p_values` counts 1. Oracle: `scripts/reference/gen_symmetry.R` (`TestSymmetry_MatchesR`).

**`OVERLAY_RETENTION`.** Per from-state stay / switch / churn rates. Scope `row`, DESCRIPTIVE (no p-site, no weight class); joins the Level / Within-zero gate. `internal/processing/overlay_retention.go`.

- **Rates** over `R_i` = the row's total over EVERY column: `stay` = the cell whose column key names the row's state (no such column ⇒ 0 — a row-only state is never refused), `churn` = Σ over the columns named in `params.absorbing`, `switch` = every other column. Each is its own sum over `R_i` (never 1 minus the others), so they add to 1 up to rounding. Without `absorbing`, `churn` is absent everywhere.
- **A from-state named in `absorbing` is not reported** (churn is the way into it; its own row is a reactivation, not a retention rate).
- A row with `R_i = 0` has `null` rates (undefined, never 0) and one `PULSE_OVERLAY_REF_ZERO` `{kind, axis: "row", states, reason: "zero_row"}`; there is no zero-total refusal.
- Spec refusals are shared, predict AND runtime: a set `Ref` is `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, a scope other than `row` `PULSE_OVERLAY_SCOPE_UNSUPPORTED`. Params: `absorbing` only.
- **Output:** SERIES, one entry per reported from-state in host row order, `Summary.Statistic` = stay, `Summary.Parameters` `switch`, `from_total` (`R_i`) and `churn` (with `absorbing`). Layer `Summary.Parameters` pool the reported rows (`stay`, `switch`, `churn`, `states`, `total`; each pooled rate = Σ numerators / Σ totals, `null` when the total is 0).
- **Weights:** none-class — a ratio of host cells, so the Σw table is read as is under either kind (frequency ≡ `rep()`-expanded rows byte for byte; no Kish scaling). No product is taken, so figures are bit-identical across architectures.
