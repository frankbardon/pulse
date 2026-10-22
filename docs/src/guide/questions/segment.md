# Segment into groups

## The question

<!-- docgen:preserve begin guide-segment-question -->
You have rows described by one or more measures — customers by age and income, products by price and sales, respondents by their answers — and you want to sort them into groups: tiers along one measure, or clusters of rows that look alike across several.

It sounds like:

- "Are there natural groups of customers?"
- "Split income into four equal-sized tiers."
- "Which customers fall into the high-value segment?"

For tiers on one measure, cut it into ranges: equal-sized groups (quantiles), equal-width bands, or breakpoints you choose. For groups across several measures, a clustering grouper such as k-means places every row with the nearest of a set number of centres. A filter keeps one segment you have already defined, for example by a rule on several fields.
<!-- docgen:preserve end guide-segment-question -->

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

**Do customers fall into natural groups by age and income?**

The request (example `group_kmeans`; `pulse examples show group_kmeans` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_COUNT",
      "field": "age",
      "label": "customers"
    },
    {
      "type": "AGG_AVERAGE",
      "field": "age",
      "label": "mean_age"
    },
    {
      "type": "AGG_AVERAGE",
      "field": "income",
      "label": "mean_income"
    }
  ],
  "cohort": {
    "filename": "customers.pulse",
    "data_dir": ".data"
  },
  "groups": [
    {
      "type": "GROUP_KMEANS",
      "fields": [
        "age",
        "income"
      ],
      "params": {
        "k": 3,
        "seed": 7
      }
    }
  ],
  "return": {
    "preset": "standard",
    "precision": 4
  }
}
```

The result:

```json
{
  "data": [
    {
      "GROUP_KMEANS_age": "cluster_1",
      "customers": 97,
      "mean_age": 35.18,
      "mean_income": 3.819e+04
    },
    {
      "GROUP_KMEANS_age": "cluster_2",
      "customers": 79,
      "mean_age": 63.57,
      "mean_income": 3.965e+04
    },
    {
      "GROUP_KMEANS_age": "cluster_3",
      "customers": 24,
      "mean_age": 57.88,
      "mean_income": 1.155e+05
    }
  ],
  "metadata": {
    "total_rows": 200,
    "filtered_rows": 200,
    "cohort_file": "customers.pulse"
  },
  "returned": {
    "preset": "standard",
    "digest": "rp1:e54405ae11f7db2bbe9d6bcfa4b9a169857bd482f45431aaae78605ca90255ef",
    "precision": 4
  }
}
```

K-means splits them into the three groups it was asked for: a large younger group, an older group on a similar typical income, and a small group whose average income is roughly three times the others'. Read that as a convenient split rather than a discovery. The younger and older groups look like two halves of one continuous age range cut near the middle, and the high-income group is the long upper tail of incomes rather than a separate population. The algorithm always returns as many groups as you ask for, whether or not the data has them; check the share of the spread the groups explain in the grouper's components (the full preset shows them), try a different number of groups, and ask what each group means for a decision before naming segments. Age and income are standardised first, so both count equally despite their different units.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

<!-- docgen:preserve begin guide-segment-pitfalls -->
- **Clustering always returns the number of groups you ask for.** It finds groups whether or not the data has them; a clean split can be a convenient cut through one continuous spread. Check how much of the spread the groups explain, and try a different number of groups.
- **A segment is only useful if it means something.** Describe each group in plain terms (younger, high income, recent buyers) and check that the difference matters for a decision before you name it.
- **Units decide what counts as near.** A measure in dollars swamps one in years unless the fields are standardised first; check that the clustering puts the measures on a common scale.
- **Quantile tiers have equal counts, not equal widths.** The top tier of a skewed measure can span a far wider range than the others; read each tier's minimum and maximum.
- **Segments found and tested on the same data look better than they are.** If you then compare the segments on the measures that built them, they will differ by construction; test them on a different outcome or on fresh data.
- **Results can shift with the starting point.** K-means depends on its starting centres; fix the seed so the same data gives the same groups, and treat a segment that moves between runs with care.
<!-- docgen:preserve end guide-segment-pitfalls -->

## Next steps

**Related questions:**

- [Distribution shape](distribution_shape.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`op-agg-average`](../skills/op-agg-average.md), [`op-agg-count`](../skills/op-agg-count.md), [`op-group-kmeans`](../skills/op-group-kmeans.md)

**From the command line:**

- `pulse recommend --intent segment --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
