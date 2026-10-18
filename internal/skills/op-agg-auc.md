---
name: op-agg-auc
description: Area under the ROC curve of a score against a binary outcome — exact rank sum, ties half; gini and a DeLong interval in components.
kind: operator
category: AGG
operator: AGG_AUC
type: reference
applies_to: process, compose, predict
examples_tags: [logistic, financial]
---

<!-- generated: use-when -->

## Params

`outcome` — required; packed_bool or 0/1 numeric. `positive` — event value 0 or 1 (default 1). Missing `outcome` or another `positive` → `PROCESSING_CONFIG`; categorical / set / date → `PULSE_FIELD_TYPE_UNSUPPORTED` — predict and runtime alike. A scored outcome other than 0/1 fails the run: `PROCESSING_AUC_OUTCOME_NOT_BINARY`.

<!-- feature: capability:weighting -->Weight-aware: Σwᵢwⱼψ / (Σw₊·Σw₋); `se` and the interval null, `se_note` = `PULSE_AUC_DELONG_WEIGHTED`.<!-- /feature -->

## Inputs

`Field` is the score: u4–u64, f32/f64, packed_bool. Split train/holdout with a filter or<!-- feature: GROUP_RANGE --> `GROUP_RANGE`<!-- /feature -->.

## Output

Scalar in [0, 1]; 0.5 = no discrimination, ties count half. An empty class or a NaN score → NaN (`null`).

<!-- generated: reading-the-output -->

## Components

Floor `{n, n_null}` on `field`<!-- feature: capability:weighting --> (+ `sum_weights`, `n_eff`, `n_weight_invalid`)<!-- /feature -->; `auc`, `gini` (2·AUC − 1), `n_pos`, `n_neg`, `n_null_outcome`, DeLong `se`, `ci_lower`, `ci_upper` (clipped to [0, 1]; null under 2 rows a class), `conf_level` 0.95. `None`: buffered.

## Gotchas

- Ranking, not calibration.

## See

- `pulse_examples_search tags=[logistic, financial]`
- Skills: `op-agg-mae`, `op-attr-reg-predict`, `op-reg-glm`
