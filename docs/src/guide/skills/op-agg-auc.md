```yaml
name: op-agg-auc
description: Area under the ROC curve of a score against a binary outcome — exact rank sum, ties half; gini and a DeLong interval in components.
kind: operator
category: AGG
operator: AGG_AUC
type: reference
applies_to: process, compose, predict
examples_tags: [logistic, financial]
```

## Use when

How well a score separates a yes/no outcome: the chance that a randomly picked yes row scores higher than a randomly picked no row.

Questions it answers:

- How well does the default model rank the held-out loans?
- Does the new churn score separate leavers from stayers better than the old one?

Use something else:

- `AGG_MAE` when you need the size of the miss of a numeric prediction, not how well it ranks.

## Params

`outcome` — required; packed_bool or 0/1 numeric. `positive` — event value 0 or 1 (default 1). Missing `outcome` or another `positive` → `PROCESSING_CONFIG`; categorical / set / date → `PULSE_FIELD_TYPE_UNSUPPORTED` — predict and runtime alike. A scored outcome other than 0/1 fails the run: `PROCESSING_AUC_OUTCOME_NOT_BINARY`.

Weight-aware: Σwᵢwⱼψ / (Σw₊·Σw₋); `se` and the interval null, `se_note` = `PULSE_AUC_DELONG_WEIGHTED`.

## Inputs

`Field` is the score: u4–u64, f32/f64, packed_bool. Split train/holdout with a filter or `GROUP_RANGE`.

## Output

Scalar in [0, 1]; 0.5 = no discrimination, ties count half. An empty class or a NaN score → NaN (`null`).

## Reading the output

- `value`: The chance that a randomly picked positive row scores higher than a randomly picked negative row, a tie counting half. 0.5 is a coin flip; 1 means every positive outscores every negative; below 0.5 the score ranks backwards.
- `components.gini`: The Gini coefficient (accuracy ratio) 2 x AUC - 1: 0 is no discrimination and 1 perfect. It is the credit-scoring convention for the same ranking power.
- `components.ci_lower`: The lower end of the 95% DeLong confidence interval for the AUC, clipped to 0. The true ranking power is plausibly anywhere in the interval.

## Components

- `auc`, `gini` (2·AUC − 1), `n_pos`, `n_neg`, `n_null_outcome`; DeLong `se`, `ci_lower`, `ci_upper` (clipped to [0, 1]; null under 2 rows a class), `conf_level` 0.95.
- The `n` / `n_null` floor counts `field` only.

## Contract

- Types (`field`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Types (`outcome`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: buffered only; components `none`. No streaming discrimination proxy exists; accept the buffered path (a stream reports it at terminal flush).
- Components: `n`, `n_null`, `auc`, `gini`, `n_pos`, `n_neg`, `n_null_outcome`, `se`, `ci_lower`, `ci_upper`, `conf_level`; optional `sum_weights`, `n_eff`, `n_weight_invalid`, `se_note`.

## Gotchas

- Ranking, not calibration.

## See

- `pulse_examples_search tags=[logistic, financial]`
- Skills: [`op-agg-mae`](op-agg-mae.md), [`op-attr-reg-predict`](op-attr-reg-predict.md), [`op-reg-glm`](op-reg-glm.md)
