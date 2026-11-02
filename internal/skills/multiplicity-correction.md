---
name: multiplicity-correction
description: Multiple-comparison correction on a Request — the multiplicity block (method, family, alpha), which p-values are corrected together, the per-surface family table and its refusals, precedence, the additive adjusted outputs, row and column geometry on pairwise overlays, and what is never corrected.
type: guide
kind: design
applies_to: process, compose, facet, predict
covers: [multiplicity, p_adjusted, significant_adjusted, family-wise error, false discovery rate, Bonferroni, Holm, Benjamini-Hochberg, correlation matrix p-values]
requires: [capability:multiplicity]
---
# Multiple-comparison correction

Many tests in one answer make a lucky hit likely. Name a `multiplicity` block and Pulse adds corrected p-values BESIDE the raw ones; raw `p_value` / `reject_null` never change. No block (or `{"method": "none"}`) is byte-identical to before.

## The block

`multiplicity: {method, family, alpha}`, every key optional:

- `method` — `none`, `bonferroni`, `holm` (both family-wise error), `bh`, `by` (false discovery rate; `by` is valid under any dependence). Matches R `p.adjust`.
- `family` — which p-values are corrected together (below).
- `alpha` — overlay significance level in (0, 1), default `0.05`. A test's block refuses `alpha` (it reads its own).

It rides on `Request`, each `tests[]` / `post_tests[]` entry, each `overlays[]` spec<!-- feature: capability:matrices -->, each correlation `matrices[]` spec (implies `params.inference`)<!-- /feature -->, each `regressions[]` spec, the Compose `ComposedRequest` and its overlay specs, and engine-wide as `Options.DefaultMultiplicity`. Each key falls through on its own: slot → request → `ComposedRequest` → engine default → none.

## Families

| Family | Pools | Offered on |
|---|---|---|
| `layer` | one overlay layer | overlays (default) |
| `row` / `column` | one row / column of one layer's matrix | overlays whose payload is a MATRIX |
| `request` | the request's tests (one per test × group), post-tests (one per partition), `request`-family overlays and member regression coefficients | tests and regressions (default), request overlays |
| `compose` | every `compose` member over all slots and Compose-host layers | inside Compose only |
<!-- feature: capability:matrices -->| `matrix` | one matrix result's distinct pairs (upper triangle, m = k(k − 1)/2), per bucket | matrix specs (default) |<!-- /feature -->

Refused `PULSE_MULTIPLICITY_INVALID` (predict too): `row` / `column` on a non-matrix kind, `compose` outside Compose, `request` on a Compose-host overlay, anything but `layer` / `row` / `column` on a facet overlay, `alpha` on a test<!-- feature: capability:matrices -->, `matrix` off a matrix spec, anything but `matrix` / `request` on one, a block on a matrix operator without p-values<!-- /feature -->, an unknown or repeated regression `coefficients` name, a non-`none` method on a p-value-free regression (<!-- feature: REG_BAYES_LINEAR -->`REG_BAYES_LINEAR`, <!-- /feature -->a custom one lacking a `multiplicity` slot). An INHERITED family a surface does not offer falls back to its default; only an explicit one is refused. Members of one `request` / `compose` family resolving to different methods are `PULSE_MULTIPLICITY_CONFLICT`. An explicit block on a descriptive overlay is accepted and inert.

## Outputs (all additive)

- Test result: `p_adjusted`, `significant_adjusted` (against the test's own `alpha`) and `multiplicity {method, family, alpha, m, excluded}`; `m` counts the defined p-values corrected.
- Overlay summary: `p_adjusted` / `significant_adjusted` beside `p_value`.
- Overlay matrix payload: parallel `payload.p_adjusted` / `significant_adjusted` grids; a panel cell maps element for element.
- Pairwise `params.letters`: `above` follows `significant_adjusted` (an explicit `letters.alpha` compares `p_adjusted` instead).
- Overlay layer: a `multiplicity` echo.
<!-- feature: capability:matrices -->- Matrix result: `auxiliary.p_adjusted` / `significant_adjusted` (1 / 0) beside `auxiliary.p`, mirrored, diagonal null, plus a `multiplicity` echo. An inherited block reaches only a spec under `params.inference`.<!-- /feature -->
<!-- feature: MAT_SEM -->- `MAT_SEM`: own block only; every estimates row with a p (`:=` too) joins: `sem-defined-parameters`.<!-- /feature -->
- Regression result: `p_adjusted` / `significant_adjusted` maps beside `p_values` over its member `coefficients` (default every predictor) + echo; `alpha` as overlays. Only its own block opts it in. `margins`: own family (`margins.multiplicity`).
- An undefined p (NaN) gives `p_adjusted: null`, no `significant_adjusted`, and is left out of `m`, counted in `excluded` (absent when 0).

## Row and column geometry

`row` / `column` make one family per row / column INDEX of the layer's own matrix, never across rows, columns or layers. A pairwise overlay's rows are the compared groups, so `row` corrects each group's comparisons; a panel cell's elements join its row / column. The echo adds `m_per` (sizes aligned with the rows or columns, `0` where no p is defined); `m` is their sum.

## Where it runs

- Process: every arm (streaming, crosstab, join, shard, decode).
- Compose: after all slots finish, serial = parallel; non-compose families stay per slot, `compose` pools all; `--stream` rows carry no corrections; a failed slot errors the batch first.
- ProcessChain: each stage folds its own, never across stages; whole-chain overlays take no block.
- Facet: each overlay layer alone.
- Tests from extensions join families like built-ins.
<!-- feature: capability:matrices -->- An extension matrix operator joins only if it declares the auxiliary key of its p-values; it then joins under any block (no `params.inference`), and its twins are `<key>_adjusted` / `significant_adjusted`.<!-- /feature -->

## Never corrected

<!-- feature: TEST_TUKEY_HSD -->
- `TEST_TUKEY_HSD` is already family-wise: an explicit non-`none` block on it is refused, an inherited one skipped, and it never joins a family.
<!-- /feature -->
- A p-value outside any family stays raw; predict's `p_values {total, uncorrected, basis, threshold}` counts those (a regression: one per non-intercept predictor), and `uncorrected` ≥ `threshold` (10) with no block anywhere raises `PULSE_ADVISORY_MANY_TESTS`; `basis` is `exact`, `dictionary` (every dictionary entry assumed a bucket) or `lower_bound` (`predict-advisories`).

## Choosing

Few planned tests, false positives costly: `holm`. Screening many cells or pairs: `bh`. Odd dependence: `by`. Match the question: `row` for "does any group differ from the rest", `layer` for one table, `request` / `compose` for one claim over many tests.

## See

`statistical-testing` · `predict-advisories` · `overlay-system`<!-- feature: capability:compose --> · `compose-requests`<!-- /feature --><!-- feature: capability:process_chain --> · `process-chain`<!-- /feature --><!-- feature: capability:facet --> · `facet-design`<!-- /feature --> · `request-envelope` · `response-components` · `pulse_skills_get glossary` (family-wise-error, false-discovery-rate) · `pulse_errors_lookup` for `PULSE_MULTIPLICITY_*`.
