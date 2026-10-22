# Segment into groups

## The question

Questions of this kind sound like:

- Are there natural groups of customers?
- Split this into tiers.

## Pick your analysis

This question fits data shaped like:

- `measures`: 1 or more numeric fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Groupers

| Use | In plain words | Level |
|---|---|---|
| [`GROUP_RANGE`](../catalog/grouper.md#op-group_range) | Splits a number into bands of equal width, such as ages 20-30 and 30-40, to see how values spread or to compare bands. | basic |
| [`GROUP_ROUNDED`](../catalog/grouper.md#op-group_rounded) | Groups a number by rounding it down to a multiple of a step, such as 23 and 27 both to 20, naming each group by that multiple. | basic |
| [`GROUP_NUMERIC_RANGES`](../catalog/grouper.md#op-group_numeric_ranges) | Labels each row with the named number range its value falls in, such as a train/holdout split on a row index or custom score bands. | intermediate |
| [`GROUP_QUANTILE`](../catalog/grouper.md#op-group_quantile) | Splits the rows into equal-sized groups by rank of a number, such as quartiles or deciles of spend, to compare top and bottom. | intermediate |
| [`GROUP_KMEANS`](../catalog/grouper.md#op-group_kmeans) | Finds k segments of rows that are alike across several numbers at once, such as customer types by spend, visits and tenure. | advanced |

### Feature operators

| Use | In plain words | Level |
|---|---|---|
| [`FEAT_BUCKETIZE`](../catalog/feature.md#op-feat_bucketize) | Puts each value into an ordered bin, using cut points you give or bins holding about equal numbers of records, and adds the bin number. | basic |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_DISTANCE`](../catalog/matrix.md#op-mat_distance) | How far apart segments are across several numbers at once: the gap between each pair of segment centres. | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent segment` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Distribution shape](distribution_shape.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md)

**From the command line:**

- `pulse recommend --intent segment --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
