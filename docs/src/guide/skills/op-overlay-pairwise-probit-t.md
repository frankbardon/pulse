```yaml
name: op-overlay-pairwise-probit-t
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_PROBIT_T
description: Intra-matrix axis-pairwise probit t-test (Φ⁻¹ transform of each leg's proportion, Student-t tail).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
```

Probit-transformed t between host slots ALONG one axis of the SAME crosstab — sibling of the pairwise proportion z: same share + n inputs, probit transform instead. Shared pairwise contract (spec, grid, letters, skips, weighted legs): [`overlay-pairwise`](overlay-pairwise.md).

## Use when

Pairwise t-tests on probit-transformed shares between rows (or columns) of one crosstab, matching a convention some survey tools use.

Questions it answers:

- Which segments differ in share when tested the way our survey tool does?
- Do the probit-scale shares of each region differ from each other?

Use something else:

- `OVERLAY_PAIRWISE_PROP_Z` when you want the standard test of two shares.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pair_along_dim` | int | — | Restrict pairs to same-bucket comparisons on the pair axis. |
| `n_within_depth` | int | `0` | With a within-prefix n_source, the pair-axis dim positions (depth + 1) the n denominator is summed within; inert otherwise. |
| `letters` | object | — | `{}` or `{"alpha": a}`, `0 < a < 1`: adds the `letters` part. |
| `n_source` | enum | — | Where each leg's n is read; omitted = the cell's base (the normalise denominator on a normalised host; weighted, its N*). |
| `p_source` | enum | — | How each leg's share is read; omitted = `cell_value` on a normalised host, else `cell_value_pct`. |

`Scope` (required) — `row` or `column`. `Ref` empty. n and share vocabulary, identical to the pairwise proportion z: [`pairwise-n-sources`](pairwise-n-sources.md).

## Host shape

MATRIX crosstab + `Response.Components.Crosstab` (components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime). A code cell is refused ([`overlay-pairwise`](overlay-pairwise.md)).

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values (layout: [`overlay-pairwise`](overlay-pairwise.md)). Per pair: `t = (Φ⁻¹(p_i) - Φ⁻¹(p_j)) / sqrt(1/n_i + 1/n_j)` against Student-t with `df = n_i + n_j - 2`; proportions clipped to `[1e-10, 1-1e-10]` before Φ⁻¹. A design formula (market-research convention), pinned as is to R at 1e-10: the `sqrt(1/n_i + 1/n_j)` SE is not a probit share's real spread and no stock test computes it, so read the p as a convention, not a calibrated test. `letters`: `higher` from the sign of `t`.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: The probit simplification differs from the textbook two-proportion test (OVERLAY_PAIRWISE_PROP_Z), so the two give different p-values for the same cells.
- `parts.letters.cells.value`: Significance letters for this host cell: its column's (or row's) letter, and in above the letters of the same-group columns it is significantly higher than here.

## Contract

- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`: the probit-transformed proportion t is a market-research convention with no reference weighted form); `weight: null` on the slot runs it unweighted.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- `df <= 0` (n_i + n_j <= 2) or a non-finite t skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- `p_source` mismatch (a leg outside `[0,1]`) empties the layer — never clipped; normalize a count cell first.
- Distinct-KEY n modes: the same admission and slab partition refusal as the proportion z ([`pairwise-n-sources`](pairwise-n-sources.md)).
- The host crosstab still FUSES on a mergeable cell aggregator.

## See

- Skills: [`overlay-pairwise`](overlay-pairwise.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-pairwise-prop-z`](op-overlay-pairwise-prop-z.md), [`op-test-t`](op-test-t.md).
