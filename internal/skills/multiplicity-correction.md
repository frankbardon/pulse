---
name: multiplicity-correction
description: Multiple-comparison correction on a Request — the multiplicity block (method, family, alpha), which p-values are corrected together, the per-surface family table and its refusals, precedence, the additive adjusted outputs, row and column geometry on pairwise overlays, and what is never corrected.
type: guide
kind: design
applies_to: process, compose, facet, predict
covers: [multiplicity, p_adjusted, significant_adjusted, family-wise error, false discovery rate, Bonferroni, Holm, Benjamini-Hochberg]
requires: [capability:multiplicity]
---
# Multiple-comparison correction

Many tests in one answer raise the chance that one comes out significant by luck. Name a `multiplicity` block and Pulse adds corrected p-values BESIDE the raw ones; raw `p_value` / `reject_null` never change. No block (or `{"method": "none"}`) is byte-identical to before.

## The block

`multiplicity: {method, family, alpha}`, every key optional:

- `method` — `none`, `bonferroni`, `holm` (both family-wise error), `bh`, `by` (false discovery rate; `by` is valid under any dependence). Same results as R `p.adjust`.
- `family` — which p-values are corrected together (below).
- `alpha` — overlay significance level in (0, 1), default `0.05`. A test reads its own `alpha`, so `alpha` on a test's block is refused.

It rides on `Request`, each `tests[]` / `post_tests[]` entry, each `overlays[]` spec, the Compose `ComposedRequest` and its overlay specs, and engine-wide as `Options.DefaultMultiplicity`. Each key falls through on its own: slot → request → `ComposedRequest` → engine default → none.

## Families

| Family | Pools | Offered on |
|---|---|---|
| `layer` | one overlay layer | overlays (default) |
| `row` / `column` | one row / column of one layer's matrix | overlays whose payload is a MATRIX |
| `request` | the request's tests, post-tests and `request`-family overlays | request tests (default), request overlays |
| `compose` | every `compose` member over all slots and Compose-host layers | inside Compose only |

Refused `PULSE_MULTIPLICITY_INVALID`: `row` / `column` on a non-matrix kind, `compose` outside Compose, `request` on a Compose-host overlay, anything but `layer` / `row` / `column` on a facet overlay, `alpha` on a test. An INHERITED family a surface does not offer falls back to that surface's default; only an explicit one is refused. Members of one `request` / `compose` family resolving to different methods are `PULSE_MULTIPLICITY_CONFLICT`. An explicit block on a descriptive overlay is accepted and inert.

`pulse predict` applies the same rules to a Request without running it.

## Outputs (all additive)

- Test result: `p_adjusted`, `significant_adjusted` (against the test's own `alpha`) and `multiplicity {method, family, alpha, m}`; `m` is the number of defined p-values corrected together.
- Overlay summary: `p_adjusted` / `significant_adjusted` beside `p_value` (or the `statistic` the t / z-versus-reference kinds carry their p in).
- Overlay matrix payload: parallel `payload.p_adjusted` / `payload.significant_adjusted` matrices on identical headers and coordinates; a panel cell's vector maps element for element.
- Overlay layer: a `multiplicity` echo, present only when a correction ran.
- An undefined p (NaN) gives `p_adjusted: null`, no `significant_adjusted`, and is left out of `m`.

## Row and column geometry

`row` / `column` make one family per row / column INDEX of the layer's own matrix, by coordinate: never across rows, columns or layers. A pairwise overlay's rows are the compared groups, so `row` corrects each group's comparisons against all others. Every element of a panel cell joins its cell's row / column. The echo adds `m_per` (family sizes aligned with the rows, or columns; `0` where an index has no defined p) and `m` is their sum.

## Where it runs

- Process, every arm: serial, streaming, fused or buffered crosstab, join, shard, parallel decode.
- Compose: after all slots finish. Serial and parallel are byte-identical; non-compose families stay per slot, `compose` pools all. `--stream` emits rows only (no corrections). A failed slot errors the batch before any fold.
- ProcessChain: each stage folds its own request, never across stages; the whole-chain overlays take no block.
- Facet: each overlay layer on its own.
- Tests from extensions join families like built-ins.

## Never corrected

<!-- feature: TEST_TUKEY_HSD -->
- `TEST_TUKEY_HSD` is already family-wise: an explicit non-`none` block on it is refused, an inherited one skipped, and it never joins a family.
<!-- /feature -->
- Regression coefficient p-values are out of scope.
- A p-value outside any family (no block resolved) stays raw, and predict's `p_values {total, uncorrected, basis, threshold}` counts those: `uncorrected` at or above `threshold` (10) is the cue to add a block. `basis` is `exact`, `dictionary` (assumes every dictionary entry becomes a bucket, so it may over- or under-count) or `lower_bound`.

## Choosing

Few planned tests, any false positive costly: `holm`. Screening many cells, tolerating a few false hits: `bh`. Hits possibly dependent in odd ways: `by`. Correct within the question asked: `row` for "does any group differ from the others", `layer` for one table, `request` / `compose` for one headline claim over many tests.

## See

`statistical-testing` · `overlay-system`<!-- feature: capability:compose --> · `compose-requests`<!-- /feature --><!-- feature: capability:process_chain --> · `process-chain`<!-- /feature --><!-- feature: capability:facet --> · `facet-design`<!-- /feature --> · `request-envelope` · `response-components` · `pulse_skills_get glossary` (family-wise-error, false-discovery-rate) · `pulse_errors_lookup` for `PULSE_MULTIPLICITY_*`.
