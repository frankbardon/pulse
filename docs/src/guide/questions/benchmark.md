# Benchmark against a reference

## The question

Questions of this kind sound like:

- How does this compare to the total?
- Is this group above or below the population?
- How does this wave compare to the last?

## Pick your analysis

This question fits data shaped like:

- `measure`: 1 or more numeric, categorical or bool fields; `group`: one categorical, bool or date field

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_AUC`](../catalog/aggregator.md#op-agg_auc) | How well a score separates a yes/no outcome: the chance that a randomly picked yes row scores higher than a randomly picked no row. | intermediate |
| [`AGG_MAE`](../catalog/aggregator.md#op-agg_mae) | Average size of a prediction's miss in the actual's own units, comparing an actual column with a prediction column row by row. | intermediate |
| [`AGG_MAPE`](../catalog/aggregator.md#op-agg_mape) | Average size of a prediction's miss as a share of the actual value, comparing an actual column with a prediction column row by row. | intermediate |
| [`AGG_RMSE`](../catalog/aggregator.md#op-agg_rmse) | Typical size of a prediction's miss in the actual's own units, with large misses counting more: the root of the average squared miss. | intermediate |

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_PERCENTILE`](../catalog/attribute.md#op-attr_percentile) | Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied. | basic |
| [`ATTR_PROFILE_SIMILARITY`](../catalog/attribute.md#op-attr_profile_similarity) | Adds to every row how closely its related fields match a target profile: an ideal pattern, a benchmark, or its own segment target. | intermediate |
| [`ATTR_TSCORE`](../catalog/attribute.md#op-attr_tscore) | Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10. | intermediate |
| [`ATTR_VEC_DISTANCE`](../catalog/attribute.md#op-attr_vec_distance) | Adds to every row how far its set of related fields lies from a target profile, optionally in units of each field's spread. | intermediate |
| [`ATTR_ZSCORE`](../catalog/attribute.md#op-attr_zscore) | Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean. | intermediate |
| [`ATTR_REG_FITTED`](../catalog/attribute.md#op-attr_reg_fitted) | Adds to every row the value a straight-line model predicts for its target from its predictors. | advanced |
| [`ATTR_REG_PREDICT`](../catalog/attribute.md#op-attr_reg_predict) | Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values. | advanced |
| [`ATTR_REG_RESIDUAL`](../catalog/attribute.md#op-attr_reg_residual) | Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it. | advanced |

### Window operators

| Use | In plain words | Level |
|---|---|---|
| [`WIN_DELTA`](../catalog/window.md#op-win_delta) | Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month. | basic |
| [`WIN_DENSE_RANK`](../catalog/window.md#op-win_dense_rank) | Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next follows on: 1, 2, 2, 3. | basic |
| [`WIN_PCT_CHANGE`](../catalog/window.md#op-win_pct_change) | Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month. | basic |
| [`WIN_RANK`](../catalog/window.md#op-win_rank) | Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next skips: 1, 2, 2, 4. | basic |
| [`WIN_SHARE`](../catalog/window.md#op-win_share) | Adds each row's share of its partition's total, or the running share down the ordered rows, such as the share of defaults caught so far. | basic |
| [`WIN_LIFT`](../catalog/window.md#op-win_lift) | Adds how many times its share of one column a row holds of another, such as a decile's share of defaults over its share of loans. | intermediate |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_T`](../catalog/test.md#op-test_t) | Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version). | intermediate |
| [`TEST_HOTELLING_T2`](../catalog/test.md#op-test_hotelling_t2) | Checks whether several numeric fields, taken together as one profile, differ from a target, between two groups, or before vs after. | advanced |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_DELTA_VS_MARGIN`](../catalog/overlay.md#op-overlay_delta_vs_margin) | Each crosstab cell minus its row, column or grand margin, in the cell's own units: how far a cell sits above or below its margin. | basic |
| [`OVERLAY_DELTA_VS_SIBLING`](../catalog/overlay.md#op-overlay_delta_vs_sibling) | Each group's value minus one named group's value, in the value's own units: how far every group sits from, say, the control group. | basic |
| [`OVERLAY_INDEX_VS_MARGIN`](../catalog/overlay.md#op-overlay_index_vs_margin) | Index value of each crosstab cell against its row, column or grand margin (cell / margin x 100): which cells over- or under-index? | basic |
| [`OVERLAY_INDEX_VS_POP`](../catalog/overlay.md#op-overlay_index_vs_pop) | Each category's (or histogram bin's) share in a facet subset as an index value against its share in a comparison population (x 100). | basic |
| [`OVERLAY_INDEX_VS_SIBLING`](../catalog/overlay.md#op-overlay_index_vs_sibling) | Each group's value as an index value against one named group (group / sibling x 100), such as every store against the flagship. | basic |
| [`OVERLAY_CHISQ_VS_POP`](../catalog/overlay.md#op-overlay_chisq_vs_pop) | Chi-square goodness-of-fit test on a facet: checks whether a subset's category mix departs from a comparison population's mix. | intermediate |
| [`OVERLAY_DELTA_VS_REF`](../catalog/overlay.md#op-overlay_delta_vs_ref) | Each cell or group of a target Compose request minus the same coordinate in the reference request, in the target's own units. | intermediate |
| [`OVERLAY_DELTA_VS_STAGE`](../catalog/overlay.md#op-overlay_delta_vs_stage) | A process-chain stage's result minus an earlier stage's result at the same coordinate, in the later stage's own units. | intermediate |
| [`OVERLAY_INDEX_VS_REF`](../catalog/overlay.md#op-overlay_index_vs_ref) | Index value of each cell or group of a target Compose request against the same spot in the reference request (target / reference x 100). | intermediate |
| [`OVERLAY_INDEX_VS_STAGE`](../catalog/overlay.md#op-overlay_index_vs_stage) | Index value of a process-chain stage's result against an earlier stage's result at the same coordinate (target / reference x 100). | intermediate |
| [`OVERLAY_PANEL_INDEX_VS_REF`](../catalog/overlay.md#op-overlay_panel_index_vs_ref) | Index values of several target Compose requests against one shared reference request, one layer per target (target / reference x 100). | intermediate |
| [`OVERLAY_ZSCORE_VS_MARGIN`](../catalog/overlay.md#op-overlay_zscore_vs_margin) | Each crosstab cell's gap from its row, column or grand margin, in standard deviations of the cells in that slice: which cells stand out? | intermediate |
| [`OVERLAY_ZSCORE_VS_POP`](../catalog/overlay.md#op-overlay_zscore_vs_pop) | Puts each category's share gap with a comparison population on a z-score scale; for numeric bins it says nothing about the subset. | intermediate |
| [`OVERLAY_ZSCORE_VS_TOTAL`](../catalog/overlay.md#op-overlay_zscore_vs_total) | How many standard deviations each group's value sits from the average of all groups on a grouped result: which groups stand out? | intermediate |
| [`OVERLAY_KS_VS_POP`](../catalog/overlay.md#op-overlay_ks_vs_pop) | Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's. | advanced |
| [`OVERLAY_RAKE`](../catalog/overlay.md#op-overlay_rake) | Rake a crosstab to known row and column totals: the fitted table, per-cell adjustment factors and whether the fit converged. | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent benchmark` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Find drivers](drivers.md)
- [Mix and share](composition.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`facet-design`](../skills/facet-design.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`process-chain`](../skills/process-chain.md), [`statistical-testing`](../skills/statistical-testing.md), [`window-design`](../skills/window-design.md)

**From the command line:**

- `pulse recommend --intent benchmark --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
