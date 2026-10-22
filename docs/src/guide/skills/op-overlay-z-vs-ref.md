```yaml
name: op-overlay-z-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_Z_VS_REF
description: Compose-host per-group two-sample z-test on the means against the reference slot's matching group (SERIES); parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, z, byte-equal-test]
```

Compose-only parity overlay: the two-means z per group, target slot against the reference slot; series sibling of the cell overlay. Shared contract: [`overlay-reference-tests`](overlay-reference-tests.md).

## Use when

Large-sample z-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's?

Questions it answers:

- With large samples, which regions' average spend changed since last quarter?
- Which high-volume channels' average basket differs between the two periods?

Use something else:

- `OVERLAY_T_VS_REF` when any group is small; the t-based version is more honest there.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL to score on every slot (`label`, else `TYPE_field`) or an element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; missing on a slot → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `variance_target` | float | — | Target sample variance, > 0; required for a measure without mean + variance components. |
| `variance_ref` | float | — | Reference sample variance; likewise. |
| `variance` | float | — | Both sides, when a side's own key is absent. |
| `sample_size_target` | float | — | Target sample size; likewise. |
| `sample_size_ref` | float | — | Reference sample size; likewise. |
| `sample_size` | float | — | Both sides, when a side's own key is absent. |

`Scope` required, must be `group`. `Reference` / `Targets` required slot labels; rows key on group columns only. A scalar scored aggregation (anything but `AGG_WELFORD`) REQUIRES the variance and sample-size params, applied to EVERY group; missing → `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime.

## Host shape

COMPOSE — SERIES grouped Process on both slots. Reads `{n, mean, variance}` from the scored `AGG_WELFORD`'s per-group `Response.Components` (components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`), else the required `params`. Other non-scalar measures → `non_scalar`; crosstab slots → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (use the cell twin).

## Output

SERIES — one `SeriesEntry` per host group key, the p-value on `Summary.Statistic`; one layer per target, `Baseline` unset.

## Reading the output

- `summary.statistic`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Despite the field name, each series entry's summary.statistic holds that group's two-sided z-test p-value, not a z value; Pulse does not emit z.
  - Caveat: It is read from the normal curve, so with small groups it comes out too small.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- A NaN value on either side → the entry keeps its key, NO `statistic` (absent, not `null`).
- An engine `AGG_WELFORD` series is pinned to R `2·pnorm(-|z|)` on the Welch SE per group at 1e-10, weighted or not.
- Weighted: legs read N* (`sum_weights` / `n_eff`), never the raw `n`.
- **Byte-equal** to the two-sample z-test (shared `normalTwoSidedP`).
- Missing reference row or degenerate inputs → `PULSE_OVERLAY_REF_ZERO` (`ref_missing=true`), entry NaN.

## See

- Skills: [`overlay-reference-tests`](overlay-reference-tests.md), [`overlay-system`](overlay-system.md), [`op-overlay-z-cell`](op-overlay-z-cell.md), [`op-overlay-t-vs-ref`](op-overlay-t-vs-ref.md).
