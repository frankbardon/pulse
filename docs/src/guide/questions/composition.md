# Mix and share

## The question

Questions of this kind sound like:

- What's the mix?
- What share does each option have?
- Which attributes go with which brand?

## Pick your analysis

This question fits data shaped like:

- `category`: one categorical, bool or set field; `by`: optionally one categorical or bool field

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_FREQUENCY`](../catalog/aggregator.md#op-agg_frequency) | How many rows hold one chosen value of a field, such as the Yes answers, over all rows or per group. | basic |
| [`AGG_MODE_COUNT`](../catalog/aggregator.md#op-agg_mode_count) | How many rows share a field's most common value, over all rows or per group. | basic |
| [`AGG_RATIO`](../catalog/aggregator.md#op-agg_ratio) | Total of one field divided by the total of another, over all rows or per group, such as revenue per order. | basic |
| [`AGG_SET_FREQUENCY`](../catalog/aggregator.md#op-agg_set_frequency) | For a multi-select field, how many rows chose each option, over all rows or per group. | basic |
| [`AGG_SUM`](../catalog/aggregator.md#op-agg_sum) | Total of a numeric field, over all rows or per group. | basic |

### Groupers

| Use | In plain words | Level |
|---|---|---|
| [`GROUP_CATEGORY`](../catalog/grouper.md#op-group_category) | Splits the rows into one group per distinct value of a field, such as one group per region or per answer option. | basic |
| [`GROUP_SET_PER_ELEMENT`](../catalog/grouper.md#op-group_set_per_element) | Splits a multi-select answer into one group per option, counting each row once in every option it picked. | intermediate |
| [`GROUP_SET_VALUE`](../catalog/grouper.md#op-group_set_value) | Groups the rows by the exact combination of options picked in a multi-select answer, such as A only, A and B, or none. | intermediate |

### Window operators

| Use | In plain words | Level |
|---|---|---|
| [`WIN_SHARE`](../catalog/window.md#op-win_share) | Adds each row's share of its partition's total, or the running share down the ordered rows, such as the share of defaults caught so far. | basic |
| [`WIN_LIFT`](../catalog/window.md#op-win_lift) | Adds how many times its share of one column a row holds of another, such as a decile's share of defaults over its share of loans. | intermediate |

### Feature operators

| Use | In plain words | Level |
|---|---|---|
| [`FEAT_FREQUENCY_ENCODE`](../catalog/feature.md#op-feat_frequency_encode) | Replaces each category with the share of records that carry it, one number that says how common the category is. | intermediate |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_INDEX_VS_TOTAL`](../catalog/overlay.md#op-overlay_index_vs_total) | Each group's value on a grouped result as an index value against the sum over all groups (group / total x 100). | basic |
| [`OVERLAY_SHARE_OF_COL`](../catalog/overlay.md#op-overlay_share_of_col) | Each crosstab cell as a share of its column margin (0 to 1): the mix of rows within each column, as a 100% stacked column shows it. | basic |
| [`OVERLAY_SHARE_OF_ROW`](../catalog/overlay.md#op-overlay_share_of_row) | Each crosstab cell as a share of its row margin (0 to 1): the mix of columns within each row, as a 100% stacked bar shows it. | basic |
| [`OVERLAY_SHARE_OF_TOTAL`](../catalog/overlay.md#op-overlay_share_of_total) | Each crosstab cell or group of a grouped result as a share of the grand total (0 to 1): what part of the whole each piece makes up. | basic |
| [`OVERLAY_CORRESPONDENCE`](../catalog/overlay.md#op-overlay_correspondence) | Correspondence analysis of a crosstab: a perceptual map placing rows and columns in a few dimensions so similar profiles sit close together. | advanced |
| [`OVERLAY_RAKE`](../catalog/overlay.md#op-overlay_rake) | Rake a crosstab to known row and column totals: the fitted table, per-cell adjustment factors and whether the fit converged. | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent composition` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Benchmark against a reference](benchmark.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`window-design`](../skills/window-design.md)

**From the command line:**

- `pulse recommend --intent composition --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
